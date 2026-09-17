# Auditoria de implementação e documentação

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
| BET, WIN, LOSS (§7) | Parcial | `process_wager_transaction.go` | WIN com referência é rejeitado pelo domínio e pelo schema |
| REFUND, ROLLBACK e referências (§7) | **[QA 17/09] Implementado**, incluindo `PENDING_REFERENCE` e expiração por TTL | `process_wager_transaction.go`, `reference_retry_worker.go` | — |
| Pendências duráveis e FAILED (§6.3, §7) | **[QA 17/09] Implementado**: worker de retomada + TTL de expiração (`REFERENCE_PENDING_TTL`, lacuna real corrigida nesta rodada) | `wagertx.go`, `reference_retry_worker.go`, `process_wager_transaction.go` | — |
| Ledger append-only (§6.4) | Implementado no fluxo e protegido contra UPDATE/DELETE; **[QA 17/09]** reconciliação exposta e testada | migration 0003, `ledger.go`, `reconcile_wallet.go` | — |
| Hash persistente e replay (§9) | Parcial | `idempotency.go`, `resolveExisting`, `replayOutput` | Falta exclusão concorrente por chave; replay de rejeição lê saldo atual (não revisitado nesta rodada) |
| OAuth/OIDC e autorização (§2) | **[QA 17/09] Implementado**: `aud`/`exp` obrigatórios, validado com Keycloak real | `idp/middleware.go`, `jwks.go`, handlers | Integração real com Keycloak validada manualmente, não automatizada em CI |
| Consultas e reconciliação (§9) | **[QA 17/09] Implementado**: ledger com cursor, consulta por provedor/ID externo, reconciliação | `wallets.go`, `wagering.go`, `reconcile_wallet.go` | — |
| HTTP e classificação de falhas (§9) | Parcial (não revisitado nesta rodada) | `writeProcessWagerTransactionError` | Erros inesperados de infraestrutura ainda podem virar 400; fora do escopo desta rodada de QA |
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

### P1 — Mesma chave com operações diferentes pode ser confirmada duas vezes

A migration 0005 cria um índice comum, não UNIQUE, sobre `idempotency_key`. A constraint da migration 0002 protege apenas `(provider_id, external_transaction_id)`. Duas requisições concorrentes com a mesma chave, IDs externos diferentes e carteiras diferentes podem ambas executar os SELECTs sem encontrar registro e confirmar duas operações. O retry por versão da carteira não coordena carteiras diferentes. O teste de 50 requisições da mesma operação não cobre esse caso. Definir o escopo da chave (global ou por provedor), torná-lo único no banco e alinhar lookup, tratamento de conflito e teste concorrente ao escopo escolhido.

### P2 — Replay de rejeição não preserva o saldo originalmente retornado

`reject` retorna o saldo da carteira, mas não o persiste como resultado financeiro. `replayOutput` usa resultado persistido apenas para PROCESSED; para REJECTED consulta a carteira atual. Uma aposta rejeitada, seguida por um crédito, retorna saldo diferente no replay. O README exige resultado persistido para operações concluídas e define REJECTED como terminal. Persistir o resultado retornado também para rejeições ou resolver explicitamente essa divergência contratual.

### P2 — Money ainda aceita valores de domínio inválidos e tem limite não coberto

`Money{}.Add(Money{})`, `Compare` e `MarshalJSON` não rejeitam moeda vazia. `normalizeCurrency` valida somente três letras, aceitando códigos como `ZZZ`, sem comprovar ISO 4217. `FromMinorUnits` aceita `math.MinInt64`, porém `DecimalString` executa `abs = -abs`, que transborda nesse limite e produz representação incorreta. Corrigir validação de valores não inicializados, política de moedas suportadas e serialização do limite mínimo; incluir testes específicos. A ausência de float está correta e deve ser mantida.

### P2 — Contrato WIN com referência diverge do domínio e do banco

O README §7 permite WIN referenciar BET da mesma rodada. `NewExternalTransaction` rejeita referência para tipos que não a exigem, incluindo WIN; a constraint `wager_tx_reference_by_kind` também só permite REFUND/ROLLBACK. Implementar a referência opcional e sua validação ou explicitar a pendência, sem chamar todo o fluxo WIN de completo.

### P2 — Falhas de infraestrutura retornam erro de entrada

O default de `writeProcessWagerTransactionError` retorna 400 e `err.Error()`. Falhas de conexão, timeout ou commit não classificadas caem nesse caminho, confundindo indisponibilidade transitória com requisição inválida e podendo expor detalhes internos. Classificar erros conhecidos, retornar 503 para indisponibilidade transitória e 500 para falhas inesperadas, com mensagem pública adequada.

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

1. Automatizar em CI a validação com processos de SO reais e restart de
   container (hoje manual, documentada em `docs/QA_LOG.md`).
2. Resolver a exclusão concorrente por `idempotency_key` quando usada por
   operações diferentes (unicidade não implementada no schema — ver
   ARCHITECTURE.md, limitação 3).
3. Classificar erros HTTP de infraestrutura (500/503) separadamente de erro
   de entrada (400) — não revisitado nesta rodada.
4. Avaliar tracing distribuído (OpenTelemetry) como diferencial opcional.

Os agentes de commits e deploy devem registrar o escopo efetivamente entregue e os resultados de validação; não devem usar build/testes unitários como substituto das garantias financeiras e distribuídas ainda pendentes.
