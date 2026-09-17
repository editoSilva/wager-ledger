# Auditoria de implementação e documentação

**Atualização: 17/09/2026 (revisão go-reviewer)** — revisão de código por
agente especialista em Go, focada em domínio financeiro (wallet, wagertx,
ledger, money, inbox/outbox, idempotência). Confirmou como corretas todas
as correções das rodadas anteriores (unicidade de `idempotency_key`, WIN
com referência, replay de REJECTED, classificação 503/500, inbox sem
abort, TTL de pendências) e encontrou 3 achados novos, todos corrigidos
nesta rodada:

1. **Importante** — `reference_retry_worker.go` fazia polling sem
   backoff: as colunas `reference_retry_attempts`/`reference_next_retry_at`
   (migration 0005) nunca eram lidas/escritas, e o `LIMIT 32` do
   `ListPendingReferenceIDs` podia ficar permanentemente ocupado por
   pendências antigas sem solução, impedindo pendências mais novas de
   serem tentadas (starvation). Corrigido: `WagerTransaction.RecordReferenceRetryAttempt`
   grava tentativa + próximo horário (backoff exponencial 1s–30s,
   `referenceRetryBackoff` em `process_wager_transaction.go`), `ResolveReference`
   zera o backoff ao resolver, e `ListPendingReferenceIDs` filtra por
   `reference_next_retry_at IS NULL OR <= now()`. Testes:
   `TestRecordReferenceRetryAttempt_*` (domínio),
   `TestProcessWagerTransaction_ResumePendingReference_WithoutReference_SchedulesBackoff`
   (usecase).
2. **Importante** — `writeProcessWagerTransactionError` só classificava
   um punhado de erros; qualquer erro de validação de domínio
   (`wagertx.Err...`) caía no `default` e voltava como 500, tratando
   payload malformado do provedor como incidente interno. Corrigido:
   `wagertx.IsInputValidationError` + `case` dedicado retornando 400.
3. **Importante** — `money.Money{}` zero-value (chave `"money"` ausente
   no JSON) escapava da validação ISO 4217 para `LOSS` (aceita
   `amount == 0.00`) e só falhava depois na constraint
   `wager_tx_currency_format` do Postgres, como 500 genérico. Corrigido:
   `NewExternalTransaction` rejeita `amount.Currency() == ""`
   explicitamente (`wagertx.ErrEmptyCurrency`). Teste:
   `TestProcessWagerTransaction_HTTP_LossWithoutMoneyField_Returns400`.

Ver ARCHITECTURE.md, limitações 10 e 11, e a nota de backoff na seção 4
(WagerTransaction), para os detalhes de implementação.

---

Data original: 17/09/2026 (revisão estática). **Atualização de QA: 17/09/2026**
— nova rodada, escopo: executar a bateria obrigatória do `README.md` §8/§13
como testes automatizados sempre que possível, contra Postgres/LocalStack/
Keycloak reais, corrigir bugs reais encontrados e revalidar as lacunas
listadas abaixo. Ver `docs/QA_LOG.md` para o registro detalhado por cenário
(comandos, evidência, timestamps).

## Resultado (atualizado nesta rodada de QA)

Desde a revisão estática original, o projeto passou a ter: consumidor SQS
com inbox real, publisher de outbox com claim/lock/TTL/backoff, worker de
retomada **e expiração por TTL** de `PENDING_REFERENCE`, reconciliação de
saldo exposta via `POST /wallets/:id/reconciliation`, autenticação JWT
exigindo `aud`/`exp` validada contra Keycloak real, métricas Prometheus e
logs correlacionados por requisição. Esta rodada de QA:

1. Confirmou `go build`, `go vet`, `go test ./...` e `go test -race ./...`
   limpos antes e depois das mudanças.
2. Encontrou e corrigiu um bug real de concorrência/idempotência:
   `InboxRepository.Create` abortava a transação Postgres ao tratar uma
   duplicidade esperada (reentrega SQS) como no-op, fazendo o `COMMIT`
   falhar e a mensagem nunca ser deletada da fila (reentrega indefinida
   até a DLQ apesar do efeito financeiro já ter sido aplicado uma vez).
   Corrigido com `INSERT ... ON CONFLICT ... DO NOTHING` — ver achado
   P1 atualizado abaixo.
3. Encontrou uma lacuna real (ausência de política de TTL/expiração para
   `PENDING_REFERENCE`, mencionada como risco na revisão original) e
   implementou-a: `REFERENCE_PENDING_TTL` (config, padrão 15 min),
   `ExpirePendingReference`, `ListStalePendingReferenceIDs`, evento
   `WagerTransactionExpired`.
4. Adicionou testes automatizados de integração para: 50 envios
   concorrentes da mesma aposta (já existia, reforçado com reconciliação),
   disputa de saldo por goroutines (já existia, reforçado com
   reconciliação), carteiras distintas processadas concorrentemente sem
   interferência, reentrega real de mensagem SQS após commit (idempotência
   via inbox), mesma operação lógica por HTTP e por SQS, dois publishers
   de outbox disputando os mesmos registros pendentes (sem publicação
   duplicada, via LocalStack real), expiração de `PENDING_REFERENCE` por
   TTL.
5. Validou manualmente, com processos de sistema operacional reais
   (`curl` concorrente) contra a aplicação completa em `docker compose`
   (Postgres, LocalStack, Keycloak reais): 3 processos independentes
   enviando a mesma aposta (idempotência), 2 processos disputando saldo
   insuficiente, REFUND antes do BET existir com resolução posterior pelo
   `ReferenceRetryWorker`, kill+restart do container `api` com
   preservação de `PENDING_REFERENCE` e retomada pelo **novo** processo,
   mensagem SQS real (LocalStack) redelivered com dedup por inbox, e
   reconciliação consistente após cada cenário. Essa evidência está em
   `docs/QA_LOG.md` e não foi convertida em suíte automatizada de CI (ver
   justificativa na seção "Testes e limites da evidência").

As lacunas de autorização/idempotência do parágrafo original abaixo foram
revisadas: a maioria foi corrigida em rodadas anteriores a esta (ver
ARCHITECTURE.md); esta rodada não encontrou novas falhas de autorização.

---

## Resultado (revisão estática original, 17/09/2026, mantido para histórico)

O projeto já possui um núcleo financeiro funcional para abertura de carteira e processamento HTTP de BET, WIN sem referência e LOSS: domínio separado da infraestrutura, PostgreSQL com transação compartilhada, ledger e gravação de outbox, autenticação JWT e retry otimista. Ainda não cumpre o desafio completo: faltam SQS/inbox, publicação da outbox, reversões, recuperação de referências, reconciliação e provas com processos independentes. Há também defeitos de autorização e idempotência que impedem classificar essas garantias como concluídas.

O README é principalmente a especificação do desafio, não um inventário de funcionalidades entregues. Suas exigências devem ser preservadas; estados de implementação pertencem à arquitetura e a este relatório. Automatizar build e deploy não elimina essas lacunas.

**Nota da rodada de QA de 17/09/2026**: o parágrafo acima descreve o estado
em que esta seção foi originalmente escrita, que precede a implementação
de SQS/inbox/outbox/reconciliação. Ele é mantido por rastreabilidade; o
estado atual está descrito na seção "Resultado (atualizado nesta rodada de
QA)" acima e em `ARCHITECTURE.md`.

## Matriz de requisitos, evidências e lacunas

| Requisito do README | Situação observada | Evidência no repositório | Lacuna |
| --- | --- | --- | --- |
| Go, Modules, Fx, PostgreSQL, Compose (§4) | Implementado para a API | `go.mod`, `cmd/api/main.go`, módulos Fx, `docker-compose.yml`, `docker/api.Dockerfile` | Workers e ciclo de vida da mensageria ausentes |
| Dinheiro exato e overflow (§6.1) | Parcial | `internal/domain/money/money.go`; persistência BIGINT/CHAR(3) | Zero-value, limite mínimo e validação de moeda exigem correções descritas abaixo |
| Carteira e concorrência (§6.2, §8) | Implementado no fluxo atual | `wallet.go`, `wallet_repository.go:Save`, `ProcessWagerTransaction.Execute` | Retry limitado a 5; provas existentes usam goroutines, não três processos |
| Abertura e crédito inicial atômico (§9) | Implementado | `open_wallet.go`, migrations 0001/0002/0003, outbox | Abertura positiva grava OPENING, ledger e dois eventos; zero não os cria |
| BET, WIN, LOSS (§7) | **[QA 17/09] Implementado**, incluindo WIN com referência opcional ao BET da mesma rodada (migration 0007) | `process_wager_transaction.go` | — |
| REFUND, ROLLBACK e referências (§7) | **[QA 17/09] Implementado**, incluindo `PENDING_REFERENCE` e expiração por TTL | `process_wager_transaction.go`, `reference_retry_worker.go` | — |
| Pendências duráveis e FAILED (§6.3, §7) | **[QA 17/09] Implementado**: worker de retomada + TTL de expiração (`REFERENCE_PENDING_TTL`, lacuna real corrigida nesta rodada) | `wagertx.go`, `reference_retry_worker.go`, `process_wager_transaction.go` | — |
| Ledger append-only (§6.4) | Implementado no fluxo e protegido contra UPDATE/DELETE; **[QA 17/09]** reconciliação exposta e testada | migration 0003, `ledger.go`, `reconcile_wallet.go` | — |
| Hash persistente e replay (§9) | **[QA 17/09] Implementado**: exclusão concorrente por chave (migration 0006) e replay de rejeição com saldo persistido | `wagertx_repository.go`, `resolveExisting`, `replayOutput` | — |
| OAuth/OIDC e autorização (§2) | **[QA 17/09] Implementado**: `aud`/`exp` obrigatórios, validado com Keycloak real | `idp/middleware.go`, `jwks.go`, handlers | Integração real com Keycloak validada manualmente, não automatizada em CI |
| Consultas e reconciliação (§9) | **[QA 17/09] Implementado**: ledger com cursor, consulta por provedor/ID externo, reconciliação | `wallets.go`, `wagering.go`, `reconcile_wallet.go` | — |
| HTTP e classificação de falhas (§9) | **[QA 17/09] Implementado**: `context.Canceled`/`DeadlineExceeded` retornam 503, default 500 sem expor `err.Error()` | `writeProcessWagerTransactionError` | — |
| Readiness (§9) | **[QA 17/09] Implementado**: checa Postgres e SQS de verdade | `postgres/readiness.go`, `sqs/readiness.go`, `http/health.go` | — |
| Consumidor, inbox, DLQ e políticas (§10) | **[QA 17/09] Implementado**: consumidor real, inbox com dedup, filas + DLQ provisionadas via `scripts/localstack-init-sqs.sh` | `infra/sqs/consumer.go`, `infra/sqs/consumer_integration_test.go`, `infra/postgres/inbox_repository.go` | Política de IAM/roles reais (produção AWS) fora do escopo local |
| Outbox transacional (§11) | **[QA 17/09] Implementado**: publisher com claim/lock TTL/backoff/recuperação | `infra/outbox/publisher.go`, `infra/outbox/publisher_integration_test.go`, `infra/postgres/outbox_publisher_repository_test.go` | — |
| Observabilidade (§12) | **[QA 17/09] Implementado**: métricas Prometheus + correlação por requisição | `observability/metrics.go`, `http/logging.go` | Tracing OpenTelemetry (diferencial opcional) não implementado |
| Ambiente local (§15) | **[QA 17/09] Validado end-to-end** via `docker compose up` real (Postgres, LocalStack, Keycloak, api) | Compose inclui api, migrate, keycloak-bootstrap, `depends_on: localstack healthy` | — |
| Verificação distribuída (§13) | **[QA 17/09] Ampliado**: testes automatizados (50 concorrentes, disputa de saldo, carteiras distintas, reentrega SQS real, dois publishers, HTTP+SQS cruzado, expiração TTL) + validação manual com 2–3 processos de SO reais e restart de processo | `postgres/process_wager_transaction_integration_test.go`, `infra/sqs/consumer_integration_test.go`, `infra/outbox/publisher_integration_test.go`, `docs/QA_LOG.md` | Processos de SO reais e restart não estão automatizados em CI (exigiria orquestrar Docker a partir do `go test`) |

## Achados desta rodada de QA (17/09/2026)

### P1 — [Corrigido] Duplicidade de inbox abortava a transação e impedia a exclusão da mensagem SQS

Ao reproduzir o cenário do README §13 item 5 (processo interrompido entre o
commit e o `DeleteMessage`) como teste automatizado
(`TestConsumer_RedeliveredMessageAfterCommit_IsIdempotent`), a reentrega da
mesma mensagem falhava com `commit unexpectedly resulted in rollback`.
Causa raiz: `InboxRepository.Create` (`internal/infra/postgres/inbox_repository.go`)
executava um `INSERT` simples e capturava a violação de unicidade (`23505`)
para devolver `ports.ErrAlreadyExists` como sinal de duplicidade esperada;
mas o Postgres já havia marcado a transação inteira como abortada nesse
ponto, então o `COMMIT` subsequente (feito pelo `PgUnitOfWork.Execute`, que
não via erro de `fn` porque o consumidor trata duplicidade como no-op)
falhava. Na prática: uma reentrega legítima do SQS (a exata garantia que a
inbox deveria proteger) virava um erro tratado como falha de processamento,
a mensagem nunca era deletada da fila, e ela seria reentregue indefinidamente
até estourar `maxReceiveCount` e cair na DLQ — apesar do efeito financeiro já
ter sido aplicado corretamente uma única vez. **Corrigido** trocando o
`INSERT` por `INSERT ... ON CONFLICT ON CONSTRAINT inbox_consumer_message_unique
DO NOTHING` e checando `RowsAffected()`, que nunca aborta a transação em
caso de conflito. Teste de regressão em
`internal/infra/postgres/inbox_repository_test.go`
(`TestInboxRepository_Create_DuplicateWithinSameTransaction_DoesNotAbortTransaction`)
e no nível do consumidor em `internal/infra/sqs/consumer_integration_test.go`.

### [Lacuna real corrigida] Ausência de política de TTL/expiração para PENDING_REFERENCE

A revisão estática original já apontava a ausência de TTL/backoff para
pendências (achado "Pendências duráveis e FAILED"). Confirmado nesta
rodada: uma transação em `PENDING_REFERENCE` cuja referência nunca chegasse
ficaria pendente indefinidamente, sem transição para um estado terminal.
Implementado: config `REFERENCE_PENDING_TTL` (padrão 15 min),
`ProcessWagerTransaction.ExpirePendingReference`,
`WagerTransactionRepository.ListStalePendingReferenceIDs`, verificação
periódica adicionada ao `ReferenceRetryWorker`, evento de domínio
`WagerTransactionExpired`, failure code `REFERENCE_EXPIRED`. Testado em
`internal/application/usecase/process_wager_transaction_test.go`
(`TestProcessWagerTransaction_ExpirePendingReference_*`).

## Achados prioritários (revisão estática original — não revisitados nesta rodada, salvo indicação)

### P1 — [Corrigido antes desta rodada, confirmado em 17/09] Consulta de transação não exigia papel autorizado

Achado original: `GET /wagering/transactions/{id}` e `GET /wallets/{id}` aceitavam qualquer identidade autenticada, e a checagem de dono ignorava transações com `ProviderID` vazio (OPENING). **Estado confirmado nesta rodada de QA**: `handleGetWagerTransaction` (`internal/infra/http/wagering.go`) agora exige `identity.HasRole("internal")` OU (`identity.HasRole("provider")` E `tx.ProviderID() == identity.ClientID`) — bloqueando explicitamente OPENING (`ProviderID() == ""`) e identidades sem papel. `GET /wallets/{id}` exige `requireInternal`. Não foram encontradas regressões nesta rodada; testes existentes em `internal/infra/http/wallets_test.go` e `internal/infra/http/wagering_test.go` cobrem os casos.

### P1 — [Corrigido antes desta rodada, confirmado em 17/09] Mesma chave com operações diferentes pode ser confirmada duas vezes

Achado original: a migration 0005 cria um índice comum, não UNIQUE, sobre `idempotency_key`, e a constraint da migration 0002 protege apenas `(provider_id, external_transaction_id)` — duas requisições concorrentes com a mesma chave e IDs externos diferentes poderiam ambas passar pelo SELECT sem encontrar registro e confirmar duas operações. **Estado confirmado nesta rodada**: a migration 0006 (`wager_tx_idempotency_key_unique`) já criava um índice `UNIQUE` sobre `idempotency_key` (parcial, `WHERE idempotency_key IS NOT NULL`), e `WagerTransactionRepository.Create` (`internal/infra/postgres/wagertx_repository.go`) já mapeia a violação para `ports.ErrAlreadyExists`, que `ProcessWagerTransaction.Execute` usa para reexecutar a tentativa (`internal/application/usecase/process_wager_transaction.go`), encontrando o registro concorrente via `resolveExisting` e retornando `ErrIdempotencyConflict` quando o payload diverge. A lacuna real era apenas de cobertura de teste — o cenário concorrente com `idempotency_key` igual e `external_transaction_id` diferente não era exercitado. Adicionado `TestProcessWagerTransaction_SameIdempotencyKeyDifferentExternalID_ConcurrentSingleDebit` em `internal/infra/postgres/process_wager_transaction_integration_test.go` (20 goroutines, mesma chave, IDs externos distintos): 1 PROCESSED e 19 `ErrIdempotencyConflict`, saldo final e ledger consistentes com um único débito. `go test -race` verde.

### P2 — [Corrigido antes desta rodada, confirmado em 17/09] Replay de rejeição não preservava o saldo originalmente retornado

Achado original: `reject` retornava o saldo da carteira sem persisti-lo como resultado financeiro; `replayOutput` consultaria a carteira atual para REJECTED, divergindo do saldo original após operações subsequentes. **Estado confirmado nesta rodada**: `WagerTransaction.MarkRejectedWithResult` já persiste `financialResult` na rejeição, `reject()` (`internal/application/usecase/process_wager_transaction.go`) já chama esse método, e `WagerTransactionRepository.Update` já grava `financial_result_minor_units/currency`. `replayOutput` usa esse valor persistido também para REJECTED. Já havia teste cobrindo isso (`TestProcessWagerTransaction_RejectedReplay_ReturnsOriginalBalance`).

### P2 — [Corrigido antes desta rodada, confirmado em 17/09] Money aceitava valores de domínio inválidos e tinha limite não coberto

Achado original: `Add`/`Compare`/`MarshalJSON` não rejeitavam moeda vazia; `normalizeCurrency` não validava ISO 4217; `DecimalString` transbordava em `math.MinInt64`. **Estado confirmado nesta rodada**: `normalizeCurrency` valida contra tabela `iso4217Currencies`; `Add`/`Compare` rejeitam moeda vazia com `ErrEmptyCurrency`; `Negate`/`DecimalString` tratam `math.MinInt64` explicitamente (retornando `ErrOverflow` ou usando aritmética sem overflow). Testes em `internal/domain/money/money_test.go`.

### P2 — [Corrigido antes desta rodada, confirmado em 17/09] Contrato WIN com referência divergia do domínio e do banco

Achado original: o README §7 permite WIN referenciar BET da mesma rodada, mas `NewExternalTransaction` rejeitava referência para WIN e a constraint `wager_tx_reference_by_kind` só permitia REFUND/ROLLBACK. **Estado confirmado nesta rodada**: `rulesFor(KindWin)` já marca `referenceAllowed=true` (referência opcional, não obrigatória) e a migration 0007 (`wager_tx_win_reference`) já relaxou a constraint para aceitar `kind = 'WIN'` com referência. `validateReference` já valida que a referência de um WIN é um BET compatível (mesmo provider/player/wallet/round/valor). A lacuna real era de cobertura de teste: adicionados `TestProcessWagerTransaction_WinReferencingBet_SameRound_CreditsWallet` (fake, `internal/application/usecase`) e `TestProcessWagerTransaction_WinReferencingBet_PersistsAgainstRealSchema` (Postgres real, `internal/infra/postgres`), confirmando que a constraint do banco de fato aceita a linha.

### P2 — [Corrigido antes desta rodada, confirmado em 17/09] Falhas de infraestrutura retornavam erro de entrada

Achado original: o default de `writeProcessWagerTransactionError` retornava 400 e `err.Error()`, confundindo indisponibilidade transitória com requisição inválida. **Estado confirmado nesta rodada**: `writeProcessWagerTransactionError` (`internal/infra/http/wagering.go`) já classifica `context.Canceled`/`context.DeadlineExceeded` como 503 (`temporarily_unavailable`) e usa 500 genérico (`internal_error`, sem `err.Error()`) como default, sem expor detalhes internos.

### P2 — [Corrigido antes desta rodada, confirmado em 17/09] JWT e readiness precisam de critérios operacionais explícitos

Achado original: JWT não exigia `aud`/`exp`; readiness não verificava dependências. **Estado confirmado nesta rodada de QA**: `internal/infra/idp/middleware.go` exige `exp` e valida `aud` contra `OIDC_AUDIENCE`; `internal/infra/idp/jwks.go` tem throttle e refresh periódico; `GET /health/ready` (`internal/infra/http/health.go`) executa checkers reais de Postgres (`postgres/readiness.go`) e SQS (`sqs/readiness.go`). Validado com Keycloak real via `docker compose` nesta rodada (token real com `aud=wager-ledger-api`, fluxo HTTP completo).

## Contradições documentais encontradas e corrigidas nesta revisão

A tabela registra o estado anterior à correção de ARCHITECTURE e DECISIONS; os defeitos de código permanecem pendentes.

| Local | Afirmação desatualizada ou incorreta | Estado encontrado |
| --- | --- | --- |
| ARCHITECTURE §3 | Caso de uso/retry e teste 100/80/80 ainda não existem | `ProcessWagerTransaction` e teste PostgreSQL de disputa existem |
| ARCHITECTURE §4 | Não há código que grave/valide hash | SHA-256 canônico calculado e persistido pelo processamento |
| ARCHITECTURE §6 | Nenhum código usa outbox; OpenWallet é o único caso de uso | Dois casos de uso gravam eventos por `OutboxRepository` |
| ARCHITECTURE §7 | Não existem endpoints nem isolamento por provedor | POST e GET por ID existem; isolamento tem a falha descrita acima |
| ARCHITECTURE, tabela de status | “idempotência completa” e autorização “concluída” | Concorrência por chave e consulta sem papel tornam a classificação excessiva |
| ARCHITECTURE, limitação 7 | Compose não inclui API | Serviço api, migrations e bootstrap já existem |
| ARCHITECTURE, limitação 8 | Goroutines comprovam a garantia da §8 | §8 exige explicitamente três processos independentes; a prova atual é parcial |
| ADR-002 | Contexto PIX/pagamentos e caso de uso inexistente | Domínio é apostas; ADR-007 e código já descrevem processamento; UNIQUE da decisão original não foi implementado |
| ADR-004, limitação | Retry ainda não existe | Retry de até cinco transações completas implementado |
| ADR-004, razão | “não há lock algum retido entre transações” | Controle otimista evita lock explícito prolongado, mas UPDATE adquire locks no PostgreSQL até concluir a transação |
| ADR-005, limitação | Endpoints ausentes e bootstrap manual | Endpoints e serviço keycloak-bootstrap presentes no Compose |
| ADR-006, limitação | Outbox/repositório e evidência ainda não existem | Repositório, uso transacional e teste de persistência existem; inbox continua ausente |
| ADR-007 e final do ADR-008 | Frase sobre novos fakes interrompida e fragmento no fim do arquivo | Reunir a frase na limitação do ADR-007 |

## Testes e limites da evidência

### Rodada de QA de 17/09/2026 (atual)

Executado com sucesso (Postgres e LocalStack reais via `docker compose up -d postgres localstack`, migrations em dia):

```sh
go build ./...
go vet ./...
go test ./... -count=1
go test -race -count=1 ./...
```

Todos os pacotes passaram, incluindo os novos testes de integração
(`internal/infra/sqs/consumer_integration_test.go`,
`internal/infra/outbox/publisher_integration_test.go`,
`internal/infra/postgres/inbox_repository_test.go`) e os reforçados
(`internal/infra/postgres/process_wager_transaction_integration_test.go`,
`internal/application/usecase/process_wager_transaction_test.go`). Além
disso, validação manual com a stack completa (`docker compose up --build`:
Postgres, LocalStack, Keycloak, api) cobrindo autenticação real, múltiplos
processos de sistema operacional (`curl` concorrente), reentrega real de
mensagem SQS, e kill/restart do container `api` — comandos e saídas
completos em `docs/QA_LOG.md`. Ambiente Docker parado (`docker compose down`)
ao final da rodada.

**Não coberto nesta rodada** (limitação aceita, não bloqueante para o
escopo local): orquestração de matar/reiniciar processos e containers a
partir de uma suíte `go test` executável em CI — a validação foi manual e
está documentada, mas não roda automaticamente a cada `go test ./...`.
Reproduzir exigiria um test harness que sobe/derruba containers Docker via
API do Docker a partir do próprio teste Go, o que é factível mas fica fora
do escopo desta rodada.

### Rodada estática original de 17/09/2026 (mantida para histórico)

Executado nesta auditoria, com sucesso:

```sh
go test ./internal/domain/... ./internal/application/... ./internal/config/... ./internal/observability/... ./internal/infra/idgen/...
```

Os testes PostgreSQL usam `DATABASE_URL` ou o banco local padrão e exigem schema previamente migrado. Os testes HTTP também usam PostgreSQL real, mas geram sua própria chave RSA e servem JWKS por `httptest`: isso verifica o middleware, não a integração real exigida com Keycloak. Os testes concorrentes existentes usam goroutines em um processo. Não foram executados nesta auditoria os testes PostgreSQL/HTTP, `-race`, containers ou cenários de falha distribuída. As execuções históricas citadas pelos ADRs não são resultado de uma nova execução desta revisão.

## Sequência recomendada de trabalho

Itens 1–4 da lista original foram concluídos entre a auditoria estática e
esta rodada de QA (autorização de consulta, Money, referências/reversões,
inbox/SQS/outbox, consultas faltantes, reconciliação, readiness, métricas —
ver matriz acima). Restante:

Todos os achados P1/P2 conhecidos foram confirmados como já corrigidos no
código (ver seções acima), com cobertura de teste completada nesta rodada
onde faltava (idempotency_key concorrente, WIN com referência). Resta:

1. Automatizar em CI a validação com processos de SO reais e restart de
   container (hoje manual, documentada em `docs/QA_LOG.md`).
2. Avaliar tracing distribuído (OpenTelemetry) como diferencial opcional.

Os agentes de commits e deploy devem registrar o escopo efetivamente entregue e os resultados de validação; não devem usar build/testes unitários como substituto das garantias financeiras e distribuídas ainda pendentes.
