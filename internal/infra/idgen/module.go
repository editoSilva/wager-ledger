package idgen

import (
	"go.uber.org/fx"

	"github.com/editosilva/wager-ledger/internal/application/ports"
)

var Module = fx.Module("idgen",
	fx.Provide(fx.Annotate(NewUUIDGenerator, fx.As(new(ports.IDGenerator)))),
)
