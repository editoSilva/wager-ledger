package usecase

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/editosilva/wager-ledger/internal/domain/ledger"
	"github.com/editosilva/wager-ledger/internal/domain/money"
)

type fakeSnapshotReader struct {
	calls int
}

func (r *fakeSnapshotReader) ReadSnapshot(ctx context.Context, fn func(ctx context.Context) error) error {
	r.calls++
	return fn(ctx)
}

func newTestReconcileWallet() (*ReconcileWallet, *fakeWalletRepo, *fakeLedgerRepo, *fakeSnapshotReader) {
	walletRepo := newFakeWalletRepo()
	ledgerRepo := &fakeLedgerRepo{}
	snapshot := &fakeSnapshotReader{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	uc := NewReconcileWallet(snapshot, walletRepo, ledgerRepo, nil, logger)
	return uc, walletRepo, ledgerRepo, snapshot
}

func TestReconcileWallet_Consistent(t *testing.T) {
	uc, walletRepo, ledgerRepo, snapshot := newTestReconcileWallet()
	w := seedWallet(t, walletRepo, "100.00")

	entry, err := ledger.NewEntry(ledger.ID("entry-1"), w.ID(), "tx-1", ledger.DirectionCredit, mustMoneyUsecase(t, "100.00"), mustMoneyUsecase(t, "0.00"), mustMoneyUsecase(t, "100.00"), time.Now())
	if err != nil {
		t.Fatalf("NewEntry: %v", err)
	}
	if err := ledgerRepo.Create(context.Background(), entry); err != nil {
		t.Fatalf("Create: %v", err)
	}

	out, err := uc.Execute(context.Background(), w.ID())
	if err != nil {
		t.Fatalf("Execute erro inesperado: %v", err)
	}
	if !out.Consistent {
		t.Errorf("Consistent = false, esperado true (stored=%s calculated=%s)", out.StoredBalance.DecimalString(), out.CalculatedBalance.DecimalString())
	}
	if snapshot.calls != 1 {
		t.Errorf("ReadSnapshot chamado %d vezes, esperado 1 (todas as leituras num único snapshot)", snapshot.calls)
	}
}

func TestReconcileWallet_Divergent(t *testing.T) {
	uc, walletRepo, ledgerRepo, _ := newTestReconcileWallet()
	w := seedWallet(t, walletRepo, "100.00")

	entry, err := ledger.NewEntry(ledger.ID("entry-1"), w.ID(), "tx-1", ledger.DirectionCredit, mustMoneyUsecase(t, "50.00"), mustMoneyUsecase(t, "0.00"), mustMoneyUsecase(t, "50.00"), time.Now())
	if err != nil {
		t.Fatalf("NewEntry: %v", err)
	}
	if err := ledgerRepo.Create(context.Background(), entry); err != nil {
		t.Fatalf("Create: %v", err)
	}

	out, err := uc.Execute(context.Background(), w.ID())
	if err != nil {
		t.Fatalf("Execute erro inesperado: %v", err)
	}
	if out.Consistent {
		t.Errorf("Consistent = true, esperado false (stored=100.00, ledger soma 50.00)")
	}
	if out.Difference.DecimalString() != "50.00" {
		t.Errorf("Difference = %s, esperado 50.00", out.Difference.DecimalString())
	}
}

func mustMoneyUsecase(t *testing.T, amount string) money.Money {
	t.Helper()
	m, err := money.FromDecimalString(amount, "BRL")
	if err != nil {
		t.Fatalf("FromDecimalString(%s): %v", amount, err)
	}
	return m
}
