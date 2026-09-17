# Registro de alterações

Este arquivo é o histórico operacional dos commits criados após a adoção
desta política. Cada commit deve incluir sua própria entrada, preparada antes
do commit e revisada junto ao diff.

## Formato

```md
## AAAA-MM-DD — categoria — tipo(escopo): resumo

- Efeito: resultado entregue ou corrigido.
- Arquivos: `caminho/alterado`, `outro/caminho`.
```

Categorias permitidas: `feature`, `bugfix` e `manutenção`. Não registre
segredos, tokens, conteúdos de arquivos `.env` ou dados de clientes. O hash
não entra na própria entrada porque ele só é calculado depois que o conteúdo
do commit é definido; a associação é feita pelo commit que versiona a entrada.

## 2026-09-17 — feature — feat(sqs): consome mensagens com inbox transacional

- Efeito: adiciona consumidor SQS, deduplicação persistente por inbox e teste do envelope de entrada.
- Arquivos: `internal/infra/sqs/`, `internal/infra/postgres/inbox_repository.go`, `internal/application/usecase/process_wager_transaction.go` e configurações associadas.

## 2026-09-17 — feature — feat(references): retoma pendências após a referência chegar

- Efeito: adiciona worker Fx com shutdown gracioso e lock de linha para concluir transações `PENDING_REFERENCE` de forma segura.
- Arquivos: `internal/application/usecase/reference_retry_worker.go`, `internal/application/usecase/process_wager_transaction.go`, `internal/application/ports/ports.go`, `internal/infra/postgres/wagertx_repository.go` e testes associados.

## 2026-09-17 — feature — feat(reversals): processa REFUND, ROLLBACK e pendências de referência

- Efeito: adiciona validação de referência, crédito/débito inverso, proteção de reversão duplicada no banco e persistência de operações que aguardam a referência.
- Arquivos: `internal/application/usecase/process_wager_transaction.go`, `internal/domain/wagertx/wagertx.go`, `internal/infra/postgres/wagertx_repository.go`, `migrations/0007_wagertx_win_reference*.sql`, `migrations/0008_wagertx_reversal_unique*.sql` e testes associados.

## 2026-09-17 — bugfix — fix(wagering): fortalece idempotência e autorização

- Efeito: torna a chave de idempotência única no banco, preserva o saldo de respostas rejeitadas em replays e restringe leituras de carteiras e transações às identidades autorizadas.
- Arquivos: `migrations/0006_wagertx_idempotency_key_unique.up.sql`, `migrations/0006_wagertx_idempotency_key_unique.down.sql`, `internal/application/usecase/process_wager_transaction.go`, `internal/domain/wagertx/wagertx.go`, `internal/infra/http/wagering.go`, `internal/infra/http/wallets.go` e testes associados.

## 2026-09-17 — manutenção — docs(commit-log): institui registro obrigatório de commits

- Efeito: cria o log versionado e exige sua atualização pelo agente de commits.
- Arquivos: `.claude/agents/commit-manager.md`, `docs/COMMIT_LOG.md`.

## 2026-09-17 — feature — feat(money): valida moeda ISO 4217 e corrige conversão de valores extremos

- Efeito: rejeita códigos de moeda fora da lista ISO 4217, bloqueia operações sobre o valor zero-value de Money e corrige overflow na conversão decimal de math.MinInt64.
- Arquivos: `internal/domain/money/money.go`, `internal/domain/money/money_test.go`.

## 2026-09-17 — feature — feat(observability): adiciona métricas Prometheus e logging estruturado de requisições

- Efeito: expõe contadores/histogramas Prometheus (retries, expirações, replays de idempotência, outbox) e adiciona middleware de logging estruturado por requisição HTTP.
- Arquivos: `internal/observability/metrics.go`, `internal/observability/module.go`, `internal/infra/http/logging.go`, `internal/infra/http/logging_test.go`, `internal/infra/http/server.go`, `go.mod`, `go.sum`.

## 2026-09-17 — feature — feat(health): adiciona readiness de Postgres e SQS e expõe métricas

- Efeito: `/health/ready` passa a verificar conectividade real com Postgres e SQS com timeout, e `/metrics` expõe o registro Prometheus para scraping.
- Arquivos: `internal/infra/http/health.go`, `internal/infra/http/health_test.go`, `internal/infra/http/module.go`, `internal/infra/postgres/module.go`, `internal/infra/postgres/readiness.go`, `internal/infra/sqs/readiness.go`.

## 2026-09-17 — feature — feat(wagering): expira referências pendentes após TTL e consulta por provedor/externalId

- Efeito: worker de retomada passa a expirar transações PENDING_REFERENCE além do TTL configurável, emite evento WagerTransactionExpired, instrumenta métricas de retries/replays/expiração e expõe GET por providerId+externalId.
- Arquivos: `internal/domain/event/event.go`, `internal/application/usecase/process_wager_transaction.go`, `internal/application/usecase/process_wager_transaction_test.go`, `internal/application/usecase/reference_retry_worker.go`, `internal/application/usecase/open_wallet_test.go`, `internal/application/ports/ports.go`, `internal/config/config.go`, `internal/infra/postgres/wagertx_repository.go`, `internal/infra/http/wagering.go`.

## 2026-09-17 — feature — feat(sqs): fortalece deduplicação de inbox e instrumenta o consumo de mensagens

- Efeito: usa ON CONFLICT DO NOTHING para deduplicar mensagens de forma idempotente sob concorrência e registra métricas de duplicatas, falhas de entrega e latência de processamento do consumidor SQS.
- Arquivos: `internal/infra/postgres/inbox_repository.go`, `internal/infra/postgres/inbox_repository_test.go`, `internal/infra/sqs/consumer.go`, `internal/infra/sqs/consumer_integration_test.go`.

## 2026-09-17 — feature — feat(outbox): publica eventos pendentes com worker dedicado e reentrega com backoff

- Efeito: adiciona worker que reivindica eventos da outbox com trava por linha (FOR UPDATE SKIP LOCKED), publica na fila SQS de eventos e reentrega com backoff exponencial em caso de falha, com fila `wager-events` provisionada no LocalStack.
- Arquivos: `internal/infra/outbox/module.go`, `internal/infra/outbox/publisher.go`, `internal/infra/outbox/publisher_integration_test.go`, `internal/infra/postgres/outbox_repository.go`, `internal/infra/postgres/outbox_publisher_repository_test.go`, `migrations/0009_outbox_events_version.up.sql`, `migrations/0009_outbox_events_version.down.sql`, `cmd/api/main.go`, `scripts/localstack-init-sqs.sh`, `internal/application/ports/ports.go`, `internal/config/config.go`, `internal/application/usecase/open_wallet_test.go`.
