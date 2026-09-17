package wallet

import (
	"errors"
	"testing"
	"time"

	"github.com/editosilva/wager-ledger/internal/domain/money"
)

var fixedNow = time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)

func mustMoney(t *testing.T, amount, currency string) money.Money {
	t.Helper()
	m, err := money.FromDecimalString(amount, currency)
	if err != nil {
		t.Fatalf("mustMoney(%q, %q) erro inesperado: %v", amount, currency, err)
	}
	return m
}

func TestNewWallet_Valid(t *testing.T) {
	initial := mustMoney(t, "1000.00", "BRL")

	w, err := NewWallet("wallet-1", "player-1", initial, fixedNow)
	if err != nil {
		t.Fatalf("NewWallet erro inesperado: %v", err)
	}
	if w.ID() != "wallet-1" {
		t.Errorf("ID() = %v, esperado wallet-1", w.ID())
	}
	if w.PlayerID() != "player-1" {
		t.Errorf("PlayerID() = %v, esperado player-1", w.PlayerID())
	}
	if w.Currency() != "BRL" {
		t.Errorf("Currency() = %v, esperado BRL", w.Currency())
	}
	if !w.Balance().Equals(initial) {
		t.Errorf("Balance() = %v, esperado %v", w.Balance(), initial)
	}
	if w.Version() != 1 {
		t.Errorf("Version() = %d, esperado 1 (seção 6.2 do desafio)", w.Version())
	}
	if !w.CreatedAt().Equal(fixedNow) || !w.UpdatedAt().Equal(fixedNow) {
		t.Error("CreatedAt/UpdatedAt deveriam ser fixedNow na criação")
	}
}

func TestNewWallet_ZeroInitialBalance_Allowed(t *testing.T) {
	zero, _ := money.Zero("BRL")
	w, err := NewWallet("wallet-1", "player-1", zero, fixedNow)
	if err != nil {
		t.Fatalf("saldo inicial zero deveria ser permitido, got err: %v", err)
	}
	if !w.Balance().IsZero() {
		t.Error("saldo deveria ser zero")
	}
}

func TestNewWallet_NegativeInitialBalance_Rejected(t *testing.T) {
	positive := mustMoney(t, "10.00", "BRL")
	negative, err := positive.Negate()
	if err != nil {
		t.Fatalf("Negate erro inesperado: %v", err)
	}

	_, err = NewWallet("wallet-1", "player-1", negative, fixedNow)
	if !errors.Is(err, ErrNegativeInitial) {
		t.Errorf("esperava ErrNegativeInitial, got %v", err)
	}
}

func TestNewWallet_EmptyID(t *testing.T) {
	initial := mustMoney(t, "10.00", "BRL")
	_, err := NewWallet("", "player-1", initial, fixedNow)
	if !errors.Is(err, ErrEmptyID) {
		t.Errorf("esperava ErrEmptyID, got %v", err)
	}
}

func TestNewWallet_EmptyPlayerID(t *testing.T) {
	initial := mustMoney(t, "10.00", "BRL")
	_, err := NewWallet("wallet-1", "", initial, fixedNow)
	if !errors.Is(err, ErrEmptyPlayerID) {
		t.Errorf("esperava ErrEmptyPlayerID, got %v", err)
	}
}

func TestRehydrate_DoesNotReapplyMovements(t *testing.T) {
	balance := mustMoney(t, "500.00", "BRL")
	createdAt := fixedNow.Add(-24 * time.Hour)
	updatedAt := fixedNow

	w, err := Rehydrate("wallet-1", "player-1", balance, 7, createdAt, updatedAt)
	if err != nil {
		t.Fatalf("Rehydrate erro inesperado: %v", err)
	}
	if w.Version() != 7 {
		t.Errorf("Version() = %d, esperado 7 (preservada do banco)", w.Version())
	}
	if !w.Balance().Equals(balance) {
		t.Errorf("Balance() = %v, esperado %v", w.Balance(), balance)
	}
	if !w.CreatedAt().Equal(createdAt) {
		t.Error("CreatedAt deveria ser preservado exatamente como persistido")
	}
}

func TestRehydrate_InvalidVersion(t *testing.T) {
	balance := mustMoney(t, "10.00", "BRL")
	_, err := Rehydrate("wallet-1", "player-1", balance, 0, fixedNow, fixedNow)
	if !errors.Is(err, ErrInvalidVersion) {
		t.Errorf("esperava ErrInvalidVersion, got %v", err)
	}
}

func TestDebit_Success(t *testing.T) {
	initial := mustMoney(t, "100.00", "BRL")
	w, _ := NewWallet("wallet-1", "player-1", initial, fixedNow)

	amount := mustMoney(t, "30.00", "BRL")
	later := fixedNow.Add(time.Minute)

	if err := w.Debit(amount, later); err != nil {
		t.Fatalf("Debit erro inesperado: %v", err)
	}

	if got := w.Balance().DecimalString(); got != "70.00" {
		t.Errorf("Balance() = %s, esperado 70.00", got)
	}
	if w.Version() != 2 {
		t.Errorf("Version() = %d, esperado 2 (incrementou com a mudança de saldo)", w.Version())
	}
	if !w.UpdatedAt().Equal(later) {
		t.Error("UpdatedAt deveria refletir o instante do débito")
	}
}

func TestDebit_InsufficientBalance_LeavesWalletUnchanged(t *testing.T) {
	initial := mustMoney(t, "100.00", "BRL")
	w, _ := NewWallet("wallet-1", "player-1", initial, fixedNow)

	amount := mustMoney(t, "150.00", "BRL")
	err := w.Debit(amount, fixedNow.Add(time.Minute))

	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("esperava ErrInsufficientBalance, got %v", err)
	}
	if !w.Balance().Equals(initial) {
		t.Errorf("Balance() mudou após débito rejeitado: %v", w.Balance())
	}
	if w.Version() != 1 {
		t.Errorf("Version() = %d, não deveria incrementar em débito rejeitado", w.Version())
	}
}

func TestDebit_ExactBalance_ResultsInZero(t *testing.T) {
	initial := mustMoney(t, "100.00", "BRL")
	w, _ := NewWallet("wallet-1", "player-1", initial, fixedNow)

	amount := mustMoney(t, "100.00", "BRL")
	if err := w.Debit(amount, fixedNow.Add(time.Minute)); err != nil {
		t.Fatalf("Debit erro inesperado: %v", err)
	}
	if !w.Balance().IsZero() {
		t.Errorf("Balance() = %v, esperado zero", w.Balance())
	}
}

func TestDebit_CurrencyMismatch(t *testing.T) {
	initial := mustMoney(t, "100.00", "BRL")
	w, _ := NewWallet("wallet-1", "player-1", initial, fixedNow)

	usd := mustMoney(t, "10.00", "USD")
	if err := w.Debit(usd, fixedNow); !errors.Is(err, ErrCurrencyMismatch) {
		t.Errorf("esperava ErrCurrencyMismatch, got %v", err)
	}
}

func TestDebit_ZeroOrNegativeAmount_Rejected(t *testing.T) {
	initial := mustMoney(t, "100.00", "BRL")

	zero, _ := money.Zero("BRL")
	w, _ := NewWallet("wallet-1", "player-1", initial, fixedNow)
	if err := w.Debit(zero, fixedNow); !errors.Is(err, ErrAmountMustBePositive) {
		t.Errorf("débito de zero deveria ser rejeitado, got %v", err)
	}
}

func TestCredit_Success(t *testing.T) {
	initial := mustMoney(t, "100.00", "BRL")
	w, _ := NewWallet("wallet-1", "player-1", initial, fixedNow)

	amount := mustMoney(t, "25.00", "BRL")
	later := fixedNow.Add(time.Minute)

	if err := w.Credit(amount, later); err != nil {
		t.Fatalf("Credit erro inesperado: %v", err)
	}
	if got := w.Balance().DecimalString(); got != "125.00" {
		t.Errorf("Balance() = %s, esperado 125.00", got)
	}
	if w.Version() != 2 {
		t.Errorf("Version() = %d, esperado 2", w.Version())
	}
}

func TestCredit_CurrencyMismatch(t *testing.T) {
	initial := mustMoney(t, "100.00", "BRL")
	w, _ := NewWallet("wallet-1", "player-1", initial, fixedNow)

	usd := mustMoney(t, "10.00", "USD")
	if err := w.Credit(usd, fixedNow); !errors.Is(err, ErrCurrencyMismatch) {
		t.Errorf("esperava ErrCurrencyMismatch, got %v", err)
	}
}

func TestVersion_IncrementsOnlyOnBalanceChange(t *testing.T) {
	initial := mustMoney(t, "1000.00", "BRL")
	w, _ := NewWallet("wallet-1", "player-1", initial, fixedNow)

	amount := mustMoney(t, "10.00", "BRL")

	_ = w.Debit(amount, fixedNow.Add(1*time.Minute))
	_ = w.Debit(amount, fixedNow.Add(2*time.Minute))

	hugeAmount := mustMoney(t, "999999.00", "BRL")
	_ = w.Debit(hugeAmount, fixedNow.Add(3*time.Minute))

	if w.Version() != 3 {
		t.Errorf("Version() = %d, esperado 3 (2 débitos bem-sucedidos, 1 rejeitado não conta)", w.Version())
	}
}
func TestConcurrentDebits_SameWalletInstance(t *testing.T) {
	t.Skip("documentação de design — concorrência real é testada na Fase 14 contra o Postgres")
}
