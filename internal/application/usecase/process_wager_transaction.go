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
	"github.com/editosilva/wager-ledger/internal/observability"
)

var (
	ErrIdempotencyConflict  = errors.New("usecase: conflito de idempotência")
	ErrUnsupportedKind      = errors.New("usecase: tipo de operação não suportado por este caso de uso")
	ErrWalletPlayerMismatch = errors.New("usecase: playerId não corresponde ao dono da carteira")
	ErrTooManyRetries       = errors.New("usecase: número máximo de tentativas de concorrência excedido")
)

const (
	failureCodeReferenceMismatch           = "REFERENCE_MISMATCH"
	failureCodeReferenceNotProcessable     = "REFERENCE_NOT_PROCESSABLE"
	failureCodeReferenceAlreadyReversed    = "REFERENCE_ALREADY_REVERSED"
	failureCodeReversalInsufficientBalance = "REVERSAL_INSUFFICIENT_BALANCE"
	failureCodeReferenceExpired            = "REFERENCE_EXPIRED"
)

type ProcessWagerTransactionInput struct {
	ProviderID                     string
	ExternalTransactionID          string
	IdempotencyKey                 string
	PlayerID                       string
	WalletID                       string
	RoundID                        string
	GameID                         string
	Kind                           string
	Money                          money.Money
	ReferenceExternalTransactionID string
}

type ProcessWagerTransactionOutput struct {
	TransactionID    string
	Status           string
	Balance          money.Money
	FailureCode      string
	IdempotentReplay bool
}

type ProcessWagerTransaction struct {
	uow        ports.UnitOfWork
	walletRepo ports.WalletRepository
	txRepo     ports.WagerTransactionRepository
	ledgerRepo ports.LedgerRepository
	outboxRepo ports.OutboxRepository
	idGen      ports.IDGenerator
	metrics    *observability.Metrics
	now        func() time.Time
	maxRetries int
}

func NewProcessWagerTransaction(
	uow ports.UnitOfWork,
	walletRepo ports.WalletRepository,
	txRepo ports.WagerTransactionRepository,
	ledgerRepo ports.LedgerRepository,
	outboxRepo ports.OutboxRepository,
	idGen ports.IDGenerator,
	metrics *observability.Metrics,
) *ProcessWagerTransaction {
	return &ProcessWagerTransaction{
		uow:        uow,
		walletRepo: walletRepo,
		txRepo:     txRepo,
		ledgerRepo: ledgerRepo,
		outboxRepo: outboxRepo,
		idGen:      idGen,
		metrics:    metrics,
		now:        time.Now,
		maxRetries: 5,
	}
}

func (uc *ProcessWagerTransaction) Execute(ctx context.Context, in ProcessWagerTransactionInput) (*ProcessWagerTransactionOutput, error) {
	kind := wagertx.Kind(in.Kind)
	switch kind {
	case wagertx.KindBet, wagertx.KindWin, wagertx.KindLoss, wagertx.KindRefund, wagertx.KindRollback:
	default:
		return nil, ErrUnsupportedKind
	}

	hash, err := computePayloadHash(canonicalWagerFields{
		ProviderID:                     in.ProviderID,
		ExternalTransactionID:          in.ExternalTransactionID,
		PlayerID:                       in.PlayerID,
		WalletID:                       in.WalletID,
		RoundID:                        in.RoundID,
		GameID:                         in.GameID,
		Kind:                           in.Kind,
		MoneyAmount:                    in.Money.DecimalString(),
		MoneyCurrency:                  in.Money.Currency(),
		ReferenceExternalTransactionID: in.ReferenceExternalTransactionID,
	})
	if err != nil {
		return nil, err
	}

	var out *ProcessWagerTransactionOutput
	for attempt := 0; attempt < uc.maxRetries; attempt++ {
		execErr := uc.uow.Execute(ctx, func(txCtx context.Context) error {
			result, err := uc.attempt(txCtx, in, kind, hash)
			if err != nil {
				return err
			}
			out = result
			return nil
		})
		if execErr == nil {
			uc.recordOutcome(kind, out)
			return out, nil
		}
		if errors.Is(execErr, ports.ErrOptimisticLock) || errors.Is(execErr, ports.ErrAlreadyExists) {
			if uc.metrics != nil {
				uc.metrics.OptimisticLockRetriesTotal.Inc()
			}
			continue
		}
		return nil, execErr
	}
	if uc.metrics != nil {
		uc.metrics.TooManyRetriesTotal.Inc()
	}
	return nil, ErrTooManyRetries
}

func (uc *ProcessWagerTransaction) recordOutcome(kind wagertx.Kind, out *ProcessWagerTransactionOutput) {
	if uc.metrics == nil || out == nil {
		return
	}
	uc.metrics.WagerTransactionsTotal.WithLabelValues(out.Status, string(kind)).Inc()
	if out.IdempotentReplay {
		uc.metrics.IdempotentReplaysTotal.Inc()
	}
}

// ExecuteWithin executa dentro de uma transação já aberta, para que o
// consumidor SQS confirme inbox e efeito financeiro no mesmo commit.
func (uc *ProcessWagerTransaction) ExecuteWithin(ctx context.Context, in ProcessWagerTransactionInput) (*ProcessWagerTransactionOutput, error) {
	kind := wagertx.Kind(in.Kind)
	switch kind {
	case wagertx.KindBet, wagertx.KindWin, wagertx.KindLoss, wagertx.KindRefund, wagertx.KindRollback:
	default:
		return nil, ErrUnsupportedKind
	}
	hash, err := computePayloadHash(canonicalWagerFields{ProviderID: in.ProviderID, ExternalTransactionID: in.ExternalTransactionID, PlayerID: in.PlayerID, WalletID: in.WalletID, RoundID: in.RoundID, GameID: in.GameID, Kind: in.Kind, MoneyAmount: in.Money.DecimalString(), MoneyCurrency: in.Money.Currency(), ReferenceExternalTransactionID: in.ReferenceExternalTransactionID})
	if err != nil {
		return nil, err
	}
	out, err := uc.attempt(ctx, in, kind, hash)
	if err == nil {
		uc.recordOutcome(kind, out)
	}
	return out, err
}

// ResumePendingReference tenta concluir uma transação persistida que aguardava
// sua referência. O lock de linha impede que dois workers a liquidem juntos.
func (uc *ProcessWagerTransaction) ResumePendingReference(ctx context.Context, id wagertx.ID) error {
	return uc.uow.Execute(ctx, func(txCtx context.Context) error {
		tx, err := uc.txRepo.FindByIDForUpdate(txCtx, id)
		if err != nil || tx.Status() != wagertx.StatusPendingReference {
			return err
		}
		w, err := uc.walletRepo.FindByID(txCtx, tx.WalletID())
		if err != nil {
			return err
		}
		reference, err := uc.txRepo.FindByProviderAndExternalID(txCtx, tx.ProviderID(), tx.ReferenceExternalID())
		if errors.Is(err, ports.ErrNotFound) || (err == nil && reference.Status() != wagertx.StatusProcessed) {
			return nil
		}
		if err != nil {
			return err
		}
		now := uc.now().UTC()
		correlationID := string(tx.ID())
		if failureCode := validateReference(tx.Kind(), tx, reference); failureCode != "" {
			_, err := uc.reject(txCtx, tx, w, failureCode, now, correlationID)
			return err
		}
		if tx.Kind() == wagertx.KindRefund || tx.Kind() == wagertx.KindRollback {
			alreadyReversed, err := uc.txRepo.FindProcessedReversalByReference(txCtx, reference.ID())
			if err != nil && !errors.Is(err, ports.ErrNotFound) {
				return err
			}
			if alreadyReversed != nil {
				_, err := uc.reject(txCtx, tx, w, failureCodeReferenceAlreadyReversed, now, correlationID)
				return err
			}
		}
		if err := tx.ResolveReference(reference.ID(), now); err != nil {
			return err
		}
		previousVersion, balanceBefore := w.Version(), w.Balance()
		switch tx.Kind() {
		case wagertx.KindWin, wagertx.KindRefund:
			if err := w.Credit(tx.Money(), now); err != nil {
				return err
			}
			_, err = uc.settle(txCtx, tx, w, previousVersion, balanceBefore, ledger.DirectionCredit, tx.Money(), now, correlationID)
			return err
		case wagertx.KindRollback:
			if reference.Kind() == wagertx.KindWin || reference.Kind() == wagertx.KindRefund {
				if err := w.Debit(tx.Money(), now); err != nil {
					if errors.Is(err, wallet.ErrInsufficientBalance) {
						_, err = uc.reject(txCtx, tx, w, failureCodeReversalInsufficientBalance, now, correlationID)
					}
					return err
				}
				_, err = uc.settle(txCtx, tx, w, previousVersion, balanceBefore, ledger.DirectionDebit, tx.Money(), now, correlationID)
				return err
			}
			if err := w.Credit(tx.Money(), now); err != nil {
				return err
			}
			_, err = uc.settle(txCtx, tx, w, previousVersion, balanceBefore, ledger.DirectionCredit, tx.Money(), now, correlationID)
			return err
		default:
			return ErrUnsupportedKind
		}
	})
}

// ExpirePendingReference finaliza uma transação que aguardava referência havia
// tempo demais além do TTL configurado, transicionando-a para FAILED. O lock de
// linha impede que a expiração colida com a chegada tardia da referência.
func (uc *ProcessWagerTransaction) ExpirePendingReference(ctx context.Context, id wagertx.ID) error {
	return uc.uow.Execute(ctx, func(txCtx context.Context) error {
		tx, err := uc.txRepo.FindByIDForUpdate(txCtx, id)
		if err != nil {
			return err
		}
		if tx.Status() != wagertx.StatusPendingReference {
			return nil
		}
		now := uc.now().UTC()
		if err := tx.MarkFailed(failureCodeReferenceExpired, now); err != nil {
			return err
		}
		if err := uc.txRepo.Update(txCtx, tx); err != nil {
			return err
		}
		expiredEvent := event.NewWagerTransactionExpired(uc.idGen.NewID(), string(tx.WalletID()), string(tx.ID()), now, event.WagerTransactionExpiredData{
			TransactionID: string(tx.ID()), WalletID: string(tx.WalletID()), ProviderID: tx.ProviderID(),
			ExternalID: tx.ExternalID(), ReferenceExternalID: tx.ReferenceExternalID(), FailureCode: failureCodeReferenceExpired,
		})
		return uc.outboxRepo.Create(txCtx, expiredEvent)
	})
}

func (uc *ProcessWagerTransaction) attempt(ctx context.Context, in ProcessWagerTransactionInput, kind wagertx.Kind, hash string) (*ProcessWagerTransactionOutput, error) {
	if out, found, err := uc.resolveExisting(ctx, in, hash); err != nil {
		return nil, err
	} else if found {
		return out, nil
	}

	w, err := uc.walletRepo.FindByID(ctx, wallet.ID(in.WalletID))
	if err != nil {
		return nil, err
	}
	if string(w.PlayerID()) != in.PlayerID {
		return nil, ErrWalletPlayerMismatch
	}

	now := uc.now().UTC()
	tx, err := wagertx.NewExternalTransaction(
		wagertx.ID(uc.idGen.NewID()), in.ProviderID, in.ExternalTransactionID, in.IdempotencyKey, hash,
		w.ID(), w.PlayerID(), in.RoundID, in.GameID, kind, in.Money, in.ReferenceExternalTransactionID, now,
	)
	if err != nil {
		return nil, err
	}
	if err := uc.txRepo.Create(ctx, tx); err != nil {
		return nil, err
	}

	correlationID := string(tx.ID())

	if w.Currency() != in.Money.Currency() {
		return uc.reject(ctx, tx, w, wagertx.FailureCodeCurrencyMismatch, now, correlationID)
	}

	var reference *wagertx.WagerTransaction
	if in.ReferenceExternalTransactionID != "" {
		reference, err = uc.txRepo.FindByProviderAndExternalID(ctx, in.ProviderID, in.ReferenceExternalTransactionID)
		if errors.Is(err, ports.ErrNotFound) {
			return uc.pendingReference(ctx, tx, w, now, correlationID)
		}
		if err != nil {
			return nil, err
		}
		if reference.Status() != wagertx.StatusProcessed {
			if reference.IsTerminal() {
				return uc.reject(ctx, tx, w, failureCodeReferenceNotProcessable, now, correlationID)
			}
			return uc.pendingReference(ctx, tx, w, now, correlationID)
		}
		if failureCode := validateReference(kind, tx, reference); failureCode != "" {
			return uc.reject(ctx, tx, w, failureCode, now, correlationID)
		}
		if kind == wagertx.KindRefund || kind == wagertx.KindRollback {
			alreadyReversed, err := uc.txRepo.FindProcessedReversalByReference(ctx, reference.ID())
			if err != nil && !errors.Is(err, ports.ErrNotFound) {
				return nil, err
			}
			if alreadyReversed != nil {
				return uc.reject(ctx, tx, w, failureCodeReferenceAlreadyReversed, now, correlationID)
			}
		}
		if err := tx.ResolveReference(reference.ID(), now); err != nil {
			return nil, err
		}
	}

	previousVersion := w.Version()
	balanceBefore := w.Balance()

	switch kind {
	case wagertx.KindBet:
		if err := w.Debit(in.Money, now); err != nil {
			if errors.Is(err, wallet.ErrInsufficientBalance) {
				return uc.reject(ctx, tx, w, wagertx.FailureCodeInsufficientBalance, now, correlationID)
			}
			return nil, err
		}
		return uc.settle(ctx, tx, w, previousVersion, balanceBefore, ledger.DirectionDebit, in.Money, now, correlationID)

	case wagertx.KindWin:
		if err := w.Credit(in.Money, now); err != nil {
			return nil, err
		}
		return uc.settle(ctx, tx, w, previousVersion, balanceBefore, ledger.DirectionCredit, in.Money, now, correlationID)

	case wagertx.KindRefund:
		if err := w.Credit(in.Money, now); err != nil {
			return nil, err
		}
		return uc.settle(ctx, tx, w, previousVersion, balanceBefore, ledger.DirectionCredit, in.Money, now, correlationID)

	case wagertx.KindRollback:
		direction := ledger.DirectionCredit
		if reference.Kind() == wagertx.KindWin || reference.Kind() == wagertx.KindRefund {
			direction = ledger.DirectionDebit
			if err := w.Debit(in.Money, now); err != nil {
				if errors.Is(err, wallet.ErrInsufficientBalance) {
					return uc.reject(ctx, tx, w, failureCodeReversalInsufficientBalance, now, correlationID)
				}
				return nil, err
			}
		} else if err := w.Credit(in.Money, now); err != nil {
			return nil, err
		}
		return uc.settle(ctx, tx, w, previousVersion, balanceBefore, direction, in.Money, now, correlationID)

	case wagertx.KindLoss:
		if err := tx.MarkProcessed(w.Balance(), now); err != nil {
			return nil, err
		}
		if err := uc.txRepo.Update(ctx, tx); err != nil {
			return nil, err
		}
		processedEvent := event.NewWagerTransactionProcessed(uc.idGen.NewID(), string(w.ID()), correlationID, now, event.WagerTransactionProcessedData{
			TransactionID: string(tx.ID()), WalletID: string(w.ID()), PlayerID: string(w.PlayerID()),
			Kind: string(kind), ProviderID: in.ProviderID, ExternalID: in.ExternalTransactionID, Money: in.Money,
		})
		if err := uc.outboxRepo.Create(ctx, processedEvent); err != nil {
			return nil, err
		}
		return &ProcessWagerTransactionOutput{
			TransactionID: string(tx.ID()), Status: string(tx.Status()), Balance: w.Balance(),
		}, nil
	}

	return nil, ErrUnsupportedKind
}

func (uc *ProcessWagerTransaction) pendingReference(ctx context.Context, tx *wagertx.WagerTransaction, w *wallet.Wallet, now time.Time, correlationID string) (*ProcessWagerTransactionOutput, error) {
	if err := tx.MarkPendingReference(now); err != nil {
		return nil, err
	}
	if err := uc.txRepo.Update(ctx, tx); err != nil {
		return nil, err
	}
	pendingEvent := event.NewWagerTransactionPendingReference(uc.idGen.NewID(), string(w.ID()), correlationID, now, event.WagerTransactionPendingReferenceData{
		TransactionID: string(tx.ID()), WalletID: string(w.ID()), ProviderID: tx.ProviderID(), ExternalID: tx.ExternalID(), ReferenceExternalID: tx.ReferenceExternalID(),
	})
	if err := uc.outboxRepo.Create(ctx, pendingEvent); err != nil {
		return nil, err
	}
	return &ProcessWagerTransactionOutput{TransactionID: string(tx.ID()), Status: string(tx.Status()), Balance: w.Balance()}, nil
}

func validateReference(kind wagertx.Kind, tx, reference *wagertx.WagerTransaction) string {
	if tx.ProviderID() != reference.ProviderID() || tx.PlayerID() != reference.PlayerID() || tx.WalletID() != reference.WalletID() || tx.RoundID() != reference.RoundID() || !tx.Money().Equals(reference.Money()) {
		return failureCodeReferenceMismatch
	}
	switch kind {
	case wagertx.KindWin, wagertx.KindRefund:
		if reference.Kind() != wagertx.KindBet {
			return failureCodeReferenceNotProcessable
		}
	case wagertx.KindRollback:
		if reference.Kind() != wagertx.KindBet && reference.Kind() != wagertx.KindWin && reference.Kind() != wagertx.KindRefund {
			return failureCodeReferenceNotProcessable
		}
	}
	return ""
}

func (uc *ProcessWagerTransaction) resolveExisting(ctx context.Context, in ProcessWagerTransactionInput, hash string) (*ProcessWagerTransactionOutput, bool, error) {
	if in.IdempotencyKey != "" {
		existing, err := uc.txRepo.FindByIdempotencyKey(ctx, in.IdempotencyKey)
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return nil, false, err
		}
		if existing != nil {
			if existing.PayloadHash() != hash {
				return nil, false, ErrIdempotencyConflict
			}
			out, err := uc.replayOutput(ctx, existing)
			if err != nil {
				return nil, false, err
			}
			return out, true, nil
		}
	}

	existingByOp, err := uc.txRepo.FindByProviderAndExternalID(ctx, in.ProviderID, in.ExternalTransactionID)
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		return nil, false, err
	}
	if existingByOp != nil {
		if existingByOp.IdempotencyKey() != in.IdempotencyKey || existingByOp.PayloadHash() != hash {
			return nil, false, ErrIdempotencyConflict
		}
		out, err := uc.replayOutput(ctx, existingByOp)
		if err != nil {
			return nil, false, err
		}
		return out, true, nil
	}

	return nil, false, nil
}

func (uc *ProcessWagerTransaction) replayOutput(ctx context.Context, tx *wagertx.WagerTransaction) (*ProcessWagerTransactionOutput, error) {
	balance := money.Money{}
	if tx.FinancialResult() != nil {
		balance = *tx.FinancialResult()
	} else {
		w, err := uc.walletRepo.FindByID(ctx, tx.WalletID())
		if err != nil {
			return nil, err
		}
		balance = w.Balance()
	}
	return &ProcessWagerTransactionOutput{
		TransactionID:    string(tx.ID()),
		Status:           string(tx.Status()),
		Balance:          balance,
		FailureCode:      tx.FailureCode(),
		IdempotentReplay: true,
	}, nil
}

func (uc *ProcessWagerTransaction) reject(ctx context.Context, tx *wagertx.WagerTransaction, w *wallet.Wallet, failureCode string, now time.Time, correlationID string) (*ProcessWagerTransactionOutput, error) {
	if err := tx.MarkRejectedWithResult(failureCode, w.Balance(), now); err != nil {
		return nil, err
	}
	if err := uc.txRepo.Update(ctx, tx); err != nil {
		return nil, err
	}
	rejectedEvent := event.NewWagerTransactionRejected(uc.idGen.NewID(), string(w.ID()), correlationID, now, event.WagerTransactionRejectedData{
		TransactionID: string(tx.ID()), WalletID: string(w.ID()), ProviderID: tx.ProviderID(), ExternalID: tx.ExternalID(),
		Kind: string(tx.Kind()), FailureCode: failureCode,
	})
	if err := uc.outboxRepo.Create(ctx, rejectedEvent); err != nil {
		return nil, err
	}
	return &ProcessWagerTransactionOutput{
		TransactionID: string(tx.ID()), Status: string(tx.Status()), Balance: w.Balance(), FailureCode: failureCode,
	}, nil
}

func (uc *ProcessWagerTransaction) settle(
	ctx context.Context,
	tx *wagertx.WagerTransaction,
	w *wallet.Wallet,
	previousVersion int64,
	balanceBefore money.Money,
	direction ledger.Direction,
	amount money.Money,
	now time.Time,
	correlationID string,
) (*ProcessWagerTransactionOutput, error) {
	if err := uc.walletRepo.Save(ctx, w, previousVersion); err != nil {
		return nil, err
	}

	entry, err := ledger.NewEntry(ledger.ID(uc.idGen.NewID()), w.ID(), tx.ID(), direction, amount, balanceBefore, w.Balance(), now)
	if err != nil {
		return nil, err
	}
	if err := uc.ledgerRepo.Create(ctx, entry); err != nil {
		return nil, err
	}

	if err := tx.MarkProcessed(w.Balance(), now); err != nil {
		return nil, err
	}
	if err := uc.txRepo.Update(ctx, tx); err != nil {
		return nil, err
	}

	processedEvent := event.NewWagerTransactionProcessed(uc.idGen.NewID(), string(w.ID()), correlationID, now, event.WagerTransactionProcessedData{
		TransactionID: string(tx.ID()), WalletID: string(w.ID()), PlayerID: string(w.PlayerID()),
		Kind: string(tx.Kind()), ProviderID: tx.ProviderID(), ExternalID: tx.ExternalID(), Money: amount,
	})
	if err := uc.outboxRepo.Create(ctx, processedEvent); err != nil {
		return nil, err
	}

	balanceChangedEvent := event.NewWalletBalanceChanged(uc.idGen.NewID(), string(w.ID()), correlationID, now, event.WalletBalanceChangedData{
		WalletID: string(w.ID()), TransactionID: string(tx.ID()), Direction: string(direction),
		Money: amount, BalanceBefore: balanceBefore, BalanceAfter: w.Balance(), WalletVersion: w.Version(),
	})
	if err := uc.outboxRepo.Create(ctx, balanceChangedEvent); err != nil {
		return nil, err
	}

	return &ProcessWagerTransactionOutput{
		TransactionID: string(tx.ID()), Status: string(tx.Status()), Balance: w.Balance(),
	}, nil
}
