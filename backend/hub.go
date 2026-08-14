package main

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type Frame struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type PresenceViewer struct {
	ChatJID   string `json:"chatJid"`
	AdminID   int64  `json:"adminId"`
	AdminName string `json:"adminName"`
}

type PresencePayload struct {
	Viewers []PresenceViewer `json:"viewers"`
}

type wsConn struct {
	ws        *websocket.Conn
	send      chan Frame
	once      sync.Once
	adminID   int64
	adminName string
	viewing   string // chat JID currently open in this client, "" for none
}

func (c *wsConn) writeLoop() {
	for f := range c.send {
		data, err := json.Marshal(f)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = c.ws.Write(ctx, websocket.MessageText, data)
		cancel()
		if err != nil {
			_ = c.ws.Close(websocket.StatusInternalError, "write failed")
			return
		}
	}
	_ = c.ws.Close(websocket.StatusNormalClosure, "")
}

// shutdown is only safe after the conn has been removed from the hub map
// (no more sends can race with the close).
func (c *wsConn) shutdown() {
	c.once.Do(func() { close(c.send) })
}

type Hub struct {
	mu      sync.Mutex
	conns   map[*wsConn]struct{}
	onCount func(int)
}

func NewHub() *Hub {
	return &Hub{conns: make(map[*wsConn]struct{})}
}

func (h *Hub) SetCountListener(f func(int)) {
	h.mu.Lock()
	h.onCount = f
	h.mu.Unlock()
}

func (h *Hub) Register(ws *websocket.Conn, adminID int64, adminName string) *wsConn {
	c := &wsConn{ws: ws, send: make(chan Frame, 64), adminID: adminID, adminName: adminName}
	h.mu.Lock()
	h.conns[c] = struct{}{}
	n := len(h.conns)
	cb := h.onCount
	h.mu.Unlock()
	go c.writeLoop()
	if cb != nil {
		cb(n)
	}
	return c
}

func (h *Hub) Unregister(c *wsConn) {
	h.mu.Lock()
	_, ok := h.conns[c]
	if ok {
		delete(h.conns, c)
	}
	n := len(h.conns)
	cb := h.onCount
	h.mu.Unlock()
	if !ok {
		return
	}
	c.shutdown()
	if cb != nil {
		cb(n)
	}
	h.BroadcastPresence()
}

// SetViewing records which chat the connection is looking at and, when it
// actually changed, broadcasts a fresh presence snapshot.
func (h *Hub) SetViewing(c *wsConn, chatJID string) {
	h.mu.Lock()
	_, ok := h.conns[c]
	changed := ok && c.viewing != chatJID
	if changed {
		c.viewing = chatJID
	}
	h.mu.Unlock()
	if changed {
		h.BroadcastPresence()
	}
}

// presenceSnapshot lists every connection with an open chat, deduplicated by
// (adminID, chatJID). Callers must hold h.mu.
func (h *Hub) presenceSnapshot() PresencePayload {
	seen := make(map[[2]any]struct{}, len(h.conns))
	viewers := make([]PresenceViewer, 0, len(h.conns))
	for c := range h.conns {
		if c.viewing == "" {
			continue
		}
		key := [2]any{c.adminID, c.viewing}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		viewers = append(viewers, PresenceViewer{ChatJID: c.viewing, AdminID: c.adminID, AdminName: c.adminName})
	}
	return PresencePayload{Viewers: viewers}
}

// BroadcastPresence pushes the current snapshot to every client. Called on
// connect (which also delivers it to the new client), disconnect and any
// viewing change.
func (h *Hub) BroadcastPresence() {
	h.mu.Lock()
	f := Frame{Type: "presence", Data: h.presenceSnapshot()}
	h.mu.Unlock()
	h.Broadcast(f)
}

// Broadcast queues the frame on every connection; connections whose buffer is
// full are closed instead of silently dropping frames.
func (h *Hub) Broadcast(f Frame) {
	h.mu.Lock()
	var overflowed []*wsConn
	for c := range h.conns {
		select {
		case c.send <- f:
		default:
			overflowed = append(overflowed, c)
		}
	}
	h.mu.Unlock()
	for _, c := range overflowed {
		h.Unregister(c)
	}
}

func (h *Hub) SendTo(c *wsConn, f Frame) {
	h.mu.Lock()
	if _, ok := h.conns[c]; !ok {
		h.mu.Unlock()
		return
	}
	select {
	case c.send <- f:
		h.mu.Unlock()
		return
	default:
	}
	h.mu.Unlock()
	h.Unregister(c)
}
