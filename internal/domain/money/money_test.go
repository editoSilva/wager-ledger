package money

import (
	"errors"
	"math"
	"testing"
)

func TestFromDecimalString_Valid(t *testing.T) {
	cases := []struct {
		amount   string
		currency string
		want     string
	}{
		{"25.00", "brl", "25.00"},
		{"0.00", "BRL", "0.00"},
		{"1000.00", "BRL", "1000.00"},
		{"25", "BRL", "25.00"},
		{"25.5", "BRL", "25.50"},
	}
	for _, tc := range cases {
		m, err := FromDecimalString(tc.amount, tc.currency)
		if err != nil {
			t.Fatalf("FromDecimalString(%q, %q) erro inesperado: %v", tc.amount, tc.currency, err)
		}
		if got := m.DecimalString(); got != tc.want {
			t.Errorf("FromDecimalString(%q).DecimalString() = %q, esperado %q", tc.amount, got, tc.want)
		}
		if m.Currency() != "BRL" {
			t.Errorf("Currency() = %q, esperado BRL", m.Currency())
		}
	}
}

func TestFromDecimalString_Invalid(t *testing.T) {
	cases := []struct {
		name    string
		amount  string
		wantErr error
	}{
		{"vazio", "", ErrEmptyAmount},
		{"NaN", "NaN", ErrInvalidFormat},
		{"Infinity", "Infinity", ErrInvalidFormat},
		{"notação científica", "1e10", ErrInvalidFormat},
		{"escala excedente", "25.123", ErrExcessiveScale},
		{"negativo em entrada externa", "-25.00", ErrNegativeNotAllowed},
		{"letras", "abc", ErrInvalidFormat},
		{"só ponto", ".", ErrInvalidFormat},
		{"espaço", " 25.00", ErrInvalidFormat},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := FromDecimalString(tc.amount, "BRL")
			if err == nil {
				t.Fatalf("esperava erro para %q, não houve", tc.amount)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("erro = %v, esperado %v", err, tc.wantErr)
			}
		})
	}
}

func TestFromDecimalString_InvalidCurrency(t *testing.T) {
	cases := []string{"", "R", "BRLL", "12A"}
	for _, cur := range cases {
		if _, err := FromDecimalString("10.00", cur); err == nil {
			t.Errorf("moeda %q deveria ser rejeitada", cur)
		}
	}
}

func TestAdd_Sub_SameCurrency(t *testing.T) {
	a, _ := FromDecimalString("100.00", "BRL")
	b, _ := FromDecimalString("30.00", "BRL")

	sum, err := a.Add(b)
	if err != nil {
		t.Fatalf("Add erro inesperado: %v", err)
	}
	if sum.DecimalString() != "130.00" {
		t.Errorf("Add = %s, esperado 130.00", sum.DecimalString())
	}

	diff, err := a.Sub(b)
	if err != nil {
		t.Fatalf("Sub erro inesperado: %v", err)
	}
	if diff.DecimalString() != "70.00" {
		t.Errorf("Sub = %s, esperado 70.00", diff.DecimalString())
	}
}

func TestSub_CanGoNegative_ForInternalUse(t *testing.T) {
	a, _ := FromDecimalString("30.00", "BRL")
	b, _ := FromDecimalString("100.00", "BRL")

	diff, err := a.Sub(b)
	if err != nil {
		t.Fatalf("Sub erro inesperado: %v", err)
	}
	if !diff.IsNegative() {
		t.Error("resultado deveria ser negativo (uso interno permite)")
	}
	if diff.DecimalString() != "-70.00" {
		t.Errorf("Sub = %s, esperado -70.00", diff.DecimalString())
	}
}

func TestCurrencyMismatch(t *testing.T) {
	brl, _ := FromDecimalString("10.00", "BRL")
	usd, _ := FromDecimalString("10.00", "USD")

	if _, err := brl.Add(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Errorf("Add entre moedas diferentes deveria falhar com ErrCurrencyMismatch, got %v", err)
	}
	if _, err := brl.Sub(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Errorf("Sub entre moedas diferentes deveria falhar com ErrCurrencyMismatch, got %v", err)
	}
	if _, err := brl.Compare(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Errorf("Compare entre moedas diferentes deveria falhar com ErrCurrencyMismatch, got %v", err)
	}
}

func TestNegate(t *testing.T) {
	a, _ := FromDecimalString("25.00", "BRL")
	neg, err := a.Negate()
	if err != nil {
		t.Fatalf("Negate erro inesperado: %v", err)
	}
	if neg.DecimalString() != "-25.00" {
		t.Errorf("Negate = %s, esperado -25.00", neg.DecimalString())
	}
	backToPositive, err := neg.Negate()
	if err != nil {
		t.Fatalf("Negate erro inesperado: %v", err)
	}
	if !backToPositive.Equals(a) {
		t.Error("Negate(Negate(x)) deveria ser igual a x")
	}
}

func TestCompareAndEquals(t *testing.T) {
	a, _ := FromDecimalString("10.00", "BRL")
	b, _ := FromDecimalString("20.00", "BRL")
	aAgain, _ := FromDecimalString("10.00", "BRL")

	if cmp, _ := a.Compare(b); cmp != -1 {
		t.Errorf("Compare(10, 20) = %d, esperado -1", cmp)
	}
	if cmp, _ := b.Compare(a); cmp != 1 {
		t.Errorf("Compare(20, 10) = %d, esperado 1", cmp)
	}
	if cmp, _ := a.Compare(aAgain); cmp != 0 {
		t.Errorf("Compare(10, 10) = %d, esperado 0", cmp)
	}
	if !a.Equals(aAgain) {
		t.Error("Equals deveria ser true para valores iguais")
	}
	if a.Equals(b) {
		t.Error("Equals deveria ser false para valores diferentes")
	}
}

func TestZero(t *testing.T) {
	z, err := Zero("BRL")
	if err != nil {
		t.Fatalf("Zero erro inesperado: %v", err)
	}
	if !z.IsZero() {
		t.Error("Zero() deveria ser IsZero()")
	}
	if z.DecimalString() != "0.00" {
		t.Errorf("Zero().DecimalString() = %s, esperado 0.00", z.DecimalString())
	}
}

func TestJSON_RoundTrip(t *testing.T) {
	original, _ := FromDecimalString("25.00", "BRL")

	data, err := original.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON erro inesperado: %v", err)
	}
	want := `{"amount":"25.00","currency":"BRL"}`
	if string(data) != want {
		t.Errorf("MarshalJSON = %s, esperado %s", data, want)
	}

	var decoded Money
	if err := decoded.UnmarshalJSON(data); err != nil {
		t.Fatalf("UnmarshalJSON erro inesperado: %v", err)
	}
	if !decoded.Equals(original) {
		t.Error("round-trip JSON deveria preservar o valor")
	}
}

func TestJSON_UnmarshalRejectsInvalid(t *testing.T) {
	var m Money
	err := m.UnmarshalJSON([]byte(`{"amount":"-5.00","currency":"BRL"}`))
	if !errors.Is(err, ErrNegativeNotAllowed) {
		t.Errorf("esperava ErrNegativeNotAllowed, got %v", err)
	}
}

func TestOverflow_Add(t *testing.T) {
	huge := Money{minorUnits: 9223372036854775807, currency: "BRL"}
	one, _ := FromDecimalString("0.01", "BRL")

	if _, err := huge.Add(one); !errors.Is(err, ErrOverflow) {
		t.Errorf("esperava ErrOverflow, got %v", err)
	}
}

func TestZeroValue_RejectsOperationsWithEmptyCurrency(t *testing.T) {
	var zero Money
	other, _ := FromDecimalString("10.00", "BRL")

	if _, err := zero.Add(other); !errors.Is(err, ErrEmptyCurrency) {
		t.Errorf("Add com Money zero-value deveria falhar com ErrEmptyCurrency, got %v", err)
	}
	if _, err := zero.Add(zero); !errors.Is(err, ErrEmptyCurrency) {
		t.Errorf("Add entre dois Money zero-value deveria falhar com ErrEmptyCurrency, got %v", err)
	}
	if _, err := zero.Sub(other); !errors.Is(err, ErrEmptyCurrency) {
		t.Errorf("Sub com Money zero-value deveria falhar com ErrEmptyCurrency, got %v", err)
	}
	if _, err := zero.Compare(other); !errors.Is(err, ErrEmptyCurrency) {
		t.Errorf("Compare com Money zero-value deveria falhar com ErrEmptyCurrency, got %v", err)
	}
	if _, err := zero.Negate(); !errors.Is(err, ErrEmptyCurrency) {
		t.Errorf("Negate com Money zero-value deveria falhar com ErrEmptyCurrency, got %v", err)
	}
	if _, err := zero.MarshalJSON(); !errors.Is(err, ErrEmptyCurrency) {
		t.Errorf("MarshalJSON com Money zero-value deveria falhar com ErrEmptyCurrency, got %v", err)
	}
}

func TestNormalizeCurrency_RejectsNonISOCode(t *testing.T) {
	if _, err := FromDecimalString("10.00", "ZZZ"); !errors.Is(err, ErrInvalidCurrency) {
		t.Errorf("código de três letras fora do ISO 4217 deveria ser rejeitado, got %v", err)
	}
}

func TestDecimalString_MinInt64_DoesNotOverflow(t *testing.T) {
	m, err := FromMinorUnits(math.MinInt64, "BRL")
	if err != nil {
		t.Fatalf("FromMinorUnits erro inesperado: %v", err)
	}
	if got := m.DecimalString(); got != "-92233720368547758.08" {
		t.Errorf("DecimalString(MinInt64) = %s, esperado -92233720368547758.08", got)
	}
}
