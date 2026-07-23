package servicekit

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

// Main starts a baseline domain-service template with health and identity endpoints.
func Main(serviceName, defaultAddr string) {
	cfg, err := LoadConfig(serviceName, defaultAddr)
	if err != nil {
		log.Fatal(err)
	}
	logger := NewLogger(serviceName)
	handler := NewHandler(cfg, logger, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /api/v1/_service", func(w http.ResponseWriter, _ *http.Request) {
			WriteJSON(w, http.StatusOK, map[string]string{"service": serviceName, "status": "baseline-ready"})
		})
	})
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := Run(ctx, cfg, logger, handler); err != nil {
		logger.Error("service stopped", "error", err)
		os.Exit(1)
	}
}
