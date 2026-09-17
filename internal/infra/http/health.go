package httpserver

import (
	"encoding/json"
	"net/http"
)

type ReadinessChecker interface {
	Name() string
	Ready() error
}

func RegisterHealthRoutes(mux *http.ServeMux, checkers ...ReadinessChecker) {
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		failures := make(map[string]string)
		for _, c := range checkers {
			if err := c.Ready(); err != nil {
				failures[c.Name()] = err.Error()
			}
		}

		w.Header().Set("Content-Type", "application/json")
		if len(failures) > 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "unavailable", "failures": failures})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	})
}
