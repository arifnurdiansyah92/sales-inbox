package main

import (
	"context"
	"database/sql"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver

	"sales-inbox/backend/internal/appdb"
)

// accountID scopes every query; single-tenant for now (SaaS prep).
const accountID = appdb.AccountID

const insertMessageSQL = `
INSERT INTO messages (account_id, chat_jid, id, sender_jid, sender_name, from_me, text, msg_type, ts, mime_type, file_name, file_size, raw_msg, status)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT (account_id, chat_jid, id) DO NOTHING`

const upsertChatSQL = `
INSERT INTO chats (account_id, jid, name, is_group, last_msg_text, last_msg_ts, unread_count)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (account_id, jid) DO UPDATE SET
  name = CASE WHEN excluded.name != '' THEN excluded.name ELSE chats.name END,
  last_msg_text = CASE WHEN excluded.last_msg_ts >= chats.last_msg_ts THEN excluded.last_msg_text ELSE chats.last_msg_text END,
  last_msg_ts   = GREATEST(chats.last_msg_ts, excluded.last_msg_ts),
  unread_count  = chats.unread_count + excluded.unread_count`

// All timestamps are unix milliseconds.
type Chat struct {
	JID           string `json:"jid"`
	Name          string `json:"name"`
	IsGroup       bool   `json:"isGroup"`
	LastMessage   string `json:"lastMessage"`
	LastMessageAt int64  `json:"lastMessageAt"`
	UnreadCount   int    `json:"unreadCount"`
}

type Message struct {
	ID         string `json:"id"`
	ChatJID    string `json:"chatJid"`
	SenderJID  string `json:"senderJid"`
	SenderName string `json:"senderName"`
	FromMe     bool   `json:"fromMe"`
	Text       string `json:"text"`
	Type       string `json:"type"`
	Timestamp  int64  `json:"timestamp"`
	MimeType   string `json:"mimeType"`
	FileName   string `json:"fileName"`
	FileSize   int64  `json:"fileSize"`
	HasMedia   bool   `json:"hasMedia"`
	Status     string `json:"status"` // ""|"sent"|"delivered"|"read" (from_me only)
	RawMsg     []byte `json:"-"`      // marshaled waE2E.Message, media messages only
}

// Message status column values: 0 none, 1 sent, 2 delivered, 3 read.
func statusToString(s int) string {
	switch s {
	case 1:
		return "sent"
	case 2:
		return "delivered"
	case 3:
		return "read"
	default:
		return ""
	}
}

func statusToInt(s string) int {
	switch s {
	case "sent":
		return 1
	case "delivered":
		return 2
	case "read":
		return 3
	default:
		return 0
	}
}

// dbtx is satisfied by both *sql.DB and *sql.Tx.
type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type Store struct {
	db *sql.DB
}

func OpenStore(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	if err := appdb.Init(context.Background(), db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// InsertMessage reports whether a new row was actually inserted.
func (s *Store) InsertMessage(ctx context.Context, q dbtx, m Message) (bool, error) {
	if q == nil {
		q = s.db
	}
	var raw any // NULL unless the message carries media
	if len(m.RawMsg) > 0 {
		raw = m.RawMsg
	}
	res, err := q.ExecContext(ctx, insertMessageSQL,
		accountID, m.ChatJID, m.ID, m.SenderJID, m.SenderName, m.FromMe, m.Text, m.Type, m.Timestamp,
		m.MimeType, m.FileName, m.FileSize, raw, statusToInt(m.Status))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) UpsertChat(ctx context.Context, q dbtx, jid, name string, isGroup bool, lastText string, lastTs int64, unreadInc int) error {
	if q == nil {
		q = s.db
	}
	_, err := q.ExecContext(ctx, upsertChatSQL, accountID, jid, name, isGroup, lastText, lastTs, unreadInc)
	return err
}

func (s *Store) GetChat(ctx context.Context, jid string) (Chat, bool, error) {
	var c Chat
	err := s.db.QueryRowContext(ctx,
		`SELECT jid, name, is_group, last_msg_text, last_msg_ts, unread_count FROM chats WHERE account_id=$1 AND jid=$2`,
		accountID, jid).
		Scan(&c.JID, &c.Name, &c.IsGroup, &c.LastMessage, &c.LastMessageAt, &c.UnreadCount)
	if err == sql.ErrNoRows {
		return Chat{}, false, nil
	}
	if err != nil {
		return Chat{}, false, err
	}
	return c, true, nil
}

func (s *Store) ListChats(ctx context.Context) ([]Chat, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT jid, name, is_group, last_msg_text, last_msg_ts, unread_count FROM chats WHERE account_id=$1 ORDER BY last_msg_ts DESC`,
		accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Chat, 0)
	for rows.Next() {
		var c Chat
		if err := rows.Scan(&c.JID, &c.Name, &c.IsGroup, &c.LastMessage, &c.LastMessageAt, &c.UnreadCount); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListMessages returns up to limit messages before the (beforeTs, beforeID)
// keyset cursor, in ascending order. beforeTs == 0 means "latest".
func (s *Store) ListMessages(ctx context.Context, chatJID string, limit int, beforeTs int64, beforeID string) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT chat_jid, id, sender_jid, sender_name, from_me, text, msg_type, ts,
       mime_type, file_name, file_size, status,
       raw_msg IS NOT NULL AND LENGTH(raw_msg) > 0
FROM messages
WHERE account_id=$1 AND chat_jid=$2 AND ($3::BIGINT=0 OR ts<$3 OR (ts=$3 AND id<$4))
ORDER BY ts DESC, id DESC
LIMIT $5`, accountID, chatJID, beforeTs, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Message, 0, limit)
	for rows.Next() {
		var m Message
		var status int
		if err := rows.Scan(&m.ChatJID, &m.ID, &m.SenderJID, &m.SenderName, &m.FromMe, &m.Text, &m.Type, &m.Timestamp,
			&m.MimeType, &m.FileName, &m.FileSize, &status, &m.HasMedia); err != nil {
			return nil, err
		}
		m.Status = statusToString(status)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// GetMessageMedia returns the stored raw proto and media metadata for one message.
func (s *Store) GetMessageMedia(ctx context.Context, chatJID, id string) (raw []byte, mimeType, fileName, msgType string, found bool, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT raw_msg, mime_type, file_name, msg_type FROM messages WHERE account_id=$1 AND chat_jid=$2 AND id=$3`,
		accountID, chatJID, id).
		Scan(&raw, &mimeType, &fileName, &msgType)
	if err == sql.ErrNoRows {
		return nil, "", "", "", false, nil
	}
	if err != nil {
		return nil, "", "", "", false, err
	}
	return raw, mimeType, fileName, msgType, true, nil
}

// UpdateMessageStatus upgrades an own message's delivery status (never
// downgrades, never touches incoming rows). Reports whether the row changed.
func (s *Store) UpdateMessageStatus(ctx context.Context, chatJID, id string, status int) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE messages SET status=$1 WHERE account_id=$2 AND chat_jid=$3 AND id=$4 AND from_me AND status<$1 AND status>0`,
		status, accountID, chatJID, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) ClearUnread(ctx context.Context, jid string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chats SET unread_count=0 WHERE account_id=$1 AND jid=$2`, accountID, jid)
	return err
}

func (s *Store) SetChatNameIfEmpty(ctx context.Context, jid, name string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chats SET name=$3 WHERE account_id=$1 AND jid=$2 AND name=''`, accountID, jid, name)
	return err
}

func (s *Store) Wipe(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM messages WHERE account_id=$1`, accountID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM chats WHERE account_id=$1`, accountID)
	return err
}
