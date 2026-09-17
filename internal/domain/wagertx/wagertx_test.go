package wagertx

import (
	"errors"
	"testing"
	"time"

	"github.com/editosilva/wager-ledger/internal/domain/money"
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

func newValidBet(t *testing.T) *WagerTransaction {
	t.Helper()
	tx, err := NewExternalTransaction(
		"tx-1", "provider-a", "ext-123", "provider-a:ext-123", "hash-abc",
		"wallet-1", "player-1", "round-1", "game-1",
		KindBet, mustMoney(t, "25.00"), "", fixedNow,
	)
	if err != nil {
		t.Fatalf("newValidBet: erro inesperado: %v", err)
	}
	return tx
}

func TestNewExternalTransaction_Bet_Valid(t *testing.T) {
	tx := newValidBet(t)
	if tx.Status() != StatusPending {
		t.Errorf("Status() = %v, esperado PENDING", tx.Status())
	}
	if tx.Kind() != KindBet {
		t.Errorf("Kind() = %v, esperado BET", tx.Kind())
	}
}

func TestNewExternalTransaction_RejectsOpeningKind(t *testing.T) {
	_, err := NewExternalTransaction(
		"tx-1", "provider-a", "ext-123", "key", "hash",
		"wallet-1", "player-1", "round-1", "game-1",
		KindOpening, mustMoney(t, "10.00"), "", fixedNow,
	)
	if !errors.Is(err, ErrOpeningNotExternal) {
		t.Errorf("esperava ErrOpeningNotExternal, got %v", err)
	}
}

func TestNewExternalTransaction_Loss_RequiresZeroAmount(t *testing.T) {
	zero, _ := money.Zero("BRL")
	tx, err := NewExternalTransaction(
		"tx-1", "provider-a", "ext-123", "key", "hash",
		"wallet-1", "player-1", "round-1", "game-1",
		KindLoss, zero, "", fixedNow,
	)
	if err != nil {
		t.Fatalf("LOSS com valor zero deveria ser aceito, got err: %v", err)
	}
	if tx.Kind() != KindLoss {
		t.Errorf("Kind() = %v, esperado LOSS", tx.Kind())
	}

	_, err = NewExternalTransaction(
		"tx-2", "provider-a", "ext-124", "key2", "hash2",
		"wallet-1", "player-1", "round-1", "game-1",
		KindLoss, mustMoney(t, "10.00"), "", fixedNow,
	)
	if !errors.Is(err, ErrLossAmountMustBeZero) {
		t.Errorf("LOSS com valor != 0 deveria ser rejeitado, got %v", err)
	}
}

func TestNewExternalTransaction_BetWinRefundRollback_RequirePositiveAmount(t *testing.T) {
	zero, _ := money.Zero("BRL")
	kinds := []Kind{KindBet, KindWin}
	for _, k := range kinds {
		_, err := NewExternalTransaction(
			"tx-1", "provider-a", "ext-123", "key", "hash",
			"wallet-1", "player-1", "round-1", "game-1",
			k, zero, "", fixedNow,
		)
		if !errors.Is(err, ErrAmountMustBePositive) {
			t.Errorf("%s com valor zero deveria ser rejeitado, got %v", k, err)
		}
	}

	refundRollbackKinds := []Kind{KindRefund, KindRollback}
	for _, k := range refundRollbackKinds {
		_, err := NewExternalTransaction(
			"tx-1", "provider-a", "ext-123", "key", "hash",
			"wallet-1", "player-1", "round-1", "game-1",
			k, zero, "ext-original", fixedNow,
		)
		if !errors.Is(err, ErrAmountMustBePositive) {
			t.Errorf("%s com valor zero deveria ser rejeitado, got %v", k, err)
		}
	}
}

func TestNewExternalTransaction_RefundRollback_RequireReference(t *testing.T) {
	kinds := []Kind{KindRefund, KindRollback}
	for _, k := range kinds {
		_, err := NewExternalTransaction(
			"tx-1", "provider-a", "ext-123", "key", "hash",
			"wallet-1", "player-1", "round-1", "game-1",
			k, mustMoney(t, "10.00"), "", fixedNow,
		)
		if !errors.Is(err, ErrReferenceRequired) {
			t.Errorf("%s sem referência deveria ser rejeitado, got %v", k, err)
		}
	}
}

func TestNewExternalTransaction_BetWinLoss_RejectReference(t *testing.T) {
	_, err := NewExternalTransaction(
		"tx-1", "provider-a", "ext-123", "key", "hash",
		"wallet-1", "player-1", "round-1", "game-1",
		KindBet, mustMoney(t, "10.00"), "ext-nao-deveria-existir", fixedNow,
	)
	if !errors.Is(err, ErrReferenceNotAllowed) {
		t.Errorf("BET com referência preenchida deveria ser rejeitado, got %v", err)
	}
}

func TestNewExternalTransaction_RequiredFieldsMissing(t *testing.T) {
	valid := mustMoney(t, "10.00")
	cases := []struct {
		name       string
		providerID string
		externalID string
		key        string
		hash       string
		walletID   string
		playerID   string
		roundID    string
		gameID     string
		wantErr    error
	}{
		{"providerID vazio", "", "e", "k", "h", "w", "p", "r", "g", ErrEmptyProviderID},
		{"externalID vazio", "prov", "", "k", "h", "w", "p", "r", "g", ErrEmptyExternalID},
		{"idempotencyKey vazio", "prov", "e", "", "h", "w", "p", "r", "g", ErrEmptyIdempotencyKey},
		{"payloadHash vazio", "prov", "e", "k", "", "w", "p", "r", "g", ErrEmptyPayloadHash},
		{"walletID vazio", "prov", "e", "k", "h", "", "p", "r", "g", ErrEmptyWalletID},
		{"playerID vazio", "prov", "e", "k", "h", "w", "", "r", "g", ErrEmptyPlayerID},
		{"roundID vazio", "prov", "e", "k", "h", "w", "p", "", "g", ErrEmptyRoundID},
		{"gameID vazio", "prov", "e", "k", "h", "w", "p", "r", "", ErrEmptyGameID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewExternalTransaction(
				"tx-1", tc.providerID, tc.externalID, tc.key, tc.hash,
				wallet.ID(tc.walletID), wallet.PlayerID(tc.playerID), tc.roundID, tc.gameID,
				KindBet, valid, "", fixedNow,
			)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("esperava %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestNewOpeningTransaction_Valid_StartsProcessed(t *testing.T) {
	tx, err := NewOpeningTransaction("tx-1", "wallet-1", "player-1", mustMoney(t, "1000.00"), fixedNow)
	if err != nil {
		t.Fatalf("NewOpeningTransaction erro inesperado: %v", err)
	}
	if tx.Kind() != KindOpening {
		t.Errorf("Kind() = %v, esperado OPENING", tx.Kind())
	}
	if tx.Status() != StatusProcessed {
		t.Errorf("Status() = %v, esperado PROCESSED (OPENING é sempre síncrono)", tx.Status())
	}
	if !tx.IsTerminal() {
		t.Error("OPENING recém-criada deveria já estar em estado terminal")
	}
}

func TestNewOpeningTransaction_RequiresPositiveAmount(t *testing.T) {
	zero, _ := money.Zero("BRL")
	_, err := NewOpeningTransaction("tx-1", "wallet-1", "player-1", zero, fixedNow)
	if !errors.Is(err, ErrAmountMustBePositive) {
		t.Errorf("esperava ErrAmountMustBePositive, got %v", err)
	}
}

func TestTransitions_PendingToProcessed(t *testing.T) {
	tx := newValidBet(t)
	result := mustMoney(t, "975.00")

	if err := tx.MarkProcessed(result, fixedNow.Add(time.Second)); err != nil {
		t.Fatalf("MarkProcessed erro inesperado: %v", err)
	}
	if tx.Status() != StatusProcessed {
		t.Errorf("Status() = %v, esperado PROCESSED", tx.Status())
	}
	if tx.FinancialResult() == nil || !tx.FinancialResult().Equals(result) {
		t.Error("FinancialResult() deveria refletir o valor passado a MarkProcessed")
	}
}

func TestTransitions_PendingToPendingReferenceToProcessed(t *testing.T) {
	tx := newValidBet(t)

	if err := tx.MarkPendingReference(fixedNow.Add(time.Second)); err != nil {
		t.Fatalf("MarkPendingReference erro inesperado: %v", err)
	}
	if tx.Status() != StatusPendingReference {
		t.Errorf("Status() = %v, esperado PENDING_REFERENCE", tx.Status())
	}

	result := mustMoney(t, "100.00")
	if err := tx.MarkProcessed(result, fixedNow.Add(2*time.Second)); err != nil {
		t.Fatalf("MarkProcessed a partir de PENDING_REFERENCE deveria ser válido, got %v", err)
	}
}

func TestTransitions_PendingToRejected(t *testing.T) {
	tx := newValidBet(t)
	if err := tx.MarkRejected("INSUFFICIENT_BALANCE", fixedNow.Add(time.Second)); err != nil {
		t.Fatalf("MarkRejected erro inesperado: %v", err)
	}
	if tx.Status() != StatusRejected {
		t.Errorf("Status() = %v, esperado REJECTED", tx.Status())
	}
	if tx.FailureCode() != "INSUFFICIENT_BALANCE" {
		t.Errorf("FailureCode() = %q, esperado INSUFFICIENT_BALANCE", tx.FailureCode())
	}
}

func TestTransitions_PendingToFailed(t *testing.T) {
	tx := newValidBet(t)
	if err := tx.MarkFailed("DATABASE_UNAVAILABLE", fixedNow.Add(time.Second)); err != nil {
		t.Fatalf("MarkFailed erro inesperado: %v", err)
	}
	if tx.Status() != StatusFailed {
		t.Errorf("Status() = %v, esperado FAILED", tx.Status())
	}
}

func TestTransitions_RejectMarkFailed_RequireFailureCode(t *testing.T) {
	tx := newValidBet(t)
	if err := tx.MarkRejected("", fixedNow); !errors.Is(err, ErrEmptyFailureCode) {
		t.Errorf("esperava ErrEmptyFailureCode, got %v", err)
	}

	tx2 := newValidBet(t)
	if err := tx2.MarkFailed("", fixedNow); !errors.Is(err, ErrEmptyFailureCode) {
		t.Errorf("esperava ErrEmptyFailureCode, got %v", err)
	}
}

func TestTransitions_NoTransitionFromTerminalState(t *testing.T) {
	terminalSetups := []func(t *testing.T) *WagerTransaction{
		func(t *testing.T) *WagerTransaction {
			tx := newValidBet(t)
			_ = tx.MarkProcessed(mustMoney(t, "10.00"), fixedNow)
			return tx
		},
		func(t *testing.T) *WagerTransaction {
			tx := newValidBet(t)
			_ = tx.MarkRejected("CODE", fixedNow)
			return tx
		},
		func(t *testing.T) *WagerTransaction {
			tx := newValidBet(t)
			_ = tx.MarkFailed("CODE", fixedNow)
			return tx
		},
	}

	for i, setup := range terminalSetups {
		tx := setup(t)
		if !tx.IsTerminal() {
			t.Fatalf("caso %d: transação deveria estar em estado terminal", i)
		}
		if err := tx.MarkProcessed(mustMoney(t, "1.00"), fixedNow); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("caso %d: MarkProcessed a partir de estado terminal deveria falhar, got %v", i, err)
		}
		if err := tx.MarkRejected("X", fixedNow); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("caso %d: MarkRejected a partir de estado terminal deveria falhar, got %v", i, err)
		}
		if err := tx.MarkFailed("X", fixedNow); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("caso %d: MarkFailed a partir de estado terminal deveria falhar, got %v", i, err)
		}
		if err := tx.MarkPendingReference(fixedNow); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("caso %d: MarkPendingReference a partir de estado terminal deveria falhar, got %v", i, err)
		}
	}
}

func TestResolveReference(t *testing.T) {
	tx, err := NewExternalTransaction(
		"tx-1", "provider-a", "ext-refund", "key", "hash",
		"wallet-1", "player-1", "round-1", "game-1",
		KindRefund, mustMoney(t, "25.00"), "ext-original-bet", fixedNow,
	)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}

	if err := tx.ResolveReference("tx-original-bet-id", fixedNow.Add(time.Second)); err != nil {
		t.Fatalf("ResolveReference erro inesperado: %v", err)
	}
	if tx.ResolvedReferenceID() != "tx-original-bet-id" {
		t.Errorf("ResolvedReferenceID() = %v, esperado tx-original-bet-id", tx.ResolvedReferenceID())
	}
}

func TestResolveReference_RejectedAfterTerminal(t *testing.T) {
	tx := newValidBet(t)
	_ = tx.MarkProcessed(mustMoney(t, "10.00"), fixedNow)

	if err := tx.ResolveReference("ref-1", fixedNow); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("esperava ErrInvalidTransition, got %v", err)
	}
}

func TestNewExternalTransaction_Getters(t *testing.T) {
	amount := mustMoney(t, "25.00")
	tx, err := NewExternalTransaction(
		"tx-1", "provider-a", "ext-1", "key-1", "hash-1",
		"wallet-1", "player-1", "round-1", "game-1",
		KindBet, amount, "", fixedNow,
	)
	if err != nil {
		t.Fatalf("NewExternalTransaction erro inesperado: %v", err)
	}

	if tx.ID() != ID("tx-1") {
		t.Errorf("ID() = %v, esperado tx-1", tx.ID())
	}
	if tx.WalletID() != wallet.ID("wallet-1") {
		t.Errorf("WalletID() = %v, esperado wallet-1", tx.WalletID())
	}
	if tx.PlayerID() != wallet.PlayerID("player-1") {
		t.Errorf("PlayerID() = %v, esperado player-1", tx.PlayerID())
	}
	if !tx.Money().Equals(amount) {
		t.Errorf("Money() = %v, esperado %v", tx.Money(), amount)
	}
	if tx.ProviderID() != "provider-a" {
		t.Errorf("ProviderID() = %v, esperado provider-a", tx.ProviderID())
	}
	if tx.ExternalID() != "ext-1" {
		t.Errorf("ExternalID() = %v, esperado ext-1", tx.ExternalID())
	}
	if tx.IdempotencyKey() != "key-1" {
		t.Errorf("IdempotencyKey() = %v, esperado key-1", tx.IdempotencyKey())
	}
	if tx.PayloadHash() != "hash-1" {
		t.Errorf("PayloadHash() = %v, esperado hash-1", tx.PayloadHash())
	}
	if tx.RoundID() != "round-1" {
		t.Errorf("RoundID() = %v, esperado round-1", tx.RoundID())
	}
	if tx.GameID() != "game-1" {
		t.Errorf("GameID() = %v, esperado game-1", tx.GameID())
	}
	if tx.ReferenceExternalID() != "" {
		t.Errorf("ReferenceExternalID() = %v, esperado vazio", tx.ReferenceExternalID())
	}
	if !tx.CreatedAt().Equal(fixedNow) {
		t.Errorf("CreatedAt() = %v, esperado %v", tx.CreatedAt(), fixedNow)
	}
	if !tx.UpdatedAt().Equal(fixedNow) {
		t.Errorf("UpdatedAt() = %v, esperado %v", tx.UpdatedAt(), fixedNow)
	}
}

func TestRehydrate_RoundTrip(t *testing.T) {
	amount := mustMoney(t, "25.00")
	financialResult := mustMoney(t, "75.00")
	updatedAt := fixedNow.Add(time.Minute)

	tx, err := Rehydrate(
		ID("tx-1"), KindBet, StatusProcessed, wallet.ID("wallet-1"), wallet.PlayerID("player-1"), amount,
		"provider-a", "ext-1", "key-1", "hash-1", "round-1", "game-1", "",
		ID("resolved-1"), "FAILURE_CODE", &financialResult,
		fixedNow, updatedAt,
	)
	if err != nil {
		t.Fatalf("Rehydrate erro inesperado: %v", err)
	}

	if tx.Status() != StatusProcessed {
		t.Errorf("Status() = %v, esperado PROCESSED", tx.Status())
	}
	if tx.ResolvedReferenceID() != ID("resolved-1") {
		t.Errorf("ResolvedReferenceID() = %v, esperado resolved-1", tx.ResolvedReferenceID())
	}
	if tx.FailureCode() != "FAILURE_CODE" {
		t.Errorf("FailureCode() = %v, esperado FAILURE_CODE", tx.FailureCode())
	}
	if tx.FinancialResult() == nil || !tx.FinancialResult().Equals(financialResult) {
		t.Errorf("FinancialResult() = %v, esperado %v", tx.FinancialResult(), financialResult)
	}
	if !tx.UpdatedAt().Equal(updatedAt) {
		t.Errorf("UpdatedAt() = %v, esperado %v", tx.UpdatedAt(), updatedAt)
	}
}

func TestRehydrate_EmptyID_ReturnsError(t *testing.T) {
	_, err := Rehydrate(
		ID(""), KindBet, StatusPending, wallet.ID("wallet-1"), wallet.PlayerID("player-1"), mustMoney(t, "1.00"),
		"", "", "", "", "", "", "",
		ID(""), "", nil,
		fixedNow, fixedNow,
	)
	if !errors.Is(err, ErrEmptyID) {
		t.Errorf("esperava ErrEmptyID, got %v", err)
	}
}
