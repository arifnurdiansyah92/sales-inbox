// Command migrate copies a sales-inbox SQLite data set (app.db + whatsmeow.db)
// into the Postgres database pointed to by POSTGRES_DSN.
//
// The whatsmeow_* target tables are always replaced (a session copy on top of
// existing rows would be corrupt); -wipe additionally clears the app tables.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver
	"github.com/joho/godotenv"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
	_ "modernc.org/sqlite"

	"sales-inbox/backend/internal/appdb"
)

const batchSize = 1000

var identRe = regexp.MustCompile(`^[a-z0-9_]+$`)

func main() {
	log.SetFlags(0)
	sqliteDir := flag.String("sqlite-dir", filepath.Join(os.Getenv("LOCALAPPDATA"), "sales-inbox"),
		"directory containing app.db and whatsmeow.db")
	wipe := flag.Bool("wipe", true, "clear the Postgres app tables (chats, messages) before copying")
	flag.Parse()

	if err := run(context.Background(), *sqliteDir, *wipe); err != nil {
		log.Fatalf("migrate: %v", err)
	}
}

func run(ctx context.Context, sqliteDir string, wipe bool) error {
	// .env in the working directory is optional; a missing file is not an error.
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("load .env: %w", err)
	}
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		return errors.New("POSTGRES_DSN is required (set it in the environment or .env)")
	}

	pg, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open postgres: %w", err)
	}
	defer pg.Close()
	pg.SetMaxOpenConns(10)
	if err := pg.PingContext(ctx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}

	// (a) app schema.
	if err := appdb.Init(ctx, pg); err != nil {
		return fmt.Errorf("init app schema: %w", err)
	}
	// (b) whatsmeow schema. NewWithDB does not run migrations; Upgrade does.
	// The container is never closed here: closing it would close pg.
	container := sqlstore.NewWithDB(pg, "postgres", waLog.Stdout("DB", "WARN", true))
	if err := container.Upgrade(ctx); err != nil {
		return fmt.Errorf("upgrade whatsmeow schema: %w", err)
	}

	if wipe {
		for _, table := range []string{"messages", "chats"} {
			if _, err := pg.ExecContext(ctx, `DELETE FROM `+table); err != nil {
				return fmt.Errorf("wipe %s: %w", table, err)
			}
		}
		log.Printf("wiped app tables (messages, chats)")
	}

	// (c) app data.
	appSrc, err := openSQLite(filepath.Join(sqliteDir, "app.db"))
	if err != nil {
		return fmt.Errorf("open app.db: %w", err)
	}
	defer appSrc.Close()
	nChats, err := copyChats(ctx, appSrc, pg)
	if err != nil {
		return fmt.Errorf("copy chats: %w", err)
	}
	log.Printf("chats: %d rows copied", nChats)
	nMsgs, err := copyMessages(ctx, appSrc, pg)
	if err != nil {
		return fmt.Errorf("copy messages: %w", err)
	}
	log.Printf("messages: %d rows copied", nMsgs)

	// (d) whatsmeow session.
	waSrc, err := openSQLite(filepath.Join(sqliteDir, "whatsmeow.db"))
	if err != nil {
		return fmt.Errorf("open whatsmeow.db: %w", err)
	}
	defer waSrc.Close()
	if err := copyWhatsmeow(ctx, waSrc, pg); err != nil {
		return fmt.Errorf("copy whatsmeow session: %w", err)
	}

	// (e) verify.
	var devices, pgChats, pgMsgs int64
	if err := pg.QueryRowContext(ctx, `SELECT count(*) FROM whatsmeow_device`).Scan(&devices); err != nil {
		return fmt.Errorf("count devices: %w", err)
	}
	if devices != 1 {
		return fmt.Errorf("expected exactly 1 whatsmeow_device row, found %d", devices)
	}
	if err := pg.QueryRowContext(ctx, `SELECT count(*) FROM chats`).Scan(&pgChats); err != nil {
		return fmt.Errorf("count chats: %w", err)
	}
	if err := pg.QueryRowContext(ctx, `SELECT count(*) FROM messages`).Scan(&pgMsgs); err != nil {
		return fmt.Errorf("count messages: %w", err)
	}
	log.Printf("verified: 1 whatsmeow_device row, %d chats, %d messages in Postgres", pgChats, pgMsgs)
	return nil
}

func openSQLite(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// flushBatch inserts one batch inside a transaction and reports how many rows
// were actually inserted (ON CONFLICT DO NOTHING skips count as 0).
func flushBatch(ctx context.Context, pg *sql.DB, insertSQL string, batch [][]any) (int64, error) {
	if len(batch) == 0 {
		return 0, nil
	}
	tx, err := pg.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, insertSQL)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	var n int64
	for _, args := range batch {
		res, err := stmt.ExecContext(ctx, args...)
		if err != nil {
			return 0, err
		}
		if a, err := res.RowsAffected(); err == nil {
			n += a
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}

func copyChats(ctx context.Context, src, pg *sql.DB) (int64, error) {
	const insertSQL = `
INSERT INTO chats (account_id, jid, name, is_group, last_msg_text, last_msg_ts, unread_count)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (account_id, jid) DO NOTHING`
	rows, err := src.QueryContext(ctx,
		`SELECT jid, name, is_group, last_msg_text, last_msg_ts, unread_count FROM chats`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var total int64
	batch := make([][]any, 0, batchSize)
	for rows.Next() {
		var jid, name, lastText string
		var isGroup, lastTs, unread int64
		if err := rows.Scan(&jid, &name, &isGroup, &lastText, &lastTs, &unread); err != nil {
			return total, err
		}
		batch = append(batch, []any{appdb.AccountID, jid, name, isGroup != 0, lastText, lastTs, unread})
		if len(batch) == batchSize {
			n, err := flushBatch(ctx, pg, insertSQL, batch)
			if err != nil {
				return total, err
			}
			total += n
			batch = batch[:0]
		}
	}
	if err := rows.Err(); err != nil {
		return total, err
	}
	n, err := flushBatch(ctx, pg, insertSQL, batch)
	return total + n, err
}

func copyMessages(ctx context.Context, src, pg *sql.DB) (int64, error) {
	const insertSQL = `
INSERT INTO messages (account_id, chat_jid, id, sender_jid, sender_name, from_me, text, msg_type, ts, mime_type, file_name, file_size, raw_msg, status)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT (account_id, chat_jid, id) DO NOTHING`
	rows, err := src.QueryContext(ctx, `
SELECT chat_jid, id, sender_jid, sender_name, from_me, text, msg_type, ts,
       mime_type, file_name, file_size, raw_msg, status
FROM messages`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var total int64
	batch := make([][]any, 0, batchSize)
	for rows.Next() {
		var chatJID, id, senderJID, senderName, text, msgType, mimeType, fileName string
		var fromMe, ts, fileSize, status int64
		var raw []byte
		if err := rows.Scan(&chatJID, &id, &senderJID, &senderName, &fromMe, &text, &msgType, &ts,
			&mimeType, &fileName, &fileSize, &raw, &status); err != nil {
			return total, err
		}
		batch = append(batch, []any{appdb.AccountID, chatJID, id, senderJID, senderName, fromMe != 0,
			text, msgType, ts, mimeType, fileName, fileSize, raw, status})
		if len(batch) == batchSize {
			n, err := flushBatch(ctx, pg, insertSQL, batch)
			if err != nil {
				return total, err
			}
			total += n
			batch = batch[:0]
		}
	}
	if err := rows.Err(); err != nil {
		return total, err
	}
	n, err := flushBatch(ctx, pg, insertSQL, batch)
	return total + n, err
}

// copyWhatsmeow replaces the whatsmeow_* rows in Postgres with the contents of
// the SQLite session store. Everything runs in ONE transaction with
// session_replication_role=replica (requires superuser) so foreign-key
// ordering between the tables does not matter.
func copyWhatsmeow(ctx context.Context, src, pg *sql.DB) error {
	rows, err := src.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'whatsmeow_%' ORDER BY name`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		if name == "whatsmeow_version" { // Upgrade already populated the Postgres one
			continue
		}
		if !identRe.MatchString(name) {
			rows.Close()
			return fmt.Errorf("unexpected table name %q", name)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := pg.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		return fmt.Errorf("set replica role (superuser required): %w", err)
	}
	for _, table := range tables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM "`+table+`"`); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	for _, table := range tables {
		n, err := copyWhatsmeowTable(ctx, tx, src, pg, table)
		if err != nil {
			return err
		}
		log.Printf("%s: %d rows copied", table, n)
	}
	return tx.Commit()
}

// copyWhatsmeowTable copies one table generically: column names come from the
// SQLite result set, and SQLite 0/1 integers are converted to bool wherever
// the Postgres column type is boolean.
func copyWhatsmeowTable(ctx context.Context, tx *sql.Tx, src, pg *sql.DB, table string) (int64, error) {
	pgCols, err := pgColumnTypes(ctx, pg, table)
	if err != nil {
		return 0, err
	}
	if len(pgCols) == 0 {
		return 0, fmt.Errorf("table %s does not exist in the Postgres schema", table)
	}

	rows, err := src.QueryContext(ctx, `SELECT * FROM "`+table+`"`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	isBool := make([]bool, len(cols))
	quoted := make([]string, len(cols))
	placeholders := make([]string, len(cols))
	for i, c := range cols {
		typ, ok := pgCols[c]
		if !ok {
			return 0, fmt.Errorf("column %s.%s is missing in the Postgres schema", table, c)
		}
		isBool[i] = typ == "boolean"
		quoted[i] = `"` + c + `"`
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	stmt, err := tx.PrepareContext(ctx, fmt.Sprintf(`INSERT INTO "%s" (%s) VALUES (%s)`,
		table, strings.Join(quoted, ","), strings.Join(placeholders, ",")))
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var n int64
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return n, err
		}
		for i := range vals {
			if !isBool[i] {
				continue
			}
			switch v := vals[i].(type) {
			case nil, bool:
			case int64:
				vals[i] = v != 0
			default:
				return n, fmt.Errorf("%s.%s: cannot convert %T to boolean", table, cols[i], v)
			}
		}
		if _, err := stmt.ExecContext(ctx, vals...); err != nil {
			return n, fmt.Errorf("insert into %s: %w", table, err)
		}
		n++
	}
	return n, rows.Err()
}

func pgColumnTypes(ctx context.Context, pg *sql.DB, table string) (map[string]string, error) {
	rows, err := pg.QueryContext(ctx, `
SELECT column_name, data_type FROM information_schema.columns
WHERE table_schema='public' AND table_name=$1`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return nil, err
		}
		out[name] = typ
	}
	return out, rows.Err()
}
