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
	uc := NewProcessWagerTransaction(newFakeUOW(walletRepo, txRepo, ledgerRepo, outboxRepo), walletRepo, txRepo, ledgerRepo, outboxRepo, &fakeIDGen{})
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
	in.Kind = string(wagertx.KindRefund)

	_, err := uc.Execute(context.Background(), in)
	if !errors.Is(err, ErrUnsupportedKind) {
		t.Errorf("esperava ErrUnsupportedKind, got %v", err)
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
	uc := NewProcessWagerTransaction(newFakeUOW(baseWalletRepo, txRepo, ledgerRepo, outboxRepo), walletRepo, txRepo, ledgerRepo, outboxRepo, &fakeIDGen{})

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
