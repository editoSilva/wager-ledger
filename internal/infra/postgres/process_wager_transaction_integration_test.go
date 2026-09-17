package postgres

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/editosilva/wager-ledger/internal/application/usecase"
	"github.com/editosilva/wager-ledger/internal/domain/money"
	"github.com/editosilva/wager-ledger/internal/domain/wagertx"
	"github.com/editosilva/wager-ledger/internal/infra/idgen"
)

func TestProcessWagerTransaction_ConcurrentBets_OneProcessedOneRejected(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	walletRepo := NewWalletRepository(pool)
	txRepo := NewWagerTransactionRepository(pool)
	ledgerRepo := NewLedgerRepository(pool)
	outboxRepo := NewOutboxRepository(pool)
	uow := NewUnitOfWork(pool)
	idGen := idgen.NewUUIDGenerator()

	uc := usecase.NewProcessWagerTransaction(uow, walletRepo, txRepo, ledgerRepo, outboxRepo, idGen)

	w := newTestWallet(t, pool, "100.00")
	amount, _ := money.FromDecimalString("80.00", "BRL")

	providerID := "provider-a"
	runID := uuid.NewString()
	roundID := uuid.NewString()

	results := make([]*usecase.ProcessWagerTransactionOutput, 2)
	errs := make([]error, 2)

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = uc.Execute(ctx, usecase.ProcessWagerTransactionInput{
				ProviderID:            providerID,
				ExternalTransactionID: fmt.Sprintf("%s-tx-%d", runID, idx),
				IdempotencyKey:        fmt.Sprintf("%s:%s-tx-%d", providerID, runID, idx),
				PlayerID:              string(w.PlayerID()),
				WalletID:              string(w.ID()),
				RoundID:               roundID,
				GameID:                "game-1",
				Kind:                  string(wagertx.KindBet),
				Money:                 amount,
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("Execute[%d] erro inesperado: %v", i, err)
		}
	}

	var processed, rejected int
	for _, out := range results {
		switch out.Status {
		case string(wagertx.StatusProcessed):
			processed++
		case string(wagertx.StatusRejected):
			rejected++
			if out.FailureCode != wagertx.FailureCodeInsufficientBalance {
				t.Errorf("FailureCode = %s, esperado %s", out.FailureCode, wagertx.FailureCodeInsufficientBalance)
			}
		default:
			t.Errorf("status inesperado: %s", out.Status)
		}
	}
	if processed != 1 || rejected != 1 {
		t.Fatalf("esperava 1 PROCESSED e 1 REJECTED, got processed=%d rejected=%d", processed, rejected)
	}

	final, err := walletRepo.FindByID(ctx, w.ID())
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if final.Balance().DecimalString() != "20.00" {
		t.Errorf("Balance() final = %s, esperado 20.00", final.Balance().DecimalString())
	}

	ledgerSum, err := ledgerRepo.SumByWallet(ctx, w.ID())
	if err != nil {
		t.Fatalf("SumByWallet: %v", err)
	}
	if ledgerSum.DecimalString() != "-80.00" {
		t.Errorf("SumByWallet() = %s, esperado -80.00 (um único débito)", ledgerSum.DecimalString())
	}

	var ledgerCount int
	row := pool.QueryRow(ctx, `SELECT COUNT(*) FROM wallet_ledger_entries WHERE wallet_id = $1`, string(w.ID()))
	if err := row.Scan(&ledgerCount); err != nil {
		t.Fatalf("erro ao contar wallet_ledger_entries: %v", err)
	}
	if ledgerCount != 1 {
		t.Errorf("wallet_ledger_entries = %d, esperado 1", ledgerCount)
	}
}

func TestProcessWagerTransaction_SameBetSentConcurrently_SingleDebit(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	walletRepo := NewWalletRepository(pool)
	txRepo := NewWagerTransactionRepository(pool)
	ledgerRepo := NewLedgerRepository(pool)
	outboxRepo := NewOutboxRepository(pool)
	uow := NewUnitOfWork(pool)
	idGen := idgen.NewUUIDGenerator()

	uc := usecase.NewProcessWagerTransaction(uow, walletRepo, txRepo, ledgerRepo, outboxRepo, idGen)

	w := newTestWallet(t, pool, "1000.00")
	amount, _ := money.FromDecimalString("25.00", "BRL")

	providerID := "provider-a"
	runID := uuid.NewString()
	externalID := runID + "-tx-dup"
	idempotencyKey := providerID + ":" + externalID

	const n = 50
	results := make([]*usecase.ProcessWagerTransactionOutput, n)
	errs := make([]error, n)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = uc.Execute(ctx, usecase.ProcessWagerTransactionInput{
				ProviderID:            providerID,
				ExternalTransactionID: externalID,
				IdempotencyKey:        idempotencyKey,
				PlayerID:              string(w.PlayerID()),
				WalletID:              string(w.ID()),
				RoundID:               "round-1",
				GameID:                "game-1",
				Kind:                  string(wagertx.KindBet),
				Money:                 amount,
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("Execute[%d] erro inesperado: %v", i, err)
		}
	}

	var replays int
	for _, out := range results {
		if out.Status != string(wagertx.StatusProcessed) {
			t.Errorf("Status = %s, esperado PROCESSED em todas as respostas", out.Status)
		}
		if out.IdempotentReplay {
			replays++
		}
	}
	if replays != n-1 {
		t.Errorf("IdempotentReplay=true em %d respostas, esperado %d (todas menos a primeira)", replays, n-1)
	}

	final, err := walletRepo.FindByID(ctx, w.ID())
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if final.Balance().DecimalString() != "975.00" {
		t.Errorf("Balance() final = %s, esperado 975.00 (um único débito de 25.00)", final.Balance().DecimalString())
	}

	var ledgerCount int
	row := pool.QueryRow(ctx, `SELECT COUNT(*) FROM wallet_ledger_entries WHERE wallet_id = $1`, string(w.ID()))
	if err := row.Scan(&ledgerCount); err != nil {
		t.Fatalf("erro ao contar wallet_ledger_entries: %v", err)
	}
	if ledgerCount != 1 {
		t.Errorf("wallet_ledger_entries = %d, esperado 1 (sem duplicidade)", ledgerCount)
	}
}
