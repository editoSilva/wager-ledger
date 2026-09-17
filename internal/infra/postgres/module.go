package postgres

import (
	"go.uber.org/fx"

	"github.com/editosilva/wager-ledger/internal/application/ports"
)

var Module = fx.Module("postgres",
	fx.Provide(
		NewPool,
		fx.Annotate(NewWalletRepository, fx.As(new(ports.WalletRepository))),
		fx.Annotate(NewWagerTransactionRepository, fx.As(new(ports.WagerTransactionRepository))),
		fx.Annotate(NewLedgerRepository, fx.As(new(ports.LedgerRepository))),
		fx.Annotate(NewOutboxRepository, fx.As(new(ports.OutboxRepository))),
		fx.Annotate(NewUnitOfWork, fx.As(new(ports.UnitOfWork))),
	),
)
