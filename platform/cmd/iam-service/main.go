package main

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"asset-platform/internal/iam"
	"asset-platform/internal/servicekit"
)

func main() {
	cfg, err := servicekit.LoadConfig("iam-service", ":8081")
	if err != nil {
		log.Fatal(err)
	}
	logger := servicekit.NewLogger("iam-service")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	store, err := iam.OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	privateKey, ephemeral, err := loadPrivateKey()
	if err != nil {
		log.Fatal(err)
	}
	if ephemeral {
		logger.Warn("using ephemeral RSA signing key; configure IAM_PRIVATE_KEY_FILE before production")
	}
	signer, err := iam.NewTokenSigner(privateKey, env("IAM_TOKEN_ISSUER", "asset-platform-iam"), env("IAM_TOKEN_AUDIENCE", "asset-platform"))
	if err != nil {
		log.Fatal(err)
	}
	service, err := iam.NewService(store, signer)
	if err != nil {
		log.Fatal(err)
	}
	if err := bootstrapAdmin(ctx, service, store); err != nil {
		log.Fatal(err)
	}

	cookieSecure, err := strconv.ParseBool(env("IAM_COOKIE_SECURE", "true"))
	if err != nil {
		log.Fatal("IAM_COOKIE_SECURE must be true or false")
	}
	api := iam.NewHTTPHandler(service, iam.HTTPConfig{CookieSecure: cookieSecure}, logger)
	handler := servicekit.NewHandler(cfg, logger, api.Register)
	if err := servicekit.Run(ctx, cfg, logger, handler); err != nil {
		logger.Error("iam-service stopped", "error", err)
		os.Exit(1)
	}
}

func loadPrivateKey() (*rsa.PrivateKey, bool, error) {
	if path := strings.TrimSpace(os.Getenv("IAM_PRIVATE_KEY_FILE")); path != "" {
		value, err := os.ReadFile(path)
		if err != nil {
			return nil, false, fmt.Errorf("read IAM_PRIVATE_KEY_FILE: %w", err)
		}
		key, err := iam.ParseRSAPrivateKeyPEM(value)
		return key, false, err
	}
	if value := strings.TrimSpace(os.Getenv("IAM_PRIVATE_KEY_PEM")); value != "" {
		key, err := iam.ParseRSAPrivateKeyPEM([]byte(value))
		return key, false, err
	}
	key, err := iam.GenerateRSAKey()
	return key, true, err
}

func bootstrapAdmin(ctx context.Context, service *iam.Service, store iam.Store) error {
	username := env("IAM_BOOTSTRAP_ADMIN_USERNAME", "admin")
	if _, err := store.GetUserByUsername(ctx, username); err == nil {
		return nil
	} else if !errors.Is(err, iam.ErrNotFound) {
		return fmt.Errorf("check bootstrap administrator: %w", err)
	}
	password := strings.TrimSpace(os.Getenv("IAM_BOOTSTRAP_ADMIN_PASSWORD"))
	if password == "" {
		password = strings.TrimSpace(os.Getenv("ADMIN_PASSWORD"))
	}
	if len(password) < 12 {
		return fmt.Errorf("IAM_BOOTSTRAP_ADMIN_PASSWORD must contain at least 12 characters")
	}
	_, err := service.CreateUser(ctx, iam.NewUser{
		Username: username, DisplayName: "平台管理员", Password: password,
		Roles: []iam.Role{iam.RoleAdmin}, DataScope: iam.ScopeGlobal,
		MustChangePassword: true,
	})
	if err != nil {
		return fmt.Errorf("create bootstrap administrator: %w", err)
	}
	return nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
