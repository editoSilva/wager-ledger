package idp

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestKeySet_StartAutoRefresh_PeriodicallyRefetchesJWKS(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("erro ao gerar chave RSA de teste: %v", err)
	}
	jwkOut := jwk{
		Kty: "RSA",
		Kid: testKid,
		Alg: "RS256",
		N:   base64.RawURLEncoding.EncodeToString(priv.PublicKey.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.PublicKey.E)).Bytes()),
	}

	var requestCount int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwksResponse{Keys: []jwk{jwkOut}})
	}))
	defer server.Close()

	keySet, err := NewKeySet(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewKeySet erro inesperado: %v", err)
	}

	stop := keySet.StartAutoRefresh(10 * time.Millisecond)
	defer stop()

	deadline := time.Now().Add(500 * time.Millisecond)
	for atomic.LoadInt64(&requestCount) < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if got := atomic.LoadInt64(&requestCount); got < 3 {
		t.Fatalf("requestCount = %d, esperava ao menos 3 chamadas periódicas de refresh", got)
	}
}
