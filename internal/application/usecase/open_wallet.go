package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/editosilva/wager-ledger/internal/application/ports"
	"github.com/editosilva/wager-ledger/internal/domain/event"
	"github.com/editosilva/wager-ledger/internal/domain/ledger"
	"github.com/editosilva/wager-ledger/internal/domain/money"
	"github.com/editosilva/wager-ledger/internal/domain/wagertx"
	"github.com/editosilva/wager-ledger/internal/domain/wallet"
)

var ErrWalletAlreadyExists = errors.New("usecase: já existe uma carteira para este player e moeda")

type OpenWalletInput struct {
	PlayerID       string
	InitialBalance money.Money
}

type OpenWalletOutput struct {
	ID       string
	PlayerID string
	Balance  money.Money
	Version  int64
}

type OpenWallet struct {
	uow        ports.UnitOfWork
	walletRepo ports.WalletRepository
	txRepo     ports.WagerTransactionRepository
	ledgerRepo ports.LedgerRepository
	outboxRepo ports.OutboxRepository
	idGen      ports.IDGenerator
	now        func() time.Time
}

func NewOpenWallet(
	uow ports.UnitOfWork,
	walletRepo ports.WalletRepository,
	txRepo ports.WagerTransactionRepository,
	ledgerRepo ports.LedgerRepository,
	outboxRepo ports.OutboxRepository,
	idGen ports.IDGenerator,
) *OpenWallet {
	return &OpenWallet{
		uow:        uow,
		walletRepo: walletRepo,
		txRepo:     txRepo,
		ledgerRepo: ledgerRepo,
		outboxRepo: outboxRepo,
		idGen:      idGen,
		now:        time.Now,
	}
}

func (uc *OpenWallet) Execute(ctx context.Context, in OpenWalletInput) (*OpenWalletOutput, error) {
	now := uc.now().UTC()

	w, err := wallet.NewWallet(wallet.ID(uc.idGen.NewID()), wallet.PlayerID(in.PlayerID), in.InitialBalance, now)
	if err != nil {
		return nil, err
	}

	err = uc.uow.Execute(ctx, func(txCtx context.Context) error {
		if err := uc.walletRepo.Create(txCtx, w); err != nil {
			return err
		}

		if in.InitialBalance.IsZero() {
			return nil
		}

		openingTx, err := wagertx.NewOpeningTransaction(
			wagertx.ID(uc.idGen.NewID()), w.ID(), w.PlayerID(), in.InitialBalance, now,
		)
		if err != nil {
			return err
		}
		if err := uc.txRepo.Create(txCtx, openingTx); err != nil {
			return err
		}

		zero, err := money.Zero(in.InitialBalance.Currency())
		if err != nil {
			return err
		}
		entry, err := ledger.NewEntry(
			ledger.ID(uc.idGen.NewID()), w.ID(), openingTx.ID(),
			ledger.DirectionCredit, in.InitialBalance, zero, w.Balance(), now,
		)
		if err != nil {
			return err
		}
		if err := uc.ledgerRepo.Create(txCtx, entry); err != nil {
			return err
		}

		correlationID := string(openingTx.ID())

		processedEvent := event.NewWagerTransactionProcessed(
			uc.idGen.NewID(), string(w.ID()), correlationID, now,
			event.WagerTransactionProcessedData{
				TransactionID: string(openingTx.ID()),
				WalletID:      string(w.ID()),
				PlayerID:      string(w.PlayerID()),
				Kind:          string(wagertx.KindOpening),
				Money:         in.InitialBalance,
			},
		)
		if err := uc.outboxRepo.Create(txCtx, processedEvent); err != nil {
			return err
		}

		balanceChangedEvent := event.NewWalletBalanceChanged(
			uc.idGen.NewID(), string(w.ID()), correlationID, now,
			event.WalletBalanceChangedData{
				WalletID:      string(w.ID()),
				TransactionID: string(openingTx.ID()),
				Direction:     string(ledger.DirectionCredit),
				Money:         in.InitialBalance,
				BalanceBefore: zero,
				BalanceAfter:  w.Balance(),
				WalletVersion: w.Version(),
			},
		)
		return uc.outboxRepo.Create(txCtx, balanceChangedEvent)
	})

	if err != nil {
		if errors.Is(err, ports.ErrAlreadyExists) {
			return nil, ErrWalletAlreadyExists
		}
		return nil, err
	}

	return &OpenWalletOutput{
		ID:       string(w.ID()),
		PlayerID: string(w.PlayerID()),
		Balance:  w.Balance(),
		Version:  w.Version(),
	}, nil
}
