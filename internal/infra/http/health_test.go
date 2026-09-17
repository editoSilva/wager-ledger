package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeChecker struct {
	name string
	err  error
}

func (c fakeChecker) Name() string                    { return c.name }
func (c fakeChecker) Ready(ctx context.Context) error { return c.err }

func TestHealthRoutes_Live_AlwaysReturns200(t *testing.T) {
	mux := http.NewServeMux()
	RegisterHealthRoutes(mux, fakeChecker{name: "postgres", err: errors.New("indisponível")})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/live", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, esperado 200", rec.Code)
	}
}

func TestHealthRoutes_Ready_AllHealthy_Returns200(t *testing.T) {
	mux := http.NewServeMux()
	RegisterHealthRoutes(mux, fakeChecker{name: "postgres"}, fakeChecker{name: "sqs"})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, esperado 200, corpo: %s", rec.Code, rec.Body.String())
	}
}

func TestHealthRoutes_Ready_DependencyDown_Returns503(t *testing.T) {
	mux := http.NewServeMux()
	RegisterHealthRoutes(mux,
		fakeChecker{name: "postgres", err: errors.New("timeout de conexão")},
		fakeChecker{name: "sqs"},
	)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, esperado 503, corpo: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "postgres") {
		t.Errorf("corpo deveria citar o checker com falha: %s", rec.Body.String())
	}
}
