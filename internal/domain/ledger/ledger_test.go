package ledger

import (
	"errors"
	"testing"
	"time"

	"github.com/editosilva/wager-ledger/internal/domain/money"
	"github.com/editosilva/wager-ledger/internal/domain/wagertx"
	"github.com/editosilva/wager-ledger/internal/domain/wallet"
)

var fixedNow = time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)

func mustMoney(t *testing.T, amount string) money.Money {
	t.Helper()
	m, err := money.FromDecimalString(amount, "BRL")
	if err != nil {
		t.Fatalf("mustMoney(%q) erro inesperado: %v", amount, err)
	}
	return m
}

func TestNewEntry_Debit_Valid(t *testing.T) {
	before := mustMoney(t, "100.00")
	amount := mustMoney(t, "25.00")
	after := mustMoney(t, "75.00")

	e, err := NewEntry("entry-1", "wallet-1", "tx-1", DirectionDebit, amount, before, after, fixedNow)
	if err != nil {
		t.Fatalf("NewEntry erro inesperado: %v", err)
	}
	if e.Direction() != DirectionDebit {
		t.Errorf("Direction() = %v, esperado DEBIT", e.Direction())
	}
	if !e.BalanceAfter().Equals(after) {
		t.Errorf("BalanceAfter() = %v, esperado %v", e.BalanceAfter(), after)
	}
}

func TestNewEntry_Credit_Valid(t *testing.T) {
	before := mustMoney(t, "100.00")
	amount := mustMoney(t, "25.00")
	after := mustMoney(t, "125.00")

	e, err := NewEntry("entry-1", "wallet-1", "tx-1", DirectionCredit, amount, before, after, fixedNow)
	if err != nil {
		t.Fatalf("NewEntry erro inesperado: %v", err)
	}
	if e.Direction() != DirectionCredit {
		t.Errorf("Direction() = %v, esperado CREDIT", e.Direction())
	}
	if !e.BalanceAfter().Equals(after) {
		t.Errorf("BalanceAfter() = %v, esperado %v", e.BalanceAfter(), after)
	}
}

func TestNewEntry_Debit_BalanceMismatch_Rejected(t *testing.T) {
	before := mustMoney(t, "100.00")
	amount := mustMoney(t, "25.00")
	wrongAfter := mustMoney(t, "80.00") // deveria ser 75.00

	_, err := NewEntry("entry-1", "wallet-1", "tx-1", DirectionDebit, amount, before, wrongAfter, fixedNow)
	if !errors.Is(err, ErrBalanceMismatch) {
		t.Errorf("esperava ErrBalanceMismatch, got %v", err)
	}
}

func TestNewEntry_Credit_BalanceMismatch_Rejected(t *testing.T) {
	before := mustMoney(t, "100.00")
	amount := mustMoney(t, "25.00")
	wrongAfter := mustMoney(t, "200.00") // deveria ser 125.00

	_, err := NewEntry("entry-1", "wallet-1", "tx-1", DirectionCredit, amount, before, wrongAfter, fixedNow)
	if !errors.Is(err, ErrBalanceMismatch) {
		t.Errorf("esperava ErrBalanceMismatch, got %v", err)
	}
}

func TestNewEntry_ZeroOrNegativeAmount_Rejected(t *testing.T) {
	before := mustMoney(t, "100.00")
	zero, _ := money.Zero("BRL")

	_, err := NewEntry("entry-1", "wallet-1", "tx-1", DirectionDebit, zero, before, before, fixedNow)
	if !errors.Is(err, ErrAmountMustBePositive) {
		t.Errorf("esperava ErrAmountMustBePositive para valor zero, got %v", err)
	}
}

func TestNewEntry_InvalidDirection_Rejected(t *testing.T) {
	before := mustMoney(t, "100.00")
	amount := mustMoney(t, "10.00")
	after := mustMoney(t, "90.00")

	_, err := NewEntry("entry-1", "wallet-1", "tx-1", "SIDEWAYS", amount, before, after, fixedNow)
	if !errors.Is(err, ErrInvalidDirection) {
		t.Errorf("esperava ErrInvalidDirection, got %v", err)
	}
}

func TestNewEntry_CurrencyMismatch_Rejected(t *testing.T) {
	before := mustMoney(t, "100.00")
	after := mustMoney(t, "75.00")
	usdAmount, _ := money.FromDecimalString("25.00", "USD")

	_, err := NewEntry("entry-1", "wallet-1", "tx-1", DirectionDebit, usdAmount, before, after, fixedNow)
	if !errors.Is(err, ErrCurrencyMismatch) {
		t.Errorf("esperava ErrCurrencyMismatch, got %v", err)
	}
}

func TestNewEntry_EmptyIDs_Rejected(t *testing.T) {
	before := mustMoney(t, "100.00")
	amount := mustMoney(t, "10.00")
	after := mustMoney(t, "90.00")

	cases := []struct {
		name          string
		id            ID
		walletID      string
		transactionID string
		wantErr       error
	}{
		{"id vazio", "", "wallet-1", "tx-1", ErrEmptyID},
		{"walletId vazio", "entry-1", "", "tx-1", ErrEmptyWalletID},
		{"transactionId vazio", "entry-1", "wallet-1", "", ErrEmptyTransactionID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewEntry(tc.id, wallet.ID(tc.walletID), wagertx.ID(tc.transactionID), DirectionDebit, amount, before, after, fixedNow)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("esperava %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestNewEntry_ExactZeroBalanceAfter_Valid(t *testing.T) {
	before := mustMoney(t, "50.00")
	amount := mustMoney(t, "50.00")
	after, _ := money.Zero("BRL")

	e, err := NewEntry("entry-1", "wallet-1", "tx-1", DirectionDebit, amount, before, after, fixedNow)
	if err != nil {
		t.Fatalf("débito até saldo exatamente zero deveria ser válido, got err: %v", err)
	}
	if !e.BalanceAfter().IsZero() {
		t.Error("BalanceAfter() deveria ser zero")
	}
}

func TestNewEntry_Getters(t *testing.T) {
	before := mustMoney(t, "100.00")
	amount := mustMoney(t, "25.00")
	after := mustMoney(t, "75.00")

	e, err := NewEntry(ID("entry-1"), wallet.ID("wallet-1"), wagertx.ID("tx-1"), DirectionDebit, amount, before, after, fixedNow)
	if err != nil {
		t.Fatalf("NewEntry erro inesperado: %v", err)
	}

	if e.ID() != ID("entry-1") {
		t.Errorf("ID() = %v, esperado entry-1", e.ID())
	}
	if e.WalletID() != wallet.ID("wallet-1") {
		t.Errorf("WalletID() = %v, esperado wallet-1", e.WalletID())
	}
	if e.TransactionID() != wagertx.ID("tx-1") {
		t.Errorf("TransactionID() = %v, esperado tx-1", e.TransactionID())
	}
	if !e.Amount().Equals(amount) {
		t.Errorf("Amount() = %v, esperado %v", e.Amount(), amount)
	}
	if !e.BalanceBefore().Equals(before) {
		t.Errorf("BalanceBefore() = %v, esperado %v", e.BalanceBefore(), before)
	}
	if !e.CreatedAt().Equal(fixedNow) {
		t.Errorf("CreatedAt() = %v, esperado %v", e.CreatedAt(), fixedNow)
	}
}
