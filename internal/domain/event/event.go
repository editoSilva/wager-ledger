package event

import (
	"time"

	"github.com/editosilva/wager-ledger/internal/domain/money"
)

const (
	TypeWagerTransactionProcessed        = "WagerTransactionProcessed"
	TypeWagerTransactionRejected         = "WagerTransactionRejected"
	TypeWalletBalanceChanged             = "WalletBalanceChanged"
	TypeWagerTransactionPendingReference = "WagerTransactionPendingReference"
)

type Event struct {
	ID            string
	Type          string
	AggregateID   string
	CorrelationID string
	CausationID   string
	OccurredAt    time.Time
	Version       int
	Data          any
}

type WagerTransactionProcessedData struct {
	TransactionID    string       `json:"transactionId"`
	WalletID         string       `json:"walletId"`
	PlayerID         string       `json:"playerId"`
	Kind             string       `json:"kind"`
	ProviderID       string       `json:"providerId,omitempty"`
	ExternalID       string       `json:"externalTransactionId,omitempty"`
	Money            money.Money  `json:"money"`
	FinancialResult  *money.Money `json:"financialResult,omitempty"`
	IdempotentReplay bool         `json:"idempotentReplay"`
}

func NewWagerTransactionProcessed(id, aggregateID, correlationID string, occurredAt time.Time, data WagerTransactionProcessedData) Event {
	return Event{
		ID: id, Type: TypeWagerTransactionProcessed, AggregateID: aggregateID,
		CorrelationID: correlationID, OccurredAt: occurredAt, Version: 1, Data: data,
	}
}

type WagerTransactionRejectedData struct {
	TransactionID string `json:"transactionId"`
	WalletID      string `json:"walletId"`
	ProviderID    string `json:"providerId,omitempty"`
	ExternalID    string `json:"externalTransactionId,omitempty"`
	Kind          string `json:"kind"`
	FailureCode   string `json:"failureCode"`
}

func NewWagerTransactionRejected(id, aggregateID, correlationID string, occurredAt time.Time, data WagerTransactionRejectedData) Event {
	return Event{
		ID: id, Type: TypeWagerTransactionRejected, AggregateID: aggregateID,
		CorrelationID: correlationID, OccurredAt: occurredAt, Version: 1, Data: data,
	}
}

type WalletBalanceChangedData struct {
	WalletID      string      `json:"walletId"`
	TransactionID string      `json:"transactionId"`
	Direction     string      `json:"direction"`
	Money         money.Money `json:"money"`
	BalanceBefore money.Money `json:"balanceBefore"`
	BalanceAfter  money.Money `json:"balanceAfter"`
	WalletVersion int64       `json:"walletVersion"`
}

func NewWalletBalanceChanged(id, aggregateID, correlationID string, occurredAt time.Time, data WalletBalanceChangedData) Event {
	return Event{
		ID: id, Type: TypeWalletBalanceChanged, AggregateID: aggregateID,
		CorrelationID: correlationID, OccurredAt: occurredAt, Version: 1, Data: data,
	}
}

type WagerTransactionPendingReferenceData struct {
	TransactionID       string `json:"transactionId"`
	WalletID            string `json:"walletId"`
	ProviderID          string `json:"providerId"`
	ExternalID          string `json:"externalTransactionId"`
	ReferenceExternalID string `json:"referenceExternalTransactionId"`
}

func NewWagerTransactionPendingReference(id, aggregateID, correlationID string, occurredAt time.Time, data WagerTransactionPendingReferenceData) Event {
	return Event{
		ID: id, Type: TypeWagerTransactionPendingReference, AggregateID: aggregateID,
		CorrelationID: correlationID, OccurredAt: occurredAt, Version: 1, Data: data,
	}
}
