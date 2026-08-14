package main

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/joho/godotenv"
)

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	// .env in the working directory is optional; a missing file is not an error.
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Fatalf("load .env: %v", err)
	}

	port := envDefault("PORT", "8080")
	frontendOrigin := envDefault("FRONTEND_ORIGIN", "http://localhost:3000")
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		log.Fatal("POSTGRES_DSN is required (set it in the environment or .env)")
	}

	// DATA_DIR only holds media files now; all state lives in Postgres.
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			if d, err := os.UserCacheDir(); err == nil {
				base = d
			} else {
				base = "."
			}
		}
		dataDir = filepath.Join(base, "sales-inbox")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("create data dir %s: %v", dataDir, err)
	}

	st, err := OpenStore(dsn)
	if err != nil {
		log.Fatalf("open app db: %v", err)
	}

	if err := EnsureDefaultAdmin(context.Background(), st); err != nil {
		log.Fatalf("seed default admin: %v", err)
	}

	hub := NewHub()
	agentSignature := envDefault("AGENT_SIGNATURE", "on") != "off"
	mgr, err := NewManager(context.Background(), dataDir, st, hub, agentSignature)
	if err != nil {
		log.Fatalf("init whatsapp: %v", err)
	}
	hub.SetCountListener(mgr.OnClientCountChange)

	auth := NewAuth(st, frontendOrigin)
	api := NewAPI(mgr, st, hub, auth, frontendOrigin)
	srv := &http.Server{Addr: ":" + port, Handler: api.Routes()}

	go func() {
		log.Printf("listening on :%s (media dir: %s)", port, dataDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server: %v", err)
		}
	}()

	mgr.Start()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Println("shutting down")

	shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shCtx)
	mgr.Shutdown()
	_ = st.Close()
}
