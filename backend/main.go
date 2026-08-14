package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	port := envDefault("PORT", "8080")
	frontendOrigin := envDefault("FRONTEND_ORIGIN", "http://localhost:3000")

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

	st, err := OpenStore(dataDir)
	if err != nil {
		log.Fatalf("open app db: %v", err)
	}

	hub := NewHub()
	mgr, err := NewManager(context.Background(), dataDir, st, hub)
	if err != nil {
		log.Fatalf("init whatsapp: %v", err)
	}
	hub.SetCountListener(mgr.OnClientCountChange)

	api := NewAPI(mgr, st, hub, frontendOrigin)
	srv := &http.Server{Addr: ":" + port, Handler: api.Routes()}

	go func() {
		log.Printf("listening on :%s (data dir: %s)", port, dataDir)
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
