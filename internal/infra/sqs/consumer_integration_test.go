package sqsinfra

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/editosilva/wager-ledger/internal/application/ports"
	"github.com/editosilva/wager-ledger/internal/application/usecase"
	"github.com/editosilva/wager-ledger/internal/domain/money"
	"github.com/editosilva/wager-ledger/internal/domain/wallet"
	"github.com/editosilva/wager-ledger/internal/infra/idgen"
	"github.com/editosilva/wager-ledger/internal/infra/postgres"
	"github.com/editosilva/wager-ledger/internal/observability"
)

func integrationTestPool(t *testing.T) *pgxpool.Pool {
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

func newIntegrationWallet(t *testing.T, pool *pgxpool.Pool, balance string) *wallet.Wallet {
	t.Helper()
	repo := postgres.NewWalletRepository(pool)
	amount, err := money.FromDecimalString(balance, "BRL")
	if err != nil {
		t.Fatalf("FromDecimalString: %v", err)
	}
	w, err := wallet.NewWallet(wallet.ID(uuid.NewString()), wallet.PlayerID(uuid.NewString()), amount, time.Now().UTC())
	if err != nil {
		t.Fatalf("NewWallet: %v", err)
	}
	if err := repo.Create(context.Background(), w); err != nil {
		t.Fatalf("Create wallet: %v", err)
	}
	return w
}

func newIntegrationConsumer(pool *pgxpool.Pool) *Consumer {
	inbox := postgres.NewInboxRepository(pool)
	uow := postgres.NewUnitOfWork(pool)
	process := usecase.NewProcessWagerTransaction(
		uow,
		postgres.NewWalletRepository(pool),
		postgres.NewWagerTransactionRepository(pool),
		postgres.NewLedgerRepository(pool),
		postgres.NewOutboxRepository(pool),
		idgen.NewUUIDGenerator(),
		observability.NewMetrics(),
	)
	return &Consumer{inbox: inbox, uow: uow, process: process, metrics: observability.NewMetrics()}
}

func betEnvelopeBody(providerID, externalID, idempotencyKey, walletID, playerID string, amount string) *string {
	body := fmt.Sprintf(
		`{"messageId":%q,"type":"WagerTransactionRequested","data":{"providerId":%q,"externalTransactionId":%q,"idempotencyKey":%q,"playerId":%q,"walletId":%q,"roundId":"round-1","gameId":"game-1","kind":"BET","money":{"amount":%q,"currency":"BRL"}}}`,
		uuid.NewString(), providerID, externalID, idempotencyKey, playerID, walletID, amount,
	)
	return &body
}

// TestConsumer_RedeliveredMessageAfterCommit_IsIdempotent reproduz o cenário
// do README §13 item 5: o commit da transação (inbox + efeito financeiro)
// acontece, mas o DeleteMessage nunca ocorre (processo derrubado, crash,
// timeout de rede). O SQS reentrega a mesma mensagem; o consumidor deve
// tratar a reentrega via inbox sem duplicar o débito.
func TestConsumer_RedeliveredMessageAfterCommit_IsIdempotent(t *testing.T) {
	pool := integrationTestPool(t)
	ctx := context.Background()
	c := newIntegrationConsumer(pool)

	w := newIntegrationWallet(t, pool, "100.00")
	providerID := "provider-a"
	externalID := uuid.NewString()
	idempotencyKey := providerID + ":" + externalID
	sqsMsgID := uuid.NewString()

	body := fmt.Sprintf(
		`{"messageId":%q,"type":"WagerTransactionRequested","data":{"providerId":%q,"externalTransactionId":%q,"idempotencyKey":%q,"playerId":%q,"walletId":%q,"roundId":"round-1","gameId":"game-1","kind":"BET","money":{"amount":"40.00","currency":"BRL"}}}`,
		sqsMsgID, providerID, externalID, idempotencyKey, string(w.PlayerID()), string(w.ID()),
	)

	// Primeira entrega: commit ocorre, mas simulamos a falha do processo
	// ANTES do DeleteMessage — ou seja, simplesmente não chamamos DeleteMessage
	// (é exatamente o estado observável quando o processo morre nessa janela).
	if err := c.handle(ctx, &body, &sqsMsgID); err != nil {
		t.Fatalf("primeira entrega: erro inesperado: %v", err)
	}

	// SQS reentrega a mesma mensagem (visibility timeout expirou porque a
	// mensagem nunca foi deletada). O novo processo/consumidor recebe o
	// mesmo corpo e o mesmo messageId.
	if err := c.handle(ctx, &body, &sqsMsgID); err != nil {
		t.Fatalf("reentrega: erro inesperado: %v", err)
	}
	// Uma terceira reentrega para reforçar que múltiplas redeliveries continuam idempotentes.
	if err := c.handle(ctx, &body, &sqsMsgID); err != nil {
		t.Fatalf("segunda reentrega: erro inesperado: %v", err)
	}

	found, err := postgres.NewWalletRepository(pool).FindByID(ctx, w.ID())
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if found.Balance().DecimalString() != "60.00" {
		t.Errorf("Balance() final = %s, esperado 60.00 (débito único apesar de 3 entregas)", found.Balance().DecimalString())
	}

	var ledgerCount int
	row := pool.QueryRow(ctx, `SELECT COUNT(*) FROM wallet_ledger_entries WHERE wallet_id = $1`, string(w.ID()))
	if err := row.Scan(&ledgerCount); err != nil {
		t.Fatalf("erro ao contar wallet_ledger_entries: %v", err)
	}
	if ledgerCount != 1 {
		t.Errorf("wallet_ledger_entries = %d, esperado 1 (inbox deve bloquear efeito duplicado)", ledgerCount)
	}

	var inboxCount int
	row = pool.QueryRow(ctx, `SELECT COUNT(*) FROM inbox_messages WHERE consumer_name = $1 AND message_id = $2`, consumerName, sqsMsgID)
	if err := row.Scan(&inboxCount); err != nil {
		t.Fatalf("erro ao contar inbox_messages: %v", err)
	}
	if inboxCount != 1 {
		t.Errorf("inbox_messages para essa mensagem = %d, esperado 1 (chave única do inbox)", inboxCount)
	}
}

// TestConsumer_DistinctWallets_ProcessedConcurrentlyWithoutInterference cobre
// o item 3 do README §13: apostas de carteiras diferentes, processadas ao
// mesmo tempo, não podem interferir entre si.
func TestConsumer_DistinctWallets_ProcessedConcurrentlyWithoutInterference(t *testing.T) {
	pool := integrationTestPool(t)
	ctx := context.Background()
	c := newIntegrationConsumer(pool)

	const nWallets = 5
	wallets := make([]*wallet.Wallet, nWallets)
	for i := range wallets {
		wallets[i] = newIntegrationWallet(t, pool, "100.00")
	}

	var wg sync.WaitGroup
	errs := make([]error, nWallets)
	for i, w := range wallets {
		wg.Add(1)
		go func(idx int, w *wallet.Wallet) {
			defer wg.Done()
			providerID := "provider-a"
			externalID := uuid.NewString()
			idempotencyKey := providerID + ":" + externalID
			sqsMsgID := uuid.NewString()
			body := betEnvelopeBody(providerID, externalID, idempotencyKey, string(w.ID()), string(w.PlayerID()), "30.00")
			errs[idx] = c.handle(ctx, body, &sqsMsgID)
		}(i, w)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("handle[%d]: erro inesperado: %v", i, err)
		}
	}

	walletRepo := postgres.NewWalletRepository(pool)
	for i, w := range wallets {
		found, err := walletRepo.FindByID(ctx, w.ID())
		if err != nil {
			t.Fatalf("FindByID[%d]: %v", i, err)
		}
		if found.Balance().DecimalString() != "70.00" {
			t.Errorf("carteira %d: Balance() = %s, esperado 70.00 (sem interferência entre carteiras)", i, found.Balance().DecimalString())
		}
	}
}

// TestCrossChannel_HTTPThenSQS_SameOperation_IsIdempotent cobre o item 10 do
// README §13: a mesma operação lógica (mesmo idempotencyKey / (providerId,
// externalTransactionId)) tentada uma vez via HTTP (ExecuteWithin síncrono,
// simulando o handler HTTP) e uma vez via SQS deve ser deduplicada.
func TestCrossChannel_HTTPThenSQS_SameOperation_IsIdempotent(t *testing.T) {
	pool := integrationTestPool(t)
	ctx := context.Background()
	c := newIntegrationConsumer(pool)

	w := newIntegrationWallet(t, pool, "100.00")
	providerID := "provider-a"
	externalID := uuid.NewString()
	idempotencyKey := providerID + ":" + externalID

	// Canal HTTP: chamada direta ao caso de uso (como o handler HTTP faz).
	httpOut, err := c.process.Execute(ctx, usecase.ProcessWagerTransactionInput{
		ProviderID: providerID, ExternalTransactionID: externalID, IdempotencyKey: idempotencyKey,
		PlayerID: string(w.PlayerID()), WalletID: string(w.ID()), RoundID: "round-1", GameID: "game-1",
		Kind: "BET", Money: mustMoney(t, "40.00"),
	})
	if err != nil {
		t.Fatalf("Execute via HTTP: %v", err)
	}
	if httpOut.IdempotentReplay {
		t.Fatalf("primeira chamada HTTP não deveria ser replay")
	}

	// Canal SQS: a mesma operação lógica chega depois via fila (ex.: reprocessamento,
	// consumidor duplicado, retry do provedor apontando para os dois canais).
	sqsMsgID := uuid.NewString()
	body := betEnvelopeBody(providerID, externalID, idempotencyKey, string(w.ID()), string(w.PlayerID()), "40.00")
	if err := c.handle(ctx, body, &sqsMsgID); err != nil {
		t.Fatalf("handle via SQS: %v", err)
	}

	found, err := postgres.NewWalletRepository(pool).FindByID(ctx, w.ID())
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if found.Balance().DecimalString() != "60.00" {
		t.Errorf("Balance() final = %s, esperado 60.00 (débito único mesmo cruzando HTTP e SQS)", found.Balance().DecimalString())
	}

	var ledgerCount int
	row := pool.QueryRow(ctx, `SELECT COUNT(*) FROM wallet_ledger_entries WHERE wallet_id = $1`, string(w.ID()))
	if err := row.Scan(&ledgerCount); err != nil {
		t.Fatalf("erro ao contar wallet_ledger_entries: %v", err)
	}
	if ledgerCount != 1 {
		t.Errorf("wallet_ledger_entries = %d, esperado 1 (mesma operação vinda de dois canais)", ledgerCount)
	}
}

func mustMoney(t *testing.T, amount string) money.Money {
	t.Helper()
	m, err := money.FromDecimalString(amount, "BRL")
	if err != nil {
		t.Fatalf("FromDecimalString: %v", err)
	}
	return m
}

var _ ports.InboxRepository = (*postgres.InboxRepository)(nil)
