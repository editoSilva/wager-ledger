package httpserver

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWithRequestLogging_EmitsCorrelationIDAndStatus(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	mw := WithRequestLogging(logger)(inner)

	req := httptest.NewRequest(http.MethodPost, "/wallets", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, esperado 201", rec.Code)
	}
	correlationID := rec.Header().Get("X-Correlation-Id")
	if correlationID == "" {
		t.Fatal("resposta deveria ter X-Correlation-Id preenchido")
	}

	var logLine map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &logLine); err != nil {
		t.Fatalf("erro ao decodificar log JSON: %v, corpo: %s", err, buf.String())
	}
	if logLine["correlationId"] != correlationID {
		t.Errorf("correlationId no log = %v, esperado %v", logLine["correlationId"], correlationID)
	}
	if int(logLine["status"].(float64)) != http.StatusCreated {
		t.Errorf("status no log = %v, esperado 201", logLine["status"])
	}
}

func TestWithRequestLogging_PropagatesIncomingCorrelationID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := WithRequestLogging(logger)(inner)

	req := httptest.NewRequest(http.MethodGet, "/wallets/1", nil)
	req.Header.Set("X-Correlation-Id", "req-fixed-id")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Correlation-Id"); got != "req-fixed-id" {
		t.Errorf("X-Correlation-Id = %q, esperado reaproveitar req-fixed-id", got)
	}
	if !strings.Contains(buf.String(), "req-fixed-id") {
		t.Errorf("log deveria conter o correlationId recebido: %s", buf.String())
	}
}
