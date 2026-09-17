package idp

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testIssuer = "http://test-issuer/realms/wager-ledger"
const testKid = "test-key-1"
const testAudience = "wager-ledger-api"

func testEnv(t *testing.T) (*KeySet, *rsa.PrivateKey) {
	t.Helper()

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

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwksResponse{Keys: []jwk{jwkOut}})
	}))
	t.Cleanup(server.Close)

	keySet, err := NewKeySet(server.URL, server.Client())
	if err != nil {
		t.Fatalf("NewKeySet erro inesperado: %v", err)
	}

	return keySet, priv
}

func signToken(t *testing.T, priv *rsa.PrivateKey, c claims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
	tok.Header["kid"] = testKid
	signed, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("erro ao assinar token de teste: %v", err)
	}
	return signed
}

func validClaims(clientID string, roles []string, expiresIn time.Duration) claims {
	now := time.Now()
	return claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   "service-account-" + clientID,
			Audience:  jwt.ClaimStrings{testAudience},
			ExpiresAt: jwt.NewNumericDate(now.Add(expiresIn)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
		AuthorizedParty: clientID,
		RealmAccess:     realmAccess{Roles: roles},
	}
}

func handlerRecordingIdentity(t *testing.T) (http.Handler, *Identity) {
	captured := &Identity{}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFromContext(r.Context())
		if !ok {
			t.Error("identidade deveria estar no context dentro do handler")
		}
		*captured = id
		w.WriteHeader(http.StatusOK)
	})
	return h, captured
}

func TestAuthenticate_ValidToken(t *testing.T) {
	keySet, priv := testEnv(t)
	next, captured := handlerRecordingIdentity(t)
	mw := Authenticate(keySet, testIssuer, testAudience)(next)

	token := signToken(t, priv, validClaims("provider-a", []string{"provider"}, time.Hour))

	req := httptest.NewRequest(http.MethodGet, "/wallets/1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, esperado 200", rec.Code)
	}
	if captured.ClientID != "provider-a" {
		t.Errorf("ClientID = %q, esperado provider-a", captured.ClientID)
	}
	if !captured.HasRole("provider") {
		t.Error("identidade deveria ter a role provider")
	}
}

func TestAuthenticate_MissingHeader(t *testing.T) {
	keySet, _ := testEnv(t)
	next, _ := handlerRecordingIdentity(t)
	mw := Authenticate(keySet, testIssuer, testAudience)(next)

	req := httptest.NewRequest(http.MethodGet, "/wallets/1", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, esperado 401", rec.Code)
	}
}

func TestAuthenticate_MalformedHeader(t *testing.T) {
	keySet, _ := testEnv(t)
	next, _ := handlerRecordingIdentity(t)
	mw := Authenticate(keySet, testIssuer, testAudience)(next)

	req := httptest.NewRequest(http.MethodGet, "/wallets/1", nil)
	req.Header.Set("Authorization", "Token abc123")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, esperado 401", rec.Code)
	}
}

func TestAuthenticate_ExpiredToken(t *testing.T) {
	keySet, priv := testEnv(t)
	next, _ := handlerRecordingIdentity(t)
	mw := Authenticate(keySet, testIssuer, testAudience)(next)

	token := signToken(t, priv, validClaims("provider-a", []string{"provider"}, -time.Hour))

	req := httptest.NewRequest(http.MethodGet, "/wallets/1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, esperado 401", rec.Code)
	}
}

func TestAuthenticate_WrongSignature(t *testing.T) {
	keySet, _ := testEnv(t)
	next, _ := handlerRecordingIdentity(t)
	mw := Authenticate(keySet, testIssuer, testAudience)(next)

	otherPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("erro ao gerar chave: %v", err)
	}
	token := signToken(t, otherPriv, validClaims("provider-a", []string{"provider"}, time.Hour))

	req := httptest.NewRequest(http.MethodGet, "/wallets/1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, esperado 401 (assinatura não corresponde à chave do JWKS)", rec.Code)
	}
}

func TestAuthenticate_WrongIssuer(t *testing.T) {
	keySet, priv := testEnv(t)
	next, _ := handlerRecordingIdentity(t)
	mw := Authenticate(keySet, testIssuer, testAudience)(next)

	c := validClaims("provider-a", []string{"provider"}, time.Hour)
	c.Issuer = "http://outro-issuer/realms/outro"
	token := signToken(t, priv, c)

	req := httptest.NewRequest(http.MethodGet, "/wallets/1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, esperado 401 (issuer não confere)", rec.Code)
	}
}

func TestAuthenticate_WrongAudience(t *testing.T) {
	keySet, priv := testEnv(t)
	next, _ := handlerRecordingIdentity(t)
	mw := Authenticate(keySet, testIssuer, testAudience)(next)

	c := validClaims("provider-a", []string{"provider"}, time.Hour)
	c.Audience = jwt.ClaimStrings{"outro-serviço"}
	token := signToken(t, priv, c)

	req := httptest.NewRequest(http.MethodGet, "/wallets/1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, esperado 401 (audience não confere)", rec.Code)
	}
}

func TestAuthenticate_MissingExpiration(t *testing.T) {
	keySet, priv := testEnv(t)
	next, _ := handlerRecordingIdentity(t)
	mw := Authenticate(keySet, testIssuer, testAudience)(next)

	c := validClaims("provider-a", []string{"provider"}, time.Hour)
	c.ExpiresAt = nil
	token := signToken(t, priv, c)

	req := httptest.NewRequest(http.MethodGet, "/wallets/1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, esperado 401 (token sem exp)", rec.Code)
	}
}

func TestRequireRole_Allowed(t *testing.T) {
	keySet, priv := testEnv(t)
	inner, _ := handlerRecordingIdentity(t)
	protected := RequireRole("internal")(inner)
	mw := Authenticate(keySet, testIssuer, testAudience)(protected)

	token := signToken(t, priv, validClaims("wager-internal", []string{"internal"}, time.Hour))

	req := httptest.NewRequest(http.MethodPost, "/wallets", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, esperado 200", rec.Code)
	}
}

func TestRequireRole_Forbidden(t *testing.T) {
	keySet, priv := testEnv(t)
	inner, _ := handlerRecordingIdentity(t)
	protected := RequireRole("internal")(inner)
	mw := Authenticate(keySet, testIssuer, testAudience)(protected)

	token := signToken(t, priv, validClaims("provider-a", []string{"provider"}, time.Hour))

	req := httptest.NewRequest(http.MethodPost, "/wallets", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, esperado 403 (provider não deveria acessar rota interna)", rec.Code)
	}
}

func TestKeySet_UnknownKid_ReturnsError(t *testing.T) {
	keySet, _ := testEnv(t)
	_, err := keySet.Key("kid-que-nao-existe")
	if err == nil {
		t.Error("esperava erro para kid desconhecido mesmo após refresh")
	}
}
