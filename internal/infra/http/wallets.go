package httpserver

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

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
	ledgerRepo ports.LedgerRepository,
	reconcileWallet *usecase.ReconcileWallet,
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
		authenticate, requireInternal,
	))

	mux.Handle("GET /wallets/{id}/ledger", chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handleGetWalletLedger(w, r, ledgerRepo)
		}),
		authenticate, requireInternal,
	))

	mux.Handle("POST /wallets/{id}/reconciliation", chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handleReconcileWallet(w, r, reconcileWallet)
		}),
		authenticate, requireInternal,
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

	writeJSON(w, http.StatusCreated, walletResponse{
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

	writeJSON(w, http.StatusOK, walletResponse{
		ID:       string(found.ID()),
		PlayerID: string(found.PlayerID()),
		Balance:  found.Balance(),
		Version:  found.Version(),
	})
}

type ledgerEntryResponse struct {
	ID            string      `json:"id"`
	TransactionID string      `json:"transactionId"`
	Direction     string      `json:"direction"`
	Amount        money.Money `json:"amount"`
	BalanceBefore money.Money `json:"balanceBefore"`
	BalanceAfter  money.Money `json:"balanceAfter"`
	CreatedAt     string      `json:"createdAt"`
}

type ledgerPageResponse struct {
	Entries    []ledgerEntryResponse `json:"entries"`
	NextCursor string                `json:"nextCursor,omitempty"`
}

const maxLedgerPageSize = 200

func handleGetWalletLedger(w http.ResponseWriter, r *http.Request, ledgerRepo ports.LedgerRepository) {
	id := wallet.ID(r.PathValue("id"))
	cursor := r.URL.Query().Get("cursor")

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > maxLedgerPageSize {
			writeJSONError(w, http.StatusBadRequest, "invalid_request", "limit deve ser um inteiro positivo até 200")
			return
		}
		limit = parsed
	}

	entries, nextCursor, err := ledgerRepo.ListByWallet(r.Context(), id, cursor, limit)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_cursor", "cursor inválido: "+err.Error())
		return
	}

	out := make([]ledgerEntryResponse, 0, len(entries))
	for _, e := range entries {
		out = append(out, ledgerEntryResponse{
			ID:            string(e.ID()),
			TransactionID: string(e.TransactionID()),
			Direction:     string(e.Direction()),
			Amount:        e.Amount(),
			BalanceBefore: e.BalanceBefore(),
			BalanceAfter:  e.BalanceAfter(),
			CreatedAt:     e.CreatedAt().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		})
	}

	writeJSON(w, http.StatusOK, ledgerPageResponse{Entries: out, NextCursor: nextCursor})
}

type reconciliationResponse struct {
	WalletID          string      `json:"walletId"`
	StoredBalance     money.Money `json:"storedBalance"`
	CalculatedBalance money.Money `json:"calculatedBalance"`
	Difference        money.Money `json:"difference"`
	Consistent        bool        `json:"consistent"`
	CheckedEntries    int         `json:"checkedEntries"`
}

func handleReconcileWallet(w http.ResponseWriter, r *http.Request, uc *usecase.ReconcileWallet) {
	id := wallet.ID(r.PathValue("id"))

	out, err := uc.Execute(r.Context(), id)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "not_found", "carteira não encontrada")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "erro ao reconciliar carteira")
		return
	}

	writeJSON(w, http.StatusOK, reconciliationResponse{
		WalletID:          out.WalletID,
		StoredBalance:     out.StoredBalance,
		CalculatedBalance: out.CalculatedBalance,
		Difference:        out.Difference,
		Consistent:        out.Consistent,
		CheckedEntries:    out.CheckedEntries,
	})
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSONBody(w, map[string]string{"error": code, "message": message})
}

// writeJSON escreve o header/status e o corpo JSON de uma resposta de
// sucesso. writeJSONBody escreve só o corpo — usada quando o header/status
// já foram definidos separadamente (ex.: writeJSONError).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSONBody(w, v)
}

// writeJSONBody serializa v no corpo da resposta. Uma falha aqui só pode
// ocorrer depois que o header/status já foram escritos (não há como voltar
// a um erro HTTP nesse ponto), então é logada em vez de descartada — sem
// isso, o cliente recebe um corpo truncado/vazio sem nenhum registro do
// motivo.
func writeJSONBody(w http.ResponseWriter, v any) {
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Default().Error("falha ao serializar corpo da resposta HTTP", slog.Any("error", err))
	}
}

func chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
