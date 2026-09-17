package observability

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/editosilva/wager-ledger/internal/config"
)

func TestNewLogger_DevelopmentUsesDebugLevel(t *testing.T) {
	logger := NewLogger(config.Config{Environment: "development"})
	if !logger.Enabled(nil, slog.LevelDebug) {
		t.Error("logger em development deveria aceitar nível Debug")
	}
}

func TestNewLogger_ProductionUsesInfoLevel(t *testing.T) {
	logger := NewLogger(config.Config{Environment: "production"})
	if logger.Enabled(nil, slog.LevelDebug) {
		t.Error("logger fora de development não deveria aceitar nível Debug")
	}
	if !logger.Enabled(nil, slog.LevelInfo) {
		t.Error("logger deveria aceitar nível Info")
	}
}

func TestNewLogger_EmitsJSONWithServiceAndEnvFields(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, nil)
	logger := slog.New(handler).With(
		slog.String("service", "wager-ledger"),
		slog.String("env", "development"),
	)
	logger.Info("mensagem de teste")

	var decoded map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &decoded); err != nil {
		t.Fatalf("saída do logger não é JSON válido: %v (%s)", err, buf.String())
	}
	if decoded["service"] != "wager-ledger" {
		t.Errorf("service = %v, esperado wager-ledger", decoded["service"])
	}
	if decoded["env"] != "development" {
		t.Errorf("env = %v, esperado development", decoded["env"])
	}
	if !strings.Contains(buf.String(), "mensagem de teste") {
		t.Errorf("mensagem não encontrada na saída: %s", buf.String())
	}
}
