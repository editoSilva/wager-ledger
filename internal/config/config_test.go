package config

import (
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() retornou erro inesperado: %v", err)
	}
	if cfg.HTTPPort != "8080" {
		t.Errorf("HTTPPort = %q, esperado 8080", cfg.HTTPPort)
	}
	if cfg.Environment != "development" {
		t.Errorf("Environment = %q, esperado development", cfg.Environment)
	}
	if cfg.ShutdownTimeout != 15*time.Second {
		t.Errorf("ShutdownTimeout = %v, esperado 15s", cfg.ShutdownTimeout)
	}
}

func TestLoad_CustomValues(t *testing.T) {
	t.Setenv("HTTP_PORT", "9090")
	t.Setenv("APP_ENV", "production")
	t.Setenv("SHUTDOWN_TIMEOUT", "30s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() retornou erro inesperado: %v", err)
	}
	if cfg.HTTPPort != "9090" {
		t.Errorf("HTTPPort = %q, esperado 9090", cfg.HTTPPort)
	}
	if cfg.Environment != "production" {
		t.Errorf("Environment = %q, esperado production", cfg.Environment)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Errorf("ShutdownTimeout = %v, esperado 30s", cfg.ShutdownTimeout)
	}
}

func TestLoad_InvalidPort(t *testing.T) {
	t.Setenv("HTTP_PORT", "not-a-port")
	if _, err := Load(); err == nil {
		t.Fatal("Load() deveria falhar com HTTP_PORT inválida")
	}
}

func TestLoad_InvalidShutdownTimeout(t *testing.T) {
	t.Setenv("SHUTDOWN_TIMEOUT", "not-a-duration")
	if _, err := Load(); err == nil {
		t.Fatal("Load() deveria falhar com SHUTDOWN_TIMEOUT inválida")
	}
}
