package idp

import (
	"net/http"
	"time"

	"go.uber.org/fx"

	"github.com/editosilva/wager-ledger/internal/config"
)

func NewKeySetFromConfig(cfg config.Config) (*KeySet, error) {
	return NewKeySet(cfg.OIDCJWKSURL, &http.Client{Timeout: 10 * time.Second})
}

// Module ainda NÃO está incluído em cmd/api/main.go — sua construção faz
// uma busca síncrona ao JWKS, então incluí-lo exigiria o Keycloak já de
// pé para a aplicação sequer iniciar. Entra na composição na Fase 9,
// junto com os primeiros endpoints protegidos.
var Module = fx.Module("idp",
	fx.Provide(NewKeySetFromConfig),
)
