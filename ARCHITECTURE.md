# Arquitetura

Este documento descreve o estado atual da implementação do desafio
"Processamento Distribuído de Apostas em Go" e as decisões técnicas por
trás dele. Ele é atualizado conforme o trabalho avança — a seção
[Status por área](#status-por-área) reflete o que está pronto, parcial
ou não iniciado no momento. Revisão de 17/09/2026 (rodada de QA): consumidor
SQS, worker publicador de outbox, worker de retry/expiração de
`PENDING_REFERENCE` e reconciliação de saldo — descritos como "não
iniciado" em revisões anteriores deste documento — estão implementados e
cobertos por testes automatizados (unitários, de integração contra Postgres
real e, para os cenários de concorrência/distribuição, contra três ou mais
processos de sistema operacional reais via `docker compose` + Keycloak +
LocalStack reais). Veja a [auditoria de implementação](docs/IMPLEMENTATION_AUDIT.md)
e o [log de QA](docs/QA_LOG.md) para o detalhamento por cenário e a evidência
de cada verificação.

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
  moeda de três letras (a lista ISO 4217 ainda não é validada), sem `float32`/`float64` em nenhum ponto do parsing,
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

Limitações: valores `Money{}` não são rejeitados em todas as operações;
`DecimalString` não trata corretamente `math.MinInt64`. O parser normaliza
valores como `25`, `25.0`, `+25.00` e `025.00` para `25.00`; moedas são
convertidas para maiúsculas antes de calcular o hash.

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
  diferentes não usam um lock global da aplicação. UPDATEs ainda adquirem
  locks transacionais no PostgreSQL.
- Invariantes de saldo (não negatividade) e de versão (`>= 1`) também
  são impostas por `CHECK` constraints no schema
  (`wallets_balance_non_negative`, `wallets_version_positive`), como
  defesa em profundidade além da validação em memória.
- `ProcessWagerTransaction.Execute` repete a transação SQL completa até
  cinco vezes após `ErrOptimisticLock` ou `ErrAlreadyExists`.
- Existem testes PostgreSQL para 100/80/80 e 50 envios da mesma aposta
  (goroutines em um processo, `postgres/process_wager_transaction_integration_test.go`),
  além de validação manual com processos de sistema operacional reais
  (`curl` concorrentes, três e mais processos) contra a API rodando em
  Docker — ver `docs/QA_LOG.md`, rodada de 17/09/2026.

## 4. WagerTransaction

- Tipos: `BET`, `WIN`, `LOSS`, `REFUND`, `ROLLBACK` (externos) e
  `OPENING` (interno). `ProcessWagerTransaction` processa todos os tipos
  externos, tanto via HTTP (`Execute`) quanto via consumidor SQS
  (`ExecuteWithin`, que roda dentro da mesma transação do inbox —
  `internal/infra/sqs/consumer.go`).
- `REFUND`/`ROLLBACK` com referência ainda não persistida ficam em
  `PENDING_REFERENCE`; `ReferenceRetryWorker`
  (`usecase/reference_retry_worker.go`) faz polling a cada segundo e
  resolve a pendência assim que a referência chega (`ResumePendingReference`),
  ou a expira para `FAILED` com `failureCode=REFERENCE_EXPIRED` após o TTL
  configurável `REFERENCE_PENDING_TTL` (padrão 15 minutos —
  `ExpirePendingReference`). Cada tentativa sem sucesso grava backoff
  exponencial (1s a 30s, `referenceRetryBackoff`) em
  `reference_retry_attempts`/`reference_next_retry_at`, e
  `ListPendingReferenceIDs` só seleciona transações cujo backoff já
  expirou — sem isso, uma pendência antiga sem solução ocuparia
  permanentemente as vagas do `LIMIT` do worker, impedindo pendências
  mais novas de serem tentadas (achado de revisão corrigido nesta
  rodada; as colunas já existiam desde a migration 0005 mas não eram
  usadas). Essa política de TTL era uma lacuna real
  identificada nesta rodada de QA (não havia expiração/política para
  pendências) e foi implementada com teste dedicado.
- Máquina de estados (`internal/domain/wagertx/wagertx.go`):
  `PENDING → {PENDING_REFERENCE, PROCESSED, REJECTED, FAILED}`, e
  `PENDING_REFERENCE → {PROCESSED, REJECTED, FAILED}`. Estados
  terminais (`PROCESSED`, `REJECTED`, `FAILED`) não aceitam novas
  transições (`ErrInvalidTransition`). `MarkRejected`/`MarkFailed`
  exigem `failureCode` não vazio.
- Regras de valor por tipo, validadas na construção
  (`NewExternalTransaction`): `LOSS` exige `amount == 0.00`;
  `BET`/`WIN`/`REFUND`/`ROLLBACK` exigem valor `> 0`.
  `REFUND`/`ROLLBACK` exigem `referenceExternalTransactionId`; `WIN`
  aceita esse campo como opcional (referência ao BET da mesma rodada,
  README §7); `BET`/`LOSS` rejeitam o campo se presente. A constraint
  `wager_tx_reference_by_kind` (migration 0007) acompanha essa regra no
  schema.
- `NewOpeningTransaction` constrói já em `PROCESSED`, sem os metadados
  externos (provider, chave de idempotência, hash, rodada, jogo,
  referência), que fazem sentido apenas para operações vindas de
  provedor.
- Distinção `FAILED` (falha permanente de infraestrutura, para
  auditoria) vs `REJECTED` (regra de negócio) está modelada como
  estados terminais distintos com `failureCode` próprio em cada um,
  mas apenas REJECTED é usado hoje para saldo insuficiente e moeda
  incompatível. A política de FAILED ainda não foi implementada.
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

O processamento persiste SHA-256 do JSON com chaves ordenadas, campos de
negócio e Money normalizado, excluindo a chave de idempotência e metadados
de transporte (`usecase/idempotency.go`). Consulta por chave e depois por
operação para distinguir replay e conflito; PROCESSED usa saldo persistido.

`MarkRejectedWithResult` persiste o saldo retornado também para REJECTED,
então `replayOutput` usa o resultado gravado em ambos os casos (PROCESSED e
REJECTED), não o saldo atual da carteira. `idempotency_key` já tem índice
`UNIQUE` parcial (migration 0006), e `ProcessWagerTransaction` trata a
violação como `ErrIdempotencyConflict`, impedindo duas operações distintas
de confirmarem simultaneamente a mesma chave (coberto por teste concorrente
em `internal/infra/postgres/process_wager_transaction_integration_test.go`).

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
- `OutboxRepository` grava eventos na mesma transação de `OpenWallet`
  (saldo positivo) e `ProcessWagerTransaction`. Processamento financeiro
  gera `WagerTransactionProcessed` e `WalletBalanceChanged`; LOSS gera
  apenas o primeiro; rejeição gera `WagerTransactionRejected`; pendência
  de referência gera `WagerTransactionPendingReference`; expiração de
  pendência gera `WagerTransactionExpired`.
- **Outbox — publisher** (`internal/infra/outbox/publisher.go`): worker
  gerido pelo Fx que faz `Claim` (via `SELECT ... FOR UPDATE SKIP LOCKED`
  com TTL de lock de 30s) em lotes, publica cada evento no SQS FIFO
  (`wager-events.fifo`, com `MessageDeduplicationId = eventId`,
  preservando o mesmo `eventId` em republicações) e marca `published_at`.
  Falha de publicação incrementa `attempts` e agenda o próximo retry com
  backoff exponencial (`next_attempt_at`). Dois publishers concorrentes
  disputando os mesmos registros pendentes não publicam o mesmo evento
  duas vezes (lock com `SKIP LOCKED`) e um publisher travado tem seu lock
  retomado por outro após o TTL — testado em
  `internal/infra/outbox/publisher_integration_test.go` contra Postgres e
  LocalStack reais, e em `internal/infra/postgres/outbox_publisher_repository_test.go`
  no nível do repositório.
- **Inbox — consumidor SQS** (`internal/infra/sqs/consumer.go`): consome
  `wager-transactions.fifo`, grava `inbox_messages` e chama
  `ProcessWagerTransaction.ExecuteWithin` na mesma transação SQL, só
  removendo a mensagem da fila (`DeleteMessage`) após o commit. Uma
  reentrega do SQS após o commit (ex.: o processo morre antes do
  `DeleteMessage`) é detectada pelo `inbox_messages` (chave única
  `(consumer_name, message_id)`) e tratada como no-op idempotente — sem
  efeito financeiro duplicado. Essa combinação teve um bug real corrigido
  nesta rodada de QA: ver [Limitações conhecidas](#limitações-conhecidas-e-trabalho-não-concluído),
  item de correção "inbox duplicado abortava a transação".

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
- Modelo atual: `POST /wallets` exige `internal`; GET de carteira exige
  somente autenticação, divergindo da restrição de operações internas.
  POST de aposta exige `provider` e `providerId == azp`. GET de transação
  verifica dono apenas quando existe role `provider` e o registro tem
  provedor preenchido: identidades sem role autorizada e consultas de
  OPENING não estão protegidas adequadamente. Correção pendente.
- JWT exige `exp` (rejeita token sem expiração) e valida `aud` contra
  `OIDC_AUDIENCE` (padrão `wager-ledger-api`), provisionado como client
  scope de audience no Keycloak (`scripts/keycloak-bootstrap.sh`). O JWKS
  tem throttle de refresh e atualização periódica (`internal/infra/idp/jwks.go`).
  Testes de unidade usam JWKS simulado; a integração real com Keycloak
  (token real, `aud` correto) foi validada manualmente via `docker compose`
  nesta rodada de QA — ver `docs/QA_LOG.md`.
- `scripts/keycloak-bootstrap.sh` provisiona automaticamente: realm
  `wager-ledger`, roles `provider`/`internal`, e três service accounts
  (`provider-a`, `provider-b` com role `provider`; `wager-internal`
  com role `internal`) via API admin do Keycloak, aguardando o
  container ficar disponível antes de agir.
- Mensageria (SQS): controle de acesso ao broker usa credenciais
  estáticas (`AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`) apontando para
  o endpoint configurado (`SQS_ENDPOINT`), adequado ao LocalStack local;
  uma política de IAM real de produção (roles por serviço, sem
  credenciais estáticas) fica fora do escopo local e é uma lacuna
  conhecida para deploy em AWS real.

## 8. Persistência e transações (Postgres)

- Driver: `pgx/v5` com SQL explícito (sem ORM, sem `sqlc`).
- `internal/infra/postgres/txmanager.go`: `WithinTx(ctx, pool, fn)`
  abre uma transação, injeta `pgx.Tx` no `context.Context` via chave
  privada (`txCtxKey`), e cada repositório resolve a partir do
  context se há uma transação em andamento (`querierFrom`) ou usa o
  pool diretamente (fora de transação). Isso permite que múltiplos
  repositórios (`wallet`, `wagertx`, `ledger`, `outbox`) participem da mesma
  transação SQL sem que cada um precise saber dos outros.
- `PgUnitOfWork.Execute` é a implementação de `ports.UnitOfWork` usada
  pelos casos de uso — delimita exatamente onde a transação SQL
  começa e termina em torno de uma operação de negócio (ex.:
  `OpenWallet` cria a carteira + `OPENING` + ledger + eventos de outbox, tudo
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
- **Workers geridos pelo Fx**: consumidor SQS (`sqs.NewConsumer`),
  publisher de outbox (`outbox.NewPublisher`) e worker de retry/expiração
  de referência pendente (`usecase.NewReferenceRetryWorker`) seguem todos
  o mesmo padrão do servidor HTTP — `OnStart` inicia o loop em goroutine
  própria, `OnStop` cancela o contexto e aguarda (`<-done`) o término
  observável do trabalho em andamento antes de `fx.StopTimeout` expirar.
- No consumidor SQS, mensagens já recebidas em lote (`ReceiveMessage` traz
  até 10) mas ainda não processadas quando o `OnStop` cancela o contexto
  têm sua visibilidade liberada explicitamente (`ChangeMessageVisibility`
  com `VisibilityTimeout: 0`, em `sqs/consumer.go`), permitindo reentrega
  imediata em vez de esperar o `VisibilityTimeout` da fila (30s, ver
  `scripts/localstack-init-sqs.sh`) expirar naturalmente.

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
- `internal/infra/http/logging.go`: middleware de correlação por
  requisição — gera/propaga `correlationId`, registra log estruturado por
  requisição (método, rota, status, latência).
- `internal/observability/metrics.go`: métricas Prometheus expostas em
  `GET /metrics` — contadores de transações por status/kind, replays
  idempotentes, retries por lock otimista, duplicidades de inbox, falhas
  de publicação da outbox, checagens de reconciliação e divergências.
- **Ainda não implementado**: tracing distribuído OpenTelemetry
  (diferencial opcional, fora do escopo desta rodada).

## 12. Ambiente local (Docker Compose)

`docker compose up --build` está configurado para subir a API e suas
dependências com migrations e bootstrap automáticos. Reexecutado
integralmente na rodada de QA de 17/09/2026 (ver `docs/QA_LOG.md`):

- `postgres:16-alpine` — banco principal, com healthcheck via
  `pg_isready`.
- `localstack/localstack:3` com `SERVICES: sqs` — SQS local, provisionado
  automaticamente via `scripts/localstack-init-sqs.sh` (filas
  `wager-transactions.fifo`/`wager-events.fifo` + DLQs correspondentes com
  `RedrivePolicy`, `maxReceiveCount=5`) e consumido pelo consumidor SQS e
  publicado pelo outbox publisher.
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
- `api` também depende de `localstack: service_healthy` (adicionado
  nesta rodada — antes a `api` podia iniciar antes das filas existirem).

## Status por área

| Área (referência da seção do desafio) | Status | Evidência |
| --- | --- | --- |
| Money (6.1) | **Parcial**: precisão exata; `Money{}` (moeda vazia) agora rejeitado em todas as operações; ISO 4217 validado; overflow de `DecimalString` em `math.MinInt64` corrigido | `domain/money` |
| Wallet — modelo e concorrência otimista (6.2, 8) | **Concluído**, incluindo disputa (retry otimista) e validação com processos de SO reais | `domain/wallet`, `wallet_repository.go`, `usecase/process_wager_transaction.go` |
| WagerTransaction — modelo e máquina de estados (6.3) | **Concluído**: BET/WIN/LOSS/REFUND/ROLLBACK, incluindo `PENDING_REFERENCE` com retomada e expiração por TTL | `domain/wagertx`, `usecase/process_wager_transaction.go`, `usecase/reference_retry_worker.go` |
| WalletLedgerEntry (6.4) | **Concluído** | `domain/ledger`, trigger de imutabilidade |
| Inbox/outbox — schema e workers (6.5, 11) | **Concluído**: outbox publisher com claim/lock/TTL/backoff; inbox com dedup real (bug de transação abortada em duplicidade corrigido nesta rodada) | migration `0004`, `infra/outbox/publisher.go`, `infra/sqs/consumer.go`, `infra/postgres/inbox_repository.go` |
| Abertura de carteira (`POST /wallets`) (9) | **Concluído** | `usecase/open_wallet.go`, `postgres/outbox_repository.go`, `domain/event` |
| Leitura de carteira (`GET /wallets/:id`) (9) | **Concluído** | `http/wallets.go` |
| Ledger paginado (`GET /wallets/:id/ledger`) (9) | **Concluído** | `http/wallets.go`, `postgres/ledger_repository.go` |
| Envio de operação (`POST /wagering/transactions`) — BET/WIN/LOSS/REFUND/ROLLBACK (9) | **Concluído** | `usecase/process_wager_transaction.go`, `http/wagering.go` |
| Consulta de transação (`GET /wagering/transactions/:id`, `GET /providers/:id/wagering/transactions/:externalId`) (9) | **Concluído** | `http/wagering.go` |
| Reconciliação (`POST /wallets/:id/reconciliation`) (9) | **Concluído**, validado em cenários concorrentes e após restart do processo | `usecase/reconcile_wallet.go`, `http/wallets.go` |
| Health checks (`/health/live`, `/health/ready`) (9) | **Concluído**: `ready` checa Postgres e SQS de verdade | `http/health.go`, `postgres/readiness.go`, `sqs/readiness.go` |
| Consumidor SQS (10) | **Concluído**: at-least-once + dedup por inbox validado com reentrega real via LocalStack | `infra/sqs/consumer.go` |
| Worker de outbox (11) | **Concluído**: dois publishers concorrentes sem publicação duplicada, lock com TTL retomável | `infra/outbox/publisher.go` |
| Worker de referência pendente (`PENDING_REFERENCE`) (7) | **Concluído**: retomada quando a referência chega e expiração por TTL (`REFERENCE_PENDING_TTL`, padrão 15 min) quando não chega | `usecase/reference_retry_worker.go` |
| Autenticação/autorização — validação de token (2) | **Concluído**: assinatura, issuer, `exp` obrigatório e `aud` exigidos; validado com Keycloak real | `infra/idp`, `idp/jwks.go`, `idp/middleware.go` |
| Autorização — isolamento por provider | **Concluído** | `http/wagering.go` |
| Observabilidade — logs JSON (12) | **Concluído**: correlação por requisição | `observability/logger.go`, `http/logging.go` |
| Observabilidade — métricas (12) | **Concluído**: Prometheus em `GET /metrics` | `observability/metrics.go` |
| Testes unitários de domínio (13) | **Concluído**: cobertura em todos os pacotes de domínio | ver arquivos `*_test.go` |
| Testes de integração (13) | **Concluído**: Postgres real, incluindo inbox/outbox/reconciliação | `postgres/*_test.go`, `infra/sqs/*_test.go`, `infra/outbox/*_test.go` |
| Testes de autenticação (13) | **Parcial**: middleware e JWKS simulados cobertos por unitários; integração real com Keycloak validada manualmente (não automatizada em CI) | `idp/middleware_test.go`, `idp/jwks_test.go` |
| Testes de concorrência — mesmo processo (13) | **Concluído**: 100/80/80, 50 envios da mesma aposta, carteiras distintas, cross HTTP+SQS, reentrega SQS real, dois publishers da outbox | `postgres/process_wager_transaction_integration_test.go`, `infra/sqs/consumer_integration_test.go`, `infra/outbox/publisher_integration_test.go` |
| Testes de concorrência — múltiplos processos reais (13) | **Validado manualmente** (curl concorrente contra API em Docker; 3+ processos de SO para dedup, 2 processos para disputa de saldo, restart de processo); não automatizado como suíte executável em CI — ver justificativa em `docs/IMPLEMENTATION_AUDIT.md` | `docs/QA_LOG.md` |

## Limitações conhecidas e trabalho não concluído

1. **[Corrigido nesta rodada]** `InboxRepository.Create` capturava a
   violação de unicidade (`23505`) dentro da transação e retornava
   `ports.ErrAlreadyExists` como no-op, mas o Postgres já havia abortado a
   transação — o `COMMIT` subsequente falhava com "commit unexpectedly
   resulted in rollback", fazendo o consumidor tratar uma reentrega
   idempotente como erro (mensagem nunca deletada, reentregue
   indefinidamente até a DLQ). Corrigido trocando o `INSERT` por
   `INSERT ... ON CONFLICT ... DO NOTHING` + checagem de `RowsAffected`,
   que nunca aborta a transação. Ver `internal/infra/postgres/inbox_repository.go`
   e o teste de regressão `TestInboxRepository_Create_DuplicateWithinSameTransaction_DoesNotAbortTransaction`.
2. **[Corrigido nesta rodada]** Não havia política de TTL/expiração para
   `PENDING_REFERENCE` — uma pendência cuja referência nunca chegasse
   ficaria pendente para sempre. Implementado `REFERENCE_PENDING_TTL`
   (config, padrão 15 min) + `ExpirePendingReference` (usecase) +
   `ListStalePendingReferenceIDs` (repositório) + verificação periódica no
   `ReferenceRetryWorker`, transicionando para `FAILED` com
   `failureCode=REFERENCE_EXPIRED`.
3. ~~`idempotency_key` não tem unicidade própria no schema~~ — a migration
   `0006` adiciona índice `UNIQUE` parcial em `idempotency_key`, e
   `WagerTransactionRepository.Create` mapeia a violação para
   `ports.ErrAlreadyExists`, levando `ProcessWagerTransaction.Execute` a
   reexecutar e retornar `ErrIdempotencyConflict` quando o payload diverge.
   Cenário concorrente (mesma chave, `external_transaction_id` diferentes)
   coberto por
   `TestProcessWagerTransaction_SameIdempotencyKeyDifferentExternalID_ConcurrentSingleDebit`.
4. Testes de concorrência/distribuição com processos de sistema
   operacional reais (README §8/§13, itens 4 e 8) foram executados e
   validados manualmente nesta rodada (curl concorrente, kill/restart do
   container `api`), mas não foram convertidos em suíte automatizada
   executável em CI — exigiriam orquestração de containers Docker a
   partir do próprio `go test`, fora do escopo desta rodada. A evidência
   está registrada em `docs/QA_LOG.md`.
5. Autenticação/autorização end-to-end com Keycloak real (token real,
   `aud` correto) foi validada manualmente via `docker compose`, não como
   teste automatizado (os testes de unidade usam JWKS simulado).
6. Credenciais de acesso ao SQS são estáticas (adequadas ao LocalStack
   local); uma política de IAM/roles por serviço para AWS real fica fora
   do escopo local.
7. Tracing distribuído (OpenTelemetry) não foi implementado — diferencial
   opcional do desafio.
8. ~~WIN não podia referenciar o BET da mesma rodada~~ — `rulesFor(KindWin)`
   já permite referência opcional e a migration `0007`
   (`wager_tx_win_reference`) já relaxa `wager_tx_reference_by_kind` para
   aceitar isso; coberto por `TestProcessWagerTransaction_WinReferencingBet_SameRound_CreditsWallet`
   e pelo teste de integração `TestProcessWagerTransaction_WinReferencingBet_PersistsAgainstRealSchema`.
9. ~~Falhas de infraestrutura HTTP voltavam como 400~~ —
   `writeProcessWagerTransactionError` já classifica
   `context.Canceled`/`DeadlineExceeded` como 503 e usa 500 genérico
   (sem `err.Error()`) como default.
10. **[Corrigido nesta rodada]** Erros de validação de domínio
    (`wagertx.Err...`, ex. `ErrAmountMustBePositive`, `ErrEmptyRoundID`)
    caíam no `default` de `writeProcessWagerTransactionError` e voltavam
    como 500 em vez de 400 — um payload malformado do provedor era
    reportado como incidente interno do wager-ledger. Adicionado
    `wagertx.IsInputValidationError` (lista fechada de sentinelas de
    entrada, exclui `ErrInvalidTransition`/`ErrEmptyFailureCode`, que são
    invariantes internas) e um `case` dedicado em
    `writeProcessWagerTransactionError` que classifica esses erros como
    400.
11. **[Corrigido nesta rodada]** `money.Money{}` zero-value (chave
    `"money"` ausente no JSON) escapava da validação ISO 4217, porque
    `encoding/json` só chama `UnmarshalJSON` quando a chave existe no
    payload. Para `LOSS` (que aceita `amount == 0.00`), isso passava
    `NewExternalTransaction` com `currency=""` e só falhava depois, na
    constraint `wager_tx_currency_format` do Postgres, como erro 500
    genérico. Adicionada checagem explícita de `amount.Currency() == ""`
    em `NewExternalTransaction` (`wagertx.ErrEmptyCurrency`), coberta por
    `TestProcessWagerTransaction_HTTP_LossWithoutMoneyField_Returns400`.
