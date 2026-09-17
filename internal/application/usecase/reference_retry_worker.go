package usecase

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"go.uber.org/fx"

	"github.com/editosilva/wager-ledger/internal/application/ports"
	"github.com/editosilva/wager-ledger/internal/config"
	"github.com/editosilva/wager-ledger/internal/domain/wagertx"
)

type ReferenceRetryWorker struct {
	repo    ports.WagerTransactionRepository
	process *ProcessWagerTransaction
	logger  *slog.Logger
	ttl     time.Duration
	now     func() time.Time
	cancel  context.CancelFunc
	done    chan struct{}
}

func NewReferenceRetryWorker(lc fx.Lifecycle, repo ports.WagerTransactionRepository, process *ProcessWagerTransaction, logger *slog.Logger, cfg config.Config) *ReferenceRetryWorker {
	w := &ReferenceRetryWorker{repo: repo, process: process, logger: logger, ttl: cfg.ReferencePendingTTL, now: time.Now, done: make(chan struct{})}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			ctx, cancel := context.WithCancel(context.Background())
			w.cancel = cancel
			go w.run(ctx)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			w.cancel()
			select {
			case <-w.done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	return w
}

func (w *ReferenceRetryWorker) run(ctx context.Context) {
	defer close(w.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		w.processPending(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *ReferenceRetryWorker) processPending(ctx context.Context) {
	ids, err := w.repo.ListPendingReferenceIDs(ctx, 32)
	if err != nil {
		w.logger.Error("falha ao buscar referências pendentes", slog.Any("error", err))
		return
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		wg.Add(1)
		go func(id wagertx.ID) {
			defer wg.Done()
			if err := w.process.ResumePendingReference(ctx, id); err != nil {
				w.logger.Error("falha ao retomar referência pendente", slog.String("transactionId", string(id)), slog.Any("error", err))
			}
		}(id)
	}
	wg.Wait()

	w.expireStale(ctx)
}

func (w *ReferenceRetryWorker) expireStale(ctx context.Context) {
	if w.ttl <= 0 {
		return
	}
	cutoff := w.now().UTC().Add(-w.ttl)
	ids, err := w.repo.ListStalePendingReferenceIDs(ctx, cutoff, 32)
	if err != nil {
		w.logger.Error("falha ao buscar referências pendentes expiradas", slog.Any("error", err))
		return
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		wg.Add(1)
		go func(id wagertx.ID) {
			defer wg.Done()
			if err := w.process.ExpirePendingReference(ctx, id); err != nil {
				w.logger.Error("falha ao expirar referência pendente", slog.String("transactionId", string(id)), slog.Any("error", err))
			}
		}(id)
	}
	wg.Wait()
}
