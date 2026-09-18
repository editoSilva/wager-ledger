package httpserver

import (
	"context"
	"net/http"
	"time"
)

type ReadinessChecker interface {
	Name() string
	Ready(ctx context.Context) error
}

const readinessTimeout = 3 * time.Second

func RegisterHealthRoutes(mux *http.ServeMux, checkers ...ReadinessChecker) {
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
		defer cancel()

		failures := make(map[string]string)
		for _, c := range checkers {
			if err := c.Ready(ctx); err != nil {
				failures[c.Name()] = err.Error()
			}
		}

		if len(failures) > 0 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unavailable", "failures": failures})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
}
