package main

import (
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

	"github.com/coder/websocket"
	"go.mau.fi/whatsmeow/types"
)

type API struct {
	mgr             *Manager
	st              *Store
	hub             *Hub
	frontendOrigin  string
	wsOriginPattern string
}

func NewAPI(mgr *Manager, st *Store, hub *Hub, frontendOrigin string) *API {
	pattern := frontendOrigin
	if u, err := url.Parse(frontendOrigin); err == nil && u.Host != "" {
		pattern = u.Host
	}
	return &API{mgr: mgr, st: st, hub: hub, frontendOrigin: frontendOrigin, wsOriginPattern: pattern}
}

func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", a.handleStatus)
	mux.HandleFunc("GET /api/chats", a.handleChats)
	mux.HandleFunc("GET /api/chats/{jid}/messages", a.handleListMessages)
	mux.HandleFunc("POST /api/chats/{jid}/messages", a.handleSendMessage)
	mux.HandleFunc("POST /api/chats/{jid}/media", a.handleSendMedia)
	mux.HandleFunc("GET /api/media/{jid}/{id}", a.handleMedia)
	mux.HandleFunc("POST /api/logout", a.handleLogout)
	mux.HandleFunc("GET /api/avatar/{jid}", a.handleAvatar)
	mux.HandleFunc("GET /ws", a.handleWS)
	return a.cors(mux)
}

func (a *API) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", a.frontendOrigin)
		h.Set("Vary", "Origin")
		h.Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
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
	if err := a.st.ClearUnread(r.Context(), key); err != nil {
		log.Printf("clear unread %s: %v", key, err)
	}
	writeJSON(w, http.StatusOK, msgs)
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
	msg, err := a.mgr.SendText(r.Context(), jid, body.Text)
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

	msg, err := a.mgr.SendMedia(r.Context(), jid, data, detectMime(hdr, data), hdr.Filename, caption)
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

func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) {
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

func (a *API) handleWS(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{a.wsOriginPattern},
	})
	if err != nil {
		return
	}
	c := a.hub.Register(ws)
	for _, f := range a.mgr.InitialFrames() {
		a.hub.SendTo(c, f)
	}
	// Discard incoming data messages; exit when the connection closes.
	for {
		if _, _, err := ws.Read(r.Context()); err != nil {
			break
		}
	}
	a.hub.Unregister(c)
}
