package httpserver

import (
	"net/http"

	"go.uber.org/fx"

	"github.com/editosilva/wager-ledger/internal/application/ports"
	"github.com/editosilva/wager-ledger/internal/application/usecase"
	"github.com/editosilva/wager-ledger/internal/config"
	"github.com/editosilva/wager-ledger/internal/infra/idp"
)

var Module = fx.Module("http",
	fx.Provide(
		NewRouter,
		NewServer,
	),
	fx.Invoke(
		func(mux *http.ServeMux) {
			RegisterHealthRoutes(mux)
		},
		func(
			mux *http.ServeMux,
			openWallet *usecase.OpenWallet,
			walletRepo ports.WalletRepository,
			keySet *idp.KeySet,
			cfg config.Config,
		) {
			authenticate := idp.Authenticate(keySet, cfg.OIDCIssuerURL)
			requireInternal := idp.RequireRole("internal")
			RegisterWalletRoutes(mux, openWallet, walletRepo, authenticate, requireInternal)
		},
		func(
			mux *http.ServeMux,
			processWagerTx *usecase.ProcessWagerTransaction,
			txRepo ports.WagerTransactionRepository,
			keySet *idp.KeySet,
			cfg config.Config,
		) {
			authenticate := idp.Authenticate(keySet, cfg.OIDCIssuerURL)
			requireProvider := idp.RequireRole("provider")
			RegisterWageringRoutes(mux, processWagerTx, txRepo, authenticate, requireProvider)
		},
		func(*http.Server) {},
	),
)
