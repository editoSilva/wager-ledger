package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/editosilva/wager-ledger/internal/application/ports"
	"github.com/editosilva/wager-ledger/internal/application/usecase"
	"github.com/editosilva/wager-ledger/internal/domain/money"
	"github.com/editosilva/wager-ledger/internal/domain/wagertx"
	"github.com/editosilva/wager-ledger/internal/infra/idp"
)

type wageringRequest struct {
	ProviderID                     string      `json:"providerId"`
	ExternalTransactionID          string      `json:"externalTransactionId"`
	PlayerID                       string      `json:"playerId"`
	WalletID                       string      `json:"walletId"`
	RoundID                        string      `json:"roundId"`
	GameID                         string      `json:"gameId"`
	Kind                           string      `json:"kind"`
	Money                          money.Money `json:"money"`
	ReferenceExternalTransactionID string      `json:"referenceExternalTransactionId,omitempty"`
}

type wageringResponse struct {
	TransactionID    string      `json:"transactionId"`
	Status           string      `json:"status"`
	Balance          money.Money `json:"balance"`
	IdempotentReplay bool        `json:"idempotentReplay"`
	FailureCode      string      `json:"failureCode,omitempty"`
}

type wagerTransactionResponse struct {
	TransactionID         string       `json:"transactionId"`
	ProviderID            string       `json:"providerId,omitempty"`
	ExternalTransactionID string       `json:"externalTransactionId,omitempty"`
	WalletID              string       `json:"walletId"`
	PlayerID              string       `json:"playerId"`
	Kind                  string       `json:"kind"`
	Status                string       `json:"status"`
	Money                 money.Money  `json:"money"`
	FailureCode           string       `json:"failureCode,omitempty"`
	FinancialResult       *money.Money `json:"financialResult,omitempty"`
}

func RegisterWageringRoutes(
	mux *http.ServeMux,
	processWagerTx *usecase.ProcessWagerTransaction,
	txRepo ports.WagerTransactionRepository,
	authenticate func(http.Handler) http.Handler,
	requireProvider func(http.Handler) http.Handler,
) {
	mux.Handle("POST /wagering/transactions", chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handleProcessWagerTransaction(w, r, processWagerTx)
		}),
		authenticate, requireProvider,
	))

	mux.Handle("GET /wagering/transactions/{id}", chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handleGetWagerTransaction(w, r, txRepo)
		}),
		authenticate,
	))

	mux.Handle("GET /providers/{providerId}/wagering/transactions/{externalId}", chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handleGetWagerTransactionByProviderAndExternalID(w, r, txRepo)
		}),
		authenticate,
	))
}

func handleProcessWagerTransaction(w http.ResponseWriter, r *http.Request, uc *usecase.ProcessWagerTransaction) {
	identity, ok := idp.IdentityFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized", "identidade não encontrada")
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "header Idempotency-Key é obrigatório")
		return
	}

	var req wageringRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_body", "corpo da requisição inválido: "+err.Error())
		return
	}

	if req.ProviderID == "" || req.ExternalTransactionID == "" || req.PlayerID == "" || req.WalletID == "" || req.RoundID == "" || req.GameID == "" || req.Kind == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_body", "campos obrigatórios ausentes")
		return
	}

	if req.ProviderID != identity.ClientID {
		writeJSONError(w, http.StatusForbidden, "forbidden", "providerId não corresponde à identidade autenticada")
		return
	}

	out, err := uc.Execute(r.Context(), usecase.ProcessWagerTransactionInput{
		ProviderID:                     req.ProviderID,
		ExternalTransactionID:          req.ExternalTransactionID,
		IdempotencyKey:                 idempotencyKey,
		PlayerID:                       req.PlayerID,
		WalletID:                       req.WalletID,
		RoundID:                        req.RoundID,
		GameID:                         req.GameID,
		Kind:                           req.Kind,
		Money:                          req.Money,
		ReferenceExternalTransactionID: req.ReferenceExternalTransactionID,
	})
	if err != nil {
		writeProcessWagerTransactionError(w, err)
		return
	}

	status := http.StatusCreated
	if out.IdempotentReplay {
		status = http.StatusOK
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(wageringResponse{
		TransactionID:    out.TransactionID,
		Status:           out.Status,
		Balance:          out.Balance,
		IdempotentReplay: out.IdempotentReplay,
		FailureCode:      out.FailureCode,
	})
}

func writeProcessWagerTransactionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, usecase.ErrIdempotencyConflict):
		writeJSONError(w, http.StatusConflict, "idempotency_conflict", err.Error())
	case errors.Is(err, usecase.ErrUnsupportedKind):
		writeJSONError(w, http.StatusNotImplemented, "unsupported_kind", err.Error())
	case errors.Is(err, usecase.ErrWalletPlayerMismatch):
		writeJSONError(w, http.StatusBadRequest, "wallet_player_mismatch", err.Error())
	case errors.Is(err, usecase.ErrTooManyRetries):
		writeJSONError(w, http.StatusServiceUnavailable, "too_many_retries", err.Error())
	case errors.Is(err, ports.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "not_found", "carteira não encontrada")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeJSONError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "serviço temporariamente indisponível")
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "erro interno ao processar transação")
	}
}

func handleGetWagerTransaction(w http.ResponseWriter, r *http.Request, repo ports.WagerTransactionRepository) {
	id := r.PathValue("id")
	tx, err := repo.FindByID(r.Context(), wagertx.ID(id))
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "not_found", "transação não encontrada")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "erro ao buscar transação")
		return
	}

	identity, ok := idp.IdentityFromContext(r.Context())
	if !ok || (!identity.HasRole("internal") && (!identity.HasRole("provider") || tx.ProviderID() == "" || tx.ProviderID() != identity.ClientID)) {
		writeJSONError(w, http.StatusForbidden, "forbidden", "acesso restrito às próprias transações do provedor")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(wagerTransactionResponse{
		TransactionID:         string(tx.ID()),
		ProviderID:            tx.ProviderID(),
		ExternalTransactionID: tx.ExternalID(),
		WalletID:              string(tx.WalletID()),
		PlayerID:              string(tx.PlayerID()),
		Kind:                  string(tx.Kind()),
		Status:                string(tx.Status()),
		Money:                 tx.Money(),
		FailureCode:           tx.FailureCode(),
		FinancialResult:       tx.FinancialResult(),
	})
}

func handleGetWagerTransactionByProviderAndExternalID(w http.ResponseWriter, r *http.Request, repo ports.WagerTransactionRepository) {
	providerID := r.PathValue("providerId")
	externalID := r.PathValue("externalId")

	identity, ok := idp.IdentityFromContext(r.Context())
	if !ok || (!identity.HasRole("internal") && (!identity.HasRole("provider") || identity.ClientID != providerID)) {
		writeJSONError(w, http.StatusForbidden, "forbidden", "acesso restrito às próprias transações do provedor")
		return
	}

	tx, err := repo.FindByProviderAndExternalID(r.Context(), providerID, externalID)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "not_found", "transação não encontrada")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "erro ao buscar transação")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(wagerTransactionResponse{
		TransactionID:         string(tx.ID()),
		ProviderID:            tx.ProviderID(),
		ExternalTransactionID: tx.ExternalID(),
		WalletID:              string(tx.WalletID()),
		PlayerID:              string(tx.PlayerID()),
		Kind:                  string(tx.Kind()),
		Status:                string(tx.Status()),
		Money:                 tx.Money(),
		FailureCode:           tx.FailureCode(),
		FinancialResult:       tx.FinancialResult(),
	})
}
