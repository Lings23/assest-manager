package servicekit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"sync"
	"time"
)

// StartDiagnostics is opt-in and binds a separate literal loopback address.
// It never registers pprof on the public service router or Gateway.
func StartDiagnostics(ctx context.Context, cfg Config, logger *slog.Logger) (func(), error) {
	if cfg.DebugPprofAddress == "" {
		return func() {}, nil
	}
	if err := validatePprofAddress(cfg.DebugPprofAddress); err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	listener, err := net.Listen("tcp", cfg.DebugPprofAddress)
	if err != nil {
		return nil, fmt.Errorf("listen on controlled pprof address: %w", err)
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done); _ = server.Close() }) }
	go func() {
		select {
		case <-ctx.Done():
			stop()
		case <-done:
		}
	}()
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("controlled pprof server stopped", "error", err)
		}
	}()
	logger.Info("controlled pprof listener enabled", "addr", cfg.DebugPprofAddress)
	return stop, nil
}

func validatePprofAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return fmt.Errorf("DEBUG_PPROF_ADDR must use a literal loopback IP and port")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("DEBUG_PPROF_ADDR must use a literal loopback IP and port")
	}
	return nil
}
