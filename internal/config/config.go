package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPPort        string
	Environment     string
	ShutdownTimeout time.Duration
	DatabaseURL     string
	OIDCIssuerURL   string
	OIDCJWKSURL     string
}

func Load() (Config, error) {
	cfg := Config{
		HTTPPort:      getEnv("HTTP_PORT", "8080"),
		Environment:   getEnv("APP_ENV", "development"),
		DatabaseURL:   getEnv("DATABASE_URL", "postgres://wager:wager@localhost:5432/wager_ledger?sslmode=disable"),
		OIDCIssuerURL: getEnv("OIDC_ISSUER_URL", "http://localhost:8081/realms/wager-ledger"),
		OIDCJWKSURL:   getEnv("OIDC_JWKS_URL", "http://localhost:8081/realms/wager-ledger/protocol/openid-connect/certs"),
	}

	if _, err := strconv.Atoi(cfg.HTTPPort); err != nil {
		return Config{}, fmt.Errorf("config: HTTP_PORT inválida (%q): deve ser numérica", cfg.HTTPPort)
	}

	shutdownTimeoutRaw := getEnv("SHUTDOWN_TIMEOUT", "15s")
	shutdownTimeout, err := time.ParseDuration(shutdownTimeoutRaw)
	if err != nil {
		return Config{}, fmt.Errorf("config: SHUTDOWN_TIMEOUT inválida (%q): %w", shutdownTimeoutRaw, err)
	}
	cfg.ShutdownTimeout = shutdownTimeout

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
