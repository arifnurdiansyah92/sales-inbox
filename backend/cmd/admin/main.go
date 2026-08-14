// Command admin manages the admins table: it upserts one admin account
// (-username/-name/-password/-owner) or lists the existing ones (-list).
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

	"github.com/alexedwards/argon2id"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver
	"github.com/joho/godotenv"

	"sales-inbox/backend/internal/appdb"
)

func main() {
	log.SetFlags(0)
	username := flag.String("username", "", "login username (required unless -list)")
	name := flag.String("name", "", "display name shown to customers (required unless -list)")
	password := flag.String("password", "", "login password (required unless -list)")
	owner := flag.Bool("owner", false, "grant owner rights (WhatsApp disconnect)")
	list := flag.Bool("list", false, "list existing admins instead of upserting")
	flag.Parse()

	if err := run(context.Background(), *username, *name, *password, *owner, *list); err != nil {
		log.Fatalf("admin: %v", err)
	}
}

func run(ctx context.Context, username, name, password string, owner, list bool) error {
	// .env in the working directory is optional; a missing file is not an error.
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("load .env: %w", err)
	}
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		return errors.New("POSTGRES_DSN is required (set it in the environment or .env)")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open postgres: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}
	if err := appdb.Init(ctx, db); err != nil {
		return fmt.Errorf("init app schema: %w", err)
	}

	if list {
		return listAdmins(ctx, db)
	}
	if username == "" || name == "" || password == "" {
		return errors.New("-username, -name and -password are all required (or use -list)")
	}
	return upsertAdmin(ctx, db, username, name, password, owner)
}

func listAdmins(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx,
		`SELECT id, username, name, is_owner FROM admins WHERE account_id=$1 ORDER BY id`, appdb.AccountID)
	if err != nil {
		return err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id int64
		var username, name string
		var isOwner bool
		if err := rows.Scan(&id, &username, &name, &isOwner); err != nil {
			return err
		}
		role := "admin"
		if isOwner {
			role = "owner"
		}
		log.Printf("%4d  %-20s  %-30s  %s", id, username, name, role)
		n++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	log.Printf("%d admin(s)", n)
	return nil
}

func upsertAdmin(ctx context.Context, db *sql.DB, username, name, password string, owner bool) error {
	hash, err := argon2id.CreateHash(password, argon2id.DefaultParams)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	var id int64
	err = db.QueryRowContext(ctx, `
INSERT INTO admins (account_id, username, name, password_hash, is_owner)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (username) DO UPDATE SET
  name = excluded.name,
  password_hash = excluded.password_hash,
  is_owner = excluded.is_owner
RETURNING id`, appdb.AccountID, username, name, hash, owner).Scan(&id)
	if err != nil {
		return fmt.Errorf("upsert admin: %w", err)
	}
	role := "admin"
	if owner {
		role = "owner"
	}
	log.Printf("saved admin #%d: %s (%s, %s)", id, username, name, role)
	return nil
}
