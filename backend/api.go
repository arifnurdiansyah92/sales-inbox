package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"go.mau.fi/whatsmeow/types"
)

type API struct {
	mgr             *Manager
	st              *Store
	hub             *Hub
	auth            *Auth
	frontendOrigin  string
	wsOriginPattern string
}

func NewAPI(mgr *Manager, st *Store, hub *Hub, auth *Auth, frontendOrigin string) *API {
	pattern := frontendOrigin
	if u, err := url.Parse(frontendOrigin); err == nil && u.Host != "" {
		pattern = u.Host
	}
	return &API{mgr: mgr, st: st, hub: hub, auth: auth, frontendOrigin: frontendOrigin, wsOriginPattern: pattern}
}

func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.handleHealthz)
	mux.HandleFunc("POST /api/auth/login", a.auth.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", a.auth.handleLogout)
	mux.HandleFunc("GET /api/auth/me", a.auth.handleMe)
	mux.HandleFunc("GET /api/status", a.handleStatus)
	mux.HandleFunc("GET /api/chats", a.handleChats)
	mux.HandleFunc("GET /api/chats/{jid}/messages", a.handleListMessages)
	mux.HandleFunc("POST /api/chats/{jid}/messages", a.handleSendMessage)
	mux.HandleFunc("POST /api/chats/{jid}/media", a.handleSendMedia)
	mux.HandleFunc("POST /api/chats/{jid}/read", a.handleMarkRead)
	mux.HandleFunc("PATCH /api/chats/{jid}", a.handlePatchChat)
	mux.HandleFunc("GET /api/media/{jid}/{id}", a.handleMedia)
	mux.HandleFunc("POST /api/logout", a.handleLogout)
	mux.HandleFunc("GET /api/avatar/{jid}", a.handleAvatar)
	mux.HandleFunc("GET /ws", a.handleWS)
	return a.cors(a.auth.Wrap(mux))
}

func (a *API) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", a.frontendOrigin)
		h.Set("Vary", "Origin")
		h.Set("Access-Control-Allow-Methods", "GET,POST,PATCH,OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type")
		h.Set("Access-Control-Allow-Credentials", "true")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleHealthz stays outside session/auth middleware (see Auth.Wrap) so it
// keeps answering even when the session store is down.
func (a *API) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	dbOK := a.st.db.PingContext(ctx) == nil
	waConnected, loggedIn := a.mgr.Health()
	ok := dbOK && (waConnected || !loggedIn)
	status := http.StatusOK
	if !ok {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]bool{
		"ok":       ok,
		"db":       dbOK,
		"whatsapp": waConnected,
		"loggedIn": loggedIn,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func pathJID(r *http.Request) (types.JID, bool) {
	jid, err := types.ParseJID(r.PathValue("jid"))
	if err != nil {
		return types.EmptyJID, false
	}
	return jid, true
}

func (a *API) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.mgr.StatusPayload())
}

func (a *API) handleChats(w http.ResponseWriter, r *http.Request) {
	chats, err := a.st.ListChats(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, chats)
}

func (a *API) handleListMessages(w http.ResponseWriter, r *http.Request) {
	jid, ok := pathJID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "JID tidak valid")
		return
	}
	key := jid.ToNonAD().String()
	q := r.URL.Query()

	limit := 50
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeErr(w, http.StatusBadRequest, "limit tidak valid")
			return
		}
		if n > 500 {
			n = 500
		}
		limit = n
	}
	var beforeTs int64
	if v := q.Get("beforeTs"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, "beforeTs tidak valid")
			return
		}
		beforeTs = n
	}
	beforeID := q.Get("beforeId")

	msgs, err := a.st.ListMessages(r.Context(), key, limit, beforeTs, beforeID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, msgs)
}

// handleMarkRead resets the unread counter; the frontend calls it when a chat
// is actually opened (listing messages no longer clears it implicitly).
func (a *API) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	jid, ok := pathJID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "JID tidak valid")
		return
	}
	key := jid.ToNonAD().String()
	if err := a.st.ClearUnread(r.Context(), key); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
	if chat, found, err := a.st.GetChat(context.Background(), key); err == nil && found {
		a.hub.Broadcast(Frame{Type: "chat_upsert", Data: chat})
	}
}

func (a *API) handlePatchChat(w http.ResponseWriter, r *http.Request) {
	jid, ok := pathJID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "JID tidak valid")
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "Body tidak valid")
		return
	}
	if body.Status != "open" && body.Status != "resolved" {
		writeErr(w, http.StatusBadRequest, "Status tidak valid")
		return
	}
	chat, found, err := a.st.SetChatStatus(r.Context(), jid.ToNonAD().String(), body.Status)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, "Chat tidak ditemukan")
		return
	}
	a.hub.Broadcast(Frame{Type: "chat_upsert", Data: chat})
	writeJSON(w, http.StatusOK, chat)
}

func (a *API) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	jid, ok := pathJID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "JID tidak valid")
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "Body tidak valid")
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		writeErr(w, http.StatusBadRequest, "Teks pesan kosong")
		return
	}
	msg, err := a.mgr.SendText(r.Context(), jid, body.Text, adminFrom(r.Context()))
	if errors.Is(err, errNotConnected) {
		writeErr(w, http.StatusConflict, errNotConnected.Error())
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, msg)
}

// detectMime picks the best mime type for an uploaded file: declared part
// header, then content sniffing, then the file extension.
func detectMime(hdr *multipart.FileHeader, data []byte) string {
	normalize := func(ct string) string {
		if mt, _, err := mime.ParseMediaType(ct); err == nil {
			return mt
		}
		return ct
	}
	if ct := normalize(hdr.Header.Get("Content-Type")); ct != "" && ct != "application/octet-stream" {
		return ct
	}
	ct := normalize(http.DetectContentType(data))
	if ct == "application/octet-stream" {
		if byExt := mime.TypeByExtension(strings.ToLower(filepath.Ext(hdr.Filename))); byExt != "" {
			return normalize(byExt)
		}
	}
	return ct
}

func isTooLarge(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr) || strings.Contains(err.Error(), "request body too large")
}

func (a *API) handleSendMedia(w http.ResponseWriter, r *http.Request) {
	jid, ok := pathJID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "JID tidak valid")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		if isTooLarge(err) {
			writeErr(w, http.StatusRequestEntityTooLarge, "File terlalu besar (maksimum 64 MB)")
			return
		}
		writeErr(w, http.StatusBadRequest, "Form tidak valid")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "File tidak ditemukan")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "Gagal membaca file")
		return
	}
	if len(data) == 0 {
		writeErr(w, http.StatusBadRequest, "File kosong")
		return
	}
	caption := strings.TrimSpace(r.FormValue("caption"))

	msg, err := a.mgr.SendMedia(r.Context(), jid, data, detectMime(hdr, data), hdr.Filename, caption, adminFrom(r.Context()))
	if errors.Is(err, errNotConnected) {
		writeErr(w, http.StatusConflict, errNotConnected.Error())
		return
	}
	if err != nil {
		log.Printf("send media: %v", err)
		writeErr(w, http.StatusBadGateway, "Gagal mengirim media")
		return
	}
	writeJSON(w, http.StatusCreated, msg)
}

// dispositionName makes a filename safe for a quoted Content-Disposition value.
func dispositionName(name string) string {
	name = strings.NewReplacer("\r", "", "\n", "").Replace(name)
	name = strings.ReplaceAll(name, `\`, `\\`)
	name = strings.ReplaceAll(name, `"`, `\"`)
	if name == "" {
		name = "file"
	}
	return name
}

func (a *API) handleMedia(w http.ResponseWriter, r *http.Request) {
	jid, ok := pathJID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "JID tidak valid")
		return
	}
	id := r.PathValue("id")
	path, mimeType, fileName, msgType, err := a.mgr.MediaFile(r.Context(), jid.ToNonAD().String(), id)
	if errors.Is(err, errMediaNotFound) || errors.Is(err, errMediaGone) {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, errMediaDownload.Error())
		return
	}

	f, err := os.Open(path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Gagal membaca media")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Gagal membaca media")
		return
	}
	if mimeType == "" {
		buf := make([]byte, 512)
		n, _ := io.ReadFull(f, buf)
		mimeType = http.DetectContentType(buf[:n])
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			writeErr(w, http.StatusInternalServerError, "Gagal membaca media")
			return
		}
	}
	h := w.Header()
	h.Set("Content-Type", mimeType)
	h.Set("Cache-Control", "private, max-age=86400")
	if msgType == "document" {
		h.Set("Content-Disposition", `attachment; filename="`+dispositionName(fileName)+`"`)
	} else {
		h.Set("Content-Disposition", "inline")
	}
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// handleLogout disconnects the WhatsApp session for everyone — owner only.
func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !adminFrom(r.Context()).IsOwner {
		writeErr(w, http.StatusForbidden, msgOwnerOnly)
		return
	}
	a.mgr.Logout(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleAvatar(w http.ResponseWriter, r *http.Request) {
	jid, ok := pathJID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "JID tidak valid")
		return
	}
	data, err := a.mgr.Avatar(r.Context(), jid)
	if err != nil {
		writeErr(w, http.StatusNotFound, "Avatar tidak ditemukan")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(data)
}

// wsClientFrame is what clients may send over the socket. Only "viewing" is
// understood; anything malformed or unknown is ignored.
type wsClientFrame struct {
	Type string `json:"type"`
	Data struct {
		ChatJID *string `json:"chatJid"`
	} `json:"data"`
}

// handleWS runs behind requireAuth, so the session is already validated (and
// the admin known) before the websocket handshake happens.
func (a *API) handleWS(w http.ResponseWriter, r *http.Request) {
	admin := adminFrom(r.Context())
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{a.wsOriginPattern},
	})
	if err != nil {
		return
	}
	c := a.hub.Register(ws, admin.ID, admin.Name)
	for _, f := range a.mgr.InitialFrames() {
		a.hub.SendTo(c, f)
	}
	// The connect-time broadcast doubles as the newcomer's first snapshot.
	a.hub.BroadcastPresence()
	for {
		_, data, err := ws.Read(r.Context())
		if err != nil {
			break
		}
		var f wsClientFrame
		if json.Unmarshal(data, &f) != nil || f.Type != "viewing" {
			continue
		}
		viewing := ""
		if f.Data.ChatJID != nil {
			viewing = *f.Data.ChatJID
		}
		a.hub.SetViewing(c, viewing)
	}
	a.hub.Unregister(c)
}
