package usecase

import (
	"context"
	"log/slog"

	"github.com/editosilva/wager-ledger/internal/application/ports"
	"github.com/editosilva/wager-ledger/internal/domain/money"
	"github.com/editosilva/wager-ledger/internal/domain/wallet"
	"github.com/editosilva/wager-ledger/internal/observability"
)

type ReconcileWalletOutput struct {
	WalletID          string
	StoredBalance     money.Money
	CalculatedBalance money.Money
	Difference        money.Money
	Consistent        bool
	CheckedEntries    int
}

type ReconcileWallet struct {
	snapshot   ports.SnapshotReader
	walletRepo ports.WalletRepository
	ledgerRepo ports.LedgerRepository
	metrics    *observability.Metrics
	logger     *slog.Logger
}

func NewReconcileWallet(snapshot ports.SnapshotReader, walletRepo ports.WalletRepository, ledgerRepo ports.LedgerRepository, metrics *observability.Metrics, logger *slog.Logger) *ReconcileWallet {
	return &ReconcileWallet{snapshot: snapshot, walletRepo: walletRepo, ledgerRepo: ledgerRepo, metrics: metrics, logger: logger}
}

func (uc *ReconcileWallet) Execute(ctx context.Context, walletID wallet.ID) (*ReconcileWalletOutput, error) {
	var (
		stored, calculated money.Money
		checkedEntries     int
	)
	err := uc.snapshot.ReadSnapshot(ctx, func(txCtx context.Context) error {
		w, err := uc.walletRepo.FindByID(txCtx, walletID)
		if err != nil {
			return err
		}
		stored = w.Balance()

		checkedEntries, err = uc.ledgerRepo.CountByWallet(txCtx, walletID)
		if err != nil {
			return err
		}

		if checkedEntries == 0 {
			calculated, err = money.Zero(stored.Currency())
			return err
		}
		calculated, err = uc.ledgerRepo.SumByWallet(txCtx, walletID)
		return err
	})
	if err != nil {
		return nil, err
	}

	difference, err := stored.Sub(calculated)
	if err != nil {
		return nil, err
	}
	consistent := difference.IsZero()

	if uc.metrics != nil {
		uc.metrics.ReconciliationChecksTotal.Inc()
		if !consistent {
			uc.metrics.ReconciliationDivergentTotal.Inc()
		}
	}
	if !consistent {
		uc.logger.Error("divergência de reconciliação detectada",
			slog.String("walletId", string(walletID)),
			slog.String("storedBalance", stored.DecimalString()),
			slog.String("calculatedBalance", calculated.DecimalString()),
			slog.String("difference", difference.DecimalString()),
		)
	}

	return &ReconcileWalletOutput{
		WalletID:          string(walletID),
		StoredBalance:     stored,
		CalculatedBalance: calculated,
		Difference:        difference,
		Consistent:        consistent,
		CheckedEntries:    checkedEntries,
	}, nil
}
