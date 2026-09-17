package wagertx

import (
	"errors"
	"time"

	"github.com/editosilva/wager-ledger/internal/domain/money"
	"github.com/editosilva/wager-ledger/internal/domain/wallet"
)

// Sentinel errors — classificáveis via errors.Is.
var (
	ErrEmptyID              = errors.New("wagertx: id interno vazio")
	ErrEmptyProviderID      = errors.New("wagertx: providerId vazio")
	ErrEmptyExternalID      = errors.New("wagertx: externalTransactionId vazio")
	ErrEmptyIdempotencyKey  = errors.New("wagertx: idempotencyKey vazio")
	ErrEmptyPayloadHash     = errors.New("wagertx: hash do payload vazio")
	ErrEmptyWalletID        = errors.New("wagertx: walletId vazio")
	ErrEmptyPlayerID        = errors.New("wagertx: playerId vazio")
	ErrEmptyRoundID         = errors.New("wagertx: roundId vazio")
	ErrEmptyGameID          = errors.New("wagertx: gameId vazio")
	ErrEmptyCurrency        = errors.New("wagertx: money.currency vazio ou ausente")
	ErrOpeningNotExternal   = errors.New("wagertx: OPENING não pode ser criado pelo fluxo externo (HTTP/SQS)")
	ErrInvalidKind          = errors.New("wagertx: tipo de operação inválido")
	ErrLossAmountMustBeZero = errors.New("wagertx: LOSS exige money.amount igual a 0.00")
	ErrAmountMustBePositive = errors.New("wagertx: este tipo de operação exige valor maior que zero")
	ErrReferenceRequired    = errors.New("wagertx: referenceExternalTransactionId é obrigatório para REFUND/ROLLBACK")
	ErrReferenceNotAllowed  = errors.New("wagertx: referenceExternalTransactionId só se aplica a WIN, REFUND ou ROLLBACK")
	ErrEmptyFailureCode     = errors.New("wagertx: failureCode vazio")
	ErrInvalidTransition    = errors.New("wagertx: transição de estado inválida a partir do estado atual")
)

// inputValidationErrors são os erros que NewExternalTransaction retorna por
// dados de entrada malformados (payload do provedor via HTTP/SQS) — nunca
// por falha de infraestrutura. ErrEmptyFailureCode/ErrInvalidTransition não
// entram aqui: são invariantes internas de transição de estado, não erros de
// payload do provedor.
var inputValidationErrors = []error{
	ErrEmptyID, ErrEmptyProviderID, ErrEmptyExternalID, ErrEmptyIdempotencyKey,
	ErrEmptyPayloadHash, ErrEmptyWalletID, ErrEmptyPlayerID, ErrEmptyRoundID,
	ErrEmptyGameID, ErrEmptyCurrency, ErrOpeningNotExternal, ErrInvalidKind,
	ErrLossAmountMustBeZero, ErrAmountMustBePositive, ErrReferenceRequired,
	ErrReferenceNotAllowed,
}

// IsInputValidationError informa se err representa um payload malformado do
// provedor (deveria virar HTTP 400, não 500; e uma mensagem SQS deveria ir
// para a DLQ, não ser reentregue indefinidamente).
func IsInputValidationError(err error) bool {
	for _, sentinel := range inputValidationErrors {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	return false
}

// Kind é o tipo da operação.
type Kind string

const (
	KindBet      Kind = "BET"
	KindWin      Kind = "WIN"
	KindLoss     Kind = "LOSS"
	KindRefund   Kind = "REFUND"
	KindRollback Kind = "ROLLBACK"
	// KindOpening é reservado à abertura interna de carteira — nunca
	// aceito vindo de HTTP ou SQS (seção 6.3 do desafio).
	KindOpening Kind = "OPENING"
)

// Status é o estado da transação na sua máquina de estados.
type Status string

const (
	StatusPending          Status = "PENDING"
	StatusPendingReference Status = "PENDING_REFERENCE"
	StatusProcessed        Status = "PROCESSED"
	StatusRejected         Status = "REJECTED"
	StatusFailed           Status = "FAILED"
)

// ID identifica uma transação internamente, de forma estável — inclusive
// para a origem OPENING, que não tem identificador externo.
type ID string

// WagerTransaction é o registro de uma operação financeira, externa ou de
// abertura interna, e seu estado atual.
type WagerTransaction struct {
	id       ID
	kind     Kind
	status   Status
	walletID wallet.ID
	playerID wallet.PlayerID
	money    money.Money

	// Exclusivos de operações externas — vazios para OPENING.
	providerID          string
	externalID          string
	idempotencyKey      string
	payloadHash         string
	roundID             string
	gameID              string
	referenceExternalID string

	// Preenchidos conforme a transação avança — sempre podem estar vazios.
	resolvedReferenceID ID
	failureCode         string
	financialResult     *money.Money

	// Controle de backoff para tentativas de resolução de PENDING_REFERENCE
	// pelo ReferenceRetryWorker — sempre zero/nulo fora desse estado.
	referenceRetryAttempts int
	referenceNextRetryAt   *time.Time

	createdAt time.Time
	updatedAt time.Time
}

type amountRule int

const (
	amountMustBePositive amountRule = iota
	amountMustBeZero
)

func rulesFor(kind Kind) (rule amountRule, referenceRequired, referenceAllowed bool, ok bool) {
	switch kind {
	case KindBet:
		return amountMustBePositive, false, false, true
	case KindWin:
		return amountMustBePositive, false, true, true
	case KindLoss:
		return amountMustBeZero, false, false, true
	case KindRefund, KindRollback:
		return amountMustBePositive, true, true, true
	default:
		return 0, false, false, false
	}
}

// NewExternalTransaction constrói uma transação PENDING a partir de uma
// operação recebida por HTTP ou SQS.
func NewExternalTransaction(
	id ID,
	providerID string,
	externalID string,
	idempotencyKey string,
	payloadHash string,
	walletID wallet.ID,
	playerID wallet.PlayerID,
	roundID string,
	gameID string,
	kind Kind,
	amount money.Money,
	referenceExternalID string,
	now time.Time,
) (*WagerTransaction, error) {
	if id == "" {
		return nil, ErrEmptyID
	}
	if kind == KindOpening {
		return nil, ErrOpeningNotExternal
	}
	rule, refRequired, refAllowed, known := rulesFor(kind)
	if !known {
		return nil, ErrInvalidKind
	}
	if providerID == "" {
		return nil, ErrEmptyProviderID
	}
	if externalID == "" {
		return nil, ErrEmptyExternalID
	}
	if idempotencyKey == "" {
		return nil, ErrEmptyIdempotencyKey
	}
	if payloadHash == "" {
		return nil, ErrEmptyPayloadHash
	}
	if walletID == "" {
		return nil, ErrEmptyWalletID
	}
	if playerID == "" {
		return nil, ErrEmptyPlayerID
	}
	if roundID == "" {
		return nil, ErrEmptyRoundID
	}
	if gameID == "" {
		return nil, ErrEmptyGameID
	}
	if amount.Currency() == "" {
		// Cobre o caso em que o JSON de entrada não trazia a chave "money"
		// (encoding/json não invoca UnmarshalJSON para uma chave ausente,
		// então amount fica no zero-value sem passar pela validação ISO
		// 4217 de money.FromDecimalString). Sem essa checagem, um LOSS
		// (que aceita amount == 0.00) passaria a validação de valor e só
		// falharia depois, na constraint wager_tx_currency_format do
		// Postgres, como erro genérico de infraestrutura.
		return nil, ErrEmptyCurrency
	}

	switch rule {
	case amountMustBeZero:
		if !amount.IsZero() {
			return nil, ErrLossAmountMustBeZero
		}
	case amountMustBePositive:
		if amount.IsZero() || amount.IsNegative() {
			return nil, ErrAmountMustBePositive
		}
	}

	if refRequired && referenceExternalID == "" {
		return nil, ErrReferenceRequired
	}
	if !refAllowed && referenceExternalID != "" {
		return nil, ErrReferenceNotAllowed
	}

	return &WagerTransaction{
		id:                  id,
		kind:                kind,
		status:              StatusPending,
		walletID:            walletID,
		playerID:            playerID,
		money:               amount,
		providerID:          providerID,
		externalID:          externalID,
		idempotencyKey:      idempotencyKey,
		payloadHash:         payloadHash,
		roundID:             roundID,
		gameID:              gameID,
		referenceExternalID: referenceExternalID,
		createdAt:           now,
		updatedAt:           now,
	}, nil
}

func NewOpeningTransaction(id ID, walletID wallet.ID, playerID wallet.PlayerID, amount money.Money, now time.Time) (*WagerTransaction, error) {
	if id == "" {
		return nil, ErrEmptyID
	}
	if walletID == "" {
		return nil, ErrEmptyWalletID
	}
	if playerID == "" {
		return nil, ErrEmptyPlayerID
	}
	if amount.IsZero() || amount.IsNegative() {
		return nil, ErrAmountMustBePositive
	}

	return &WagerTransaction{
		id:        id,
		kind:      KindOpening,
		status:    StatusProcessed,
		walletID:  walletID,
		playerID:  playerID,
		money:     amount,
		createdAt: now,
		updatedAt: now,
	}, nil
}

func Rehydrate(
	id ID,
	kind Kind,
	status Status,
	walletID wallet.ID,
	playerID wallet.PlayerID,
	amount money.Money,
	providerID string,
	externalID string,
	idempotencyKey string,
	payloadHash string,
	roundID string,
	gameID string,
	referenceExternalID string,
	resolvedReferenceID ID,
	failureCode string,
	financialResult *money.Money,
	referenceRetryAttempts int,
	referenceNextRetryAt *time.Time,
	createdAt time.Time,
	updatedAt time.Time,
) (*WagerTransaction, error) {
	if id == "" {
		return nil, ErrEmptyID
	}
	return &WagerTransaction{
		id:                     id,
		kind:                   kind,
		status:                 status,
		walletID:               walletID,
		playerID:               playerID,
		money:                  amount,
		providerID:             providerID,
		externalID:             externalID,
		idempotencyKey:         idempotencyKey,
		payloadHash:            payloadHash,
		roundID:                roundID,
		gameID:                 gameID,
		referenceExternalID:    referenceExternalID,
		resolvedReferenceID:    resolvedReferenceID,
		failureCode:            failureCode,
		financialResult:        financialResult,
		referenceRetryAttempts: referenceRetryAttempts,
		referenceNextRetryAt:   referenceNextRetryAt,
		createdAt:              createdAt,
		updatedAt:              updatedAt,
	}, nil
}

// Getters — leitura apenas.
func (t *WagerTransaction) ID() ID                           { return t.id }
func (t *WagerTransaction) Kind() Kind                       { return t.kind }
func (t *WagerTransaction) Status() Status                   { return t.status }
func (t *WagerTransaction) WalletID() wallet.ID              { return t.walletID }
func (t *WagerTransaction) PlayerID() wallet.PlayerID        { return t.playerID }
func (t *WagerTransaction) Money() money.Money               { return t.money }
func (t *WagerTransaction) ProviderID() string               { return t.providerID }
func (t *WagerTransaction) ExternalID() string               { return t.externalID }
func (t *WagerTransaction) IdempotencyKey() string           { return t.idempotencyKey }
func (t *WagerTransaction) PayloadHash() string              { return t.payloadHash }
func (t *WagerTransaction) RoundID() string                  { return t.roundID }
func (t *WagerTransaction) GameID() string                   { return t.gameID }
func (t *WagerTransaction) ReferenceExternalID() string      { return t.referenceExternalID }
func (t *WagerTransaction) ResolvedReferenceID() ID          { return t.resolvedReferenceID }
func (t *WagerTransaction) FailureCode() string              { return t.failureCode }
func (t *WagerTransaction) FinancialResult() *money.Money    { return t.financialResult }
func (t *WagerTransaction) CreatedAt() time.Time             { return t.createdAt }
func (t *WagerTransaction) UpdatedAt() time.Time             { return t.updatedAt }
func (t *WagerTransaction) ReferenceRetryAttempts() int      { return t.referenceRetryAttempts }
func (t *WagerTransaction) ReferenceNextRetryAt() *time.Time { return t.referenceNextRetryAt }

// IsTerminal indica se o estado atual não admite mais transições.
func (t *WagerTransaction) IsTerminal() bool {
	switch t.status {
	case StatusProcessed, StatusRejected, StatusFailed:
		return true
	default:
		return false
	}
}

func (t *WagerTransaction) MarkPendingReference(now time.Time) error {
	if t.status != StatusPending {
		return ErrInvalidTransition
	}
	t.status = StatusPendingReference
	t.updatedAt = now
	return nil
}

func (t *WagerTransaction) MarkProcessed(financialResult money.Money, now time.Time) error {
	if t.status != StatusPending && t.status != StatusPendingReference {
		return ErrInvalidTransition
	}
	t.status = StatusProcessed
	t.financialResult = &financialResult
	t.updatedAt = now
	return nil
}

func (t *WagerTransaction) MarkRejected(failureCode string, now time.Time) error {
	return t.markRejected(failureCode, nil, now)
}

// MarkRejectedWithResult encerra uma operação rejeitada preservando o saldo
// que foi devolvido ao provedor. Assim, um replay não depende de leituras
// posteriores da carteira.
func (t *WagerTransaction) MarkRejectedWithResult(failureCode string, financialResult money.Money, now time.Time) error {
	return t.markRejected(failureCode, &financialResult, now)
}

func (t *WagerTransaction) markRejected(failureCode string, financialResult *money.Money, now time.Time) error {
	if failureCode == "" {
		return ErrEmptyFailureCode
	}
	if t.status != StatusPending && t.status != StatusPendingReference {
		return ErrInvalidTransition
	}
	t.status = StatusRejected
	t.failureCode = failureCode
	t.financialResult = financialResult
	t.updatedAt = now
	return nil
}

// MarkFailed transiciona para FAILED (estado terminal) — falha permanente
// de infraestrutura, distinta de rejeição de negócio.
func (t *WagerTransaction) MarkFailed(failureCode string, now time.Time) error {
	if failureCode == "" {
		return ErrEmptyFailureCode
	}
	if t.status != StatusPending && t.status != StatusPendingReference {
		return ErrInvalidTransition
	}
	t.status = StatusFailed
	t.failureCode = failureCode
	t.updatedAt = now
	return nil
}

func (t *WagerTransaction) ResolveReference(referenceID ID, now time.Time) error {
	if referenceID == "" {
		return ErrEmptyID
	}
	if t.IsTerminal() {
		return ErrInvalidTransition
	}
	t.resolvedReferenceID = referenceID
	t.referenceRetryAttempts = 0
	t.referenceNextRetryAt = nil
	t.updatedAt = now
	return nil
}

// RecordReferenceRetryAttempt registra uma tentativa sem sucesso de resolver
// a referência pendente, agendando a próxima tentativa via
// referenceNextRetryAt. Isso permite que ListPendingReferenceIDs pule
// transações que já falharam recentemente, evitando que pendências antigas
// monopolizem o lote e causem starvation de pendências mais novas.
func (t *WagerTransaction) RecordReferenceRetryAttempt(nextRetryAt time.Time, now time.Time) error {
	if t.status != StatusPendingReference {
		return ErrInvalidTransition
	}
	t.referenceRetryAttempts++
	t.referenceNextRetryAt = &nextRetryAt
	t.updatedAt = now
	return nil
}
