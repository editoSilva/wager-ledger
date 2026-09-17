package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/editosilva/wager-ledger/internal/application/ports"
	"github.com/editosilva/wager-ledger/internal/application/usecase"
	"github.com/editosilva/wager-ledger/internal/domain/money"
	"github.com/editosilva/wager-ledger/internal/domain/wallet"
)

type openWalletRequest struct {
	PlayerID       string      `json:"playerId"`
	InitialBalance money.Money `json:"initialBalance"`
}

type walletResponse struct {
	ID       string      `json:"id"`
	PlayerID string      `json:"playerId"`
	Balance  money.Money `json:"balance"`
	Version  int64       `json:"version"`
}

func RegisterWalletRoutes(
	mux *http.ServeMux,
	openWallet *usecase.OpenWallet,
	walletRepo ports.WalletRepository,
	authenticate func(http.Handler) http.Handler,
	requireInternal func(http.Handler) http.Handler,
) {
	mux.Handle("POST /wallets", chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handleOpenWallet(w, r, openWallet)
		}),
		authenticate, requireInternal,
	))

	mux.Handle("GET /wallets/{id}", chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handleGetWallet(w, r, walletRepo)
		}),
		authenticate,
	))
}

func handleOpenWallet(w http.ResponseWriter, r *http.Request, uc *usecase.OpenWallet) {
	var req openWalletRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_body", "corpo da requisição inválido: "+err.Error())
		return
	}
	if req.PlayerID == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_body", "playerId é obrigatório")
		return
	}

	out, err := uc.Execute(r.Context(), usecase.OpenWalletInput{
		PlayerID:       req.PlayerID,
		InitialBalance: req.InitialBalance,
	})
	if err != nil {
		if errors.Is(err, usecase.ErrWalletAlreadyExists) {
			writeJSONError(w, http.StatusConflict, "wallet_already_exists", "já existe uma carteira para este player e moeda")
			return
		}
		writeJSONError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(walletResponse{
		ID:       out.ID,
		PlayerID: out.PlayerID,
		Balance:  out.Balance,
		Version:  out.Version,
	})
}

func handleGetWallet(w http.ResponseWriter, r *http.Request, repo ports.WalletRepository) {
	id := r.PathValue("id")
	found, err := repo.FindByID(r.Context(), wallet.ID(id))
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "not_found", "carteira não encontrada")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "erro ao buscar carteira")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(walletResponse{
		ID:       string(found.ID()),
		PlayerID: string(found.PlayerID()),
		Balance:  found.Balance(),
		Version:  found.Version(),
	})
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": message})
}

func chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
