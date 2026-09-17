package wallet

import (
	"errors"
	"time"

	"github.com/editosilva/wager-ledger/internal/domain/money"
)

var (
	ErrEmptyID              = errors.New("wallet: id vazio")
	ErrEmptyPlayerID        = errors.New("wallet: playerId vazio")
	ErrNegativeInitial      = errors.New("wallet: saldo inicial não pode ser negativo")
	ErrCurrencyMismatch     = errors.New("wallet: moeda da movimentação diverge da moeda da carteira")
	ErrAmountMustBePositive = errors.New("wallet: valor da movimentação deve ser maior que zero")
	ErrInsufficientBalance  = errors.New("wallet: saldo insuficiente")
	ErrInvalidVersion       = errors.New("wallet: versão inválida (deve ser >= 1)")
)

type ID string

type PlayerID string

type Wallet struct {
	id        ID
	playerID  PlayerID
	balance   money.Money
	version   int64
	createdAt time.Time
	updatedAt time.Time
}

func NewWallet(id ID, playerID PlayerID, initialBalance money.Money, now time.Time) (*Wallet, error) {
	if id == "" {
		return nil, ErrEmptyID
	}
	if playerID == "" {
		return nil, ErrEmptyPlayerID
	}
	if initialBalance.IsNegative() {
		return nil, ErrNegativeInitial
	}

	return &Wallet{
		id:        id,
		playerID:  playerID,
		balance:   initialBalance,
		version:   1,
		createdAt: now,
		updatedAt: now,
	}, nil
}

func Rehydrate(id ID, playerID PlayerID, balance money.Money, version int64, createdAt, updatedAt time.Time) (*Wallet, error) {
	if id == "" {
		return nil, ErrEmptyID
	}
	if playerID == "" {
		return nil, ErrEmptyPlayerID
	}
	if balance.IsNegative() {
		return nil, ErrNegativeInitial
	}
	if version < 1 {
		return nil, ErrInvalidVersion
	}

	return &Wallet{
		id:        id,
		playerID:  playerID,
		balance:   balance,
		version:   version,
		createdAt: createdAt,
		updatedAt: updatedAt,
	}, nil
}

// ID devolve o identificador da carteira.
func (w *Wallet) ID() ID { return w.id }

// PlayerID devolve o identificador do jogador dono da carteira.
func (w *Wallet) PlayerID() PlayerID { return w.playerID }

// Currency devolve a moeda da carteira — derivada do saldo, não um campo
// independente, para que carteira e saldo nunca possam divergir em
// moeda.
func (w *Wallet) Currency() string { return w.balance.Currency() }

// Balance devolve o saldo atual.
func (w *Wallet) Balance() money.Money { return w.balance }

// Version devolve a versão atual, incrementada a cada mudança de saldo
// (nunca em qualquer outra alteração) — conforme a seção 6.2 do desafio.
func (w *Wallet) Version() int64 { return w.version }

// CreatedAt devolve o instante de criação da carteira.
func (w *Wallet) CreatedAt() time.Time { return w.createdAt }

// UpdatedAt devolve o instante da última mudança de saldo.
func (w *Wallet) UpdatedAt() time.Time { return w.updatedAt }

// Debit reduz o saldo em amount. Rejeita valor não positivo, moeda
// incompatível, e — a invariante central do agregado — qualquer débito
// que deixaria o saldo negativo. Em qualquer caso de erro, o estado da
// carteira (balance, version, updatedAt) permanece exatamente como
// estava antes da chamada.
func (w *Wallet) Debit(amount money.Money, now time.Time) error {
	if amount.IsNegative() || amount.IsZero() {
		return ErrAmountMustBePositive
	}
	if amount.Currency() != w.Currency() {
		return ErrCurrencyMismatch
	}

	newBalance, err := w.balance.Sub(amount)
	if err != nil {
		return err
	}
	if newBalance.IsNegative() {
		return ErrInsufficientBalance
	}

	w.balance = newBalance
	w.version++
	w.updatedAt = now
	return nil
}

// Credit aumenta o saldo em amount. Rejeita valor não positivo e moeda
// incompatível.
func (w *Wallet) Credit(amount money.Money, now time.Time) error {
	if amount.IsNegative() || amount.IsZero() {
		return ErrAmountMustBePositive
	}
	if amount.Currency() != w.Currency() {
		return ErrCurrencyMismatch
	}

	newBalance, err := w.balance.Add(amount)
	if err != nil {
		return err
	}

	w.balance = newBalance
	w.version++
	w.updatedAt = now
	return nil
}
