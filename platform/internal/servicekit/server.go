package servicekit

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

func Run(ctx context.Context, cfg Config, logger *slog.Logger, handler http.Handler) error {
	stopDiagnostics, err := StartDiagnostics(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer stopDiagnostics()
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("service listening", "addr", cfg.HTTPAddr)
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		if cfg.Readiness != nil {
			cfg.Readiness.Drain()
		}
		logger.Info("draining HTTP service", "delay", cfg.DrainDelay)
		timer := time.NewTimer(cfg.DrainDelay)
		<-timer.C
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return err
		}
		return nil
	}
}
