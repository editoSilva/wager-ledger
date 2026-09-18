# Wager Ledger

Serviço em Go, composto com **Uber Fx**, que processa operações financeiras de provedores de jogos (`BET`/`WIN`/`LOSS`/`REFUND`/`ROLLBACK`) via HTTP e SQS, com idempotência persistente, ledger append-only e recuperação de falhas em ambiente distribuído. Solução para o desafio descrito [mais abaixo](#desafio-backend--processamento-distribuído-de-apostas-em-go).

> Instruções de execução e evidências de conformidade abaixo. Decisões de arquitetura detalhadas em [`ARCHITECTURE.md`](ARCHITECTURE.md) e [`DECISIONS.md`](DECISIONS.md).

## Índice

- [Pré-requisitos](#pré-requisitos)
- [Quickstart](#quickstart)
- [Variáveis de ambiente](#variáveis-de-ambiente)
- [Identidades de teste (Keycloak)](#identidades-de-teste-keycloak)
- [Exemplos de chamadas](#exemplos-de-chamadas)
- [Testes](#testes)
- [Cenários obrigatórios de concorrência e recuperação](#cenários-obrigatórios-de-concorrência-e-recuperação)
- [Observabilidade](#observabilidade)
- [Estrutura do projeto](#estrutura-do-projeto)
- [Fluxo de desenvolvimento assistido por agentes](#fluxo-de-desenvolvimento-assistido-por-agentes)
- [Decisões de arquitetura e limitações conhecidas](#decisões-de-arquitetura-e-limitações-conhecidas)

## Pré-requisitos

- Docker e Docker Compose v2
- Go 1.22+ (só para rodar testes/lint fora de container)
- `curl` e `jq` (exemplos abaixo usam os dois)
- `awscli`/`awslocal` opcional, para inspecionar as filas SQS manualmente

## Quickstart

```sh
git clone <este-repositório> && cd wager-ledger
cp .env.example .env        # valores locais de exemplo, sem segredos reais
docker compose up --build
```

Isso sobe, na ordem correta de dependência (`depends_on` + healthchecks no `docker-compose.yml`):

1. `postgres` — banco, com healthcheck `pg_isready`
2. `migrate` — aplica as migrations versionadas em [`migrations/`](migrations) e encerra
3. `keycloak` + `keycloak-bootstrap` — sobe o IdP e provisiona automaticamente o realm `wager-ledger`, as roles `provider`/`internal` e os clients de teste (ver [scripts/keycloak-bootstrap.sh](scripts/keycloak-bootstrap.sh))
4. `localstack` — cria as filas `wager-transactions.fifo` / `wager-events.fifo` e suas DLQs (ver [scripts/localstack-init-sqs.sh](scripts/localstack-init-sqs.sh))
5. `api` — sobe em `http://localhost:8080` só depois que as etapas 1–4 estiverem saudáveis/concluídas

Confirme que subiu:

```sh
curl -s http://localhost:8080/health/live | jq
curl -s http://localhost:8080/health/ready | jq   # valida Postgres e SQS de verdade, não só o processo
```

Para derrubar tudo (incluindo dados do Postgres/LocalStack):

```sh
docker compose down -v
```

## Variáveis de ambiente

Descritas em [`.env.example`](.env.example). O `docker-compose.yml` já injeta os valores equivalentes para o serviço `api` local; o `.env` só é necessário para rodar a API fora de container.

| Variável | Descrição |
| --- | --- |
| `HTTP_PORT` | porta HTTP da API (padrão `8080`) |
| `APP_ENV` | `development` \| `production` |
| `SHUTDOWN_TIMEOUT` | prazo de graceful shutdown (`fx.Lifecycle`) |
| `DATABASE_URL` | DSN do Postgres |
| `OIDC_ISSUER_URL` / `OIDC_JWKS_URL` | endpoints do Keycloak para validação de token |
| `AWS_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` | credenciais dummy para o SDK falar com o LocalStack |
| `SQS_ENDPOINT`, `SQS_QUEUE_URL`, `SQS_DLQ_URL`, `EVENTS_QUEUE_URL`, `EVENTS_DLQ_URL` | endpoints das filas de entrada e de eventos |

## Identidades de teste (Keycloak)

O `keycloak-bootstrap` cria automaticamente 3 clients `client_credentials`, prontos para uso:

| Client | Role | Uso |
| --- | --- | --- |
| `provider-a` / secret `provider-a-secret` | `provider` | simula o provedor A enviando/consultando suas próprias transações |
| `provider-b` / secret `provider-b-secret` | `provider` | usado para comprovar isolamento entre provedores |
| `wager-internal` / secret `wager-internal-secret` | `internal` | abertura de carteira, leitura de saldo/ledger, reconciliação |

```sh
get_token() {
  curl -s -X POST http://localhost:8081/realms/wager-ledger/protocol/openid-connect/token \
    -d "client_id=$1" -d "client_secret=$2" -d grant_type=client_credentials | jq -r .access_token
}

PROVIDER_TOKEN=$(get_token provider-a provider-a-secret)
INTERNAL_TOKEN=$(get_token wager-internal wager-internal-secret)
```

## Exemplos de chamadas

Fluxo completo: abrir carteira → apostar → ganhar → estornar, com verificação de idempotência.

```sh
# 1. Abrir carteira (rota interna)
PLAYER_ID=$(uuidgen | tr 'A-Z' 'a-z')
WALLET=$(curl -s -X POST http://localhost:8080/wallets \
  -H "Authorization: Bearer $INTERNAL_TOKEN" -H 'Content-Type: application/json' \
  -d "{\"playerId\":\"$PLAYER_ID\",\"initialBalance\":{\"amount\":\"1000.00\",\"currency\":\"BRL\"}}")
WALLET_ID=$(echo "$WALLET" | jq -r .id)
echo "$WALLET" | jq

# 2. Apostar (rota do provedor, idempotente por header)
curl -s -X POST http://localhost:8080/wagering/transactions \
  -H "Authorization: Bearer $PROVIDER_TOKEN" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: provider-a:tx-001' \
  -d "{\"providerId\":\"provider-a\",\"externalTransactionId\":\"tx-001\",\"playerId\":\"$PLAYER_ID\",\"walletId\":\"$WALLET_ID\",\"roundId\":\"round-1\",\"gameId\":\"fortune-chimp\",\"kind\":\"BET\",\"money\":{\"amount\":\"25.00\",\"currency\":\"BRL\"}}" | jq

# 3. Reenviar a mesma chamada → deve voltar "idempotentReplay": true, mesmo transactionId e saldo
curl -s -X POST http://localhost:8080/wagering/transactions \
  -H "Authorization: Bearer $PROVIDER_TOKEN" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: provider-a:tx-001' \
  -d "{\"providerId\":\"provider-a\",\"externalTransactionId\":\"tx-001\",\"playerId\":\"$PLAYER_ID\",\"walletId\":\"$WALLET_ID\",\"roundId\":\"round-1\",\"gameId\":\"fortune-chimp\",\"kind\":\"BET\",\"money\":{\"amount\":\"25.00\",\"currency\":\"BRL\"}}" | jq

# 4. Consultar ledger e reconciliar
curl -s "http://localhost:8080/wallets/$WALLET_ID/ledger?limit=50" -H "Authorization: Bearer $INTERNAL_TOKEN" | jq
curl -s -X POST "http://localhost:8080/wallets/$WALLET_ID/reconciliation" -H "Authorization: Bearer $INTERNAL_TOKEN" | jq
```

O contrato completo de todas as rotas (payloads, respostas, headers) está documentado na [seção 9 do enunciado, abaixo](#9-contratos-http).

## Testes

```sh
go build ./...
go vet ./...

# Suíte sem dependência de infra real (domain, application, config, observability, idgen, idp, http)
go test ./internal/domain/... ./internal/application/... ./internal/config/... \
  ./internal/observability/... ./internal/infra/idgen/... ./internal/infra/idp/... ./internal/infra/http/...

# Com Postgres/LocalStack reais rodando (docker compose up -d postgres localstack)
go test ./... -count=1
go test -race -count=1 ./...
```

Os testes de integração usam containers reais de Postgres/LocalStack — nenhuma dependência de infraestrutura é mockada nesses testes (ver critérios eliminatórios, [§14](#14-critérios-de-avaliação)).

## Cenários obrigatórios de concorrência e recuperação

O script [`scripts/multi-machine-test.sh`](scripts/multi-machine-test.sh) automatiza os cenários das seções [§8](#8-concorrência) e [§13](#13-verificação-obrigatória) com **processos de SO independentes** (memória e conexão próprias), opcionalmente distribuídos por SSH em máquinas físicas diferentes via `HOSTS`:

```sh
chmod +x scripts/multi-machine-test.sh

# §8: carteira com 100.00 BRL recebe 2 apostas de 80.00 BRL simultâneas
#     → esperado: 1 PROCESSED, 1 REJECTED, saldo final 20.00, 1 único débito no ledger
./scripts/multi-machine-test.sh dispute

# §13.1: a mesma aposta enviada N vezes em paralelo → esperado: um único débito
./scripts/multi-machine-test.sh idempotency 5

# os dois cenários em sequência
./scripts/multi-machine-test.sh both
```

Sem `HOSTS`, cada processo roda localmente em background — ainda são processos de SO independentes, só não em máquinas físicas diferentes. Para distribuir de verdade:

```sh
# distribuído em 3 máquinas físicas de verdade — troque pelos SEUS hosts
# SSH reais e alcançáveis (chave sem senha, já que roda em paralelo em
# background); "user@host1" etc. abaixo são placeholders, não resolvem em
# lugar nenhum
HOSTS="user@10.0.0.11 user@10.0.0.12 user@10.0.0.13" \
REMOTE_API_URL="http://10.0.0.10:8080" \
  ./scripts/multi-machine-test.sh both
```

Não tem 3 máquinas físicas à mão? [`scripts/multi-machine-test-docker.sh`](scripts/multi-machine-test-docker.sh) sobe 3 containers Linux com `sshd` próprio (processo, rede e filesystem isolados entre si) e roda o teste distribuído neles — funciona só com `git clone` + Docker, sem hardware extra nem SSH configurado à mão:

```sh
docker compose up -d                        # stack base (se ainda não estiver no ar)
chmod +x scripts/multi-machine-test-docker.sh
./scripts/multi-machine-test-docker.sh both
```

Outros cenários da checklist do §13 (recuperação após restart, disputa de outbox entre 2 publishers, reentrega SQS após kill do consumidor) e seus resultados estão registrados em [`docs/QA_LOG.md`](docs/QA_LOG.md).

## Observabilidade

```sh
curl -s http://localhost:8080/metrics | grep wager   # métricas Prometheus: status, duplicatas, retries, DLQ, atraso de outbox
docker compose logs -f api                             # logs JSON correlacionados por correlationId/transactionId/walletId/providerId
```

## Estrutura do projeto

```
cmd/api                      # composição raiz (main + módulos Fx)
internal/
  domain/                    # entidades e regras de negócio, sem dependência de Fx/HTTP/SQS/infra
    money/  wallet/  wagertx/  ledger/  event/
  application/
    ports/                   # interfaces que o domínio expõe para a infra implementar
    usecase/                 # casos de uso (process wager transaction, reconcile wallet, ...)
  infra/
    http/                    # handlers, rotas e middlewares HTTP
    idp/                     # validação de token OIDC/JWT (Keycloak)
    postgres/                # repositórios, transações, migrations runtime
    sqs/                     # consumidor SQS
    outbox/                  # publisher transacional
    idgen/                   # geração de IDs (UUIDv7)
  config/                    # carregamento e validação de configuração
  observability/             # logger, métricas
migrations/                  # migrations versionadas (golang-migrate)
scripts/                     # bootstrap Keycloak, init SQS, teste de concorrência multi-processo
docker/                      # Dockerfiles
docs/                        # auditoria de implementação, log de QA, workflow de deploy/git
```

O domínio (`internal/domain`) não importa Fx, HTTP, SQS nem bibliotecas de persistência — dependências fluem de fora para dentro via `application/ports`, injetadas por construtores nos módulos Fx.

## Fluxo de desenvolvimento assistido por agentes

O desenvolvimento deste projeto usa um conjunto de agentes especializados, definidos em [`.claude/agents/`](.claude/agents) e referenciados em [`AGENTS.md`](AGENTS.md), cada um com escopo, ferramentas e critérios de aceite próprios — em vez de um único assistente genérico tratando revisão, testes, versionamento e deploy da mesma forma. A ideia é a mesma separação de responsabilidades que o código aplica em `internal/{domain,application,infra}`, só que aplicada ao processo de trabalho.

| Agente | Responsabilidade | Quando atua | Acionado por |
| --- | --- | --- | --- |
| [`go-reviewer`](.claude/agents/go-reviewer.md) | Revisão de código Go: invariantes de domínio (ledger/wallet/money), idioma Go, aderência à arquitetura hexagonal, transações/outbox/inbox | Proativamente após qualquer alteração em código Go, antes de considerar a tarefa concluída | mudança em `.go` |
| [`security-reviewer`](.claude/agents/security-reviewer.md) | AppSec: validação de token OIDC, IDOR entre provedores, injeção, segredos em config/log | Ao tocar autenticação, endpoints HTTP, dinheiro/saldo, Keycloak, SQS, Postgres ou config | mudança sensível a segurança |
| [`qa-tester`](.claude/agents/qa-tester.md) | Executa a aplicação de verdade — testes unitários, integração com Postgres/SQS/Keycloak reais, cenários de concorrência e recuperação das §8/§13 do enunciado | Proativamente ao final de toda funcionalidade, correção ou refatoração | conclusão de feature/fix |
| [`commit-manager`](.claude/agents/commit-manager.md) | Organiza branches, separa mudanças coerentes em commits Conventional Commits, mantém [`docs/COMMIT_LOG.md`](docs/COMMIT_LOG.md) | Ao concluir alterações autorizadas para versionamento | fim de tarefa |
| [`deploy-manager`](.claude/agents/deploy-manager.md) | CI/CD (GitHub Actions), build/publicação de imagens no GHCR, deploy e rollback em VPS Docker, conforme [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md) | Configurar, executar ou diagnosticar deploy e migrations | pedido de deploy/release |

Cada agente lê o histórico do repositório (`git status`/`diff`/`log`), a documentação correspondente e o que já foi registrado (`docs/QA_LOG.md`, `docs/COMMIT_LOG.md`, `docs/IMPLEMENTATION_AUDIT.md`) antes de agir — nenhum deles assume sucesso sem executar a verificação real (ex.: `qa-tester` não declara um cenário coberto sem rodar contra Postgres/SQS/Keycloak reais; `commit-manager` nunca comita `.env`, segredos ou binários). Isso mantém o rastro de decisão auditável: toda rodada de QA vira entrada em `docs/QA_LOG.md`, e todo commit vira entrada em `docs/COMMIT_LOG.md` com categoria e caminhos afetados.

## Decisões de arquitetura e limitações conhecidas

Decisões sobre `Money`, controle de concorrência da carteira, máquina de estados de `WagerTransaction`, inbox/outbox, autenticação/autorização, composição com Fx e shutdown — incluindo interpretações adotadas e trabalho não concluído — estão registradas em [`ARCHITECTURE.md`](ARCHITECTURE.md) (seção "Limitações conhecidas e trabalho não concluído") e no histórico de [`DECISIONS.md`](DECISIONS.md).

---

# Desafio Backend — Processamento Distribuído de Apostas em Go

Implemente um serviço em **Go**, com **Uber Fx**, para processar operações financeiras de provedores de jogos em um ambiente distribuído.

## 1. Objetivo

A aplicação deve oferecer uma API HTTP e um consumidor de mensagens que movimentem carteiras de jogadores com garantias equivalentes. Demonstre que o resultado financeiro continua correto com várias instâncias em execução e falhas entre as etapas do processamento.

A avaliação considera precisão monetária, integridade do ledger, idempotência persistente, concorrência, recuperação de falhas e decisões de arquitetura.

## 2. Autenticação e autorização

Autenticação e autorização são **obrigatórias**, com integração a um IdP externo OAuth 2.0/OIDC.

Recomenda-se **Keycloak** no Docker Compose e `client_credentials` para comunicação entre serviços. A escolha do IdP, a validação de credenciais e o modelo de permissões devem ser justificados em `ARCHITECTURE.md`. Cadastro de senhas e emissão própria de tokens estão fora do escopo.

A identidade autenticada deve determinar o `providerId` autorizado. Provedores acessam apenas suas próprias transações, inclusive em replays; operações de carteira são restritas ao serviço interno.

O acesso à mensageria deve ser controlado por credenciais e políticas do broker, preservando as validações de domínio no consumidor.

## 3. Ambiente de execução e falhas

Cada operação externa pertence a um jogador, uma carteira, um jogo, um provedor e uma rodada. Os tipos externos são `BET`, `WIN`, `LOSS`, `REFUND` e `ROLLBACK`.

Assuma entrega **at-least-once** e prepare a solução para:

- recebimento repetido de uma mesma operação, inclusive por HTTP e SQS;
- chegada de uma reversão antes da transação que ela referencia;
- processamento simultâneo de operações da mesma carteira;
- encerramento abrupto antes ou depois de um commit;
- publicação repetida de um evento de integração;
- indisponibilidade temporária do PostgreSQL ou do SQS.

Nenhuma dessas situações pode gerar movimentação duplicada, saldo negativo ou perda de um evento cujo registro foi confirmado no banco.

## 4. Stack

### Tecnologias obrigatórias

| Responsabilidade | Tecnologia |
| --- | --- |
| Linguagem e compilação | Go; declare a versão utilizada em `go.mod` e no Dockerfile |
| Dependências | Go Modules, com `go.mod` e `go.sum` versionados |
| Composição da aplicação | Uber Fx (`go.uber.org/fx`) |
| HTTP | `net/http` ou um roteador Go à sua escolha |
| Autenticação | IdP externo OAuth 2.0/OIDC; Keycloak recomendado |
| Persistência | PostgreSQL |
| Mensageria | AWS SQS, executado localmente com LocalStack ou MiniStack |
| Ambiente local | Docker Compose |
| Evolução do banco | Migrations versionadas, com aplicação e reversão documentadas |
| Testes | `testing` e `go test`, incluindo execução com `-race` |

### Acesso ao banco

`pgx` com SQL explícito é preferencial; `sqlc` é opcional. `database/sql` e GORM são aceitos. Transações, locks e constraints devem permanecer explícitos e verificáveis.

Documente em `ARCHITECTURE.md` a biblioteca escolhida, o mapeamento de `Money` e a delimitação da transação SQL entre os repositórios.

### Composição e ciclo de vida

Use Uber Fx na composição de configuração, conexões, repositórios, casos de uso, handlers e workers, com injeção por construtores e organização por `fx.Module`, `fx.Provide` e `fx.Invoke`.

Gerencie servidor, workers e recursos com `fx.Lifecycle`:

- inicialização com validação de configuração e dependências;
- cancelamento, prazos de execução e término observável dos workers;
- shutdown com interrupção de novas entradas e conclusão ou liberação do trabalho em andamento;
- fechamento das dependências após a finalização dos componentes que as utilizam.

O domínio deve permanecer independente de Fx, HTTP, SQS e bibliotecas de persistência. A organização dos pacotes fica a critério do candidato.

## 5. Garantias obrigatórias

1. Dinheiro não pode passar por `float32` ou `float64`, nem durante parsing, cálculo, serialização ou persistência.
2. Idempotência deve ser persistente e sobreviver ao reinício de todos os processos.
3. As invariantes financeiras devem ser garantidas no banco, independentemente de locks locais e da deduplicação do SQS FIFO.
4. Eventos externos só podem ser publicados depois da confirmação da transação que os originou.
5. O ledger deve ser append-only: correções financeiras exigem novos lançamentos.
6. Carteiras independentes devem avançar em paralelo; locks globais são proibidos.
7. Atualizações de saldo devem impedir lost updates.
8. Unicidade, não negatividade e imutabilidade do ledger devem ser impostas pelo schema, pelas constraints e pelos mecanismos de proteção do banco.

## 6. Modelo de domínio

### Encapsulamento e erros

Modele entidades com estado encapsulado, construtores com validação e métodos explícitos de transição. As invariantes devem ser preservadas em todas as operações públicas.

Separe criação e reidratação. A reidratação não deve reaplicar movimentações, transições ou emissão de eventos.

Valores de domínio não inicializados ou inválidos devem ser rejeitados.

Erros de domínio devem ser classificáveis por tipo ou `errors.Is`/`errors.As`. `panic` não deve representar rejeições de negócio. Operações de I/O devem receber `context.Context` e respeitar cancelamento e timeout.

### 6.1. Money

`Money` é um value object imutável, com valor e moeda. Deve suportar criação a partir de string decimal, zero por moeda, soma, subtração, negação, comparação e serialização.

Use `int64` em unidades mínimas ou uma biblioteca decimal de precisão exata. Documente a representação e seus limites.

- O contrato externo recebe e devolve valores como `{"amount":"25.00","currency":"BRL"}`.
- Use escala fixa de duas casas e código de moeda ISO 4217.
- Rejeite valores vazios, `NaN`, `Infinity`, notação científica, escala excedente e valores negativos nas entradas financeiras externas.
- Não arredonde silenciosamente uma entrada inválida. Caso aceite formas equivalentes, documente a normalização anterior ao hash de idempotência.
- Aritmética e comparação de valores monetários exigem moedas compatíveis.
- Se utilizar `int64`, trate overflow no parsing, na soma, na subtração e na negação.
- Valores negativos são permitidos em diferenças e cálculos internos, mas não no saldo da carteira.
- A persistência deve preservar exatamente valor e moeda, por exemplo com unidades mínimas em `BIGINT` ou decimal em `NUMERIC`.

É permitido operar apenas em BRL nos cenários principais, desde que o tipo carregue a moeda e existam testes de incompatibilidade entre moedas.

### 6.2. Wallet

A carteira é a raiz do agregado financeiro. Deve carregar identidade, jogador, moeda, saldo, versão e instantes de criação e atualização.

Exponha criação, reidratação e operações de débito/crédito, mantendo a alteração do saldo sob controle do agregado e da transação SQL.

- O par `(playerId, currency)` identifica uma única carteira.
- Débitos precisam preservar saldo maior ou igual a zero.
- A moeda de cada movimentação deve coincidir com a da carteira.
- Cada mudança financeira exige o lançamento correspondente no ledger, confirmado junto com o saldo.
- A versão inicial é `1`; depois da criação, incremente-a apenas quando houver mudança de saldo.
- Disputas entre escritores não podem descartar uma atualização confirmada.

A estratégia de controle de concorrência deve ser documentada.

### 6.3. WagerTransaction

Tipos: `OPENING`, `BET`, `WIN`, `LOSS`, `REFUND` e `ROLLBACK`.

Para operações externas, a transação registra os identificadores interno e externo, provedor, chave de idempotência, hash do payload, carteira, jogador, rodada, jogo, tipo, `Money`, referência externa opcional, estado e timestamps. Quando aplicável, persista também a referência interna resolvida, o código de falha e o resultado financeiro retornado ao provedor.

A transação inicia em `PENDING`. As transições para processamento, espera por referência, rejeição e falha permanente devem ser validadas pelo domínio.

| Estado | Significado |
| --- | --- |
| `PENDING` | Registro aceito, com processamento ainda não concluído |
| `PENDING_REFERENCE` | A aplicação depende de uma referência ainda indisponível |
| `PROCESSED` | Operação concluída com sucesso; estado terminal |
| `REJECTED` | Operação recusada por uma regra de negócio; estado terminal |
| `FAILED` | Falha permanente de infraestrutura registrada para auditoria; estado terminal |

Uma transação terminal não deve sofrer novas transições. Replay consulta seu resultado persistido sem reaplicar a operação. Documente a máquina de estados e como distingue falhas transitórias de falhas permanentes.

Todo `PENDING` confirmado deve ter retomada durável por outra instância após uma interrupção. Operações sem dependências podem ser concluídas de forma síncrona, sem commit intermediário de aceite.

`OPENING` é reservado à abertura interna de carteira. Rejeite esse tipo quando enviado por HTTP ou SQS.

`OPENING` exige identidade interna estável, carteira, jogador, moeda, valor, estado e timestamps. Provedor, ID externo, chave e hash externos, rodada, jogo e referência não se aplicam a essa origem. O schema deve distinguir operações internas e externas e impedir crédito inicial duplicado.

### 6.4. WalletLedgerEntry

Cada lançamento registra `id`, `walletId`, `transactionId`, direção (`DEBIT` ou `CREDIT`), valor, saldo anterior, saldo posterior e instante de criação.

O lançamento é imutável e sua construção deve validar `balanceAfter = balanceBefore ± money`, conforme a direção.

Imponha no banco a unicidade de `(walletId, transactionId)` e a proteção contra edição ou exclusão. `LOSS` e operações rejeitadas não produzem lançamentos. Um ledger de partidas dobradas é opcional.

### 6.5. Inbox e outbox

| Registro | Informações e comportamento esperados |
| --- | --- |
| Inbox | Identidade da mensagem e do consumidor, hash, recebimento e conclusão; unicidade de `(consumerName, messageId)` |
| Outbox | Identidade estável do evento, agregado, tipo, payload, ocorrência, tentativas, próximo envio e publicação; suporte a retry com backoff |

Na entrada por SQS, o registro da inbox e a conclusão durável do tratamento devem compartilhar a transação SQL das alterações de domínio, do ledger e dos eventos correspondentes. Uma referência pendente pode ter sua mensagem de entrada concluída após a pendência estar persistida; o worker de referências assume a continuidade.

## 7. Operações e referências

| Tipo | Movimentação | Condição |
| --- | --- | --- |
| `BET` | Débito | Exige valor positivo e saldo suficiente |
| `WIN` | Crédito | Exige valor positivo; pode informar uma aposta da mesma rodada como referência |
| `LOSS` | Sem movimentação | Exige `money.amount` igual a `"0.00"`; não cria ledger nem altera a versão da carteira |
| `REFUND` | Crédito | Devolve integralmente o valor de uma `BET` processada |
| `ROLLBACK` | Movimento contrário ao original | Desfaz integralmente uma `BET`, `WIN` ou `REFUND` processada |

Para `REFUND` e `ROLLBACK`, `referenceExternalTransactionId` é obrigatório e deve ser resolvido por `(providerId, referenceExternalTransactionId)`.

A operação e sua referência devem concordar em provedor, jogador, carteira, moeda e rodada. O valor da reversão precisa ser igual ao valor referenciado; reversões parciais não fazem parte do desafio.

Para este desafio, zero é aceito no saldo inicial e em `LOSS`; `BET`, `WIN`, `REFUND` e `ROLLBACK` exigem valor maior que zero. `LOSS` continua exigindo a moeda da carteira e, quando processado, produz `WagerTransactionProcessed`, sem `WalletBalanceChanged`.

Garanta que uma referência não receba duas reversões bem-sucedidas do mesmo tipo. Documente como trata combinações de `REFUND` e `ROLLBACK` sobre a mesma aposta, preservando a coerência financeira e impedindo devolução duplicada do mesmo débito.

Uma reversão que precisaria debitar mais que o saldo disponível deve ser rejeitada e auditável. Seu código de falha deve ser diferente daquele usado para uma aposta sem saldo.

### Referências ainda indisponíveis

Persista a operação como `PENDING_REFERENCE` quando a referência ainda não tiver chegado. Um worker deve tentar novamente com backoff exponencial, inclusive após reinicialização da aplicação.

Defina um número máximo de tentativas ou TTL. Quando esgotado, finalize como `REJECTED`, informando um código de referência não encontrada e produzindo o evento de rejeição. Explique também o comportamento quando a referência existe, mas ainda está pendente ou terminou sem sucesso.

Toda rejeição deve fornecer um `failureCode` estável e documentado, distinguindo entradas corrigíveis de resultados definitivos.

## 8. Concorrência

A coordenação deve ocorrer por carteira. Escolha locking pessimista, controle otimista com retry limitado, atualização atômica condicionada ou uma combinação justificável.

As garantias devem ser demonstradas com pelo menos três processos independentes, cada um com suas próprias conexões e memória.

Teste obrigatório: uma carteira com **100.00 BRL** recebe, ao mesmo tempo, duas apostas distintas de **80.00 BRL**.

O resultado deve conter uma aposta processada, uma rejeição por saldo insuficiente, saldo final de **20.00 BRL** e um único débito no ledger. Reenvios não podem alterar esse resultado. Carteiras diferentes devem continuar sendo processadas em paralelo.

## 9. Contratos HTTP

### Abertura de carteira

```http
POST /wallets
Content-Type: application/json
```

```json
{
  "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
  "initialBalance": { "amount": "1000.00", "currency": "BRL" }
}
```

Exemplo de resposta:

```json
{
  "id": "0192f291-27dd-7d3f-8071-5f8685deef37",
  "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
  "balance": { "amount": "1000.00", "currency": "BRL" },
  "version": 1
}
```

Uma abertura com saldo positivo deve criar `OPENING` em `PROCESSED`, seu lançamento de crédito e os registros de outbox para `WagerTransactionProcessed` e `WalletBalanceChanged` no mesmo commit da carteira. Esses eventos de origem interna não exigem os metadados externos inaplicáveis; a versão da carteira nessa abertura é `1`. Saldo inicial zero não cria `OPENING`, ledger nem esses eventos financeiros. Tentar abrir outra carteira para o mesmo jogador e moeda deve resultar em conflito.

### Leitura

```http
GET /wallets/:walletId
GET /wallets/:walletId/ledger?cursor=...&limit=50
GET /wagering/transactions/:transactionId
GET /providers/:providerId/wagering/transactions/:externalTransactionId
```

A paginação do ledger deve usar cursor opaco e ordenação estável. As consultas de transação devem permitir acompanhar pendências e consultar códigos de rejeição ou falha.

### Envio de operação

```http
POST /wagering/transactions
Content-Type: application/json
Idempotency-Key: provider-a:transaction-123
```

```json
{
  "providerId": "provider-a",
  "externalTransactionId": "transaction-123",
  "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
  "walletId": "0192f291-27dd-7d3f-8071-5f8685deef37",
  "roundId": "round-987",
  "gameId": "fortune-chimp",
  "kind": "BET",
  "money": { "amount": "25.00", "currency": "BRL" }
}
```

Exemplo após processamento:

```json
{
  "transactionId": "0192f298-345e-7e38-af88-e43f851a819d",
  "status": "PROCESSED",
  "balance": { "amount": "975.00", "currency": "BRL" },
  "idempotentReplay": false
}
```

Para reversões, acrescente `referenceExternalTransactionId` ao corpo.

O header `Idempotency-Key` é obrigatório. O cliente pode construí-lo como `{providerId}:{externalTransactionId}`, mas o servidor não deve substituir silenciosamente uma chave recebida por outra calculada.

Persista um hash determinístico dos campos de negócio, usando JSON canônico com ordenação de chaves. Exclua a chave de idempotência e os metadados de transporte desse cálculo. Documente algoritmo, campos e normalizações, garantindo equivalência entre HTTP e SQS.

- Chave e conteúdo equivalentes: retorne o resultado persistido, com `idempotentReplay: true`.
- Chave reutilizada com conteúdo diferente: devolva conflito.
- Uma operação financeira identificada por `(providerId, externalTransactionId)` não pode ser reaplicada usando outra chave.
- Para operações concluídas, o replay deve devolver o saldo observado no processamento original, mesmo que a carteira já tenha recebido outras movimentações.

Documente os códigos HTTP e os corpos de resposta para entrada inválida, conflito, rejeição de negócio, processamento pendente e indisponibilidade transitória. Essas situações precisam ser distinguíveis pelo contrato.

### Reconciliação

```http
POST /wallets/:walletId/reconciliation
```

Exemplo considerando apenas a abertura e a aposta apresentadas acima:

```json
{
  "walletId": "0192f291-27dd-7d3f-8071-5f8685deef37",
  "storedBalance": { "amount": "975.00", "currency": "BRL" },
  "calculatedBalance": { "amount": "975.00", "currency": "BRL" },
  "difference": { "amount": "0.00", "currency": "BRL" },
  "consistent": true,
  "checkedEntries": 2
}
```

Reconstrua o saldo a partir do ledger, incluindo a abertura, e compare os valores em uma visão consistente dos dados. `difference` é o saldo armazenado menos o saldo reconstruído.

Reporte divergências na resposta, nos logs e em uma métrica. A reconciliação não deve alterar o saldo.

### Health checks públicos

```http
GET /health/live
GET /health/ready
```

Liveness do processo e readiness de PostgreSQL e SQS.

## 10. Consumidor SQS

Provisione as filas `wager-transactions.fifo` e `wager-transactions-dlq.fifo`, incluindo a configuração de redrive.

Exemplo de corpo de mensagem:

```json
{
  "messageId": "msg-123",
  "type": "WagerTransactionRequested",
  "occurredAt": "2026-09-08T12:00:00.000Z",
  "data": {
    "providerId": "provider-a",
    "externalTransactionId": "transaction-123",
    "idempotencyKey": "provider-a:transaction-123",
    "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
    "walletId": "0192f291-27dd-7d3f-8071-5f8685deef37",
    "roundId": "round-987",
    "gameId": "fortune-chimp",
    "kind": "BET",
    "money": { "amount": "25.00", "currency": "BRL" }
  }
}
```

HTTP e SQS devem compartilhar o caso de uso e as garantias de idempotência financeira. Na entrada por SQS, a chave é `data.idempotencyKey`, com deduplicação adicional pela inbox.

- Use o `messageId` do envelope como identidade durável da mensagem para o consumidor e verifique seu hash em reentregas.
- Remova a mensagem da fila somente após o commit do seu tratamento durável.
- Rejeições de negócio confirmadas são terminais e permitem a remoção da mensagem.
- Falhas transitórias exigem retry com backoff; erros permanentes ou tentativas esgotadas devem chegar à DLQ.
- Documente limites de tentativas, visibility timeout e tratamento de mensagens inválidas.
- Em `SIGTERM`, pare de buscar trabalho e conclua o processamento em andamento dentro do prazo, ou libere sua visibilidade para reentrega segura.

Documente `MessageGroupId` e `MessageDeduplicationId` e valide a concorrência entre entradas HTTP e SQS.

## 11. Publicação com transactional outbox

Estado da operação, saldo, ledger, inbox e registros de eventos devem ser confirmados atomicamente, conforme aplicável.

Um worker separado publica os registros pendentes da outbox. Ele deve suportar múltiplos publishers, disputa por registros, backoff e recuperação de trabalho abandonado.

Demonstre recuperação após interrupção entre commit e publicação e entre publicação e confirmação na outbox. Eventos pendentes devem ser assumidos por outra instância; republicações devem preservar o `eventId`.

Provisione o destino dos eventos de saída e documente seus contratos de roteamento e consumo.

### Eventos exigidos

| Evento | Gatilho |
| --- | --- |
| `WagerTransactionProcessed` | Conclusão bem-sucedida de uma operação, incluindo `LOSS` |
| `WagerTransactionRejected` | Rejeição definitiva por regra de negócio |
| `WalletBalanceChanged` | Alteração efetiva do saldo |
| `WagerTransactionPendingReference` | Registro de espera pela referência |

Defina tipos concretos por evento. O envelope deve conter `eventId`, `eventType`, `aggregateId`, `correlationId`, `causationId` opcional, `occurredAt`, `version` e `data` tipado.

O payload de `WalletBalanceChanged` deve incluir `walletId`, `transactionId`, `direction`, `money`, `balanceBefore`, `balanceAfter` e `walletVersion`.

Tipo e versão devem ser definidos pelo construtor do evento. Use timestamps UTC em RFC 3339 e valores monetários em strings decimais. O payload da outbox deve ser um snapshot imutável.

## 12. Observabilidade

Produza logs JSON com os identificadores disponíveis para rastrear a operação: `correlationId`, `messageId`, `transactionId`, `walletId` e `providerId`. Não registre credenciais, dados sensíveis ou payloads financeiros completos.

Exponha métricas para resultados por status, duplicatas, retries, DLQ, conflitos de concorrência, atraso da outbox, latência de processamento e divergências de reconciliação.

Inclua os health checks definidos na API. Tracing com OpenTelemetry e dashboards são diferenciais opcionais.

## 13. Verificação obrigatória

### Testes unitários

Cubra parsing e operações de `Money`, escala, limites numéricos, entradas inválidas, incompatibilidade de moedas, invariantes da carteira, transições de estado, regras dos cinco tipos externos e conflito de payload para a mesma chave. Inclua a política de valores zero de cada tipo e a abertura interna com seus metadados e eventos.

### Testes de integração

Execute PostgreSQL, o IdP e LocalStack ou MiniStack em containers reais. Verifique migrations, constraints, imutabilidade do ledger, atomicidade financeira, inbox, reentrega, outbox concorrente, retry, DLQ e recuperação após reinicialização.

Adicione uma verificação da composição Fx e de seu início e encerramento, incluindo liberação de recursos dos workers. Não substitua toda a infraestrutura por mocks.

### Autenticação e autorização

- Integração real com o IdP e rejeição de credenciais ausentes, inválidas ou expiradas.
- Isolamento entre provedores, inclusive em consultas e replays, e restrição das operações internas.
- Ausência de efeitos financeiros ou exposição de dados em acessos não autorizados.

### Testes de concorrência e recuperação

1. Envie a mesma aposta 50 vezes em paralelo e comprove um único débito.
2. Execute a disputa das duas apostas de 80.00 sobre saldo de 100.00.
3. Processe carteiras distintas simultaneamente.
4. Repita cenários relevantes com pelo menos três instâncias independentes.
5. Interrompa um consumidor depois do commit e antes da remoção da mensagem; valide a reentrega.
6. Execute dois publishers disputando a mesma outbox e valide a recuperação de publicação.
7. Entregue `REFUND` ou `ROLLBACK` antes da referência e comprove a resolução posterior ou a rejeição por expiração.
8. Reinicie a aplicação e verifique que idempotência, pendências e consistência financeira foram preservadas. Se houver aceite assíncrono, interrompa o processo após confirmar `PENDING` e antes de executar a operação; outra instância deve retomá-la.

Ao final, confira o saldo armazenado contra a soma de créditos menos débitos do ledger. Inclua cenários que cruzem HTTP e SQS para a mesma operação.

Os testes de duplicidade devem exercitar a deduplicação da aplicação, com recebimentos repetidos comprovados.

Execute `go test -race` nos testes aplicáveis.

## 14. Critérios de avaliação

| Critério | Pontos | Evidência esperada |
| --- | ---: | --- |
| Integridade financeira | 20 | Precisão, invariantes, reversões e reconciliação confiáveis |
| Concorrência | 20 | Coordenação entre processos e ausência de atualizações perdidas |
| Idempotência | 15 | Persistência, detecção de conflito e reprodução do resultado original |
| Mensageria e recuperação | 15 | Inbox, outbox, retries, DLQ e encerramento seguro |
| Modelagem e arquitetura | 10 | Encapsulamento em Go, composição com Fx e políticas de autenticação e autorização |
| Testes | 10 | Integração real, isolamento entre provedores, paralelismo e interrupção |
| Observabilidade | 5 | Diagnóstico por logs, métricas e health checks |
| Documentação | 5 | Execução reproduzível e decisões técnicas explicadas |
| **Total** | **100** | |

São eliminatórios: ausência de autenticação efetiva nos endpoints de negócio, acesso não autorizado a operações ou transações, cálculo monetário em ponto flutuante, saldo negativo por concorrência, movimentação duplicada, idempotência restrita à memória, dependência de uma única instância para funcionar corretamente, publicação anterior ao commit, ausência de ledger auditável ou substituição integral de PostgreSQL, SQS e IdP por mocks nos testes.

Partidas dobradas, tracing e testes de carga são diferenciais opcionais. Testes de carga devem incluir comando reproduzível, ambiente, metodologia, throughput, p50/p95/p99, erros, conflitos e atraso da outbox. Não há meta mínima de RPS.

## 15. Entrega

Entregue o código, migrations, ambiente Docker Compose e instruções suficientes para outra pessoa reproduzir a solução a partir de um checkout limpo.

O `README.md` da solução deve explicar pré-requisitos, variáveis de ambiente, inicialização das filas, aplicação e reversão das migrations, execução da aplicação, exemplos de chamadas e comandos de teste. Inclua `.env.example` com valores locais de exemplo, sem segredos reais.

Inclua o provisionamento automático do IdP, identidades de teste e instruções para executar os fluxos autenticados.

No `ARCHITECTURE.md`, registre as decisões sobre dinheiro, transações, idempotência, locks, referências pendentes, reversões, inbox/outbox, autenticação, autorização, uso do Fx e shutdown. Explicite limitações, interpretações adotadas e trabalho não concluído.

Disponibilize os comandos abaixo ou equivalentes documentados:

```sh
docker compose up --build
go test ./...
go test -race ./...
go vet ./...
```

Documente separadamente como preparar as dependências dos testes e executar integração, múltiplas instâncias e simulações de falha. Se utilizar build tags, informe os comandos correspondentes.

Entregue código formatado com `gofmt` e dependências reproduzíveis.# wager-ledger
