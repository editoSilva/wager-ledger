package httpserver

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/fx"

	"github.com/editosilva/wager-ledger/internal/application/ports"
	"github.com/editosilva/wager-ledger/internal/application/usecase"
	"github.com/editosilva/wager-ledger/internal/config"
	"github.com/editosilva/wager-ledger/internal/infra/idp"
	"github.com/editosilva/wager-ledger/internal/infra/postgres"
	sqsinfra "github.com/editosilva/wager-ledger/internal/infra/sqs"
	"github.com/editosilva/wager-ledger/internal/observability"
)

var Module = fx.Module("http",
	fx.Provide(
		NewRouter,
		NewServer,
	),
	fx.Invoke(
		func(mux *http.ServeMux, dbChecker *postgres.ReadinessChecker, sqsChecker *sqsinfra.Consumer) {
			RegisterHealthRoutes(mux, dbChecker, sqsChecker)
		},
		func(mux *http.ServeMux, metrics *observability.Metrics) {
			mux.Handle("GET /metrics", promhttp.HandlerFor(metrics.Registry(), promhttp.HandlerOpts{}))
		},
		func(
			mux *http.ServeMux,
			openWallet *usecase.OpenWallet,
			walletRepo ports.WalletRepository,
			ledgerRepo ports.LedgerRepository,
			reconcileWallet *usecase.ReconcileWallet,
			keySet *idp.KeySet,
			cfg config.Config,
		) {
			authenticate := idp.Authenticate(keySet, cfg.OIDCIssuerURL, cfg.OIDCAudience)
			requireInternal := idp.RequireRole("internal")
			RegisterWalletRoutes(mux, openWallet, walletRepo, ledgerRepo, reconcileWallet, authenticate, requireInternal)
		},
		func(
			mux *http.ServeMux,
			processWagerTx *usecase.ProcessWagerTransaction,
			txRepo ports.WagerTransactionRepository,
			keySet *idp.KeySet,
			cfg config.Config,
		) {
			authenticate := idp.Authenticate(keySet, cfg.OIDCIssuerURL, cfg.OIDCAudience)
			requireProvider := idp.RequireRole("provider")
			RegisterWageringRoutes(mux, processWagerTx, txRepo, authenticate, requireProvider)
		},
		func(*http.Server) {},
	),
)
