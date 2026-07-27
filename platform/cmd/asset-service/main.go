package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"asset-platform/internal/asset"
	"asset-platform/internal/servicekit"
)

func main() {
	cfg, err := servicekit.LoadConfig("asset-service", ":8082")
	if err != nil {
		log.Fatal(err)
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	iamURL := env("IAM_SERVICE_URL", "http://iam-service:8081")
	authorizer, err := asset.NewIAMAuthorizer(iamURL)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	store, err := asset.OpenStore(ctx, databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	service := asset.NewService(store, authorizer)
	api := asset.NewHTTPHandler(service)
	logger := servicekit.NewLogger("asset-service")
	handler := servicekit.NewHandler(cfg, logger, api.Register)
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
