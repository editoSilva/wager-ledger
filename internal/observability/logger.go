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

	logger := slog.New(handler).With(
		slog.String("service", "wager-ledger"),
		slog.String("env", cfg.Environment),
	)

	// slog.SetDefault permite que código que não recebe *slog.Logger por
	// injeção (ex.: handlers HTTP que só logam em caminhos de erro raros,
	// como falha ao serializar a resposta) ainda use o logger estruturado
	// real da aplicação via slog.Default(), em vez de descartar o erro ou
	// cair no logger de texto não estruturado padrão do pacote slog.
	slog.SetDefault(logger)

	return logger
}
