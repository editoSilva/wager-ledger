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

## 2026-09-17 — bugfix — fix(wagering): fortalece idempotência e autorização

- Efeito: torna a chave de idempotência única no banco, preserva o saldo de respostas rejeitadas em replays e restringe leituras de carteiras e transações às identidades autorizadas.
- Arquivos: `migrations/0006_wagertx_idempotency_key_unique.up.sql`, `migrations/0006_wagertx_idempotency_key_unique.down.sql`, `internal/application/usecase/process_wager_transaction.go`, `internal/domain/wagertx/wagertx.go`, `internal/infra/http/wagering.go`, `internal/infra/http/wallets.go` e testes associados.

## 2026-09-17 — manutenção — docs(commit-log): institui registro obrigatório de commits

- Efeito: cria o log versionado e exige sua atualização pelo agente de commits.
- Arquivos: `.claude/agents/commit-manager.md`, `docs/COMMIT_LOG.md`.
