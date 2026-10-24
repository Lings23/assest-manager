package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"asset-platform/internal/asset"
	"asset-platform/internal/servicekit"
)

func main() {
	migrateOnly := flag.Bool("migrate-only", false, "apply asset migrations and exit")
	flag.Parse()
	if *migrateOnly {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := asset.Migrate(ctx, os.Getenv("DATABASE_URL")); err != nil {
			log.Fatal(err)
		}
		return
	}
	cfg, err := servicekit.LoadConfig("asset-service", ":8082")
	if err != nil {
		log.Fatal(err)
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	iamURL := env("IAM_SERVICE_URL", "http://iam-service:8081")
	authorizer, err := asset.NewIAMAuthorizer(iamURL, servicekit.MetricsFor(cfg.ServiceName))
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	store, err := asset.OpenStore(ctx, databaseURL, servicekit.MetricsFor(cfg.ServiceName))
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	service := asset.NewService(store, authorizer)
	api := asset.NewHTTPHandler(service)
	internalAPI := asset.NewInternalHTTPHandler(store, strings.TrimSpace(os.Getenv("INTERNAL_SERVICE_TOKEN")), cfg.MaxBodyBytes)
	logger := servicekit.NewLogger("asset-service")
	cfg.Readiness = servicekit.NewReadiness(store.Ping)
	handler := servicekit.NewHandler(cfg, logger, func(mux *http.ServeMux) {
		api.Register(mux)
		internalAPI.Register(mux)
	})
	if err := servicekit.Run(ctx, cfg, logger, handler); err != nil {
		logger.Error("asset-service stopped", "error", err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
