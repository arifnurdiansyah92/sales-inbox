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

type wsConn struct {
	ws   *websocket.Conn
	send chan Frame
	once sync.Once
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

func (h *Hub) Register(ws *websocket.Conn) *wsConn {
	c := &wsConn{ws: ws, send: make(chan Frame, 64)}
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
