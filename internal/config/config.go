package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPPort            string
	Environment         string
	ShutdownTimeout     time.Duration
	DatabaseURL         string
	OIDCIssuerURL       string
	OIDCJWKSURL         string
	OIDCAudience        string
	AWSRegion           string
	AWSAccessKeyID      string
	AWSSecretKey        string
	SQSEndpoint         string
	SQSQueueURL         string
	EventsQueueURL      string
	ReferencePendingTTL time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		HTTPPort:      getEnv("HTTP_PORT", "8080"),
		Environment:   getEnv("APP_ENV", "development"),
		DatabaseURL:   getEnv("DATABASE_URL", "postgres://wager:wager@localhost:5432/wager_ledger?sslmode=disable"),
		OIDCIssuerURL: getEnv("OIDC_ISSUER_URL", "http://localhost:8081/realms/wager-ledger"),
		OIDCJWKSURL:   getEnv("OIDC_JWKS_URL", "http://localhost:8081/realms/wager-ledger/protocol/openid-connect/certs"),
		OIDCAudience:  getEnv("OIDC_AUDIENCE", "wager-ledger-api"),
		AWSRegion:     getEnv("AWS_REGION", "us-east-1"), AWSAccessKeyID: getEnv("AWS_ACCESS_KEY_ID", "test"), AWSSecretKey: getEnv("AWS_SECRET_ACCESS_KEY", "test"),
		SQSEndpoint: getEnv("SQS_ENDPOINT", "http://localhost:4566"), SQSQueueURL: getEnv("SQS_QUEUE_URL", "http://localhost:4566/000000000000/wager-transactions.fifo"),
		EventsQueueURL: getEnv("EVENTS_QUEUE_URL", "http://localhost:4566/000000000000/wager-events.fifo"),
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

	referencePendingTTLRaw := getEnv("REFERENCE_PENDING_TTL", "15m")
	referencePendingTTL, err := time.ParseDuration(referencePendingTTLRaw)
	if err != nil {
		return Config{}, fmt.Errorf("config: REFERENCE_PENDING_TTL inválida (%q): %w", referencePendingTTLRaw, err)
	}
	cfg.ReferencePendingTTL = referencePendingTTL

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
