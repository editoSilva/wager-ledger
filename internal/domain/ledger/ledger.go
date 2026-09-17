package ledger

import (
	"errors"
	"time"

	"github.com/editosilva/wager-ledger/internal/domain/money"
	"github.com/editosilva/wager-ledger/internal/domain/wagertx"
	"github.com/editosilva/wager-ledger/internal/domain/wallet"
)

var (
	ErrEmptyID              = errors.New("ledger: id vazio")
	ErrEmptyWalletID        = errors.New("ledger: walletId vazio")
	ErrEmptyTransactionID   = errors.New("ledger: transactionId vazio")
	ErrInvalidDirection     = errors.New("ledger: direção inválida (esperado DEBIT ou CREDIT)")
	ErrAmountMustBePositive = errors.New("ledger: valor do lançamento deve ser maior que zero")
	ErrCurrencyMismatch     = errors.New("ledger: moeda do valor, do saldo anterior e do saldo posterior devem ser a mesma")
	ErrBalanceMismatch      = errors.New("ledger: balanceAfter não corresponde a balanceBefore ± amount para a direção informada")
)

type Direction string

const (
	DirectionDebit  Direction = "DEBIT"
	DirectionCredit Direction = "CREDIT"
)

type ID string

type Entry struct {
	id            ID
	walletID      wallet.ID
	transactionID wagertx.ID
	direction     Direction
	amount        money.Money
	balanceBefore money.Money
	balanceAfter  money.Money
	createdAt     time.Time
}

func NewEntry(
	id ID,
	walletID wallet.ID,
	transactionID wagertx.ID,
	direction Direction,
	amount money.Money,
	balanceBefore money.Money,
	balanceAfter money.Money,
	createdAt time.Time,
) (*Entry, error) {
	if id == "" {
		return nil, ErrEmptyID
	}
	if walletID == "" {
		return nil, ErrEmptyWalletID
	}
	if transactionID == "" {
		return nil, ErrEmptyTransactionID
	}
	if direction != DirectionDebit && direction != DirectionCredit {
		return nil, ErrInvalidDirection
	}
	if amount.IsZero() || amount.IsNegative() {
		return nil, ErrAmountMustBePositive
	}
	if amount.Currency() != balanceBefore.Currency() || amount.Currency() != balanceAfter.Currency() {
		return nil, ErrCurrencyMismatch
	}

	var expectedAfter money.Money
	var err error
	switch direction {
	case DirectionDebit:
		expectedAfter, err = balanceBefore.Sub(amount)
	case DirectionCredit:
		expectedAfter, err = balanceBefore.Add(amount)
	}
	if err != nil {
		return nil, err
	}
	if !expectedAfter.Equals(balanceAfter) {
		return nil, ErrBalanceMismatch
	}

	return &Entry{
		id:            id,
		walletID:      walletID,
		transactionID: transactionID,
		direction:     direction,
		amount:        amount,
		balanceBefore: balanceBefore,
		balanceAfter:  balanceAfter,
		createdAt:     createdAt,
	}, nil
}

func (e *Entry) ID() ID                     { return e.id }
func (e *Entry) WalletID() wallet.ID        { return e.walletID }
func (e *Entry) TransactionID() wagertx.ID  { return e.transactionID }
func (e *Entry) Direction() Direction       { return e.direction }
func (e *Entry) Amount() money.Money        { return e.amount }
func (e *Entry) BalanceBefore() money.Money { return e.balanceBefore }
func (e *Entry) BalanceAfter() money.Money  { return e.balanceAfter }
func (e *Entry) CreatedAt() time.Time       { return e.createdAt }
