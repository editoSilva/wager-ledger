package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/editosilva/wager-ledger/internal/application/ports"
	"github.com/editosilva/wager-ledger/internal/domain/event"
	"github.com/editosilva/wager-ledger/internal/domain/ledger"
	"github.com/editosilva/wager-ledger/internal/domain/money"
	"github.com/editosilva/wager-ledger/internal/domain/wagertx"
	"github.com/editosilva/wager-ledger/internal/domain/wallet"
)

func newTestProcessWagerTransaction() (*ProcessWagerTransaction, *fakeWalletRepo, *fakeTxRepo, *fakeLedgerRepo, *fakeOutboxRepo) {
	walletRepo := newFakeWalletRepo()
	txRepo := newFakeTxRepo()
	ledgerRepo := &fakeLedgerRepo{}
	outboxRepo := &fakeOutboxRepo{}
	uc := NewProcessWagerTransaction(newFakeUOW(walletRepo, txRepo, ledgerRepo, outboxRepo), walletRepo, txRepo, ledgerRepo, outboxRepo, &fakeIDGen{}, nil)
	return uc, walletRepo, txRepo, ledgerRepo, outboxRepo
}

func seedWallet(t *testing.T, repo *fakeWalletRepo, balance string) *wallet.Wallet {
	t.Helper()
	amount, err := money.FromDecimalString(balance, "BRL")
	if err != nil {
		t.Fatalf("FromDecimalString: %v", err)
	}
	w, err := wallet.NewWallet(wallet.ID("wallet-1"), wallet.PlayerID("player-1"), amount, time.Now().UTC())
	if err != nil {
		t.Fatalf("NewWallet: %v", err)
	}
	if err := repo.Create(context.Background(), w); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return w
}

func betInput(idempotencyKey, externalID, amount string) ProcessWagerTransactionInput {
	m, _ := money.FromDecimalString(amount, "BRL")
	return ProcessWagerTransactionInput{
		ProviderID:            "provider-a",
		ExternalTransactionID: externalID,
		IdempotencyKey:        idempotencyKey,
		PlayerID:              "player-1",
		WalletID:              "wallet-1",
		RoundID:               "round-1",
		GameID:                "game-1",
		Kind:                  string(wagertx.KindBet),
		Money:                 m,
	}
}

func TestProcessWagerTransaction_Bet_Success(t *testing.T) {
	uc, walletRepo, _, ledgerRepo, outboxRepo := newTestProcessWagerTransaction()
	seedWallet(t, walletRepo, "100.00")

	out, err := uc.Execute(context.Background(), betInput("key-1", "tx-1", "30.00"))
	if err != nil {
		t.Fatalf("Execute erro inesperado: %v", err)
	}
	if out.Status != string(wagertx.StatusProcessed) {
		t.Errorf("Status = %s, esperado PROCESSED", out.Status)
	}
	if out.Balance.DecimalString() != "70.00" {
		t.Errorf("Balance = %s, esperado 70.00", out.Balance.DecimalString())
	}
	if len(ledgerRepo.entries) != 1 || ledgerRepo.entries[0].Direction() != ledger.DirectionDebit {
		t.Fatalf("esperava 1 lançamento DEBIT, got %v", ledgerRepo.entries)
	}
	if len(outboxRepo.events) != 2 {
		t.Fatalf("esperava 2 eventos de outbox, achou %d", len(outboxRepo.events))
	}
}

func TestProcessWagerTransaction_Bet_InsufficientBalance(t *testing.T) {
	uc, walletRepo, _, ledgerRepo, outboxRepo := newTestProcessWagerTransaction()
	seedWallet(t, walletRepo, "50.00")

	out, err := uc.Execute(context.Background(), betInput("key-1", "tx-1", "80.00"))
	if err != nil {
		t.Fatalf("Execute erro inesperado: %v", err)
	}
	if out.Status != string(wagertx.StatusRejected) {
		t.Errorf("Status = %s, esperado REJECTED", out.Status)
	}
	if out.FailureCode != wagertx.FailureCodeInsufficientBalance {
		t.Errorf("FailureCode = %s, esperado %s", out.FailureCode, wagertx.FailureCodeInsufficientBalance)
	}
	if out.Balance.DecimalString() != "50.00" {
		t.Errorf("Balance = %s, esperado 50.00 (inalterado)", out.Balance.DecimalString())
	}
	if len(ledgerRepo.entries) != 0 {
		t.Errorf("rejeição não deveria criar lançamento de ledger, achou %d", len(ledgerRepo.entries))
	}
	if len(outboxRepo.events) != 1 || outboxRepo.events[0].Type != event.TypeWagerTransactionRejected {
		t.Fatalf("esperava 1 evento WagerTransactionRejected, got %v", outboxRepo.events)
	}
}

func TestProcessWagerTransaction_Win_CreditsWallet(t *testing.T) {
	uc, walletRepo, _, ledgerRepo, _ := newTestProcessWagerTransaction()
	seedWallet(t, walletRepo, "100.00")

	in := betInput("key-1", "tx-1", "40.00")
	in.Kind = string(wagertx.KindWin)

	out, err := uc.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("Execute erro inesperado: %v", err)
	}
	if out.Balance.DecimalString() != "140.00" {
		t.Errorf("Balance = %s, esperado 140.00", out.Balance.DecimalString())
	}
	if ledgerRepo.entries[0].Direction() != ledger.DirectionCredit {
		t.Errorf("Direction = %v, esperado CREDIT", ledgerRepo.entries[0].Direction())
	}
}

func TestProcessWagerTransaction_Loss_NoLedgerNoBalanceChange(t *testing.T) {
	uc, walletRepo, _, ledgerRepo, outboxRepo := newTestProcessWagerTransaction()
	seedWallet(t, walletRepo, "100.00")

	zero, _ := money.Zero("BRL")
	in := betInput("key-1", "tx-1", "0.00")
	in.Kind = string(wagertx.KindLoss)
	in.Money = zero

	out, err := uc.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("Execute erro inesperado: %v", err)
	}
	if out.Status != string(wagertx.StatusProcessed) {
		t.Errorf("Status = %s, esperado PROCESSED", out.Status)
	}
	if out.Balance.DecimalString() != "100.00" {
		t.Errorf("Balance = %s, esperado 100.00 (inalterado)", out.Balance.DecimalString())
	}
	if len(ledgerRepo.entries) != 0 {
		t.Errorf("LOSS não deveria criar lançamento de ledger, achou %d", len(ledgerRepo.entries))
	}
	if len(outboxRepo.events) != 1 || outboxRepo.events[0].Type != event.TypeWagerTransactionProcessed {
		t.Fatalf("esperava só WagerTransactionProcessed, got %v", outboxRepo.events)
	}
}

func TestProcessWagerTransaction_IdempotentReplay_SameKeySameContent(t *testing.T) {
	uc, walletRepo, txRepo, ledgerRepo, _ := newTestProcessWagerTransaction()
	seedWallet(t, walletRepo, "100.00")

	in := betInput("key-1", "tx-1", "30.00")

	first, err := uc.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("primeira Execute: %v", err)
	}

	second, err := uc.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("segunda Execute: %v", err)
	}
	if !second.IdempotentReplay {
		t.Errorf("esperava IdempotentReplay=true")
	}
	if second.TransactionID != first.TransactionID {
		t.Errorf("TransactionID divergente entre replay e original")
	}
	if second.Balance.DecimalString() != first.Balance.DecimalString() {
		t.Errorf("Balance do replay = %s, esperado %s (saldo observado no processamento original)", second.Balance.DecimalString(), first.Balance.DecimalString())
	}
	if len(txRepo.byID) != 1 {
		t.Errorf("replay não deveria criar nova transação, achou %d", len(txRepo.byID))
	}
	if len(ledgerRepo.entries) != 1 {
		t.Errorf("replay não deveria criar novo lançamento, achou %d", len(ledgerRepo.entries))
	}
}

func TestProcessWagerTransaction_RejectedReplay_ReturnsOriginalBalance(t *testing.T) {
	uc, walletRepo, _, _, _ := newTestProcessWagerTransaction()
	seedWallet(t, walletRepo, "50.00")

	rejectedInput := betInput("rejected-key", "rejected-tx", "80.00")
	first, err := uc.Execute(context.Background(), rejectedInput)
	if err != nil {
		t.Fatalf("rejeição inicial: %v", err)
	}
	if first.Status != string(wagertx.StatusRejected) || first.Balance.DecimalString() != "50.00" {
		t.Fatalf("resultado inicial = %#v, esperado REJECTED com saldo 50.00", first)
	}

	credit := betInput("credit-key", "credit-tx", "100.00")
	credit.Kind = string(wagertx.KindWin)
	if _, err := uc.Execute(context.Background(), credit); err != nil {
		t.Fatalf("crédito posterior: %v", err)
	}

	replay, err := uc.Execute(context.Background(), rejectedInput)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replay.IdempotentReplay {
		t.Error("replay deveria ser marcado como idempotente")
	}
	if replay.Balance.DecimalString() != "50.00" {
		t.Errorf("saldo do replay = %s, esperado saldo original 50.00", replay.Balance.DecimalString())
	}
}

func TestProcessWagerTransaction_IdempotencyConflict_SameKeyDifferentContent(t *testing.T) {
	uc, walletRepo, _, _, _ := newTestProcessWagerTransaction()
	seedWallet(t, walletRepo, "100.00")

	if _, err := uc.Execute(context.Background(), betInput("key-1", "tx-1", "30.00")); err != nil {
		t.Fatalf("primeira Execute: %v", err)
	}

	_, err := uc.Execute(context.Background(), betInput("key-1", "tx-1", "50.00"))
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Errorf("esperava ErrIdempotencyConflict, got %v", err)
	}
}

func TestProcessWagerTransaction_IdempotencyConflict_SameOperationDifferentKey(t *testing.T) {
	uc, walletRepo, _, _, _ := newTestProcessWagerTransaction()
	seedWallet(t, walletRepo, "100.00")

	if _, err := uc.Execute(context.Background(), betInput("key-1", "tx-1", "30.00")); err != nil {
		t.Fatalf("primeira Execute: %v", err)
	}

	_, err := uc.Execute(context.Background(), betInput("key-2", "tx-1", "30.00"))
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Errorf("esperava ErrIdempotencyConflict, got %v", err)
	}
}

func TestProcessWagerTransaction_CurrencyMismatch_Rejected(t *testing.T) {
	uc, walletRepo, _, _, _ := newTestProcessWagerTransaction()
	seedWallet(t, walletRepo, "100.00")

	in := betInput("key-1", "tx-1", "30.00")
	usd, _ := money.FromDecimalString("30.00", "USD")
	in.Money = usd

	out, err := uc.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("Execute erro inesperado: %v", err)
	}
	if out.Status != string(wagertx.StatusRejected) {
		t.Errorf("Status = %s, esperado REJECTED", out.Status)
	}
	if out.FailureCode != wagertx.FailureCodeCurrencyMismatch {
		t.Errorf("FailureCode = %s, esperado %s", out.FailureCode, wagertx.FailureCodeCurrencyMismatch)
	}
}

func TestProcessWagerTransaction_UnsupportedKind_Rejected(t *testing.T) {
	uc, walletRepo, _, _, _ := newTestProcessWagerTransaction()
	seedWallet(t, walletRepo, "100.00")

	in := betInput("key-1", "tx-1", "30.00")
	in.Kind = "UNKNOWN"

	_, err := uc.Execute(context.Background(), in)
	if !errors.Is(err, ErrUnsupportedKind) {
		t.Errorf("esperava ErrUnsupportedKind, got %v", err)
	}
}

func TestProcessWagerTransaction_Refund_CreditsReferencedBet(t *testing.T) {
	uc, walletRepo, _, ledgerRepo, _ := newTestProcessWagerTransaction()
	seedWallet(t, walletRepo, "100.00")

	bet := betInput("bet-key", "bet-1", "30.00")
	if _, err := uc.Execute(context.Background(), bet); err != nil {
		t.Fatalf("BET: %v", err)
	}

	refund := betInput("refund-key", "refund-1", "30.00")
	refund.Kind = string(wagertx.KindRefund)
	refund.ReferenceExternalTransactionID = bet.ExternalTransactionID
	out, err := uc.Execute(context.Background(), refund)
	if err != nil {
		t.Fatalf("REFUND: %v", err)
	}
	if out.Status != string(wagertx.StatusProcessed) || out.Balance.DecimalString() != "100.00" {
		t.Fatalf("resultado REFUND = %#v, esperado PROCESSED e saldo 100.00", out)
	}
	if len(ledgerRepo.entries) != 2 || ledgerRepo.entries[1].Direction() != ledger.DirectionCredit {
		t.Fatalf("ledger após REFUND = %#v, esperado crédito de devolução", ledgerRepo.entries)
	}
}

func TestProcessWagerTransaction_RollbackOfBet_CreditsWallet(t *testing.T) {
	uc, walletRepo, _, ledgerRepo, _ := newTestProcessWagerTransaction()
	seedWallet(t, walletRepo, "100.00")

	bet := betInput("bet-key", "bet-1", "30.00")
	if _, err := uc.Execute(context.Background(), bet); err != nil {
		t.Fatalf("BET: %v", err)
	}

	rollback := betInput("rollback-key", "rollback-1", "30.00")
	rollback.Kind = string(wagertx.KindRollback)
	rollback.ReferenceExternalTransactionID = bet.ExternalTransactionID
	out, err := uc.Execute(context.Background(), rollback)
	if err != nil {
		t.Fatalf("ROLLBACK: %v", err)
	}
	if out.Status != string(wagertx.StatusProcessed) || out.Balance.DecimalString() != "100.00" {
		t.Fatalf("resultado ROLLBACK = %#v, esperado PROCESSED e saldo 100.00", out)
	}
	if len(ledgerRepo.entries) != 2 || ledgerRepo.entries[1].Direction() != ledger.DirectionCredit {
		t.Fatalf("ledger após ROLLBACK = %#v, esperado crédito de reversão", ledgerRepo.entries)
	}
}

func TestProcessWagerTransaction_ReferenceNotFound_PersistsPendingReference(t *testing.T) {
	uc, walletRepo, _, ledgerRepo, outboxRepo := newTestProcessWagerTransaction()
	seedWallet(t, walletRepo, "100.00")

	refund := betInput("refund-key", "refund-1", "30.00")
	refund.Kind = string(wagertx.KindRefund)
	refund.ReferenceExternalTransactionID = "missing-bet"
	out, err := uc.Execute(context.Background(), refund)
	if err != nil {
		t.Fatalf("REFUND pendente: %v", err)
	}
	if out.Status != string(wagertx.StatusPendingReference) || out.Balance.DecimalString() != "100.00" {
		t.Fatalf("resultado pendente = %#v, esperado PENDING_REFERENCE com saldo inalterado", out)
	}
	if len(ledgerRepo.entries) != 0 {
		t.Errorf("pendência não deveria criar ledger, achou %d", len(ledgerRepo.entries))
	}
	if len(outboxRepo.events) != 1 || outboxRepo.events[0].Type != event.TypeWagerTransactionPendingReference {
		t.Fatalf("eventos = %#v, esperado WagerTransactionPendingReference", outboxRepo.events)
	}
}

func TestProcessWagerTransaction_ResumePendingReference_WhenReferenceArrives(t *testing.T) {
	uc, walletRepo, _, ledgerRepo, _ := newTestProcessWagerTransaction()
	seedWallet(t, walletRepo, "100.00")

	refund := betInput("refund-key", "refund-1", "30.00")
	refund.Kind = string(wagertx.KindRefund)
	refund.ReferenceExternalTransactionID = "bet-1"
	pending, err := uc.Execute(context.Background(), refund)
	if err != nil {
		t.Fatalf("REFUND pendente: %v", err)
	}

	bet := betInput("bet-key", "bet-1", "30.00")
	if _, err := uc.Execute(context.Background(), bet); err != nil {
		t.Fatalf("BET que resolve referência: %v", err)
	}
	if err := uc.ResumePendingReference(context.Background(), wagertx.ID(pending.TransactionID)); err != nil {
		t.Fatalf("ResumePendingReference: %v", err)
	}

	if len(ledgerRepo.entries) != 2 || ledgerRepo.entries[1].Direction() != ledger.DirectionCredit {
		t.Fatalf("ledger após retomada = %#v, esperado débito BET e crédito REFUND", ledgerRepo.entries)
	}
	found, err := walletRepo.FindByID(context.Background(), "wallet-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if found.Balance().DecimalString() != "100.00" {
		t.Errorf("saldo final = %s, esperado 100.00", found.Balance().DecimalString())
	}
}

func TestProcessWagerTransaction_ExpirePendingReference_TransitionsToFailed(t *testing.T) {
	uc, walletRepo, _, ledgerRepo, outboxRepo := newTestProcessWagerTransaction()
	seedWallet(t, walletRepo, "100.00")

	refund := betInput("refund-key", "refund-1", "30.00")
	refund.Kind = string(wagertx.KindRefund)
	refund.ReferenceExternalTransactionID = "missing-bet-forever"
	pending, err := uc.Execute(context.Background(), refund)
	if err != nil {
		t.Fatalf("REFUND pendente: %v", err)
	}
	if pending.Status != string(wagertx.StatusPendingReference) {
		t.Fatalf("status inicial = %s, esperado PENDING_REFERENCE", pending.Status)
	}

	if err := uc.ExpirePendingReference(context.Background(), wagertx.ID(pending.TransactionID)); err != nil {
		t.Fatalf("ExpirePendingReference: %v", err)
	}

	found, err := uc.txRepo.FindByID(context.Background(), wagertx.ID(pending.TransactionID))
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if found.Status() != wagertx.StatusFailed {
		t.Fatalf("status após expirar = %s, esperado FAILED", found.Status())
	}
	if found.FailureCode() != failureCodeReferenceExpired {
		t.Errorf("failureCode = %s, esperado %s", found.FailureCode(), failureCodeReferenceExpired)
	}
	if len(ledgerRepo.entries) != 0 {
		t.Errorf("expiração não deveria criar ledger, achou %d", len(ledgerRepo.entries))
	}
	lastEvent := outboxRepo.events[len(outboxRepo.events)-1]
	if lastEvent.Type != event.TypeWagerTransactionExpired {
		t.Fatalf("último evento = %s, esperado WagerTransactionExpired", lastEvent.Type)
	}

	w, err := walletRepo.FindByID(context.Background(), "wallet-1")
	if err != nil {
		t.Fatalf("FindByID wallet: %v", err)
	}
	if w.Balance().DecimalString() != "100.00" {
		t.Errorf("saldo após expirar = %s, esperado inalterado 100.00", w.Balance().DecimalString())
	}
}

func TestProcessWagerTransaction_ExpirePendingReference_NoopWhenNotPending(t *testing.T) {
	uc, walletRepo, _, _, outboxRepo := newTestProcessWagerTransaction()
	seedWallet(t, walletRepo, "100.00")

	bet := betInput("bet-key", "bet-1", "30.00")
	out, err := uc.Execute(context.Background(), bet)
	if err != nil {
		t.Fatalf("BET: %v", err)
	}
	if out.Status != string(wagertx.StatusProcessed) {
		t.Fatalf("status = %s, esperado PROCESSED", out.Status)
	}

	eventsBefore := len(outboxRepo.events)
	if err := uc.ExpirePendingReference(context.Background(), wagertx.ID(out.TransactionID)); err != nil {
		t.Fatalf("ExpirePendingReference: %v", err)
	}
	if len(outboxRepo.events) != eventsBefore {
		t.Errorf("expirar transação já processada não deveria emitir eventos novos")
	}

	found, err := uc.txRepo.FindByID(context.Background(), wagertx.ID(out.TransactionID))
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if found.Status() != wagertx.StatusProcessed {
		t.Errorf("status = %s, esperado permanecer PROCESSED", found.Status())
	}
}

func TestProcessWagerTransaction_WalletPlayerMismatch(t *testing.T) {
	uc, walletRepo, _, _, _ := newTestProcessWagerTransaction()
	seedWallet(t, walletRepo, "100.00")

	in := betInput("key-1", "tx-1", "30.00")
	in.PlayerID = "someone-else"

	_, err := uc.Execute(context.Background(), in)
	if !errors.Is(err, ErrWalletPlayerMismatch) {
		t.Errorf("esperava ErrWalletPlayerMismatch, got %v", err)
	}
}

type flakyOnceWalletRepo struct {
	*fakeWalletRepo
	failSaveOnce bool
}

func (r *flakyOnceWalletRepo) Save(ctx context.Context, w *wallet.Wallet, previousVersion int64) error {
	if r.failSaveOnce {
		r.failSaveOnce = false
		return ports.ErrOptimisticLock
	}
	return r.fakeWalletRepo.Save(ctx, w, previousVersion)
}

func TestProcessWagerTransaction_RetriesOnOptimisticLock(t *testing.T) {
	baseWalletRepo := newFakeWalletRepo()
	walletRepo := &flakyOnceWalletRepo{fakeWalletRepo: baseWalletRepo, failSaveOnce: true}
	seedWallet(t, baseWalletRepo, "100.00")

	txRepo := newFakeTxRepo()
	ledgerRepo := &fakeLedgerRepo{}
	outboxRepo := &fakeOutboxRepo{}
	uc := NewProcessWagerTransaction(newFakeUOW(baseWalletRepo, txRepo, ledgerRepo, outboxRepo), walletRepo, txRepo, ledgerRepo, outboxRepo, &fakeIDGen{}, nil)

	out, err := uc.Execute(context.Background(), betInput("key-1", "tx-1", "30.00"))
	if err != nil {
		t.Fatalf("Execute erro inesperado: %v", err)
	}
	if out.Status != string(wagertx.StatusProcessed) {
		t.Errorf("Status = %s, esperado PROCESSED após retry", out.Status)
	}
	if out.Balance.DecimalString() != "70.00" {
		t.Errorf("Balance = %s, esperado 70.00", out.Balance.DecimalString())
	}
	if len(txRepo.byID) != 1 {
		t.Errorf("esperava 1 transação persistida (a tentativa com falha deveria ter sido revertida), achou %d", len(txRepo.byID))
	}
	if len(ledgerRepo.entries) != 1 {
		t.Errorf("esperava 1 lançamento de ledger, achou %d", len(ledgerRepo.entries))
	}
}
