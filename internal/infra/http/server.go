package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/editosilva/wager-ledger/internal/config"
	"go.uber.org/fx"
)

func NewServer(lc fx.Lifecycle, cfg config.Config, mux *http.ServeMux, logger *slog.Logger) *http.Server {
	srv := &http.Server{
		Addr:              net.JoinHostPort("", cfg.HTTPPort),
		Handler:           WithRequestLogging(logger)(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			ln, err := net.Listen("tcp", srv.Addr)
			if err != nil {
				return err
			}
			logger.Info("servidor http iniciando", slog.String("addr", srv.Addr))
			go func() {
				if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
					logger.Error("servidor http encerrado inesperadamente", slog.Any("error", err))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			logger.Info("servidor http em shutdown gracioso")
			return srv.Shutdown(ctx)
		},
	})

	return srv
}
