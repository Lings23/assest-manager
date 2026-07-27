package servicekit

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config contains the baseline configuration shared by every new service.
type Config struct {
	ServiceName     string
	HTTPAddr        string
	ShutdownTimeout time.Duration
	MaxBodyBytes    int64
	AllowedOrigins  []string
}

// LoadConfig reads environment configuration and validates it before startup.
func LoadConfig(serviceName, defaultAddr string) (Config, error) {
	cfg := Config{
		ServiceName:     strings.TrimSpace(serviceName),
		HTTPAddr:        envOrDefault("HTTP_ADDR", defaultAddr),
		ShutdownTimeout: 10 * time.Second,
		MaxBodyBytes:    1 << 20,
		AllowedOrigins:  splitList(os.Getenv("CORS_ALLOWED_ORIGINS")),
	}
	if value := strings.TrimSpace(os.Getenv("SHUTDOWN_TIMEOUT")); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT: %w", err)
		}
		cfg.ShutdownTimeout = duration
	}
	if value := strings.TrimSpace(os.Getenv("MAX_BODY_BYTES")); value != "" {
		bytes, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("MAX_BODY_BYTES: %w", err)
		}
		cfg.MaxBodyBytes = bytes
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func splitList(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func (c Config) Validate() error {
	if c.ServiceName == "" {
		return fmt.Errorf("service name is required")
	}
	if !strings.HasPrefix(c.HTTPAddr, ":") || len(c.HTTPAddr) < 2 {
		return fmt.Errorf("HTTP_ADDR must use :port format")
	}
	port, err := strconv.Atoi(strings.TrimPrefix(c.HTTPAddr, ":"))
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("HTTP_ADDR contains an invalid port")
	}
	if c.ShutdownTimeout <= 0 || c.ShutdownTimeout > time.Minute {
		return fmt.Errorf("SHUTDOWN_TIMEOUT must be greater than zero and no more than one minute")
	}
	if c.MaxBodyBytes < 1024 || c.MaxBodyBytes > 100<<20 {
		return fmt.Errorf("MAX_BODY_BYTES must be between 1 KiB and 100 MiB")
	}
	for _, origin := range c.AllowedOrigins {
		parsed, err := url.ParseRequestURI(origin)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || (parsed.Path != "" && parsed.Path != "/") {
			return fmt.Errorf("CORS_ALLOWED_ORIGINS contains invalid origin %q", origin)
		}
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
