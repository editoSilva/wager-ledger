# Instruções do projeto

Leia README.md (requisitos e execução), ARCHITECTURE.md (implementação),
DECISIONS.md (decisões) e docs/IMPLEMENTATION_AUDIT.md (lacunas verificadas).
Não trate um requisito do desafio como funcionalidade pronta.

## Responsabilidades


As definições persistentes seguem o padrão já existente em `.claude/agents/`:

- `commit-manager.md`: branches, staging e commits, conforme docs/GIT_WORKFLOW.md.
- `deploy-manager.md`: GitHub Actions e VPS Docker, conforme docs/DEPLOYMENT.md.
- `go-reviewer.md` e `security-reviewer.md`: revisão especializada já existente.

Em ferramentas que não descobrem agentes Claude automaticamente, leia a definição
correspondente e execute esse papel; não presuma que um daemon está rodando.
Delegação de revisão, commits e deploy está autorizada quando útil à tarefa.
Evite dois agentes mutando o índice Git ou o ambiente de deploy ao mesmo tempo.

## Entrega de alterações

Por solicitação do mantenedor, conclua alterações autorizadas com commits locais
coerentes e informe hashes, testes e pendências, salvo instrução contrária na tarefa.
Features usam `feat`; bugfixes e hotfixes usam `fix`. Siga docs/GIT_WORKFLOW.md.
Essa convenção não autoriza push, merge ou publicação de produção por si só.
Nunca inclua `.env`, chaves ou artefatos temporários nos commits.

Para Go, execute `gofmt`, `go vet ./...` e `go test -race ./...` conforme o escopo.
A suíte completa requer PostgreSQL migrado; no ambiente local pronto use
`docker compose exec -T api go test -race ./...`.
Mudanças em CI/CD devem passar por `actionlint`, `bash -n` e validação do Compose.
Registre limitações reais; não atribua sucesso a testes que não executou.
