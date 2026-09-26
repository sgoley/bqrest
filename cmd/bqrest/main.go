package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bqrest/internal/config"
	"bqrest/internal/tui"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "tui" {
		if err := tui.Run(os.Args[2:], os.LookupEnv); err != nil {
			log.Fatalf("run admin TUI: %v", err)
		}
		return
	}

	configPath := flag.String("config", envOr("BQREST_CONFIG", "/etc/bqrest/config.json"), "path to the bqrest JSON configuration")
	addr := flag.String("listen", envOr("BQREST_LISTEN", ":8080"), "HTTP listen address")
	flag.Parse()

	if os.Getenv("BQREST_CLOUD_RUN_DEMO") == "true" {
		log.Printf("Cloud Run demo mode enabled: serving health endpoint only; runtime config is not loaded")
	} else {
		cfg, err := config.Load(*configPath, os.LookupEnv)
		if err != nil {
			log.Fatalf("load configuration: %v", err)
		}
		log.Printf("configuration loaded: %d connection(s), %d caller(s)", len(cfg.Connections), len(cfg.Callers))
	}

	server := &http.Server{
		Addr:              *addr,
		Handler:           newHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("HTTP server listening on %s", server.Addr)
		serverErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serve HTTP: %v", err)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful shutdown failed: %v", err)
			_ = server.Close()
		}
	}
}

func newHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{\"status\":\"ok\"}\n"))
	})
	return mux
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
