// Package admin serves the status API.
package admin

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"ovpnmux/internal/tunnel"
)

func ListenAndServe(ctx context.Context, addr string, m *tunnel.Manager) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(map[string]any{"ready": len(m.Ready()), "tunnels": m.Status()})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if len(m.Ready()) == 0 {
			http.Error(w, "no ready tunnel", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok\n"))
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()
	slog.Info("admin listening", "addr", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}
