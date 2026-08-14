// Package appdb defines the application's Postgres schema. It is shared by
// the server (package main) and cmd/migrate, which cannot import package main.
package appdb

import (
	"context"
	"database/sql"
)

// AccountID scopes every application row. Single-tenant for now; the column
// exists so a future SaaS setup only has to start handing out new IDs.
const AccountID = 1

const Schema = `
CREATE TABLE IF NOT EXISTS chats (
  account_id BIGINT NOT NULL DEFAULT 1,
  jid TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  is_group BOOLEAN NOT NULL DEFAULT FALSE,
  last_msg_text TEXT NOT NULL DEFAULT '',
  last_msg_ts BIGINT NOT NULL DEFAULT 0,
  unread_count INT NOT NULL DEFAULT 0,
  PRIMARY KEY (account_id, jid)
);
CREATE TABLE IF NOT EXISTS messages (
  account_id BIGINT NOT NULL DEFAULT 1,
  chat_jid TEXT NOT NULL,
  id TEXT NOT NULL,
  sender_jid TEXT NOT NULL,
  sender_name TEXT NOT NULL DEFAULT '',
  from_me BOOLEAN NOT NULL,
  text TEXT NOT NULL,
  msg_type TEXT NOT NULL DEFAULT 'text',
  ts BIGINT NOT NULL,
  mime_type TEXT NOT NULL DEFAULT '',
  file_name TEXT NOT NULL DEFAULT '',
  file_size BIGINT NOT NULL DEFAULT 0,
  raw_msg BYTEA,
  status SMALLINT NOT NULL DEFAULT 0,
  PRIMARY KEY (account_id, chat_jid, id)
);
CREATE INDEX IF NOT EXISTS idx_messages_acct_chat_ts_id ON messages(account_id, chat_jid, ts, id);
CREATE TABLE IF NOT EXISTS admins (
  id BIGSERIAL PRIMARY KEY,
  account_id BIGINT NOT NULL DEFAULT 1,
  username TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  is_owner BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS sessions (
  token TEXT PRIMARY KEY,
  data BYTEA NOT NULL,
  expiry TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_expiry_idx ON sessions (expiry);
ALTER TABLE messages ADD COLUMN IF NOT EXISTS admin_id BIGINT;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS admin_name TEXT NOT NULL DEFAULT '';
ALTER TABLE chats ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'open';
ALTER TABLE chats ADD COLUMN IF NOT EXISTS pinned BOOLEAN NOT NULL DEFAULT FALSE;
`

// Init creates the app tables and index if they do not exist.
func Init(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, Schema)
	return err
}
