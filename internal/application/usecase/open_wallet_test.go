package usecase

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/editosilva/wager-ledger/internal/application/ports"
	"github.com/editosilva/wager-ledger/internal/domain/event"
	"github.com/editosilva/wager-ledger/internal/domain/ledger"
	"github.com/editosilva/wager-ledger/internal/domain/money"
	"github.com/editosilva/wager-ledger/internal/domain/wagertx"
	"github.com/editosilva/wager-ledger/internal/domain/wallet"
)

type fakeUOW struct {
	walletRepo *fakeWalletRepo
	txRepo     *fakeTxRepo
	ledgerRepo *fakeLedgerRepo
	outboxRepo *fakeOutboxRepo
}

func newFakeUOW(walletRepo *fakeWalletRepo, txRepo *fakeTxRepo, ledgerRepo *fakeLedgerRepo, outboxRepo *fakeOutboxRepo) fakeUOW {
	return fakeUOW{walletRepo: walletRepo, txRepo: txRepo, ledgerRepo: ledgerRepo, outboxRepo: outboxRepo}
}

func (u fakeUOW) Execute(ctx context.Context, fn func(ctx context.Context) error) error {
	walletSnap := u.walletRepo.snapshot()
	txSnap := u.txRepo.snapshot()
	ledgerSnap := u.ledgerRepo.snapshot()
	outboxSnap := u.outboxRepo.snapshot()

	if err := fn(ctx); err != nil {
		u.walletRepo.restore(walletSnap)
		u.txRepo.restore(txSnap)
		u.ledgerRepo.restore(ledgerSnap)
		u.outboxRepo.restore(outboxSnap)
		return err
	}
	return nil
}

type fakeIDGen struct{ next int }

func (g *fakeIDGen) NewID() string {
	g.next++
	return fmt.Sprintf("id-%d", g.next)
}

type fakeWalletRepo struct {
	byPlayerCurrency map[string]*wallet.Wallet
	byID             map[wallet.ID]*wallet.Wallet
}

func newFakeWalletRepo() *fakeWalletRepo {
	return &fakeWalletRepo{
		byPlayerCurrency: make(map[string]*wallet.Wallet),
		byID:             make(map[wallet.ID]*wallet.Wallet),
	}
}

func (r *fakeWalletRepo) key(playerID wallet.PlayerID, currency string) string {
	return string(playerID) + ":" + currency
}

func cloneWallet(w *wallet.Wallet) *wallet.Wallet {
	clone, err := wallet.Rehydrate(w.ID(), w.PlayerID(), w.Balance(), w.Version(), w.CreatedAt(), w.UpdatedAt())
	if err != nil {
		panic(err)
	}
	return clone
}

func (r *fakeWalletRepo) FindByID(ctx context.Context, id wallet.ID) (*wallet.Wallet, error) {
	w, ok := r.byID[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return cloneWallet(w), nil
}

func (r *fakeWalletRepo) FindByPlayerAndCurrency(ctx context.Context, playerID wallet.PlayerID, currency string) (*wallet.Wallet, error) {
	w, ok := r.byPlayerCurrency[r.key(playerID, currency)]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return cloneWallet(w), nil
}

func (r *fakeWalletRepo) Create(ctx context.Context, w *wallet.Wallet) error {
	k := r.key(w.PlayerID(), w.Currency())
	if _, exists := r.byPlayerCurrency[k]; exists {
		return ports.ErrAlreadyExists
	}
	stored := cloneWallet(w)
	r.byPlayerCurrency[k] = stored
	r.byID[w.ID()] = stored
	return nil
}

func (r *fakeWalletRepo) Save(ctx context.Context, w *wallet.Wallet, previousVersion int64) error {
	existing, ok := r.byID[w.ID()]
	if !ok {
		return ports.ErrNotFound
	}
	if existing.Version() != previousVersion {
		return ports.ErrOptimisticLock
	}
	stored := cloneWallet(w)
	r.byID[w.ID()] = stored
	r.byPlayerCurrency[r.key(stored.PlayerID(), stored.Currency())] = stored
	return nil
}

func (r *fakeWalletRepo) snapshot() map[wallet.ID]*wallet.Wallet {
	snap := make(map[wallet.ID]*wallet.Wallet, len(r.byID))
	for k, v := range r.byID {
		snap[k] = cloneWallet(v)
	}
	return snap
}

func (r *fakeWalletRepo) restore(snap map[wallet.ID]*wallet.Wallet) {
	r.byID = snap
	r.byPlayerCurrency = make(map[string]*wallet.Wallet, len(snap))
	for _, w := range snap {
		r.byPlayerCurrency[r.key(w.PlayerID(), w.Currency())] = w
	}
}

type fakeTxRepo struct {
	byID map[wagertx.ID]*wagertx.WagerTransaction
}

func newFakeTxRepo() *fakeTxRepo {
	return &fakeTxRepo{byID: make(map[wagertx.ID]*wagertx.WagerTransaction)}
}

func (r *fakeTxRepo) FindByID(ctx context.Context, id wagertx.ID) (*wagertx.WagerTransaction, error) {
	tx, ok := r.byID[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return cloneWagerTx(tx), nil
}

func (r *fakeTxRepo) FindByIDForUpdate(ctx context.Context, id wagertx.ID) (*wagertx.WagerTransaction, error) {
	return r.FindByID(ctx, id)
}

func (r *fakeTxRepo) FindByProviderAndExternalID(ctx context.Context, providerID, externalID string) (*wagertx.WagerTransaction, error) {
	for _, tx := range r.byID {
		if tx.ProviderID() == providerID && tx.ExternalID() == externalID {
			return cloneWagerTx(tx), nil
		}
	}
	return nil, ports.ErrNotFound
}

func (r *fakeTxRepo) FindByIdempotencyKey(ctx context.Context, idempotencyKey string) (*wagertx.WagerTransaction, error) {
	for _, tx := range r.byID {
		if tx.IdempotencyKey() == idempotencyKey {
			return cloneWagerTx(tx), nil
		}
	}
	return nil, ports.ErrNotFound
}

func (r *fakeTxRepo) FindProcessedReversalByReference(ctx context.Context, referenceID wagertx.ID) (*wagertx.WagerTransaction, error) {
	for _, tx := range r.byID {
		if tx.ResolvedReferenceID() == referenceID && tx.Status() == wagertx.StatusProcessed && (tx.Kind() == wagertx.KindRefund || tx.Kind() == wagertx.KindRollback) {
			return cloneWagerTx(tx), nil
		}
	}
	return nil, ports.ErrNotFound
}

func (r *fakeTxRepo) ListPendingReferenceIDs(ctx context.Context, limit int) ([]wagertx.ID, error) {
	ids := make([]wagertx.ID, 0, limit)
	for id, tx := range r.byID {
		if tx.Status() == wagertx.StatusPendingReference {
			ids = append(ids, id)
			if len(ids) == limit {
				break
			}
		}
	}
	return ids, nil
}

func (r *fakeTxRepo) ListStalePendingReferenceIDs(ctx context.Context, olderThan time.Time, limit int) ([]wagertx.ID, error) {
	ids := make([]wagertx.ID, 0, limit)
	for id, tx := range r.byID {
		if tx.Status() == wagertx.StatusPendingReference && tx.CreatedAt().Before(olderThan) {
			ids = append(ids, id)
			if len(ids) == limit {
				break
			}
		}
	}
	return ids, nil
}

func (r *fakeTxRepo) Create(ctx context.Context, tx *wagertx.WagerTransaction) error {
	if _, exists := r.byID[tx.ID()]; exists {
		return ports.ErrAlreadyExists
	}
	for _, existing := range r.byID {
		if existing.ProviderID() == tx.ProviderID() && existing.ExternalID() == tx.ExternalID() {
			return ports.ErrAlreadyExists
		}
		if tx.IdempotencyKey() != "" && existing.IdempotencyKey() == tx.IdempotencyKey() {
			return ports.ErrAlreadyExists
		}
	}
	r.byID[tx.ID()] = cloneWagerTx(tx)
	return nil
}

func (r *fakeTxRepo) Update(ctx context.Context, tx *wagertx.WagerTransaction) error {
	if _, ok := r.byID[tx.ID()]; !ok {
		return ports.ErrNotFound
	}
	r.byID[tx.ID()] = cloneWagerTx(tx)
	return nil
}

func cloneWagerTx(tx *wagertx.WagerTransaction) *wagertx.WagerTransaction {
	clone, err := wagertx.Rehydrate(
		tx.ID(), tx.Kind(), tx.Status(), tx.WalletID(), tx.PlayerID(), tx.Money(),
		tx.ProviderID(), tx.ExternalID(), tx.IdempotencyKey(), tx.PayloadHash(),
		tx.RoundID(), tx.GameID(), tx.ReferenceExternalID(),
		tx.ResolvedReferenceID(), tx.FailureCode(), tx.FinancialResult(),
		tx.CreatedAt(), tx.UpdatedAt(),
	)
	if err != nil {
		panic(err)
	}
	return clone
}

func (r *fakeTxRepo) snapshot() map[wagertx.ID]*wagertx.WagerTransaction {
	snap := make(map[wagertx.ID]*wagertx.WagerTransaction, len(r.byID))
	for k, v := range r.byID {
		snap[k] = cloneWagerTx(v)
	}
	return snap
}

func (r *fakeTxRepo) restore(snap map[wagertx.ID]*wagertx.WagerTransaction) {
	r.byID = snap
}

type fakeLedgerRepo struct {
	entries []*ledger.Entry
}

func (r *fakeLedgerRepo) Create(ctx context.Context, e *ledger.Entry) error {
	r.entries = append(r.entries, e)
	return nil
}

func (r *fakeLedgerRepo) SumByWallet(ctx context.Context, walletID wallet.ID) (money.Money, error) {
	var sum money.Money
	found := false
	for _, e := range r.entries {
		if e.WalletID() != walletID {
			continue
		}
		if !found {
			sum, _ = money.Zero(e.Amount().Currency())
			found = true
		}
		if e.Direction() == ledger.DirectionCredit {
			sum, _ = sum.Add(e.Amount())
		} else {
			sum, _ = sum.Sub(e.Amount())
		}
	}
	if !found {
		return money.Money{}, ports.ErrNotFound
	}
	return sum, nil
}

func (r *fakeLedgerRepo) snapshot() []*ledger.Entry {
	return append([]*ledger.Entry{}, r.entries...)
}

func (r *fakeLedgerRepo) restore(snap []*ledger.Entry) {
	r.entries = snap
}

type fakeOutboxRepo struct {
	events []event.Event
}

func (r *fakeOutboxRepo) Create(ctx context.Context, e event.Event) error {
	r.events = append(r.events, e)
	return nil
}

func (r *fakeOutboxRepo) snapshot() []event.Event {
	return append([]event.Event{}, r.events...)
}

func (r *fakeOutboxRepo) restore(snap []event.Event) {
	r.events = snap
}

func newTestOpenWallet() (*OpenWallet, *fakeWalletRepo, *fakeTxRepo, *fakeLedgerRepo, *fakeOutboxRepo) {
	walletRepo := newFakeWalletRepo()
	txRepo := newFakeTxRepo()
	ledgerRepo := &fakeLedgerRepo{}
	outboxRepo := &fakeOutboxRepo{}
	uc := NewOpenWallet(newFakeUOW(walletRepo, txRepo, ledgerRepo, outboxRepo), walletRepo, txRepo, ledgerRepo, outboxRepo, &fakeIDGen{})
	return uc, walletRepo, txRepo, ledgerRepo, outboxRepo
}

func TestOpenWallet_WithPositiveBalance_CreatesOpeningAndLedger(t *testing.T) {
	uc, _, txRepo, ledgerRepo, outboxRepo := newTestOpenWallet()

	balance, _ := money.FromDecimalString("1000.00", "BRL")
	out, err := uc.Execute(context.Background(), OpenWalletInput{PlayerID: "player-1", InitialBalance: balance})
	if err != nil {
		t.Fatalf("Execute erro inesperado: %v", err)
	}

	if out.Balance.DecimalString() != "1000.00" {
		t.Errorf("Balance = %s, esperado 1000.00", out.Balance.DecimalString())
	}
	if out.Version != 1 {
		t.Errorf("Version = %d, esperado 1", out.Version)
	}

	if len(txRepo.byID) != 1 {
		t.Fatalf("esperava 1 transação OPENING criada, achou %d", len(txRepo.byID))
	}
	for _, tx := range txRepo.byID {
		if tx.Kind() != wagertx.KindOpening {
			t.Errorf("Kind() = %v, esperado OPENING", tx.Kind())
		}
		if tx.Status() != wagertx.StatusProcessed {
			t.Errorf("Status() = %v, esperado PROCESSED", tx.Status())
		}
	}

	if len(ledgerRepo.entries) != 1 {
		t.Fatalf("esperava 1 lançamento de ledger, achou %d", len(ledgerRepo.entries))
	}
	if ledgerRepo.entries[0].Direction() != ledger.DirectionCredit {
		t.Errorf("Direction() = %v, esperado CREDIT", ledgerRepo.entries[0].Direction())
	}

	if len(outboxRepo.events) != 2 {
		t.Fatalf("esperava 2 eventos de outbox, achou %d", len(outboxRepo.events))
	}
	var sawProcessed, sawBalanceChanged bool
	for _, e := range outboxRepo.events {
		switch e.Type {
		case event.TypeWagerTransactionProcessed:
			sawProcessed = true
			data := e.Data.(event.WagerTransactionProcessedData)
			if data.Kind != string(wagertx.KindOpening) {
				t.Errorf("WagerTransactionProcessed.Kind = %s, esperado OPENING", data.Kind)
			}
		case event.TypeWalletBalanceChanged:
			sawBalanceChanged = true
			data := e.Data.(event.WalletBalanceChangedData)
			if data.WalletVersion != 1 {
				t.Errorf("WalletBalanceChanged.WalletVersion = %d, esperado 1", data.WalletVersion)
			}
			if data.BalanceAfter.DecimalString() != "1000.00" {
				t.Errorf("WalletBalanceChanged.BalanceAfter = %s, esperado 1000.00", data.BalanceAfter.DecimalString())
			}
		default:
			t.Errorf("evento inesperado: %s", e.Type)
		}
	}
	if !sawProcessed || !sawBalanceChanged {
		t.Errorf("esperava WagerTransactionProcessed e WalletBalanceChanged, sawProcessed=%v sawBalanceChanged=%v", sawProcessed, sawBalanceChanged)
	}
}

func TestOpenWallet_WithZeroBalance_CreatesOnlyWallet(t *testing.T) {
	uc, _, txRepo, ledgerRepo, outboxRepo := newTestOpenWallet()

	zero, _ := money.Zero("BRL")
	out, err := uc.Execute(context.Background(), OpenWalletInput{PlayerID: "player-1", InitialBalance: zero})
	if err != nil {
		t.Fatalf("Execute erro inesperado: %v", err)
	}

	if !out.Balance.IsZero() {
		t.Errorf("Balance deveria ser zero, got %s", out.Balance.DecimalString())
	}
	if len(txRepo.byID) != 0 {
		t.Errorf("saldo zero não deveria criar OPENING, achou %d transações", len(txRepo.byID))
	}
	if len(ledgerRepo.entries) != 0 {
		t.Errorf("saldo zero não deveria criar lançamento de ledger, achou %d", len(ledgerRepo.entries))
	}
	if len(outboxRepo.events) != 0 {
		t.Errorf("saldo zero não deveria publicar eventos, achou %d", len(outboxRepo.events))
	}
}

func TestOpenWallet_DuplicatePlayerCurrency_ReturnsAlreadyExists(t *testing.T) {
	uc, _, _, _, _ := newTestOpenWallet()

	balance, _ := money.FromDecimalString("100.00", "BRL")
	ctx := context.Background()

	if _, err := uc.Execute(ctx, OpenWalletInput{PlayerID: "player-1", InitialBalance: balance}); err != nil {
		t.Fatalf("primeira Execute falhou: %v", err)
	}

	_, err := uc.Execute(ctx, OpenWalletInput{PlayerID: "player-1", InitialBalance: balance})
	if !errors.Is(err, ErrWalletAlreadyExists) {
		t.Errorf("esperava ErrWalletAlreadyExists, got %v", err)
	}
}

func TestOpenWallet_NegativeInitialBalance_RejectedByDomain(t *testing.T) {
	uc, _, _, _, _ := newTestOpenWallet()

	positive, _ := money.FromDecimalString("10.00", "BRL")
	negative, _ := positive.Negate()

	_, err := uc.Execute(context.Background(), OpenWalletInput{PlayerID: "player-1", InitialBalance: negative})
	if !errors.Is(err, wallet.ErrNegativeInitial) {
		t.Errorf("esperava wallet.ErrNegativeInitial, got %v", err)
	}
}
