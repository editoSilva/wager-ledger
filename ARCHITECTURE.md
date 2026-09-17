# Arquitetura

Este documento descreve o estado atual da implementação do desafio
"Processamento Distribuído de Apostas em Go" e as decisões técnicas por
trás dele. Ele é atualizado conforme o trabalho avança — a seção
[Status por área](#status-por-área) reflete o que está pronto, parcial
ou não iniciado no momento.

## 1. Visão geral

O serviço segue arquitetura hexagonal (ports & adapters):

- **`internal/domain`** — entidades e value objects (`money`, `wallet`,
  `wagertx`, `ledger`). Não depende de Fx, HTTP, SQL ou qualquer
  biblioteca de infraestrutura.
- **`internal/application`** — casos de uso (`usecase`) orquestrando o
  domínio através de interfaces (`ports`) implementadas pela infra.
- **`internal/infra`** — adapters concretos: `postgres` (repositórios e
  unit of work), `http` (servidor e handlers), `idp` (validação
  OIDC/JWT), `idgen` (geração de identificadores).
- **`internal/config`** e **`internal/observability`** — composição
  transversal (variáveis de ambiente, logger).
- **`cmd/api/main.go`** — monta tudo via `fx.New`, combinando os
  módulos acima.

## 2. Dinheiro (`Money`)

- Representação: `int64` em unidades mínimas (centavos) + código de
  moeda ISO 4217, sem `float32`/`float64` em nenhum ponto do parsing,
  aritmética ou persistência (`internal/domain/money/money.go`).
- `FromDecimalString` rejeita: valor vazio, espaços, notação
  científica/NaN/Infinity, mais de duas casas decimais, e valores
  negativos (a validação de "sem negativo" é da entrada externa —
  internamente `Sub`/`Negate` podem produzir negativo, usado por quem
  aplica a operação, ex. `Wallet.Debit` rejeita saldo negativo
  explicitamente).
- Persistência: `BIGINT` em unidades mínimas (`*_minor_units`) +
  `CHAR(3)` para moeda, preservando exatamente valor e moeda.
- Aritmética (`Add`, `Sub`, `Negate`) verifica overflow de `int64` e
  moedas compatíveis (`ErrCurrencyMismatch`).
- Contrato HTTP: `{"amount":"25.00","currency":"BRL"}` via
  `MarshalJSON`/`UnmarshalJSON`, reaplicando as mesmas validações no
  parse.

## 3. Agregado Wallet

- Identidade: par `(playerId, currency)` é único —
  `wallets_player_currency_unique` (migration `0001`).
- `Wallet` (`internal/domain/wallet/wallet.go`) encapsula saldo,
  versão e timestamps; construtores separados `NewWallet` (versão
  inicial `1`) e `Rehydrate` (não reaplica movimentações).
- `Debit`/`Credit` são os únicos pontos de mutação de saldo. Rejeitam
  valor não positivo, moeda incompatível, e — em `Debit` — saldo
  resultante negativo. Em qualquer erro, o estado do agregado
  permanece inalterado (nenhuma mutação parcial).
- Versão incrementa apenas quando há mudança de saldo, nunca em
  qualquer outra operação — conforme exigido na seção 6.2 do desafio.

### Controle de concorrência

**Decisão: controle otimista via coluna `version`.**

- `WalletRepository.Save(ctx, w, previousVersion)` executa
  `UPDATE wallets SET ... WHERE id = $1 AND version = $2`
  (`internal/infra/postgres/wallet_repository.go`). Se nenhuma linha
  for afetada, devolve `ports.ErrOptimisticLock` — o chamador decide
  se tenta novamente.
- Coordenação ocorre por carteira (nenhum lock global): carteiras
  diferentes avançam em paralelo sem qualquer contenção compartilhada.
- Invariantes de saldo (não negatividade) e de versão (`>= 1`) também
  são impostas por `CHECK` constraints no schema
  (`wallets_balance_non_negative`, `wallets_version_positive`), como
  defesa em profundidade além da validação em memória.
- **Ainda não implementado**: a camada de aplicação que decide
  *quando* reler e reaplicar após um `ErrOptimisticLock` (retry com
  limite). Hoje isso só é demonstrado no teste de integração
  `TestWalletRepository_Save_OptimisticLock`, que verifica que a
  segunda escrita falha; nenhum caso de uso de processamento de
  aposta usa esse retry ainda porque esse caso de uso não existe.
- O teste obrigatório da seção 8 (100.00 BRL, duas apostas de 80.00
  simultâneas) depende do caso de uso `ProcessWagerTransaction`, que
  ainda não foi escrito — ver [Status por área](#status-por-área).

## 4. WagerTransaction

- Tipos: `BET`, `WIN`, `LOSS`, `REFUND`, `ROLLBACK` (externos) e
  `OPENING` (interno, rejeitado explicitamente se vier por HTTP/SQS —
  `ErrOpeningNotExternal`).
- Máquina de estados (`internal/domain/wagertx/wagertx.go`):
  `PENDING → {PENDING_REFERENCE, PROCESSED, REJECTED, FAILED}`, e
  `PENDING_REFERENCE → {PROCESSED, REJECTED, FAILED}`. Estados
  terminais (`PROCESSED`, `REJECTED`, `FAILED`) não aceitam novas
  transições (`ErrInvalidTransition`). `MarkRejected`/`MarkFailed`
  exigem `failureCode` não vazio.
- Regras de valor por tipo, validadas na construção
  (`NewExternalTransaction`): `LOSS` exige `amount == 0.00`;
  `BET`/`WIN`/`REFUND`/`ROLLBACK` exigem valor `> 0`.
  `REFUND`/`ROLLBACK` exigem `referenceExternalTransactionId`; os
  demais tipos rejeitam esse campo se presente.
- `NewOpeningTransaction` constrói já em `PROCESSED`, sem os metadados
  externos (provider, chave de idempotência, hash, rodada, jogo,
  referência), que fazem sentido apenas para operações vindas de
  provedor.
- Distinção `FAILED` (falha permanente de infraestrutura, para
  auditoria) vs `REJECTED` (regra de negócio) está modelada como
  estados terminais distintos com `failureCode` próprio em cada um,
  mas o código que decide *quando* usar um ou outro (lógica do caso de
  uso) ainda não existe.
- Constução vs. reidratação: `NewExternalTransaction`/
  `NewOpeningTransaction` validam e geram estado inicial;
  `wagertx.Rehydrate` (chamado pelo repositório Postgres) apenas
  remonta o objeto a partir de colunas, sem revalidar regras de
  negócio nem reemitir eventos.

### Schema (migration `0002`)

- `wager_tx_kind_valid`, `wager_tx_status_valid`: enumerações via
  `CHECK`.
- `wager_tx_amount_by_kind`: replica a regra de `LOSS == 0` vs.
  `outros > 0` no banco.
- `wager_tx_kind_fields`: garante que `OPENING` não tenha nenhum
  metadado externo, e que tipos externos tenham todos os metadados
  obrigatórios preenchidos (exceto `reference_external_transaction_id`,
  tratado à parte).
- `wager_tx_reference_by_kind`: `REFUND`/`ROLLBACK` exigem referência;
  os demais a proíbem.
- `wager_tx_provider_external_unique UNIQUE (provider_id,
  external_transaction_id)`: garante que uma operação financeira
  identificada por `(providerId, externalTransactionId)` não seja
  duplicada — a mesma invariante que a seção 9 do desafio exige para
  impedir reaplicação sob outra chave de idempotência.
- `wager_tx_one_opening_per_wallet` (índice único parcial): no máximo
  um `OPENING` por carteira.

**Limitação conhecida**: a coluna `idempotency_key` **não** tem índice
único isolado hoje — apenas `(provider_id, external_transaction_id)` é
único. O ADR-002 registra a intenção original de indexar
`idempotency_key`; na prática, a unicidade da operação financeira é
garantida por `(providerId, externalTransactionId)`, e a
correspondência entre chave de idempotência e conteúdo (mesma chave →
mesmo hash; chave reaproveitada com conteúdo diferente → conflito)
ainda precisa ser implementada no caso de uso de processamento — hoje
não há nenhum código que grave ou valide isso.

## 5. WalletLedgerEntry

- Imutável por construção (`internal/domain/ledger/ledger.go`):
  `NewEntry` valida `balanceAfter = balanceBefore ± amount` conforme a
  direção, rejeita valor não positivo e moeda inconsistente entre
  `amount`/`balanceBefore`/`balanceAfter`.
- Imutabilidade também é imposta no banco: `wallet_ledger_entries` tem
  um `TRIGGER BEFORE UPDATE OR DELETE` que levanta exceção
  (`reject_ledger_mutation`, migration `0003`) — correções financeiras
  só podem ocorrer via novos lançamentos, nunca editando um existente.
- `ledger_balance_math` (CHECK) replica no banco a mesma validação de
  aritmética que o domínio já faz em memória — defesa em profundidade.
- `ledger_wallet_transaction_unique UNIQUE (wallet_id,
  transaction_id)` impede dois lançamentos para a mesma transação na
  mesma carteira.
- `LOSS` e operações rejeitadas não produzem lançamento — isso é uma
  decisão do caso de uso que grava o ledger (nenhum código hoje
  invoca `ledger.NewEntry` para esses casos), não uma regra imposta
  pelo tipo `Entry` em si.

## 6. Inbox e outbox

- Schema criado (migration `0004`): `inbox_messages` com unicidade
  `(consumer_name, message_id)`, e `outbox_events` com colunas para
  retry com backoff (`attempts`, `next_attempt_at`), disputa entre
  publishers (`locked_by`, `locked_at`) e `published_at` para marcar
  sucesso.
- **Nenhum código de aplicação usa essas tabelas ainda**: não há
  repositório Go para inbox/outbox, não há worker publicador, e o
  caso de uso `OpenWallet` — o único caso de uso implementado — não
  grava eventos de outbox, apesar de a seção 9 do desafio exigir que
  abertura de carteira com saldo positivo produza
  `WagerTransactionProcessed` e `WalletBalanceChanged` no mesmo commit.
  Isso é uma lacuna conhecida, não uma omissão silenciosa.

## 7. Autenticação e autorização

**Decisão: Keycloak como IdP, client_credentials, JWT RS256 validado
localmente contra JWKS.**

- Justificativa: Keycloak é o IdP recomendado no desafio, roda bem em
  Docker Compose, e suporta `client_credentials` nativamente para
  comunicação client-to-service. Evita implementar emissão de token
  própria (fora do escopo).
- `internal/infra/idp/jwks.go`: `KeySet` mantém em cache as chaves
  públicas RSA do JWKS, indexadas por `kid`; em cache miss, refaz o
  fetch antes de falhar.
- `internal/infra/idp/middleware.go`: `Authenticate` extrai o Bearer
  token, valida assinatura RS256 contra a chave do `kid` informado,
  valida `issuer`, e injeta uma `Identity` (subject, `azp` como
  clientID, `realm_access.roles`) no `context.Context`.
- `RequireRole(role)` é um segundo middleware que checa a identidade
  já autenticada contra uma role específica (ex.: `internal` para
  rotas restritas ao serviço interno).
- Modelo de autorização atual: duas roles de realm, `provider` e
  `internal`. `POST /wallets` exige `internal`; `GET /wallets/{id}`
  exige apenas autenticação (qualquer identidade válida). O
  isolamento entre provedores (cada provider só acessa suas próprias
  transações) **ainda não está implementado** porque não há endpoints
  de transação de aposta nem de consulta por provider — ver
  [Status por área](#status-por-área).
- `scripts/keycloak-bootstrap.sh` provisiona automaticamente: realm
  `wager-ledger`, roles `provider`/`internal`, e três service accounts
  (`provider-a`, `provider-b` com role `provider`; `wager-internal`
  com role `internal`) via API admin do Keycloak, aguardando o
  container ficar disponível antes de agir.
- Mensageria (SQS): a seção 2 do desafio pede controle de acesso à
  mensageria por credenciais/políticas do broker. Como a integração
  SQS ainda não existe, essa política também não foi definida — fica
  para quando o consumidor for implementado.

## 8. Persistência e transações (Postgres)

- Driver: `pgx/v5` com SQL explícito (sem ORM, sem `sqlc`).
- `internal/infra/postgres/txmanager.go`: `WithinTx(ctx, pool, fn)`
  abre uma transação, injeta `pgx.Tx` no `context.Context` via chave
  privada (`txCtxKey`), e cada repositório resolve a partir do
  context se há uma transação em andamento (`querierFrom`) ou usa o
  pool diretamente (fora de transação). Isso permite que múltiplos
  repositórios (`wallet`, `wagertx`, `ledger`) participem da mesma
  transação SQL sem que cada um precise saber dos outros.
- `PgUnitOfWork.Execute` é a implementação de `ports.UnitOfWork` usada
  pelos casos de uso — delimita exatamente onde a transação SQL
  começa e termina em torno de uma operação de negócio (ex.:
  `OpenWallet` cria a carteira + `OPENING` + lançamento do ledger, tudo
  dentro de um único `Execute`).
- Violação de unicidade do Postgres (código `23505`) é mapeada para
  `ports.ErrAlreadyExists` (`isUniqueViolation` em `txmanager.go`),
  desacoplando os casos de uso do código de erro específico do driver.
- `NewPool` (`pool.go`) registra `OnStop` no `fx.Lifecycle` para
  fechar o pool de conexões no shutdown.

## 9. Composição com Uber Fx

`cmd/api/main.go` monta os módulos:
`config → observability → postgres → idp → idgen → usecase → http`.

- Cada pacote de infra expõe seu próprio `Module` (`fx.Module` com
  `fx.Provide`/`fx.Invoke`), e `main.go` apenas os combina — nenhuma
  lógica de composição fica fora dos módulos.
- `fx.Annotate(..., fx.As(new(ports.X)))` é usado consistentemente
  para expor implementações concretas de infra através das interfaces
  de `ports` (ex.: `NewWalletRepository` → `ports.WalletRepository`),
  mantendo o domínio e a aplicação livres de dependência direta de
  Postgres/pgx.
- `fx.Lifecycle` hoje gerencia: abertura/fechamento do pool Postgres
  (`postgres/pool.go`) e start/graceful shutdown do servidor HTTP
  (`http/server.go`, com `srv.Shutdown(ctx)` chamado no `OnStop`).
  `fx.StopTimeout(20 * time.Second)` limita o tempo total de shutdown.
- **Ainda não há workers geridos pelo Fx**: nenhum consumidor SQS,
  worker de outbox, ou worker de retry de referência pendente foi
  implementado, então não há `fx.Lifecycle` para eles ainda. Quando
  existirem, devem seguir o mesmo padrão do servidor HTTP —
  `OnStart` inicia o loop em goroutine própria, `OnStop` cancela o
  contexto e aguarda o término observável do trabalho em andamento.

## 10. Geração de identificadores

- `internal/infra/idgen/uuid.go`: `UUIDGenerator.NewID()` usa
  `uuid.NewString()` (github.com/google/uuid), que gera **UUIDv4**
  (aleatório), não UUIDv7. Os exemplos do `README.md`
  (`0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1`) sugerem UUIDv7
  (ordenável por tempo), mas isso é só formato de exemplo — o desafio
  não exige uma versão específica de UUID. Nenhuma parte do sistema
  depende de ordenação temporal do ID, então UUIDv4 é aceitável;
  revisitar se um requisito futuro precisar de IDs ordenáveis (ex.:
  paginação por cursor derivada do ID em vez de `created_at`).

## 11. Observabilidade

- `internal/observability/logger.go`: logger `slog` com handler JSON,
  nível `Debug` em `development` e `Info` em outros ambientes. Campos
  fixos: `service`, `env`.
- **Ainda não implementado**: inclusão de `correlationId`,
  `messageId`, `transactionId`, `walletId`, `providerId` nos logs de
  cada requisição/mensagem (a seção 12 do desafio exige isso); não há
  métricas expostas; não há tracing OpenTelemetry (diferencial
  opcional).

## 12. Ambiente local (Docker Compose)

`docker compose up --build` sobe a aplicação inteira, sem passos manuais:

- `postgres:16-alpine` — banco principal, com healthcheck via
  `pg_isready`.
- `localstack/localstack:3` com `SERVICES: sqs` — para SQS local,
  ainda não consumido por nenhum código da aplicação.
- `quay.io/keycloak/keycloak:25.0` em modo `start-dev` — IdP.
- `keycloak-bootstrap` — serviço one-shot (`alpine` + `scripts/keycloak-bootstrap.sh`)
  que provisiona realm, roles e clients automaticamente; a `api` só
  inicia depois dele terminar (`condition: service_completed_successfully`).
- `migrate` — serviço one-shot que aplica as migrations pendentes
  (`golang-migrate` instalado via `go install` dentro de uma imagem
  `golang:1.22-bookworm`, evitando a imagem oficial `migrate/migrate`,
  que se mostrou instável neste ambiente); idempotente, roda antes da `api`.
- `api` — `docker/api.Dockerfile`, stage `dev`: imagem `golang:1.22-bookworm`
  completa com `air` (hot-reload, equivalente ao `nodemon`) rodando
  `air -c .air.toml`; código-fonte montado como volume (`.:/src`), então
  qualquer alteração em um arquivo `.go` recompila e reinicia o servidor
  automaticamente dentro do container, sem rebuild manual da imagem.
  Volumes nomeados (`api_gocache`, `api_gomodcache`) cacheiam o build do
  Go entre reinícios do container.
- `docker/api.Dockerfile`, stage `runtime` (produção, inalterado): build
  multi-stage, binário estático (`CGO_ENABLED=0`), imagem final
  `distroless/static-debian12:nonroot` — sem shell, roda como usuário
  não-root. Os dois stages compartilham o stage `base` (download de
  dependências), então um único `Dockerfile` serve dev e produção,
  selecionados via `target:`.
- `OIDC_JWKS_URL` da `api` aponta para o hostname interno do compose
  (`http://keycloak:8080/...`, alcançável só de dentro da rede Docker);
  `OIDC_ISSUER_URL` continua `http://localhost:8081/...` porque o
  Keycloak em `start-dev` deriva o claim `iss` do host usado na
  requisição de token — como quem obtém tokens para testar a API o faz
  pela porta mapeada no host, o issuer validado tem que ser esse mesmo
  valor, não o hostname interno.
- **Ainda falta**: provisionamento automático de filas SQS/DLQ no
  LocalStack (só existe quando o consumidor SQS for implementado).

## Status por área

| Área (referência da seção do desafio) | Status | Evidência |
| --- | --- | --- |
| Money (6.1) | **Concluído** | `domain/money`, 12 testes |
| Wallet — modelo e concorrência otimista (6.2, 8) | **Concluído**, incluindo o caso de uso de disputa (retry otimista em `ProcessWagerTransaction`) | `domain/wallet`, `wallet_repository.go`, `usecase/process_wager_transaction.go` |
| WagerTransaction — modelo e máquina de estados (6.3) | **Concluído** (modelo); processamento de BET/WIN/LOSS concluído, REFUND/ROLLBACK pendente (fase 11) | `domain/wagertx`, `usecase/process_wager_transaction.go` |
| WalletLedgerEntry (6.4) | **Concluído** | `domain/ledger`, trigger de imutabilidade |
| Inbox/outbox — schema (6.5, 11) | **Parcial**: outbox gravado pela abertura de carteira; inbox e worker publicador ainda não existem | migration `0004`, `postgres/outbox_repository.go` |
| Abertura de carteira (`POST /wallets`) (9) | **Concluído**: cria carteira + OPENING + ledger + eventos de outbox (`WagerTransactionProcessed`, `WalletBalanceChanged`) no mesmo commit | `usecase/open_wallet.go`, `postgres/outbox_repository.go`, `domain/event` |
| Leitura de carteira (`GET /wallets/:id`) (9) | **Concluído** | `http/wallets.go` |
| Ledger paginado (`GET /wallets/:id/ledger`) (9) | **Não iniciado** | — |
| Envio de operação (`POST /wagering/transactions`) — BET/WIN/LOSS (9) | **Concluído**: idempotência completa, retry otimista, eventos de outbox | `usecase/process_wager_transaction.go`, `http/wagering.go` |
| Envio de operação — REFUND/ROLLBACK (9) | **Não iniciado** (fase 11) | — |
| Consulta de transação (`GET /wagering/transactions/:id`) (9) | **Concluído** (com isolamento por provider); consulta por `(providerId, externalId)` ainda não tem rota própria | `http/wagering.go` |
| Reconciliação (`POST /wallets/:id/reconciliation`) (9) | **Não iniciado** | — |
| Health checks (`/health/live`, `/health/ready`) (9) | **Concluído** (live); `ready` aceita checkers mas nenhum é registrado ainda | `http/health.go` |
| Consumidor SQS (10) | **Não iniciado**: sem dependência AWS SDK no `go.mod`, sem código | — |
| Worker de outbox (11) | **Não iniciado** | — |
| Worker de referência pendente (`PENDING_REFERENCE`) (7) | **Não iniciado** | — |
| Autenticação/autorização — validação de token (2) | **Concluído** | `infra/idp`, 9 testes, teste HTTP E2E |
| Autorização — isolamento por provider | **Concluído** para `POST /wagering/transactions` e `GET /wagering/transactions/:id`; `GET /providers/:id/...` ainda não existe | `http/wagering.go` |
| Reconciliação de saldo (9) | **Não iniciado** | — |
| Observabilidade — logs JSON (12) | **Parcial**: logger existe, sem correlação por requisição | `observability/logger.go` |
| Observabilidade — métricas (12) | **Não iniciado** | — |
| Testes unitários de domínio (13) | **Em bom andamento**: 54 testes em `money`/`wallet`/`wagertx`/`ledger` | ver arquivos `*_test.go` |
| Testes de integração (13) | **Iniciado**: Postgres real via Testcontainers-like DSN direto, 6 testes cobrindo lock otimista e fluxo completo em transação | `postgres/integration_test.go` |
| Testes de autenticação (13) | **Iniciado**: 9 testes de middleware + 5 testes HTTP E2E | `idp/middleware_test.go`, `http/wallets_test.go` |
| Testes de concorrência — mesmo processo (13) | **Concluído**: cenário obrigatório 100/80/80 e 50 requisições paralelas da mesma aposta, contra Postgres real, `-race` | `postgres/process_wager_transaction_integration_test.go` |
| Testes de concorrência — múltiplos processos reais (13) | **Não iniciado** (fase 14) | — |

## Limitações conhecidas e trabalho não concluído

1. Não há caso de uso para processar `REFUND`/`ROLLBACK` nem para
   resolver referências pendentes — `BET`/`WIN`/`LOSS` já estão
   completos (`usecase/process_wager_transaction.go`).
2. Não há consumidor SQS, então nenhuma das garantias de at-least-once,
   deduplicação por inbox, DLQ ou `SIGTERM` gracioso foi implementada
   ou testada. HTTP e SQS devem compartilhar o mesmo caso de uso
   `ProcessWagerTransaction` quando o consumidor existir.
3. Não há worker publicador de outbox — os eventos são gravados
   corretamente na tabela, mas nada os publica ainda.
4. Não há worker de retry para `PENDING_REFERENCE`, nem política de
   TTL/máximo de tentativas.
5. `idempotency_key` não tem unicidade própria no schema — apenas
   `(provider_id, external_transaction_id)`, mais um índice não-único
   em `idempotency_key` (migration `0005`) para o lookup de replay. A
   consistência é garantida pela aplicação
   (`ProcessWagerTransaction.resolveExisting`), verificada em teste de
   concorrência real (50 requisições paralelas da mesma aposta).
6. `GET /health/ready` aceita uma lista de `ReadinessChecker` mas
   nenhum é registrado hoje (nem Postgres, nem — quando existir — SQS).
7. `docker-compose.yml` não inclui o serviço da própria API nem
   provisiona filas SQS automaticamente.
8. Os testes de concorrência da fase 10 rodam múltiplas goroutines no
   mesmo processo contra o Postgres real — comprovam a garantia da
   seção 8, mas a seção 13 também exige processos de sistema
   operacional independentes, com pool de conexões próprio; isso fica
   para a fase 14.
