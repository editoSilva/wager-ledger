package main

import (
	"time"

	"go.uber.org/fx"

	"github.com/editosilva/wager-ledger/internal/application/usecase"
	"github.com/editosilva/wager-ledger/internal/config"
	httpserver "github.com/editosilva/wager-ledger/internal/infra/http"
	"github.com/editosilva/wager-ledger/internal/infra/idgen"
	"github.com/editosilva/wager-ledger/internal/infra/idp"
	"github.com/editosilva/wager-ledger/internal/infra/postgres"
	"github.com/editosilva/wager-ledger/internal/observability"
)

func main() {
	fx.New(
		config.Module,
		observability.Module,
		postgres.Module,
		idp.Module,
		idgen.Module,
		usecase.Module,
		httpserver.Module,

		fx.StopTimeout(20*time.Second),
	).Run()
}
