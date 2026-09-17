package httpserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/editosilva/wager-ledger/internal/application/usecase"
	"github.com/editosilva/wager-ledger/internal/infra/idgen"
	"github.com/editosilva/wager-ledger/internal/infra/idp"
	"github.com/editosilva/wager-ledger/internal/infra/postgres"
)

const testIssuer = "http://test-issuer/realms/wager-ledger"
const testKid = "http-test-key"

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

type testClaims struct {
	jwt.RegisteredClaims
	AuthorizedParty string      `json:"azp"`
	RealmAccess     realmAccess `json:"realm_access"`
}

type realmAccess struct {
	Roles []string `json:"roles"`
}

func signTestToken(t *testing.T, priv *rsa.PrivateKey, clientID string, roles []string) string {
	t.Helper()
	now := time.Now()
	c := testClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   "service-account-" + clientID,
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
		AuthorizedParty: clientID,
		RealmAccess:     realmAccess{Roles: roles},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
	tok.Header["kid"] = testKid
	signed, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("erro ao assinar token de teste: %v", err)
	}
	return signed
}

func testServer(t *testing.T) (baseURL string, priv *rsa.PrivateKey, pool *pgxpool.Pool) {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("erro ao gerar chave RSA de teste: %v", err)
	}

	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwksResponse{Keys: []jwk{{
			Kty: "RSA", Kid: testKid, Alg: "RS256",
			N: base64.RawURLEncoding.EncodeToString(priv.PublicKey.N.Bytes()),
			E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.PublicKey.E)).Bytes()),
		}}})
	}))
	t.Cleanup(jwksServer.Close)

	keySet, err := idp.NewKeySet(jwksServer.URL, jwksServer.Client())
	if err != nil {
		t.Fatalf("idp.NewKeySet erro inesperado: %v", err)
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://wager:wager@localhost:5432/wager_ledger?sslmode=disable"
	}
	pool, err = pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("erro ao conectar no Postgres de teste: %v", err)
	}
	t.Cleanup(pool.Close)

	walletRepo := postgres.NewWalletRepository(pool)
	txRepo := postgres.NewWagerTransactionRepository(pool)
	ledgerRepo := postgres.NewLedgerRepository(pool)
	outboxRepo := postgres.NewOutboxRepository(pool)
	uow := postgres.NewUnitOfWork(pool)
	idGen := idgen.NewUUIDGenerator()
	openWallet := usecase.NewOpenWallet(uow, walletRepo, txRepo, ledgerRepo, outboxRepo, idGen)
	processWagerTx := usecase.NewProcessWagerTransaction(uow, walletRepo, txRepo, ledgerRepo, outboxRepo, idGen)

	mux := NewRouter()
	authenticate := idp.Authenticate(keySet, testIssuer)
	requireInternal := idp.RequireRole("internal")
	requireProvider := idp.RequireRole("provider")
	RegisterWalletRoutes(mux, openWallet, walletRepo, authenticate, requireInternal)
	RegisterWageringRoutes(mux, processWagerTx, txRepo, authenticate, requireProvider)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv.URL, priv, pool
}

func TestOpenWallet_HTTP_EndToEnd_Success(t *testing.T) {
	baseURL, priv, pool := testServer(t)
	token := signTestToken(t, priv, "wager-internal", []string{"internal"})

	body := fmt.Sprintf(`{"playerId":"%s","initialBalance":{"amount":"1000.00","currency":"BRL"}}`, randomPlayerID())

	req, _ := http.NewRequest(http.MethodPost, baseURL+"/wallets", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("erro na requisição: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, esperado 201", resp.StatusCode)
	}

	var out walletResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("erro ao decodificar resposta: %v", err)
	}
	if out.Balance.DecimalString() != "1000.00" {
		t.Errorf("Balance = %s, esperado 1000.00", out.Balance.DecimalString())
	}
	if out.Version != 1 {
		t.Errorf("Version = %d, esperado 1", out.Version)
	}

	getReq, _ := http.NewRequest(http.MethodGet, baseURL+"/wallets/"+out.ID, nil)
	getReq.Header.Set("Authorization", "Bearer "+token)
	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("erro no GET: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, esperado 200", getResp.StatusCode)
	}

	var outboxCount int
	row := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = $1`, out.ID)
	if err := row.Scan(&outboxCount); err != nil {
		t.Fatalf("erro ao contar outbox_events: %v", err)
	}
	if outboxCount != 2 {
		t.Errorf("outbox_events para a carteira = %d, esperado 2 (WagerTransactionProcessed + WalletBalanceChanged)", outboxCount)
	}
}

func TestOpenWallet_HTTP_MissingAuth_Returns401(t *testing.T) {
	baseURL, _, _ := testServer(t)

	body := fmt.Sprintf(`{"playerId":"%s","initialBalance":{"amount":"100.00","currency":"BRL"}}`, randomPlayerID())
	resp, err := http.Post(baseURL+"/wallets", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("erro na requisição: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, esperado 401", resp.StatusCode)
	}
}

func TestOpenWallet_HTTP_ProviderRole_Returns403(t *testing.T) {
	baseURL, priv, _ := testServer(t)
	token := signTestToken(t, priv, "provider-a", []string{"provider"})

	body := fmt.Sprintf(`{"playerId":"%s","initialBalance":{"amount":"100.00","currency":"BRL"}}`, randomPlayerID())
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/wallets", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("erro na requisição: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, esperado 403", resp.StatusCode)
	}
}

func TestOpenWallet_HTTP_DuplicateWallet_Returns409(t *testing.T) {
	baseURL, priv, _ := testServer(t)
	token := signTestToken(t, priv, "wager-internal", []string{"internal"})
	playerID := randomPlayerID()
	body := fmt.Sprintf(`{"playerId":"%s","initialBalance":{"amount":"50.00","currency":"BRL"}}`, playerID)

	post := func() *http.Response {
		req, _ := http.NewRequest(http.MethodPost, baseURL+"/wallets", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("erro na requisição: %v", err)
		}
		return resp
	}

	first := post()
	defer first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("primeira requisição: status = %d, esperado 201", first.StatusCode)
	}

	second := post()
	defer second.Body.Close()
	if second.StatusCode != http.StatusConflict {
		t.Errorf("segunda requisição: status = %d, esperado 409", second.StatusCode)
	}
}

func TestGetWallet_HTTP_NotFound_Returns404(t *testing.T) {
	baseURL, priv, _ := testServer(t)
	token := signTestToken(t, priv, "wager-internal", []string{"internal"})

	req, _ := http.NewRequest(http.MethodGet, baseURL+"/wallets/00000000-0000-0000-0000-000000000000", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("erro na requisição: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, esperado 404", resp.StatusCode)
	}
}

func TestGetWallet_HTTP_ProviderRole_Returns403(t *testing.T) {
	baseURL, priv, _ := testServer(t)
	token := signTestToken(t, priv, "provider-a", []string{"provider"})

	req, _ := http.NewRequest(http.MethodGet, baseURL+"/wallets/00000000-0000-0000-0000-000000000000", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("erro na requisição: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, esperado 403", resp.StatusCode)
	}
}

func randomPlayerID() string {
	return uuid.NewString()
}
