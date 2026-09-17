package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/editosilva/wager-ledger/internal/application/ports"
	"github.com/editosilva/wager-ledger/internal/domain/ledger"
	"github.com/editosilva/wager-ledger/internal/domain/money"
	"github.com/editosilva/wager-ledger/internal/domain/wagertx"
	"github.com/editosilva/wager-ledger/internal/domain/wallet"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://wager:wager@localhost:5432/wager_ledger?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("erro ao conectar no Postgres de teste: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newTestWallet(t *testing.T, pool *pgxpool.Pool, initialBalance string) *wallet.Wallet {
	t.Helper()
	repo := NewWalletRepository(pool)

	balance, err := money.FromDecimalString(initialBalance, "BRL")
	if err != nil {
		t.Fatalf("mustMoney: %v", err)
	}

	w, err := wallet.NewWallet(
		wallet.ID(uuid.NewString()),
		wallet.PlayerID(uuid.NewString()),
		balance,
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("NewWallet: %v", err)
	}

	if err := repo.Create(context.Background(), w); err != nil {
		t.Fatalf("Create wallet: %v", err)
	}
	return w
}

func TestWalletRepository_CreateAndFindByID(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewWalletRepository(pool)

	w := newTestWallet(t, pool, "1000.00")

	found, err := repo.FindByID(ctx, w.ID())
	if err != nil {
		t.Fatalf("FindByID erro inesperado: %v", err)
	}
	if !found.Balance().Equals(w.Balance()) {
		t.Errorf("Balance() = %v, esperado %v", found.Balance(), w.Balance())
	}
	if found.Version() != 1 {
		t.Errorf("Version() = %d, esperado 1", found.Version())
	}
}

func TestWalletRepository_FindByID_NotFound(t *testing.T) {
	pool := testPool(t)
	repo := NewWalletRepository(pool)

	_, err := repo.FindByID(context.Background(), wallet.ID(uuid.NewString()))
	if !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("esperava ports.ErrNotFound, got %v", err)
	}
}

func TestWalletRepository_FindByPlayerAndCurrency(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewWalletRepository(pool)

	w := newTestWallet(t, pool, "500.00")

	found, err := repo.FindByPlayerAndCurrency(ctx, w.PlayerID(), "BRL")
	if err != nil {
		t.Fatalf("FindByPlayerAndCurrency erro inesperado: %v", err)
	}
	if found.ID() != w.ID() {
		t.Errorf("ID() = %v, esperado %v", found.ID(), w.ID())
	}
	if !found.Balance().Equals(w.Balance()) {
		t.Errorf("Balance() = %v, esperado %v", found.Balance(), w.Balance())
	}
}

func TestWalletRepository_FindByPlayerAndCurrency_NotFound(t *testing.T) {
	pool := testPool(t)
	repo := NewWalletRepository(pool)

	_, err := repo.FindByPlayerAndCurrency(context.Background(), wallet.PlayerID(uuid.NewString()), "BRL")
	if !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("esperava ports.ErrNotFound, got %v", err)
	}
}

func TestWalletRepository_Create_DuplicatePlayerCurrency(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewWalletRepository(pool)

	balance, _ := money.FromDecimalString("100.00", "BRL")
	playerID := wallet.PlayerID(uuid.NewString())

	w1, _ := wallet.NewWallet(wallet.ID(uuid.NewString()), playerID, balance, time.Now().UTC())
	if err := repo.Create(ctx, w1); err != nil {
		t.Fatalf("primeira Create falhou: %v", err)
	}

	w2, _ := wallet.NewWallet(wallet.ID(uuid.NewString()), playerID, balance, time.Now().UTC())
	err := repo.Create(ctx, w2)
	if !errors.Is(err, ports.ErrAlreadyExists) {
		t.Errorf("esperava ports.ErrAlreadyExists, got %v", err)
	}
}

func TestWalletRepository_Save_OptimisticLock(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewWalletRepository(pool)

	w := newTestWallet(t, pool, "100.00")

	instanceA, err := repo.FindByID(ctx, w.ID())
	if err != nil {
		t.Fatalf("FindByID (A): %v", err)
	}
	instanceB, err := repo.FindByID(ctx, w.ID())
	if err != nil {
		t.Fatalf("FindByID (B): %v", err)
	}

	debit30, _ := money.FromDecimalString("30.00", "BRL")
	versionSeenByA := instanceA.Version()
	versionSeenByB := instanceB.Version()

	if err := instanceA.Debit(debit30, time.Now().UTC()); err != nil {
		t.Fatalf("Debit (A): %v", err)
	}
	if err := repo.Save(ctx, instanceA, versionSeenByA); err != nil {
		t.Fatalf("Save (A) deveria ter sucesso, got: %v", err)
	}

	if err := instanceB.Debit(debit30, time.Now().UTC()); err != nil {
		t.Fatalf("Debit (B, em memória): %v", err)
	}
	err = repo.Save(ctx, instanceB, versionSeenByB)
	if !errors.Is(err, ports.ErrOptimisticLock) {
		t.Errorf("esperava ports.ErrOptimisticLock para B, got %v", err)
	}

	final, err := repo.FindByID(ctx, w.ID())
	if err != nil {
		t.Fatalf("FindByID final: %v", err)
	}
	if final.Balance().DecimalString() != "70.00" {
		t.Errorf("Balance() final = %s, esperado 70.00 (só o débito de A aplicado)", final.Balance().DecimalString())
	}
	if final.Version() != 2 {
		t.Errorf("Version() final = %d, esperado 2 (só um Save bem-sucedido)", final.Version())
	}
}

func TestFullFlow_Debit_And_LedgerEntry_WithinSameTx(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	walletRepo := NewWalletRepository(pool)
	txRepo := NewWagerTransactionRepository(pool)
	ledgerRepo := NewLedgerRepository(pool)

	w := newTestWallet(t, pool, "100.00")
	now := time.Now().UTC()

	betAmount, _ := money.FromDecimalString("80.00", "BRL")
	balanceBefore := w.Balance()

	tx, err := wagertx.NewExternalTransaction(
		wagertx.ID(uuid.NewString()), "provider-a", uuid.NewString(), uuid.NewString(), "hash-x",
		w.ID(), w.PlayerID(), "round-1", "game-1",
		wagertx.KindBet, betAmount, "", now,
	)
	if err != nil {
		t.Fatalf("NewExternalTransaction: %v", err)
	}

	err = WithinTx(ctx, pool, func(txCtx context.Context) error {
		if err := txRepo.Create(txCtx, tx); err != nil {
			return err
		}

		if err := w.Debit(betAmount, now); err != nil {
			return err
		}
		if err := walletRepo.Save(txCtx, w, 1); err != nil {
			return err
		}

		entry, err := ledger.NewEntry(
			ledger.ID(uuid.NewString()), w.ID(), tx.ID(),
			ledger.DirectionDebit, betAmount, balanceBefore, w.Balance(), now,
		)
		if err != nil {
			return err
		}
		if err := ledgerRepo.Create(txCtx, entry); err != nil {
			return err
		}

		if err := tx.MarkProcessed(w.Balance(), now); err != nil {
			return err
		}
		return txRepo.Update(txCtx, tx)
	})
	if err != nil {
		t.Fatalf("WithinTx falhou: %v", err)
	}

	finalWallet, err := walletRepo.FindByID(ctx, w.ID())
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if finalWallet.Balance().DecimalString() != "20.00" {
		t.Errorf("Balance() = %s, esperado 20.00", finalWallet.Balance().DecimalString())
	}

	finalTx, err := txRepo.FindByID(ctx, tx.ID())
	if err != nil {
		t.Fatalf("FindByID tx: %v", err)
	}
	if finalTx.Status() != wagertx.StatusProcessed {
		t.Errorf("Status() = %v, esperado PROCESSED", finalTx.Status())
	}

	ledgerSum, err := ledgerRepo.SumByWallet(ctx, w.ID())
	if err != nil {
		t.Fatalf("SumByWallet: %v", err)
	}
	if ledgerSum.DecimalString() != "-80.00" {
		t.Errorf("SumByWallet() = %s, esperado -80.00 (um débito de 80)", ledgerSum.DecimalString())
	}
}

func TestFullFlow_RollbackOnError(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	walletRepo := NewWalletRepository(pool)
	w := newTestWallet(t, pool, "100.00")

	debitAmount, _ := money.FromDecimalString("30.00", "BRL")

	err := WithinTx(ctx, pool, func(txCtx context.Context) error {
		if err := w.Debit(debitAmount, time.Now().UTC()); err != nil {
			return err
		}
		if err := walletRepo.Save(txCtx, w, 1); err != nil {
			return err
		}
		return errors.New("falha simulada após o débito")
	})
	if err == nil {
		t.Fatal("esperava erro propagado de WithinTx")
	}

	final, err := walletRepo.FindByID(ctx, w.ID())
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if final.Balance().DecimalString() != "100.00" {
		t.Errorf("Balance() = %s, esperado 100.00 (rollback deveria ter desfeito o débito)", final.Balance().DecimalString())
	}
	if final.Version() != 1 {
		t.Errorf("Version() = %d, esperado 1 (rollback não deveria ter persistido o incremento)", final.Version())
	}
}
