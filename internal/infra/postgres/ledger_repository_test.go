package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/editosilva/wager-ledger/internal/application/ports"
	"github.com/editosilva/wager-ledger/internal/domain/ledger"
	"github.com/editosilva/wager-ledger/internal/domain/money"
	"github.com/editosilva/wager-ledger/internal/domain/wagertx"
)

func TestLedgerRepository_Create_DuplicateWalletTransaction_ReturnsAlreadyExists(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	txRepo := NewWagerTransactionRepository(pool)
	ledgerRepo := NewLedgerRepository(pool)

	w := newTestWallet(t, pool, "100.00")
	now := time.Now().UTC()

	betAmount, _ := money.FromDecimalString("30.00", "BRL")
	tx, err := wagertx.NewExternalTransaction(
		wagertx.ID(uuid.NewString()), "provider-a", uuid.NewString(), uuid.NewString(), "hash-x",
		w.ID(), w.PlayerID(), "round-1", "game-1",
		wagertx.KindBet, betAmount, "", now,
	)
	if err != nil {
		t.Fatalf("NewExternalTransaction: %v", err)
	}
	if err := txRepo.Create(ctx, tx); err != nil {
		t.Fatalf("txRepo.Create: %v", err)
	}

	balanceAfter, err := w.Balance().Sub(betAmount)
	if err != nil {
		t.Fatalf("Sub: %v", err)
	}
	entry1, err := ledger.NewEntry(ledger.ID(uuid.NewString()), w.ID(), tx.ID(), ledger.DirectionDebit, betAmount, w.Balance(), balanceAfter, now)
	if err != nil {
		t.Fatalf("NewEntry (1): %v", err)
	}
	if err := ledgerRepo.Create(ctx, entry1); err != nil {
		t.Fatalf("primeiro Create deveria ter sucesso: %v", err)
	}

	entry2, err := ledger.NewEntry(ledger.ID(uuid.NewString()), w.ID(), tx.ID(), ledger.DirectionDebit, betAmount, w.Balance(), balanceAfter, now)
	if err != nil {
		t.Fatalf("NewEntry (2): %v", err)
	}
	err = ledgerRepo.Create(ctx, entry2)
	if !errors.Is(err, ports.ErrAlreadyExists) {
		t.Errorf("segundo lançamento para o mesmo (wallet_id, transaction_id) deveria falhar com ErrAlreadyExists, got %v", err)
	}
}

func TestLedgerRepository_SumByWallet_NoEntries_ReturnsNotFound(t *testing.T) {
	pool := testPool(t)
	repo := NewLedgerRepository(pool)

	w := newTestWallet(t, pool, "0.00")

	_, err := repo.SumByWallet(context.Background(), w.ID())
	if !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("esperava ports.ErrNotFound, got %v", err)
	}
}
