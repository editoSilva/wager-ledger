package idgen

import "testing"

func TestUUIDGenerator_NewID_ReturnsDistinctValidUUIDs(t *testing.T) {
	g := NewUUIDGenerator()

	a := g.NewID()
	b := g.NewID()

	if a == "" || b == "" {
		t.Fatal("NewID() não deveria retornar string vazia")
	}
	if a == b {
		t.Error("duas chamadas a NewID() não deveriam produzir o mesmo valor")
	}
	if len(a) != 36 {
		t.Errorf("len(NewID()) = %d, esperado 36 (formato UUID)", len(a))
	}
}
