package idp

import (
	"context"
	"net/http"
	"time"

	"go.uber.org/fx"

	"github.com/editosilva/wager-ledger/internal/config"
)

const jwksAutoRefreshInterval = 10 * time.Minute

func NewKeySetFromConfig(lc fx.Lifecycle, cfg config.Config) (*KeySet, error) {
	ks, err := NewKeySet(cfg.OIDCJWKSURL, &http.Client{Timeout: 10 * time.Second})
	if err != nil {
		return nil, err
	}

	var stop func()
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			stop = ks.StartAutoRefresh(jwksAutoRefreshInterval)
			return nil
		},
		OnStop: func(context.Context) error {
			if stop != nil {
				stop()
			}
			return nil
		},
	})
	return ks, nil
}

var Module = fx.Module("idp",
	fx.Provide(NewKeySetFromConfig),
)
