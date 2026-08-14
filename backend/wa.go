package main

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	wstore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	historyChunkSize = 500
	qrExpiredMsg     = "Kode QR kedaluwarsa. Muat ulang halaman."
	avatarTTL        = time.Hour
)

var (
	errNotConnected   = errors.New("WhatsApp belum terhubung")
	errAvatarNotFound = errors.New("avatar tidak ditemukan")
	errMediaNotFound  = errors.New("Media tidak ditemukan")
	errMediaGone      = errors.New("Media tidak tersedia lagi")
	errMediaDownload  = errors.New("Gagal mengunduh media")
	avatarHTTP        = &http.Client{Timeout: 15 * time.Second}
)

type MeInfo struct {
	JID   string `json:"jid"`
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

type StatusPayload struct {
	Status string  `json:"status"`
	Me     *MeInfo `json:"me"`
	Error  *string `json:"error"`
}

type QRPayload struct {
	DataURL   string `json:"dataUrl"`
	TimeoutMs int64  `json:"timeoutMs"`
}

type avatarEntry struct {
	data     []byte
	notFound bool
	exp      time.Time
}

type ingestMsg struct {
	chatJID    string
	id         string
	senderJID  string
	senderName string
	fromMe     bool
	text       string
	msgType    string
	ts         int64
	isGroup    bool
	chatName   string
	mimeType   string
	fileName   string
	fileSize   int64
	raw        []byte // marshaled waE2E.Message, media messages only
	status     int    // 0 none, 1 sent, 2 delivered, 3 read
	adminID    int64  // acting admin, app-sent messages only (0 = none)
	adminName  string
}

func (im ingestMsg) message() Message {
	m := Message{
		ID: im.id, ChatJID: im.chatJID, SenderJID: im.senderJID, SenderName: im.senderName,
		FromMe: im.fromMe, Text: im.text, Type: im.msgType, Timestamp: im.ts,
		MimeType: im.mimeType, FileName: im.fileName, FileSize: im.fileSize,
		HasMedia: len(im.raw) > 0, Status: statusToString(im.status), RawMsg: im.raw,
		AdminName: im.adminName,
	}
	if im.adminID != 0 {
		id := im.adminID
		m.AdminID = &id
	}
	return m
}

// chatPreview is the chats.last_msg_text string for this message.
func (im ingestMsg) chatPreview() string {
	var label string
	switch im.msgType {
	case "image":
		label = "[Gambar]"
	case "video":
		label = "[Video]"
	case "document":
		label = "[Dokumen]"
	case "audio":
		label = "[Audio]"
	case "sticker":
		label = "[Stiker]"
	default:
		return im.text
	}
	if extra := firstNonEmpty(im.text, im.fileName); extra != "" {
		return label + " " + extra
	}
	return label
}

type ReceiptPayload struct {
	ChatJID string   `json:"chatJid"`
	IDs     []string `json:"ids"`
	Status  string   `json:"status"`
}

type Manager struct {
	st             *Store
	hub            *Hub
	container      *sqlstore.Container
	mediaDir       string
	agentSignature bool // prefix outgoing content with the admin's name

	mediaLocks sync.Map // cache key -> *sync.Mutex, guards concurrent downloads

	mu         sync.Mutex
	client     *whatsmeow.Client
	status     string // disconnected | connecting | waiting_qr | connected
	errMsg     string
	qrDataURL  string
	qrDeadline time.Time
	wsClients  int
	qrLooping  bool
	qrTimeouts int
	warmed     bool

	resetMu sync.Mutex // serializes logout/rebuild

	nameMu    sync.Mutex
	nameCache map[string]string

	avatarMu sync.Mutex
	avatars  map[string]avatarEntry

	histCh chan *events.HistorySync
}

func NewManager(ctx context.Context, dataDir string, st *Store, hub *Hub, agentSignature bool) (*Manager, error) {
	wstore.DeviceProps.Os = proto.String("Sales Inbox")
	wstore.DeviceProps.PlatformType = waCompanionReg.DeviceProps_DESKTOP.Enum()

	// Share the app store's Postgres pool. NewWithDB does not run migrations,
	// so Upgrade must be called explicitly to create the whatsmeow_* tables.
	container := sqlstore.NewWithDB(st.db, "postgres", waLog.Stdout("DB", "INFO", true))
	if err := container.Upgrade(ctx); err != nil {
		return nil, err
	}
	m := &Manager{
		st:             st,
		hub:            hub,
		container:      container,
		mediaDir:       filepath.Join(dataDir, "media"),
		agentSignature: agentSignature,
		status:         "disconnected",
		nameCache:      make(map[string]string),
		avatars:        make(map[string]avatarEntry),
		histCh:         make(chan *events.HistorySync, 32),
	}
	if err := m.buildClient(ctx); err != nil {
		return nil, err
	}
	go m.historyWorker()
	return m, nil
}

func (m *Manager) buildClient(ctx context.Context) error {
	device, err := m.container.GetFirstDevice(ctx)
	if err != nil {
		return err
	}
	cli := whatsmeow.NewClient(device, waLog.Stdout("WA", "INFO", true))
	cli.AddEventHandler(m.handleEvent)
	m.mu.Lock()
	m.client = cli
	m.warmed = false
	m.mu.Unlock()
	return nil
}

func (m *Manager) getClient() *whatsmeow.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.client
}

func (m *Manager) ownJID() types.JID {
	cli := m.getClient()
	if cli != nil && cli.Store.ID != nil {
		return cli.Store.ID.ToNonAD()
	}
	return types.EmptyJID
}

func (m *Manager) Start() {
	cli := m.getClient()
	if cli.Store.ID != nil {
		m.mu.Lock()
		m.status = "connecting"
		m.mu.Unlock()
		if err := cli.Connect(); err != nil {
			m.setDisconnected("Gagal terhubung: " + err.Error())
		}
	} else {
		m.mu.Lock()
		m.status = "waiting_qr"
		m.mu.Unlock()
	}
}

func (m *Manager) Shutdown() {
	cli := m.getClient()
	if cli != nil {
		cli.Disconnect()
	}
	if err := m.container.Close(); err != nil {
		log.Printf("close whatsmeow db: %v", err)
	}
}

func (m *Manager) statusPayloadLocked() StatusPayload {
	var me *MeInfo
	if m.client != nil && m.client.Store.ID != nil {
		id := m.client.Store.ID.ToNonAD()
		me = &MeInfo{JID: id.String(), Name: m.client.Store.PushName, Phone: id.User}
	}
	var errp *string
	if m.errMsg != "" {
		e := m.errMsg
		errp = &e
	}
	return StatusPayload{Status: m.status, Me: me, Error: errp}
}

func (m *Manager) StatusPayload() StatusPayload {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusPayloadLocked()
}

func (m *Manager) broadcastStatus() {
	m.hub.Broadcast(Frame{Type: "status", Data: m.StatusPayload()})
}

func (m *Manager) setDisconnected(msg string) {
	m.mu.Lock()
	m.status = "disconnected"
	m.errMsg = msg
	m.qrDataURL = ""
	m.qrDeadline = time.Time{}
	m.mu.Unlock()
	m.broadcastStatus()
}

// InitialFrames returns the frames pushed to every freshly connected WS client.
func (m *Manager) InitialFrames() []Frame {
	m.mu.Lock()
	defer m.mu.Unlock()
	frames := []Frame{{Type: "status", Data: m.statusPayloadLocked()}}
	if m.status == "waiting_qr" && m.qrDataURL != "" {
		remaining := time.Until(m.qrDeadline).Milliseconds()
		if remaining < 0 {
			remaining = 0
		}
		frames = append(frames, Frame{Type: "qr", Data: QRPayload{DataURL: m.qrDataURL, TimeoutMs: remaining}})
	}
	return frames
}

func (m *Manager) OnClientCountChange(n int) {
	m.mu.Lock()
	prev := m.wsClients
	m.wsClients = n
	start := false
	if n > prev {
		m.qrTimeouts = 0
		if m.client != nil && m.client.Store.ID == nil && !m.qrLooping {
			m.qrLooping = true
			start = true
		}
	}
	m.mu.Unlock()
	if start {
		go m.qrLoop()
	}
}

// qrLoop runs the pairing flow while at least one WS client is connected.
// Only one instance runs at a time (qrLooping flag).
func (m *Manager) qrLoop() {
	defer func() {
		m.mu.Lock()
		m.qrLooping = false
		m.mu.Unlock()
	}()
	for {
		cli := m.getClient()
		m.mu.Lock()
		if cli == nil || cli.Store.ID != nil || m.wsClients == 0 {
			m.mu.Unlock()
			return
		}
		m.status = "waiting_qr"
		m.errMsg = ""
		m.mu.Unlock()
		m.broadcastStatus()

		// GetQRChannel must be called before Connect.
		qrChan, err := cli.GetQRChannel(context.Background())
		if err != nil {
			m.setDisconnected("Gagal memulai sesi QR: " + err.Error())
			return
		}
		if err := cli.Connect(); err != nil {
			m.setDisconnected("Gagal terhubung: " + err.Error())
			return
		}

		terminal := ""
		for item := range qrChan {
			if item.Event == "code" {
				png, err := qrcode.Encode(item.Code, qrcode.Medium, 256)
				if err != nil {
					log.Printf("encode qr: %v", err)
					continue
				}
				dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
				m.mu.Lock()
				m.status = "waiting_qr"
				m.qrDataURL = dataURL
				m.qrDeadline = time.Now().Add(item.Timeout)
				m.mu.Unlock()
				m.hub.Broadcast(Frame{Type: "qr", Data: QRPayload{DataURL: dataURL, TimeoutMs: item.Timeout.Milliseconds()}})
			} else {
				terminal = item.Event
			}
		}
		m.mu.Lock()
		m.qrDataURL = ""
		m.qrDeadline = time.Time{}
		m.mu.Unlock()

		switch terminal {
		case "success":
			return // Connected event drives the rest
		case "timeout":
			cli.Disconnect()
			m.mu.Lock()
			m.qrTimeouts++
			giveUp := m.wsClients == 0 || m.qrTimeouts >= 10
			m.mu.Unlock()
			if giveUp {
				m.setDisconnected(qrExpiredMsg)
				return
			}
			time.Sleep(2 * time.Second)
		default:
			cli.Disconnect()
			m.setDisconnected("Sesi QR berakhir: " + terminal)
			return
		}
	}
}

func (m *Manager) handleEvent(evt any) {
	switch e := evt.(type) {
	case *events.Connected:
		m.mu.Lock()
		m.status = "connected"
		m.errMsg = ""
		m.qrDataURL = ""
		m.qrDeadline = time.Time{}
		m.qrTimeouts = 0
		warm := !m.warmed
		m.warmed = true
		cli := m.client
		m.mu.Unlock()
		m.broadcastStatus()
		if warm {
			go m.warmCaches(cli)
		}
	case *events.PairSuccess:
		log.Printf("pair success: %s", e.ID)
	case *events.Disconnected:
		m.mu.Lock()
		change := m.status == "connected"
		if change {
			m.status = "connecting" // whatsmeow auto-reconnects
		}
		m.mu.Unlock()
		if change {
			m.broadcastStatus()
		}
	case *events.LoggedOut:
		log.Printf("logged out by server")
		go m.resetAfterLogout()
	case *events.StreamReplaced:
		m.setDisconnected("Sesi digantikan oleh perangkat lain")
	case *events.Message:
		m.handleLiveMessage(e)
	case *events.Receipt:
		m.handleReceipt(e)
	case *events.HistorySync:
		m.histCh <- e
	}
}

// webStatusToInt maps a history-sync status to the messages.status column.
func webStatusToInt(st waWeb.WebMessageInfo_Status) int {
	switch st {
	case waWeb.WebMessageInfo_DELIVERY_ACK:
		return 2
	case waWeb.WebMessageInfo_READ, waWeb.WebMessageInfo_PLAYED:
		return 3
	default: // SERVER_ACK and anything unknown
		return 1
	}
}

func (m *Manager) handleReceipt(e *events.Receipt) {
	var newStatus int
	switch e.Type {
	case types.ReceiptTypeDelivered, types.ReceiptTypeSender:
		newStatus = 2
	case types.ReceiptTypeRead, types.ReceiptTypeReadSelf, types.ReceiptTypePlayed:
		newStatus = 3
	default:
		return
	}
	ctx := context.Background()
	chat := m.canonicalChat(ctx, e.Chat, e.SenderAlt)
	var updated []string
	for _, id := range e.MessageIDs {
		changed, err := m.st.UpdateMessageStatus(ctx, chat.String(), id, newStatus)
		if err != nil {
			log.Printf("receipt update %s/%s: %v", chat, id, err)
			continue
		}
		if changed {
			updated = append(updated, id)
		}
	}
	if len(updated) == 0 {
		return
	}
	status := "delivered"
	if newStatus == 3 {
		status = "read"
	}
	m.hub.Broadcast(Frame{Type: "receipt", Data: ReceiptPayload{ChatJID: chat.String(), IDs: updated, Status: status}})
}

func (m *Manager) warmCaches(cli *whatsmeow.Client) {
	ctx := context.Background()
	groups, err := cli.GetJoinedGroups(ctx)
	if err != nil {
		log.Printf("get joined groups: %v", err)
	}
	for _, g := range groups {
		jid := g.JID.ToNonAD()
		if g.Name == "" {
			continue
		}
		m.nameMu.Lock()
		m.nameCache[jid.String()] = g.Name
		m.nameMu.Unlock()
		if err := m.st.UpsertChat(ctx, nil, jid.String(), g.Name, true, "", 0, 0, false); err != nil {
			log.Printf("upsert group chat: %v", err)
		}
	}
	contacts, err := cli.Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		log.Printf("get all contacts: %v", err)
		return
	}
	m.nameMu.Lock()
	for jid, info := range contacts {
		name := firstNonEmpty(info.FullName, info.PushName, info.BusinessName)
		if name != "" {
			m.nameCache[jid.ToNonAD().String()] = name
		}
	}
	m.nameMu.Unlock()
}

// resetAfterLogout wipes the app DB and rebuilds a fresh device + client.
func (m *Manager) resetAfterLogout() {
	m.resetMu.Lock()
	defer m.resetMu.Unlock()
	ctx := context.Background()

	old := m.getClient()
	if old != nil {
		old.Disconnect()
		old.RemoveEventHandlers()
	}
	if err := m.st.Wipe(ctx); err != nil {
		log.Printf("wipe app db: %v", err)
	}
	m.nameMu.Lock()
	m.nameCache = make(map[string]string)
	m.nameMu.Unlock()
	m.avatarMu.Lock()
	m.avatars = make(map[string]avatarEntry)
	m.avatarMu.Unlock()

	if err := m.buildClient(ctx); err != nil {
		m.setDisconnected("Gagal menyiapkan sesi baru: " + err.Error())
		return
	}
	m.mu.Lock()
	m.status = "waiting_qr"
	m.errMsg = ""
	m.qrDataURL = ""
	m.qrDeadline = time.Time{}
	m.qrTimeouts = 0
	start := m.wsClients > 0 && !m.qrLooping
	if start {
		m.qrLooping = true
	}
	m.mu.Unlock()
	m.broadcastStatus()
	if start {
		go m.qrLoop()
	}
}

func (m *Manager) Logout(ctx context.Context) {
	cli := m.getClient()
	if cli != nil {
		if err := cli.Logout(ctx); err != nil {
			log.Printf("logout: %v (ignored)", err)
		}
	}
	m.resetAfterLogout()
}

// canonicalChat maps @lid chat JIDs to their phone-number JID where possible.
func (m *Manager) canonicalChat(ctx context.Context, chat, senderAlt types.JID) types.JID {
	chat = chat.ToNonAD()
	if chat.Server != types.HiddenUserServer {
		return chat
	}
	if senderAlt.Server == types.DefaultUserServer {
		return senderAlt.ToNonAD()
	}
	if pn, err := m.getClient().Store.LIDs.GetPNForLID(ctx, chat); err == nil && !pn.IsEmpty() {
		return pn.ToNonAD()
	}
	return chat
}

func (m *Manager) canonicalSender(ctx context.Context, sender, senderAlt types.JID) types.JID {
	sender = sender.ToNonAD()
	if sender.Server != types.HiddenUserServer {
		return sender
	}
	if senderAlt.Server == types.DefaultUserServer {
		return senderAlt.ToNonAD()
	}
	if pn, err := m.getClient().Store.LIDs.GetPNForLID(ctx, sender); err == nil && !pn.IsEmpty() {
		return pn.ToNonAD()
	}
	return sender
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// contactName returns the contact-store name for jid, or "" if unknown.
func (m *Manager) contactName(ctx context.Context, jid types.JID) string {
	key := jid.ToNonAD().String()
	m.nameMu.Lock()
	if n, ok := m.nameCache[key]; ok {
		m.nameMu.Unlock()
		return n
	}
	m.nameMu.Unlock()
	cli := m.getClient()
	if cli == nil {
		return ""
	}
	info, err := cli.Store.Contacts.GetContact(ctx, jid)
	if err != nil || !info.Found {
		return ""
	}
	name := firstNonEmpty(info.FullName, info.PushName, info.BusinessName)
	if name != "" {
		m.nameMu.Lock()
		m.nameCache[key] = name
		m.nameMu.Unlock()
	}
	return name
}

func (m *Manager) resolveName(ctx context.Context, jid types.JID, pushName string) string {
	if n := m.contactName(ctx, jid); n != "" {
		return n
	}
	if pushName != "" {
		return pushName
	}
	return jid.User
}

func (m *Manager) groupName(ctx context.Context, jid types.JID) string {
	key := jid.ToNonAD().String()
	m.nameMu.Lock()
	if n, ok := m.nameCache[key]; ok {
		m.nameMu.Unlock()
		return n
	}
	m.nameMu.Unlock()
	name := ""
	cli := m.getClient()
	if cli != nil {
		if info, err := cli.GetGroupInfo(ctx, jid); err == nil && info != nil {
			name = info.Name
		}
	}
	if name == "" {
		return jid.User
	}
	m.nameMu.Lock()
	m.nameCache[key] = name
	m.nameMu.Unlock()
	return name
}

// Message content that carries no user-visible chat entry.
var invisibleFields = map[string]bool{
	"senderKeyDistributionMessage": true,
	"messageContextInfo":           true,
	"protocolMessage":              true,
	"reactionMessage":              true,
	"pollUpdateMessage":            true,
	"keepInChatMessage":            true,
}

type msgContent struct {
	text     string // plain text, or caption for media
	msgType  string
	mimeType string
	fileName string
	fileSize int64
	media    bool
}

// extractContent returns the storable content of the message, or ok=false when
// the message should be skipped entirely (protocol/reaction/etc.).
func extractContent(msg *waE2E.Message) (msgContent, bool) {
	if msg == nil {
		return msgContent{}, false
	}
	if t := msg.GetConversation(); t != "" {
		return msgContent{text: t, msgType: "text"}, true
	}
	if t := msg.GetExtendedTextMessage().GetText(); t != "" {
		return msgContent{text: t, msgType: "text"}, true
	}
	if im := msg.GetImageMessage(); im != nil {
		return msgContent{text: im.GetCaption(), msgType: "image",
			mimeType: im.GetMimetype(), fileSize: int64(im.GetFileLength()), media: true}, true
	}
	if vm := msg.GetVideoMessage(); vm != nil {
		return msgContent{text: vm.GetCaption(), msgType: "video",
			mimeType: vm.GetMimetype(), fileSize: int64(vm.GetFileLength()), media: true}, true
	}
	if dm := msg.GetDocumentMessage(); dm != nil {
		return msgContent{text: dm.GetCaption(), msgType: "document", fileName: dm.GetFileName(),
			mimeType: dm.GetMimetype(), fileSize: int64(dm.GetFileLength()), media: true}, true
	}
	if am := msg.GetAudioMessage(); am != nil {
		return msgContent{msgType: "audio",
			mimeType: am.GetMimetype(), fileSize: int64(am.GetFileLength()), media: true}, true
	}
	if sm := msg.GetStickerMessage(); sm != nil {
		return msgContent{msgType: "sticker",
			mimeType: sm.GetMimetype(), fileSize: int64(sm.GetFileLength()), media: true}, true
	}
	visible := false
	msg.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		if !invisibleFields[fd.JSONName()] {
			visible = true
			return false
		}
		return true
	})
	if !visible {
		return msgContent{}, false
	}
	return msgContent{text: "[Pesan tidak didukung]", msgType: "unknown"}, true
}

// marshalRawIfMedia serializes msg for later re-download, media messages only.
func marshalRawIfMedia(msg *waE2E.Message, media bool) []byte {
	if !media || msg == nil {
		return nil
	}
	raw, err := proto.Marshal(msg)
	if err != nil || len(raw) == 0 {
		return nil
	}
	return raw
}

// ingest inserts the message (idempotent) and upserts the chat row.
// Shared by live events, history sync and the send path.
func (m *Manager) ingest(ctx context.Context, q dbtx, im ingestMsg, live bool) (bool, error) {
	inserted, err := m.st.InsertMessage(ctx, q, im.message())
	if err != nil {
		return false, err
	}
	unread := 0
	if live && !im.fromMe && inserted {
		unread = 1
	}
	// Live incoming messages reopen a resolved chat; history sync never
	// touches status.
	reopen := live && !im.fromMe
	if err := m.st.UpsertChat(ctx, q, im.chatJID, im.chatName, im.isGroup, im.chatPreview(), im.ts, unread, reopen); err != nil {
		return inserted, err
	}
	return inserted, nil
}

func (m *Manager) ingestAndBroadcast(ctx context.Context, im ingestMsg) (Message, bool) {
	inserted, err := m.ingest(ctx, nil, im, true)
	if err != nil {
		log.Printf("ingest: %v", err)
		return Message{}, false
	}
	msg := im.message()
	if inserted {
		m.hub.Broadcast(Frame{Type: "message", Data: msg})
		if chat, ok, err := m.st.GetChat(ctx, im.chatJID); err == nil && ok {
			m.hub.Broadcast(Frame{Type: "chat_upsert", Data: chat})
		}
	}
	return msg, inserted
}

func (m *Manager) handleLiveMessage(e *events.Message) {
	ctx := context.Background()
	info := e.Info
	if info.Chat.Server == types.BroadcastServer || info.Chat.Server == types.NewsletterServer {
		return
	}
	content, ok := extractContent(e.Message)
	if !ok {
		return
	}
	chat := m.canonicalChat(ctx, info.Chat, info.SenderAlt)
	isGroup := chat.Server == types.GroupServer

	var senderJID types.JID
	senderName := ""
	if info.IsFromMe {
		senderJID = m.ownJID()
	} else {
		senderJID = m.canonicalSender(ctx, info.Sender, info.SenderAlt)
		senderName = m.resolveName(ctx, senderJID, info.PushName)
	}

	var chatName string
	switch {
	case isGroup:
		chatName = m.groupName(ctx, chat)
	case info.IsFromMe:
		chatName = m.resolveName(ctx, chat, "")
	default:
		chatName = m.resolveName(ctx, chat, info.PushName)
	}

	status := 0
	if info.IsFromMe {
		status = 1
	}
	m.ingestAndBroadcast(ctx, ingestMsg{
		chatJID: chat.String(), id: info.ID, senderJID: senderJID.String(), senderName: senderName,
		fromMe: info.IsFromMe, text: content.text, msgType: content.msgType, ts: info.Timestamp.UnixMilli(),
		isGroup: isGroup, chatName: chatName,
		mimeType: content.mimeType, fileName: content.fileName, fileSize: content.fileSize,
		raw: marshalRawIfMedia(e.Message, content.media), status: status,
	})
}

// signContent prepends "*<admin name>*:\n" when the agent signature is on.
// The result is what WhatsApp shows, and what the DB stores.
func (m *Manager) signContent(admin Admin, text string) string {
	if !m.agentSignature || admin.Name == "" {
		return text
	}
	return "*" + admin.Name + "*:\n" + text
}

func (m *Manager) SendText(ctx context.Context, to types.JID, text string, admin Admin) (Message, error) {
	m.mu.Lock()
	cli := m.client
	connected := m.status == "connected"
	m.mu.Unlock()
	if !connected || cli == nil {
		return Message{}, errNotConnected
	}
	text = m.signContent(admin, text)
	resp, err := cli.SendMessage(ctx, to, &waE2E.Message{Conversation: proto.String(text)})
	if err != nil {
		return Message{}, err
	}
	chat := m.canonicalChat(ctx, to, types.EmptyJID)
	isGroup := chat.Server == types.GroupServer
	var chatName string
	if isGroup {
		chatName = m.groupName(ctx, chat)
	} else {
		chatName = m.resolveName(ctx, chat, "")
	}
	// live=true broadcast so other admin tabs see it; the later self-echo
	// event dedupes via RowsAffected==0.
	msg, _ := m.ingestAndBroadcast(ctx, ingestMsg{
		chatJID: chat.String(), id: resp.ID, senderJID: m.ownJID().String(), senderName: "",
		fromMe: true, text: text, msgType: "text", ts: resp.Timestamp.UnixMilli(),
		isGroup: isGroup, chatName: chatName, status: 1,
		adminID: admin.ID, adminName: admin.Name,
	})
	return msg, nil
}

// SendMedia uploads data and sends it as an image, video or document message.
func (m *Manager) SendMedia(ctx context.Context, to types.JID, data []byte, mimeType, fileName, caption string, admin Admin) (Message, error) {
	m.mu.Lock()
	cli := m.client
	connected := m.status == "connected"
	m.mu.Unlock()
	if !connected || cli == nil {
		return Message{}, errNotConnected
	}
	if caption != "" {
		// Signature only when there is a caption to carry it.
		caption = m.signContent(admin, caption)
	}

	var mediaType whatsmeow.MediaType
	var msgType string
	switch mimeType {
	case "image/jpeg", "image/png", "image/webp":
		mediaType, msgType = whatsmeow.MediaImage, "image"
	case "video/mp4":
		mediaType, msgType = whatsmeow.MediaVideo, "video"
	default: // incl. image/gif and audio/* — sent as documents
		mediaType, msgType = whatsmeow.MediaDocument, "document"
	}

	resp, err := cli.Upload(ctx, data, mediaType)
	if err != nil {
		return Message{}, err
	}

	wmsg := &waE2E.Message{}
	rowFileName := ""
	switch msgType {
	case "image":
		wmsg.ImageMessage = &waE2E.ImageMessage{
			Caption: proto.String(caption), Mimetype: proto.String(mimeType),
			URL: proto.String(resp.URL), DirectPath: proto.String(resp.DirectPath),
			MediaKey: resp.MediaKey, FileEncSHA256: resp.FileEncSHA256, FileSHA256: resp.FileSHA256,
			FileLength: proto.Uint64(resp.FileLength),
		}
	case "video":
		wmsg.VideoMessage = &waE2E.VideoMessage{
			Caption: proto.String(caption), Mimetype: proto.String(mimeType),
			URL: proto.String(resp.URL), DirectPath: proto.String(resp.DirectPath),
			MediaKey: resp.MediaKey, FileEncSHA256: resp.FileEncSHA256, FileSHA256: resp.FileSHA256,
			FileLength: proto.Uint64(resp.FileLength),
		}
	default:
		rowFileName = fileName
		dm := &waE2E.DocumentMessage{
			FileName: proto.String(fileName), Title: proto.String(fileName),
			Mimetype: proto.String(mimeType),
			URL:      proto.String(resp.URL), DirectPath: proto.String(resp.DirectPath),
			MediaKey: resp.MediaKey, FileEncSHA256: resp.FileEncSHA256, FileSHA256: resp.FileSHA256,
			FileLength: proto.Uint64(resp.FileLength),
		}
		if caption != "" {
			dm.Caption = proto.String(caption)
		}
		wmsg.DocumentMessage = dm
	}

	sendResp, err := cli.SendMessage(ctx, to, wmsg)
	if err != nil {
		return Message{}, err
	}

	chat := m.canonicalChat(ctx, to, types.EmptyJID)
	isGroup := chat.Server == types.GroupServer
	var chatName string
	if isGroup {
		chatName = m.groupName(ctx, chat)
	} else {
		chatName = m.resolveName(ctx, chat, "")
	}

	// Seed the disk cache so GET /api/media serves instantly without redownload.
	m.writeMediaCache(chat.String(), sendResp.ID, data)

	msg, _ := m.ingestAndBroadcast(ctx, ingestMsg{
		chatJID: chat.String(), id: sendResp.ID, senderJID: m.ownJID().String(), senderName: "",
		fromMe: true, text: caption, msgType: msgType, ts: sendResp.Timestamp.UnixMilli(),
		isGroup: isGroup, chatName: chatName,
		mimeType: mimeType, fileName: rowFileName, fileSize: int64(len(data)),
		raw: marshalRawIfMedia(wmsg, true), status: 1,
		adminID: admin.ID, adminName: admin.Name,
	})
	return msg, nil
}

// Health reports the WhatsApp side of /healthz: whether the client is
// currently connected and whether a device is paired.
func (m *Manager) Health() (connected, loggedIn bool) {
	m.mu.Lock()
	cli := m.client
	m.mu.Unlock()
	if cli == nil {
		return false, false
	}
	return cli.IsConnected(), cli.Store.ID != nil
}

func (m *Manager) historyWorker() {
	for evt := range m.histCh {
		m.processHistorySync(context.Background(), evt)
	}
}

func (m *Manager) processHistorySync(ctx context.Context, evt *events.HistorySync) {
	data := evt.Data
	switch data.GetSyncType() {
	case waHistorySync.HistorySync_PUSH_NAME:
		for _, pn := range data.GetPushnames() {
			jid, err := types.ParseJID(pn.GetID())
			if err != nil || jid.Server != types.DefaultUserServer {
				continue
			}
			if name := pn.GetPushname(); name != "" {
				if err := m.st.SetChatNameIfEmpty(ctx, jid.ToNonAD().String(), name); err != nil {
					log.Printf("history pushname: %v", err)
				}
			}
		}
	case waHistorySync.HistorySync_INITIAL_BOOTSTRAP, waHistorySync.HistorySync_RECENT, waHistorySync.HistorySync_ON_DEMAND:
		for _, conv := range data.GetConversations() {
			m.processConversation(ctx, conv)
		}
	}
}

func (m *Manager) processConversation(ctx context.Context, conv *waHistorySync.Conversation) {
	cli := m.getClient()
	if cli == nil {
		return
	}
	chatJID, err := types.ParseJID(conv.GetID())
	if err != nil {
		return
	}
	if chatJID.Server == types.HiddenUserServer && conv.GetPnJID() != "" {
		if pn, err := types.ParseJID(conv.GetPnJID()); err == nil && !pn.IsEmpty() {
			chatJID = pn
		}
	}
	chat := m.canonicalChat(ctx, chatJID, types.EmptyJID)
	if chat.Server == types.BroadcastServer || chat.Server == types.NewsletterServer {
		return
	}
	isGroup := chat.Server == types.GroupServer
	chatName := conv.GetName()
	if chatName == "" && !isGroup {
		// Contact store only; leave empty otherwise so PUSH_NAME sync can fill it.
		chatName = m.contactName(ctx, chat)
	}

	msgs := conv.GetMessages()
	changed := false
	for start := 0; start < len(msgs); start += historyChunkSize {
		end := min(start+historyChunkSize, len(msgs))
		chunkChanged, err := m.ingestHistoryChunk(ctx, cli, chat, isGroup, chatName, msgs[start:end])
		if err != nil {
			log.Printf("history chunk %s: %v", chat, err)
			break
		}
		if chunkChanged {
			changed = true
		}
	}
	if changed {
		if row, ok, err := m.st.GetChat(ctx, chat.String()); err == nil && ok {
			m.hub.Broadcast(Frame{Type: "chat_upsert", Data: row})
		}
	}
}

func (m *Manager) ingestHistoryChunk(ctx context.Context, cli *whatsmeow.Client, chat types.JID, isGroup bool, chatName string, msgs []*waHistorySync.HistorySyncMsg) (bool, error) {
	tx, err := m.st.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	changed := false
	for _, hmsg := range msgs {
		evtMsg, err := cli.ParseWebMessage(chat, hmsg.GetMessage())
		if err != nil {
			continue
		}
		evtMsg = evtMsg.UnwrapRaw()
		content, ok := extractContent(evtMsg.Message)
		if !ok {
			continue
		}
		info := evtMsg.Info
		var senderJID types.JID
		senderName := ""
		if info.IsFromMe {
			senderJID = m.ownJID()
		} else {
			senderJID = m.canonicalSender(ctx, info.Sender, info.SenderAlt)
			senderName = m.resolveName(ctx, senderJID, info.PushName)
		}
		status := 0
		if info.IsFromMe {
			status = webStatusToInt(evtMsg.SourceWebMsg.GetStatus())
		}
		ins, err := m.ingest(ctx, tx, ingestMsg{
			chatJID: chat.String(), id: info.ID, senderJID: senderJID.String(), senderName: senderName,
			fromMe: info.IsFromMe, text: content.text, msgType: content.msgType, ts: info.Timestamp.UnixMilli(),
			isGroup: isGroup, chatName: chatName,
			mimeType: content.mimeType, fileName: content.fileName, fileSize: content.fileSize,
			raw: marshalRawIfMedia(evtMsg.Message, content.media), status: status,
		}, false)
		if err != nil {
			return changed, err
		}
		if ins {
			changed = true
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return changed, nil
}

func (m *Manager) Avatar(ctx context.Context, jid types.JID) ([]byte, error) {
	key := jid.ToNonAD().String()
	now := time.Now()
	m.avatarMu.Lock()
	if e, ok := m.avatars[key]; ok && now.Before(e.exp) {
		m.avatarMu.Unlock()
		if e.notFound {
			return nil, errAvatarNotFound
		}
		return e.data, nil
	}
	m.avatarMu.Unlock()

	fail := func() ([]byte, error) {
		m.avatarMu.Lock()
		m.avatars[key] = avatarEntry{notFound: true, exp: now.Add(avatarTTL)}
		m.avatarMu.Unlock()
		return nil, errAvatarNotFound
	}

	cli := m.getClient()
	if cli == nil {
		return nil, errAvatarNotFound
	}
	info, err := cli.GetProfilePictureInfo(ctx, jid, &whatsmeow.GetProfilePictureParams{Preview: true})
	if err != nil || info == nil || info.URL == "" {
		return fail()
	}
	resp, err := avatarHTTP.Get(info.URL)
	if err != nil {
		return fail()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fail()
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return fail()
	}
	m.avatarMu.Lock()
	m.avatars[key] = avatarEntry{data: data, exp: now.Add(avatarTTL)}
	m.avatarMu.Unlock()
	return data, nil
}

// sanitizeCacheName keeps [A-Za-z0-9.@-]; everything else becomes '_'.
func sanitizeCacheName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '@', r == '-':
			return r
		default:
			return '_'
		}
	}, s)
}

func (m *Manager) mediaCachePath(chatJID, id string) string {
	return filepath.Join(m.mediaDir, sanitizeCacheName(chatJID), sanitizeCacheName(id))
}

func (m *Manager) writeMediaCache(chatJID, id string, data []byte) bool {
	path := m.mediaCachePath(chatJID, id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("media cache mkdir: %v", err)
		return false
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		log.Printf("media cache write: %v", err)
		return false
	}
	return true
}

func (m *Manager) mediaLock(key string) *sync.Mutex {
	v, _ := m.mediaLocks.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// MediaFile returns the path of the cached media file for one message,
// downloading it from WhatsApp first when needed.
func (m *Manager) MediaFile(ctx context.Context, chatJID, id string) (path, mimeType, fileName, msgType string, err error) {
	raw, mimeType, fileName, msgType, found, err := m.st.GetMessageMedia(ctx, chatJID, id)
	if err != nil {
		return "", "", "", "", err
	}
	if !found || len(raw) == 0 {
		return "", "", "", "", errMediaNotFound
	}

	path = m.mediaCachePath(chatJID, id)
	lock := m.mediaLock(chatJID + "/" + id)
	lock.Lock()
	defer lock.Unlock()

	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		return path, mimeType, fileName, msgType, nil
	}

	cli := m.getClient()
	if cli == nil {
		return "", "", "", "", errMediaDownload
	}
	var wmsg waE2E.Message
	if err := proto.Unmarshal(raw, &wmsg); err != nil {
		log.Printf("media unmarshal %s/%s: %v", chatJID, id, err)
		return "", "", "", "", errMediaNotFound
	}
	data, err := cli.DownloadAny(ctx, &wmsg)
	if err != nil {
		if errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith404) ||
			errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith410) ||
			errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith403) ||
			errors.Is(err, whatsmeow.ErrNoURLPresent) ||
			errors.Is(err, whatsmeow.ErrNothingDownloadableFound) {
			return "", "", "", "", errMediaGone
		}
		log.Printf("media download %s/%s: %v", chatJID, id, err)
		return "", "", "", "", errMediaDownload
	}
	if !m.writeMediaCache(chatJID, id, data) {
		return "", "", "", "", errMediaDownload
	}
	return path, mimeType, fileName, msgType, nil
}
