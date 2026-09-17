package idp

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"
)

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwksResponse struct {
	Keys []jwk `json:"keys"`
}

// KeySet mantém em cache as chaves públicas do IdP, indexadas por "kid".
type KeySet struct {
	jwksURL    string
	httpClient *http.Client

	mu   sync.RWMutex
	keys map[string]*rsa.PublicKey
}

func NewKeySet(jwksURL string, httpClient *http.Client) (*KeySet, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	ks := &KeySet{jwksURL: jwksURL, httpClient: httpClient, keys: make(map[string]*rsa.PublicKey)}
	if err := ks.refresh(); err != nil {
		return nil, fmt.Errorf("idp: erro ao buscar JWKS inicial: %w", err)
	}
	return ks, nil
}

func (ks *KeySet) refresh() error {
	resp, err := ks.httpClient.Get(ks.jwksURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("idp: JWKS respondeu status %d", resp.StatusCode)
	}

	var parsed jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("idp: erro ao decodificar JWKS: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(parsed.Keys))
	for _, k := range parsed.Keys {
		if k.Kty != "RSA" {
			continue
		}
		pub, err := jwkToRSAPublicKey(k)
		if err != nil {
			return fmt.Errorf("idp: chave %q inválida: %w", k.Kid, err)
		}
		keys[k.Kid] = pub
	}

	ks.mu.Lock()
	ks.keys = keys
	ks.mu.Unlock()
	return nil
}

func (ks *KeySet) Key(kid string) (*rsa.PublicKey, error) {
	ks.mu.RLock()
	key, ok := ks.keys[kid]
	ks.mu.RUnlock()
	if ok {
		return key, nil
	}

	if err := ks.refresh(); err != nil {
		return nil, err
	}

	ks.mu.RLock()
	key, ok = ks.keys[kid]
	ks.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("idp: chave %q não encontrada no JWKS", kid)
	}
	return key, nil
}

func jwkToRSAPublicKey(k jwk) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("campo n inválido: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("campo e inválido: %w", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)

	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}
