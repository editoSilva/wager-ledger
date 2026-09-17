package observability

import (
	"log/slog"
	"os"

	"github.com/editosilva/wager-ledger/internal/config"
)

func NewLogger(cfg config.Config) *slog.Logger {
	level := slog.LevelInfo
	if cfg.Environment == "development" {
		level = slog.LevelDebug
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})

	return slog.New(handler).With(
		slog.String("service", "wager-ledger"),
		slog.String("env", cfg.Environment),
	)
}
