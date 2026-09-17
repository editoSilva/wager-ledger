package money

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

var (
	ErrEmptyAmount        = errors.New("money: valor vazio")
	ErrInvalidFormat      = errors.New("money: formato de valor inválido")
	ErrExcessiveScale     = errors.New("money: valor com mais de duas casas decimais")
	ErrNegativeNotAllowed = errors.New("money: valor negativo não permitido nesta entrada")
	ErrEmptyCurrency      = errors.New("money: moeda vazia")
	ErrInvalidCurrency    = errors.New("money: código de moeda inválido (esperado ISO 4217, 3 letras)")
	ErrCurrencyMismatch   = errors.New("money: operação entre moedas incompatíveis")
	ErrOverflow           = errors.New("money: overflow aritmético")
)

type Money struct {
	minorUnits int64
	currency   string
}

func Zero(currency string) (Money, error) {
	cur, err := normalizeCurrency(currency)
	if err != nil {
		return Money{}, err
	}
	return Money{minorUnits: 0, currency: cur}, nil
}

func FromDecimalString(amount, currency string) (Money, error) {
	cur, err := normalizeCurrency(currency)
	if err != nil {
		return Money{}, err
	}

	minorUnits, err := parseDecimalToMinorUnits(amount)
	if err != nil {
		return Money{}, err
	}

	if minorUnits < 0 {
		return Money{}, ErrNegativeNotAllowed
	}

	return Money{minorUnits: minorUnits, currency: cur}, nil
}

func parseDecimalToMinorUnits(amount string) (int64, error) {
	if amount == "" {
		return 0, ErrEmptyAmount
	}

	lower := strings.ToLower(strings.TrimSpace(amount))
	if lower != amount {
		return 0, fmt.Errorf("%w: espaços em branco não são permitidos", ErrInvalidFormat)
	}
	if strings.ContainsAny(lower, "einf") {
		return 0, fmt.Errorf("%w: notação científica/NaN/Infinity não são permitidos", ErrInvalidFormat)
	}

	negative := false
	rest := amount
	if strings.HasPrefix(rest, "-") {
		negative = true
		rest = rest[1:]
	} else if strings.HasPrefix(rest, "+") {
		rest = rest[1:]
	}

	if rest == "" {
		return 0, ErrInvalidFormat
	}

	parts := strings.SplitN(rest, ".", 2)
	intPart := parts[0]
	fracPart := ""
	if len(parts) == 2 {
		fracPart = parts[1]
	}

	if intPart == "" {
		return 0, ErrInvalidFormat
	}
	if !isAllDigits(intPart) {
		return 0, ErrInvalidFormat
	}
	if fracPart != "" && !isAllDigits(fracPart) {
		return 0, ErrInvalidFormat
	}
	if len(fracPart) > 2 {
		return 0, ErrExcessiveScale
	}

	for len(fracPart) < 2 {
		fracPart += "0"
	}

	intValue, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrOverflow, err)
	}
	fracValue, err := strconv.ParseInt(fracPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidFormat, err)
	}

	if intValue > (math.MaxInt64-fracValue)/100 {
		return 0, ErrOverflow
	}

	total := intValue*100 + fracValue
	if negative {
		total = -total
	}
	return total, nil
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func normalizeCurrency(currency string) (string, error) {
	if currency == "" {
		return "", ErrEmptyCurrency
	}
	cur := strings.ToUpper(currency)
	if len(cur) != 3 || !isAllLetters(cur) {
		return "", ErrInvalidCurrency
	}
	return cur, nil
}

func isAllLetters(s string) bool {
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// Currency devolve o código ISO 4217 da moeda deste valor.
func (m Money) Currency() string { return m.currency }

// IsNegative indica se o valor é negativo.
func (m Money) IsNegative() bool { return m.minorUnits < 0 }

// IsZero indica se o valor é exatamente zero.
func (m Money) IsZero() bool { return m.minorUnits == 0 }

// Add soma dois valores monetários. Exige moedas compatíveis e trata
// overflow.
func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, ErrCurrencyMismatch
	}
	sum, ok := addInt64(m.minorUnits, other.minorUnits)
	if !ok {
		return Money{}, ErrOverflow
	}
	return Money{minorUnits: sum, currency: m.currency}, nil
}

// Sub subtrai other de m. O resultado pode ser negativo (uso interno) —
// quem aplicar o resultado a um saldo de carteira rejeita negativo
// naquele contexto específico.
func (m Money) Sub(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, ErrCurrencyMismatch
	}
	diff, ok := subInt64(m.minorUnits, other.minorUnits)
	if !ok {
		return Money{}, ErrOverflow
	}
	return Money{minorUnits: diff, currency: m.currency}, nil
}

// Negate devolve o valor com o sinal invertido. Usado por ROLLBACK.
func (m Money) Negate() (Money, error) {
	if m.minorUnits == math.MinInt64 {
		return Money{}, ErrOverflow
	}
	return Money{minorUnits: -m.minorUnits, currency: m.currency}, nil
}

// Compare retorna -1, 0 ou 1. Exige moedas compatíveis.
func (m Money) Compare(other Money) (int, error) {
	if m.currency != other.currency {
		return 0, ErrCurrencyMismatch
	}
	switch {
	case m.minorUnits < other.minorUnits:
		return -1, nil
	case m.minorUnits > other.minorUnits:
		return 1, nil
	default:
		return 0, nil
	}
}

// Equals compara valor e moeda por igualdade exata.
func (m Money) Equals(other Money) bool {
	return m.minorUnits == other.minorUnits && m.currency == other.currency
}

// DecimalString devolve a representação decimal com exatamente duas
// casas (ex.: "25.00", "-5.30").
func (m Money) DecimalString() string {
	negative := m.minorUnits < 0
	abs := m.minorUnits
	if negative {
		abs = -abs
	}
	intPart := abs / 100
	fracPart := abs % 100
	sign := ""
	if negative {
		sign = "-"
	}
	return fmt.Sprintf("%s%d.%02d", sign, intPart, fracPart)
}

type moneyJSON struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

// MarshalJSON implementa json.Marshaler.
func (m Money) MarshalJSON() ([]byte, error) {
	return json.Marshal(moneyJSON{Amount: m.DecimalString(), Currency: m.currency})
}

// UnmarshalJSON implementa json.Unmarshaler, reaplicando as mesmas
// validações de FromDecimalString.
func (m *Money) UnmarshalJSON(data []byte) error {
	var raw moneyJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidFormat, err)
	}
	parsed, err := FromDecimalString(raw.Amount, raw.Currency)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

func addInt64(a, b int64) (int64, bool) {
	sum := a + b
	if (b > 0 && sum < a) || (b < 0 && sum > a) {
		return 0, false
	}
	return sum, true
}

func subInt64(a, b int64) (int64, bool) {
	diff := a - b
	if (b < 0 && diff < a) || (b > 0 && diff > a) {
		return 0, false
	}
	return diff, true
}

func (m Money) MinorUnits() int64 { return m.minorUnits }

func FromMinorUnits(minorUnits int64, currency string) (Money, error) {
	cur, err := normalizeCurrency(currency)
	if err != nil {
		return Money{}, err
	}
	return Money{minorUnits: minorUnits, currency: cur}, nil
}
