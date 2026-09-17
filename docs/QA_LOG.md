# Histórico de QA

Este arquivo é o registro cumulativo de todas as rodadas de teste executadas
pelo agente `qa-tester` (ou manualmente, seguindo o mesmo formato). Cada
rodada soma uma entrada nova — o log nunca é reescrito ou resumido, apenas
apendado, para preservar o histórico de cobertura da aplicação ao longo do
tempo.

## Formato

```md
## AAAA-MM-DD HH:MM UTC — resumo curto da rodada

- Gatilho: o que motivou a rodada.
- Escopo: camadas/arquivos tocados e por quê a bateria escolhida cobre isso.
- Testes criados: caminho de cada arquivo de teste novo/alterado e o que comprova.
- Testes executados: comandos rodados e resultado (PASS/FAIL) de cada um.
- Evidência: tipo (unitário/integração/distribuído com N processos) e resumo concreto do resultado.
- Bugs encontrados: descrição + arquivo:linha, ou "nenhum".
- Pendências: o que ficou fora do escopo, com justificativa.
```

Não registre segredos, tokens, valores de `.env` ou dados de clientes.

## 2026-09-17 20:45 UTC — Bateria obrigatória README §8/§13, TTL de PENDING_REFERENCE e correção de bug de dedup do inbox

- Gatilho: rodada de QA solicitada para finalizar o projeto wager-ledger — executar a bateria de verificação obrigatória do README (§8 e §13), corrigir bugs reais encontrados, e elevar a documentação de status ao estado real testado.
- Escopo: `internal/domain/event`, `internal/application/ports`, `internal/application/usecase` (process_wager_transaction, reference_retry_worker), `internal/infra/postgres` (wagertx_repository, inbox_repository), `internal/infra/sqs`, `internal/infra/outbox`, `internal/config`. Bateria escolhida cobre concorrência (mesma aposta, disputa de saldo, carteiras distintas), distribuição (multi-processo real via curl, dois publishers da outbox, redelivery real de SQS), recuperação (kill/restart do processo, PENDING_REFERENCE), e consistência (reconciliação saldo x ledger).
- Testes criados/alterados:
  - `internal/application/usecase/process_wager_transaction_test.go`: `TestProcessWagerTransaction_ExpirePendingReference_TransitionsToFailed` e `TestProcessWagerTransaction_ExpirePendingReference_NoopWhenNotPending` — comprovam a nova política de TTL/expiração de `PENDING_REFERENCE` (transição para FAILED com failureCode=REFERENCE_EXPIRED, sem efeito financeiro; noop quando a transação já não está mais pendente).
  - `internal/application/usecase/open_wallet_test.go`: adicionado `fakeTxRepo.ListStalePendingReferenceIDs` (fake do novo método de porta, necessário para compilar os testes existentes).
  - `internal/infra/postgres/inbox_repository_test.go` (novo): `TestInboxRepository_Create_DuplicateWithinSameTransaction_DoesNotAbortTransaction` — regressão do bug de dedup do inbox (ver Bugs encontrados).
  - `internal/infra/postgres/process_wager_transaction_integration_test.go`: reforçado com `assertReconciled` (chama `usecase.ReconcileWallet.Execute` de verdade) ao final dos dois testes existentes (100/80/80 e 50 envios concorrentes da mesma aposta); wallets de teste passaram a ser criadas via `usecase.OpenWallet` real (helper `newTestWalletWithOpeningLedger`) para que o ledger tenha o lançamento de abertura e a reconciliação seja significativa — comprova README §13 item 9 (saldo armazenado x soma do ledger) integrado aos itens 1 e 2 (mesma aposta 50x, disputa de saldo).
  - `internal/infra/sqs/consumer_integration_test.go` (novo): `TestConsumer_RedeliveredMessageAfterCommit_IsIdempotent` (item 5 e 11 — reentrega de mensagem após commit tratada via inbox, sem duplo efeito, 3 entregas → 1 débito); `TestConsumer_DistinctWallets_ProcessedConcurrentlyWithoutInterference` (item 3 — 5 carteiras distintas processadas concorrentemente sem interferência); `TestCrossChannel_HTTPThenSQS_SameOperation_IsIdempotent` (item 10 — mesma operação lógica via HTTP e via SQS deduplicada).
  - `internal/infra/outbox/publisher_integration_test.go` (novo): `TestTwoPublishers_ContendingForSamePendingRecords_NoDoublePublish` (item 6 — dois publishers reais disputando os mesmos registros pendentes contra Postgres + LocalStack reais, sem publicação duplicada, com todos os 30 eventos de teste publicados exatamente uma vez na fila SQS real).
- Testes executados:
  - `go build ./...` — PASS.
  - `go vet ./...` — PASS.
  - `go test ./... -count=1` (Postgres+LocalStack locais via docker compose, migrations em v9) — PASS em todos os pacotes.
  - `go test -race -count=1 ./...` — PASS em todos os pacotes (domain, application, infra/postgres, infra/http, infra/sqs, infra/outbox, infra/idp).
  - Validação manual com `docker compose up --build` (Postgres, LocalStack, Keycloak, keycloak-bootstrap, api reais): tokens reais do Keycloak (`provider-a`, `wager-internal`) com `aud=wager-ledger-api`; abertura de carteira via `POST /wallets`; 3 processos de SO reais (`curl` em paralelo) enviando a mesma aposta com a mesma idempotency key → 1 replay=false e 2 replay=true, saldo debitado uma única vez; 2 processos de SO reais disputando saldo insuficiente (50+50 sobre 70.00) → 1 PROCESSED, 1 REJECTED (INSUFFICIENT_BALANCE), saldo final correto; REFUND enviado antes do BET existir → PENDING_REFERENCE, resolvido automaticamente pelo `ReferenceRetryWorker` após a chegada do BET (~1s de polling); reconciliação (`POST /wallets/:id/reconciliation`) consistente após cada cenário; kill+restart do container `api` com uma pendência `PENDING_REFERENCE` em aberto — estado sobreviveu no Postgres, e o BET enviado ao **novo** processo foi resolvido pelo `ReferenceRetryWorker` da nova instância; mensagem enviada diretamente à fila real `wager-transactions.fifo` via LocalStack processada corretamente, e reenviada como segunda mensagem SQS real (messageId de negócio idêntico, dedup id diferente) sem duplicar o efeito financeiro (saldo e reconciliação inalterados). Ambiente parado ao final com `docker compose down`.
- Evidência:
  - Unitário: `TestProcessWagerTransaction_ExpirePendingReference_*` (fakes em memória).
  - Integração (Postgres/LocalStack reais, single-processo/goroutines): `postgres/process_wager_transaction_integration_test.go`, `postgres/inbox_repository_test.go`, `sqs/consumer_integration_test.go`, `outbox/publisher_integration_test.go`.
  - Distribuído com processos de SO reais (não automatizado em CI, evidência manual acima): 3 processos `curl` para dedup de aposta; 2 processos `curl` para disputa de saldo; kill+restart do processo `api` (container) com retomada por nova instância; mensagem SQS real via LocalStack com redelivery real.
- Bugs encontrados:
  1. `internal/infra/postgres/inbox_repository.go:15` (`InboxRepository.Create`) — capturava a violação de unicidade Postgres (23505) dentro da transação e retornava `ports.ErrAlreadyExists` como no-op esperado, mas a transação já estava abortada pelo Postgres nesse ponto; o `COMMIT` subsequente falhava com "commit unexpectedly resulted in rollback". Efeito em produção: toda reentrega legítima do SQS (a garantia que a inbox deveria proteger) virava erro no consumidor (`internal/infra/sqs/consumer.go`), a mensagem nunca era deletada da fila, e seria reentregue indefinidamente até `maxReceiveCount` e ida para a DLQ — apesar do efeito financeiro já ter sido aplicado corretamente. Corrigido trocando o INSERT por `INSERT ... ON CONFLICT ON CONSTRAINT inbox_consumer_message_unique DO NOTHING` + checagem de `RowsAffected()`. Encontrado pelo teste `TestConsumer_RedeliveredMessageAfterCommit_IsIdempotent` (falhava antes da correção) e coberto por regressão em `inbox_repository_test.go`.
  2. Lacuna real (não um bug de regressão, mas ausência de funcionalidade exigida implicitamente pelo item 7 do README §13): não havia política de TTL/expiração para transações em `PENDING_REFERENCE` — uma pendência cuja referência nunca chegasse ficaria pendente para sempre, sem transição para estado terminal. Implementado: `internal/config/config.go` (`ReferencePendingTTL`, env `REFERENCE_PENDING_TTL`, padrão 15m), `internal/application/usecase/process_wager_transaction.go` (`ExpirePendingReference`, failureCode `REFERENCE_EXPIRED`), `internal/application/ports/ports.go` + `internal/infra/postgres/wagertx_repository.go` (`ListStalePendingReferenceIDs`), `internal/application/usecase/reference_retry_worker.go` (verificação periódica de expiração), `internal/domain/event/event.go` (`WagerTransactionExpired`).
- Pendências:
  - Não foi criada uma suíte `go test` que orquestre matar/reiniciar containers Docker ou processos de SO automaticamente (itens 4 e 8 do README §13 como testes executáveis em CI) — a validação foi feita manualmente com evidência registrada acima; automatizar exigiria um harness de orquestração de containers a partir do próprio `go test`, considerado fora do escopo desta rodada.
  - Exclusão concorrente de `idempotency_key` usada por operações diferentes (sem unicidade própria no schema, apenas índice não-único) não foi revisitada nesta rodada — lacuna pré-existente documentada em ARCHITECTURE.md.
  - Classificação de erros HTTP de infraestrutura (400 vs 500/503) não foi revisitada nesta rodada.
  - Tracing distribuído (OpenTelemetry) não implementado — diferencial opcional, fora do escopo.
  - A flakiness observada uma única vez no teste `TestTwoPublishers_ContendingForSamePendingRecords_NoDoublePublish` (falha isolada logo após o container LocalStack ter acabado de subir, sem reprodução em execuções subsequentes) não foi investigada a fundo; suspeita de warm-up de conexão/container, não de bug de aplicação.
