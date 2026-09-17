package httpserver

import (
	"encoding/json"
	"net/http"
	"testing"
)

func getJSON(t *testing.T, baseURL, path, token string, out any) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, baseURL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("erro na requisição GET %s: %v", path, err)
	}
	if out != nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("erro ao decodificar resposta de %s: %v", path, err)
		}
	}
	return resp
}

func TestGetWalletLedger_HTTP_ReturnsEntriesInStableOrder(t *testing.T) {
	baseURL, priv, _ := testServer(t)
	internalToken := signTestToken(t, priv, "wager-internal", []string{"internal"})
	providerToken := signTestToken(t, priv, "provider-a", []string{"provider"})

	walletID, playerID := openTestWallet(t, baseURL, internalToken, "100.00")

	for i := 0; i < 3; i++ {
		externalID := randomExternalID("ledger-bet")
		resp := postWagerTransaction(t, baseURL, providerToken, "provider-a:"+externalID,
			betBody("provider-a", externalID, playerID, walletID, "10.00"))
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("aposta de setup: status = %d, esperado 201", resp.StatusCode)
		}
	}

	var page ledgerPageResponse
	resp := getJSON(t, baseURL, "/wallets/"+walletID+"/ledger?limit=2", internalToken, &page)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado 200", resp.StatusCode)
	}
	if len(page.Entries) != 2 {
		t.Fatalf("Entries = %d, esperado 2 (limit aplicado)", len(page.Entries))
	}
	if page.NextCursor == "" {
		t.Fatal("NextCursor deveria estar presente quando há mais páginas")
	}

	seen := map[string]bool{page.Entries[0].ID: true, page.Entries[1].ID: true}

	var page2 ledgerPageResponse
	resp2 := getJSON(t, baseURL, "/wallets/"+walletID+"/ledger?limit=2&cursor="+page.NextCursor, internalToken, &page2)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado 200 na segunda página", resp2.StatusCode)
	}
	for _, e := range page2.Entries {
		if seen[e.ID] {
			t.Errorf("entrada %s repetida entre páginas", e.ID)
		}
	}
	if len(page2.Entries) == 0 {
		t.Fatal("segunda página deveria conter ao menos a entrada de OPENING")
	}
}

func TestGetWalletLedger_HTTP_InvalidCursor_Returns400(t *testing.T) {
	baseURL, priv, _ := testServer(t)
	internalToken := signTestToken(t, priv, "wager-internal", []string{"internal"})
	walletID, _ := openTestWallet(t, baseURL, internalToken, "10.00")

	resp := getJSON(t, baseURL, "/wallets/"+walletID+"/ledger?cursor=not-a-valid-cursor", internalToken, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, esperado 400 para cursor inválido", resp.StatusCode)
	}
}

func TestReconcileWallet_HTTP_ConsistentAfterBet_ReturnsConsistentTrue(t *testing.T) {
	baseURL, priv, _ := testServer(t)
	internalToken := signTestToken(t, priv, "wager-internal", []string{"internal"})
	providerToken := signTestToken(t, priv, "provider-a", []string{"provider"})

	walletID, playerID := openTestWallet(t, baseURL, internalToken, "100.00")
	externalID := randomExternalID("reconcile-bet")
	betResp := postWagerTransaction(t, baseURL, providerToken, "provider-a:"+externalID,
		betBody("provider-a", externalID, playerID, walletID, "25.00"))
	betResp.Body.Close()
	if betResp.StatusCode != http.StatusCreated {
		t.Fatalf("aposta de setup: status = %d, esperado 201", betResp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodPost, baseURL+"/wallets/"+walletID+"/reconciliation", nil)
	req.Header.Set("Authorization", "Bearer "+internalToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("erro na requisição: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado 200", resp.StatusCode)
	}

	var out reconciliationResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("erro ao decodificar resposta: %v", err)
	}
	if !out.Consistent {
		t.Errorf("Consistent = false, esperado true (stored=%s calculated=%s)", out.StoredBalance.DecimalString(), out.CalculatedBalance.DecimalString())
	}
	if out.Difference.DecimalString() != "0.00" {
		t.Errorf("Difference = %s, esperado 0.00", out.Difference.DecimalString())
	}
	if out.CheckedEntries != 2 {
		t.Errorf("CheckedEntries = %d, esperado 2 (OPENING + BET)", out.CheckedEntries)
	}
	if out.StoredBalance.DecimalString() != "75.00" {
		t.Errorf("StoredBalance = %s, esperado 75.00", out.StoredBalance.DecimalString())
	}
}

func TestReconcileWallet_HTTP_UnknownWallet_Returns404(t *testing.T) {
	baseURL, priv, _ := testServer(t)
	internalToken := signTestToken(t, priv, "wager-internal", []string{"internal"})

	req, _ := http.NewRequest(http.MethodPost, baseURL+"/wallets/"+randomPlayerID()+"/reconciliation", nil)
	req.Header.Set("Authorization", "Bearer "+internalToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("erro na requisição: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, esperado 404", resp.StatusCode)
	}
}

func TestGetWagerTransactionByProviderAndExternalID_HTTP_Owner_Returns200(t *testing.T) {
	baseURL, priv, _ := testServer(t)
	internalToken := signTestToken(t, priv, "wager-internal", []string{"internal"})
	providerToken := signTestToken(t, priv, "provider-a", []string{"provider"})

	walletID, playerID := openTestWallet(t, baseURL, internalToken, "100.00")
	externalID := randomExternalID("lookup-bet")
	betResp := postWagerTransaction(t, baseURL, providerToken, "provider-a:"+externalID,
		betBody("provider-a", externalID, playerID, walletID, "5.00"))
	betResp.Body.Close()
	if betResp.StatusCode != http.StatusCreated {
		t.Fatalf("aposta de setup: status = %d, esperado 201", betResp.StatusCode)
	}

	var out wagerTransactionResponse
	resp := getJSON(t, baseURL, "/providers/provider-a/wagering/transactions/"+externalID, providerToken, &out)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado 200", resp.StatusCode)
	}
	if out.ExternalTransactionID != externalID {
		t.Errorf("ExternalTransactionID = %s, esperado %s", out.ExternalTransactionID, externalID)
	}
}

func TestGetWagerTransactionByProviderAndExternalID_HTTP_OtherProvider_Returns403(t *testing.T) {
	baseURL, priv, _ := testServer(t)
	internalToken := signTestToken(t, priv, "wager-internal", []string{"internal"})
	providerToken := signTestToken(t, priv, "provider-a", []string{"provider"})
	otherProviderToken := signTestToken(t, priv, "provider-b", []string{"provider"})

	walletID, playerID := openTestWallet(t, baseURL, internalToken, "100.00")
	externalID := randomExternalID("lookup-cross")
	betResp := postWagerTransaction(t, baseURL, providerToken, "provider-a:"+externalID,
		betBody("provider-a", externalID, playerID, walletID, "5.00"))
	betResp.Body.Close()
	if betResp.StatusCode != http.StatusCreated {
		t.Fatalf("aposta de setup: status = %d, esperado 201", betResp.StatusCode)
	}

	resp := getJSON(t, baseURL, "/providers/provider-a/wagering/transactions/"+externalID, otherProviderToken, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, esperado 403", resp.StatusCode)
	}
}
