package main

import (
	"context"
	"crypto/rsa"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"asset-platform/internal/iam"
	"asset-platform/internal/servicekit"
)

func main() {
	migrateOnly := flag.Bool("migrate-only", false, "apply IAM migrations and initialize administrator")
	flag.Parse()
	if *migrateOnly {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := iam.Migrate(ctx, os.Getenv("DATABASE_URL")); err != nil {
			log.Fatal(err)
		}
		store, err := iam.OpenPostgresStore(ctx, os.Getenv("DATABASE_URL"))
		if err != nil {
			log.Fatal(err)
		}
		defer store.Close()
		key, err := iam.GenerateRSAKey()
		if err != nil {
			log.Fatal(err)
		}
		signer, err := iam.NewTokenSigner(key, "bootstrap", "bootstrap")
		if err != nil {
			log.Fatal(err)
		}
		service, err := iam.NewService(store, signer)
		if err != nil {
			log.Fatal(err)
		}
		if err = bootstrapAdmin(ctx, service, store); err != nil {
			log.Fatal(err)
		}
		return
	}
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
	store, err := iam.OpenPostgresStore(ctx, databaseURL, servicekit.MetricsFor(cfg.ServiceName))
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

	cookieSecure, err := strconv.ParseBool(env("IAM_COOKIE_SECURE", "true"))
	if err != nil {
		log.Fatal("IAM_COOKIE_SECURE must be true or false")
	}
	proxies, err := iam.ParseTrustedProxies(os.Getenv("TRUSTED_PROXY_CIDRS"))
	if err != nil {
		log.Fatal(err)
	}
	go store.CleanupLoginLimits(ctx)
	cfg.Readiness = servicekit.NewReadiness(store.Ping)
	api := iam.NewHTTPHandler(service, iam.HTTPConfig{CookieSecure: cookieSecure, LoginGate: store, TrustedProxies: proxies}, logger)
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
	if os.Getenv("IAM_ALLOW_EPHEMERAL_KEY") != "true" {
		return nil, false, fmt.Errorf("IAM_PRIVATE_KEY_FILE is required (ephemeral keys require explicit IAM_ALLOW_EPHEMERAL_KEY=true for development)")
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
	if errors.Is(err, iam.ErrConflict) {
		if _, lookupErr := store.GetUserByUsername(ctx, username); lookupErr == nil {
			return nil
		}
	}
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
