package event

import (
	"testing"
	"time"

	"github.com/editosilva/wager-ledger/internal/domain/money"
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

func TestNewWagerTransactionProcessed(t *testing.T) {
	data := WagerTransactionProcessedData{
		TransactionID: "tx-1",
		WalletID:      "wallet-1",
		PlayerID:      "player-1",
		Kind:          "BET",
		Money:         mustMoney(t, "25.00"),
	}
	e := NewWagerTransactionProcessed("event-1", "wallet-1", "corr-1", fixedNow, data)

	if e.Type != TypeWagerTransactionProcessed {
		t.Errorf("Type = %v, esperado %v", e.Type, TypeWagerTransactionProcessed)
	}
	if e.ID != "event-1" || e.AggregateID != "wallet-1" || e.CorrelationID != "corr-1" {
		t.Errorf("envelope inesperado: %+v", e)
	}
	if !e.OccurredAt.Equal(fixedNow) {
		t.Errorf("OccurredAt = %v, esperado %v", e.OccurredAt, fixedNow)
	}
	if e.Version != 1 {
		t.Errorf("Version = %d, esperado 1", e.Version)
	}
	if e.Data.(WagerTransactionProcessedData).TransactionID != "tx-1" {
		t.Errorf("Data inesperado: %+v", e.Data)
	}
}

func TestNewWagerTransactionRejected(t *testing.T) {
	data := WagerTransactionRejectedData{
		TransactionID: "tx-1",
		WalletID:      "wallet-1",
		Kind:          "BET",
		FailureCode:   "INSUFFICIENT_BALANCE",
	}
	e := NewWagerTransactionRejected("event-1", "wallet-1", "corr-1", fixedNow, data)

	if e.Type != TypeWagerTransactionRejected {
		t.Errorf("Type = %v, esperado %v", e.Type, TypeWagerTransactionRejected)
	}
	if e.Data.(WagerTransactionRejectedData).FailureCode != "INSUFFICIENT_BALANCE" {
		t.Errorf("Data inesperado: %+v", e.Data)
	}
}

func TestNewWalletBalanceChanged(t *testing.T) {
	data := WalletBalanceChangedData{
		WalletID:      "wallet-1",
		TransactionID: "tx-1",
		Direction:     "DEBIT",
		Money:         mustMoney(t, "25.00"),
		BalanceBefore: mustMoney(t, "100.00"),
		BalanceAfter:  mustMoney(t, "75.00"),
		WalletVersion: 2,
	}
	e := NewWalletBalanceChanged("event-1", "wallet-1", "corr-1", fixedNow, data)

	if e.Type != TypeWalletBalanceChanged {
		t.Errorf("Type = %v, esperado %v", e.Type, TypeWalletBalanceChanged)
	}
	got := e.Data.(WalletBalanceChangedData)
	if got.WalletVersion != 2 {
		t.Errorf("WalletVersion = %d, esperado 2", got.WalletVersion)
	}
	if !got.BalanceAfter.Equals(data.BalanceAfter) {
		t.Errorf("BalanceAfter = %v, esperado %v", got.BalanceAfter, data.BalanceAfter)
	}
}

func TestNewWagerTransactionPendingReference(t *testing.T) {
	data := WagerTransactionPendingReferenceData{
		TransactionID:       "tx-1",
		WalletID:            "wallet-1",
		ProviderID:          "provider-a",
		ExternalID:          "ext-1",
		ReferenceExternalID: "ext-original",
	}
	e := NewWagerTransactionPendingReference("event-1", "wallet-1", "corr-1", fixedNow, data)

	if e.Type != TypeWagerTransactionPendingReference {
		t.Errorf("Type = %v, esperado %v", e.Type, TypeWagerTransactionPendingReference)
	}
	if e.Data.(WagerTransactionPendingReferenceData).ReferenceExternalID != "ext-original" {
		t.Errorf("Data inesperado: %+v", e.Data)
	}
}
