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
