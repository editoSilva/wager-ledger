package httpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func openTestWallet(t *testing.T, baseURL, internalToken, balance string) (walletID, playerID string) {
	t.Helper()
	playerID = randomPlayerID()
	body := fmt.Sprintf(`{"playerId":"%s","initialBalance":{"amount":"%s","currency":"BRL"}}`, playerID, balance)

	req, _ := http.NewRequest(http.MethodPost, baseURL+"/wallets", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+internalToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("erro ao abrir carteira de teste: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("abertura de carteira de teste: status = %d, esperado 201", resp.StatusCode)
	}

	var out walletResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("erro ao decodificar resposta de abertura de carteira: %v", err)
	}
	return out.ID, out.PlayerID
}

func postWagerTransaction(t *testing.T, baseURL, token, idempotencyKey, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/wagering/transactions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("erro na requisição: %v", err)
	}
	return resp
}

func betBody(providerID, externalID, playerID, walletID, amount string) string {
	return fmt.Sprintf(
		`{"providerId":"%s","externalTransactionId":"%s","playerId":"%s","walletId":"%s","roundId":"round-1","gameId":"game-1","kind":"BET","money":{"amount":"%s","currency":"BRL"}}`,
		providerID, externalID, playerID, walletID, amount,
	)
}

// randomExternalID gera um externalTransactionId único por execução do
// teste, evitando colisão com linhas já commitadas por execuções
// anteriores da suíte contra o mesmo Postgres persistente.
func randomExternalID(prefix string) string {
	return prefix + "-" + randomPlayerID()
}

func TestProcessWagerTransaction_HTTP_Bet_Success(t *testing.T) {
	baseURL, priv, _ := testServer(t)
	internalToken := signTestToken(t, priv, "wager-internal", []string{"internal"})
	providerToken := signTestToken(t, priv, "provider-a", []string{"provider"})

	walletID, playerID := openTestWallet(t, baseURL, internalToken, "100.00")
	externalID := randomExternalID("bet")

	resp := postWagerTransaction(t, baseURL, providerToken, "provider-a:"+externalID,
		betBody("provider-a", externalID, playerID, walletID, "30.00"))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, esperado 201", resp.StatusCode)
	}

	var out wageringResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("erro ao decodificar resposta: %v", err)
	}
	if out.Status != "PROCESSED" {
		t.Errorf("Status = %s, esperado PROCESSED", out.Status)
	}
	if out.Balance.DecimalString() != "70.00" {
		t.Errorf("Balance = %s, esperado 70.00", out.Balance.DecimalString())
	}
	if out.IdempotentReplay {
		t.Error("IdempotentReplay deveria ser false na primeira chamada")
	}
}

func TestProcessWagerTransaction_HTTP_IdempotentReplay_Returns200(t *testing.T) {
	baseURL, priv, _ := testServer(t)
	internalToken := signTestToken(t, priv, "wager-internal", []string{"internal"})
	providerToken := signTestToken(t, priv, "provider-a", []string{"provider"})

	walletID, playerID := openTestWallet(t, baseURL, internalToken, "100.00")
	externalID := randomExternalID("bet-replay")
	body := betBody("provider-a", externalID, playerID, walletID, "30.00")

	first := postWagerTransaction(t, baseURL, providerToken, "provider-a:"+externalID, body)
	first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("primeira chamada: status = %d, esperado 201", first.StatusCode)
	}

	second := postWagerTransaction(t, baseURL, providerToken, "provider-a:"+externalID, body)
	defer second.Body.Close()
	if second.StatusCode != http.StatusOK {
		t.Fatalf("replay: status = %d, esperado 200", second.StatusCode)
	}

	var out wageringResponse
	if err := json.NewDecoder(second.Body).Decode(&out); err != nil {
		t.Fatalf("erro ao decodificar resposta: %v", err)
	}
	if !out.IdempotentReplay {
		t.Error("IdempotentReplay deveria ser true no replay")
	}
}

func TestProcessWagerTransaction_HTTP_ConflictingIdempotencyKey_Returns409(t *testing.T) {
	baseURL, priv, _ := testServer(t)
	internalToken := signTestToken(t, priv, "wager-internal", []string{"internal"})
	providerToken := signTestToken(t, priv, "provider-a", []string{"provider"})

	walletID, playerID := openTestWallet(t, baseURL, internalToken, "100.00")
	externalID := randomExternalID("bet-conflict")

	first := postWagerTransaction(t, baseURL, providerToken, "provider-a:"+externalID,
		betBody("provider-a", externalID, playerID, walletID, "30.00"))
	first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("primeira chamada: status = %d, esperado 201", first.StatusCode)
	}

	second := postWagerTransaction(t, baseURL, providerToken, "provider-a:"+externalID,
		betBody("provider-a", externalID, playerID, walletID, "99.00"))
	defer second.Body.Close()
	if second.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, esperado 409", second.StatusCode)
	}
}

func TestProcessWagerTransaction_HTTP_MissingIdempotencyKey_Returns400(t *testing.T) {
	baseURL, priv, _ := testServer(t)
	internalToken := signTestToken(t, priv, "wager-internal", []string{"internal"})
	providerToken := signTestToken(t, priv, "provider-a", []string{"provider"})

	walletID, playerID := openTestWallet(t, baseURL, internalToken, "100.00")

	resp := postWagerTransaction(t, baseURL, providerToken, "",
		betBody("provider-a", randomExternalID("bet-no-key"), playerID, walletID, "30.00"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, esperado 400", resp.StatusCode)
	}
}

func TestProcessWagerTransaction_HTTP_MissingAuth_Returns401(t *testing.T) {
	baseURL, _, _ := testServer(t)
	externalID := randomExternalID("bet-noauth")

	resp := postWagerTransaction(t, baseURL, "", "provider-a:"+externalID,
		betBody("provider-a", externalID, randomPlayerID(), randomPlayerID(), "30.00"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, esperado 401", resp.StatusCode)
	}
}

func TestProcessWagerTransaction_HTTP_InternalRole_Returns403(t *testing.T) {
	baseURL, priv, _ := testServer(t)
	internalToken := signTestToken(t, priv, "wager-internal", []string{"internal"})
	externalID := randomExternalID("bet-wrongrole")

	resp := postWagerTransaction(t, baseURL, internalToken, "provider-a:"+externalID,
		betBody("provider-a", externalID, randomPlayerID(), randomPlayerID(), "30.00"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, esperado 403", resp.StatusCode)
	}
}

func TestProcessWagerTransaction_HTTP_ProviderIDMismatch_Returns403(t *testing.T) {
	baseURL, priv, _ := testServer(t)
	internalToken := signTestToken(t, priv, "wager-internal", []string{"internal"})
	providerAToken := signTestToken(t, priv, "provider-a", []string{"provider"})

	walletID, playerID := openTestWallet(t, baseURL, internalToken, "100.00")
	externalID := randomExternalID("bet-spoof")

	resp := postWagerTransaction(t, baseURL, providerAToken, "provider-a:"+externalID,
		betBody("provider-b", externalID, playerID, walletID, "30.00"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, esperado 403", resp.StatusCode)
	}
}

func TestProcessWagerTransaction_HTTP_InsufficientBalance_Returns201Rejected(t *testing.T) {
	baseURL, priv, _ := testServer(t)
	internalToken := signTestToken(t, priv, "wager-internal", []string{"internal"})
	providerToken := signTestToken(t, priv, "provider-a", []string{"provider"})

	walletID, playerID := openTestWallet(t, baseURL, internalToken, "10.00")
	externalID := randomExternalID("bet-insufficient")

	resp := postWagerTransaction(t, baseURL, providerToken, "provider-a:"+externalID,
		betBody("provider-a", externalID, playerID, walletID, "50.00"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, esperado 201 (rejeição de negócio não é erro HTTP)", resp.StatusCode)
	}

	var out wageringResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("erro ao decodificar resposta: %v", err)
	}
	if out.Status != "REJECTED" {
		t.Errorf("Status = %s, esperado REJECTED", out.Status)
	}
	if out.FailureCode != "INSUFFICIENT_BALANCE" {
		t.Errorf("FailureCode = %s, esperado INSUFFICIENT_BALANCE", out.FailureCode)
	}
}

func TestGetWagerTransaction_HTTP_Owner_Returns200(t *testing.T) {
	baseURL, priv, _ := testServer(t)
	internalToken := signTestToken(t, priv, "wager-internal", []string{"internal"})
	providerToken := signTestToken(t, priv, "provider-a", []string{"provider"})

	walletID, playerID := openTestWallet(t, baseURL, internalToken, "100.00")
	externalID := randomExternalID("bet-get")
	postResp := postWagerTransaction(t, baseURL, providerToken, "provider-a:"+externalID,
		betBody("provider-a", externalID, playerID, walletID, "10.00"))
	var created wageringResponse
	_ = json.NewDecoder(postResp.Body).Decode(&created)
	postResp.Body.Close()

	req, _ := http.NewRequest(http.MethodGet, baseURL+"/wagering/transactions/"+created.TransactionID, nil)
	req.Header.Set("Authorization", "Bearer "+providerToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("erro na requisição: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, esperado 200", resp.StatusCode)
	}
}

func TestGetWagerTransaction_HTTP_OtherProvider_Returns403(t *testing.T) {
	baseURL, priv, _ := testServer(t)
	internalToken := signTestToken(t, priv, "wager-internal", []string{"internal"})
	providerAToken := signTestToken(t, priv, "provider-a", []string{"provider"})
	providerBToken := signTestToken(t, priv, "provider-b", []string{"provider"})

	walletID, playerID := openTestWallet(t, baseURL, internalToken, "100.00")
	externalID := randomExternalID("bet-isolation")
	postResp := postWagerTransaction(t, baseURL, providerAToken, "provider-a:"+externalID,
		betBody("provider-a", externalID, playerID, walletID, "10.00"))
	var created wageringResponse
	_ = json.NewDecoder(postResp.Body).Decode(&created)
	postResp.Body.Close()

	req, _ := http.NewRequest(http.MethodGet, baseURL+"/wagering/transactions/"+created.TransactionID, nil)
	req.Header.Set("Authorization", "Bearer "+providerBToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("erro na requisição: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, esperado 403", resp.StatusCode)
	}
}

func TestGetWagerTransaction_HTTP_IdentityWithoutAuthorizedRole_Returns403(t *testing.T) {
	baseURL, priv, _ := testServer(t)
	internalToken := signTestToken(t, priv, "wager-internal", []string{"internal"})
	providerToken := signTestToken(t, priv, "provider-a", []string{"provider"})
	noRoleToken := signTestToken(t, priv, "auditor", nil)

	walletID, playerID := openTestWallet(t, baseURL, internalToken, "100.00")
	externalID := randomExternalID("bet-no-role")
	postResp := postWagerTransaction(t, baseURL, providerToken, "provider-a:"+externalID,
		betBody("provider-a", externalID, playerID, walletID, "10.00"))
	var created wageringResponse
	_ = json.NewDecoder(postResp.Body).Decode(&created)
	postResp.Body.Close()

	req, _ := http.NewRequest(http.MethodGet, baseURL+"/wagering/transactions/"+created.TransactionID, nil)
	req.Header.Set("Authorization", "Bearer "+noRoleToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("erro na requisição: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, esperado 403", resp.StatusCode)
	}
}

func TestGetWagerTransaction_HTTP_NotFound_Returns404(t *testing.T) {
	baseURL, priv, _ := testServer(t)
	providerToken := signTestToken(t, priv, "provider-a", []string{"provider"})

	req, _ := http.NewRequest(http.MethodGet, baseURL+"/wagering/transactions/"+randomPlayerID(), nil)
	req.Header.Set("Authorization", "Bearer "+providerToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("erro na requisição: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, esperado 404", resp.StatusCode)
	}
}
