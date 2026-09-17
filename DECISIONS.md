# Architecture Decision Records

Revisão de consistência: 17/09/2026. Decisões e relatos históricos são
preservados; não representam nova execução de testes. Consulte a
[auditoria atual](docs/IMPLEMENTATION_AUDIT.md) para evidências e lacunas.

## ADR-001: PostgreSQL

### Context
Precisamos de consistência transacional para pagamentos.

### Decision
Utilizar PostgreSQL.

### Reason
Suporte a transações ACID, constraints e boa integração com Go.

---

## ADR-002: Idempotency

### Context
Uma mesma operação financeira de aposta pode chegar mais de uma vez.
O registro original citava PIX; essa terminologia não pertence ao domínio atual.

### Decision
Utilizar idempotency_key com índice UNIQUE.

### Result
Intenção: evitar processamento financeiro duplicado; a implementação
ainda não impõe unicidade própria sobre a chave.

### Status atual (revisão)
A migration `0002_create_wager_transactions` garante apenas
`UNIQUE (provider_id, external_transaction_id)`; a migration 0005
adiciona índice não único em `idempotency_key`. O caso de uso já
implementa hash, replay e conflito (ADR-007), mas SELECT seguido de
INSERT não impede a mesma chave em operações distintas concorrentes.
A decisão original de unicidade da chave permanece não implementada.
É necessário definir o escopo da chave, impor UNIQUE nesse escopo e
cobrir concorrência entre operações/carteiras diferentes.

---

## ADR-003: Money sem ponto flutuante

### Context
A seção 6.1 do desafio proíbe `float32`/`float64` em qualquer etapa —
parsing, cálculo, serialização ou persistência — e exige rejeição
estrita de entradas inválidas (NaN, notação científica, escala
excedente, valores vazios).

### Decision
Representar `Money` como `int64` em unidades mínimas (centavos) mais
o código de moeda ISO 4217, com parsing manual de string decimal
(sem `strconv.ParseFloat` nem `math/big.Float`) e checagem explícita
de overflow em soma, subtração e negação.

### Reason
`int64` em centavos é suficiente para os valores do domínio (apostas
online), evita a complexidade de uma biblioteca decimal externa, e
torna trivial garantir que nenhuma operação passe por ponto flutuante
em nenhum momento — a query mais estrita da seção 6.1.

### Result
`internal/domain/money/money.go` implementa `FromDecimalString`,
`Zero`, `Add`, `Sub`, `Negate`, `Compare`, `Equals`, e
`MarshalJSON`/`UnmarshalJSON` compatíveis com o contrato
`{"amount":"25.00","currency":"BRL"}`. Persistência usa `BIGINT` em
`*_minor_units` mais `CHAR(3)` para moeda, preservando exatamente o
valor original. Testes cobrem parsing, overflow e incompatibilidade
de moeda. A revisão identificou lacunas em Money não inicializado,
validação de códigos ISO 4217 e DecimalString de math.MinInt64; ver auditoria.

---

## ADR-004: Controle de concorrência otimista por versão

### Context
A seção 8 do desafio exige coordenação por carteira sem locks globais,
proteção contra lost updates, e avanço em paralelo de carteiras
independentes. O agregado `Wallet` já carrega uma `version`
incrementada a cada mudança de saldo (seção 6.2).

### Decision
Usar controle otimista: toda escrita de saldo passa por
`UPDATE wallets SET ... WHERE id = $1 AND version = $2`
(`internal/infra/postgres/wallet_repository.go:Save`). Zero linhas
afetadas vira `ports.ErrOptimisticLock`.

### Reason
Evita lock pessimista (`SELECT ... FOR UPDATE`) held por toda a
duração da lógica de negócio em memória, que aumentaria contenção sob
concorrência alta na mesma carteira. Otimista se alinha bem com o
requisito de que carteiras diferentes nunca se bloqueiem entre si —
não há lock global na aplicação. UPDATEs ainda adquirem locks no
PostgreSQL, mantidos até a conclusão da transação.

### Result
Coberto pelo teste de integração existente
(`TestWalletRepository_Save_OptimisticLock`): duas leituras da mesma
versão, dois débitos em memória, apenas o primeiro `Save` é aceito; o
segundo falha com `ErrOptimisticLock`, e o saldo final reflete só a
escrita bem-sucedida.

### Limitação
O retry foi implementado em `ProcessWagerTransaction` (ADR-007), com
até cinco tentativas completas. O teste 100/80/80 existe como teste
automatizado de integração (goroutines, um processo) e foi
adicionalmente validado com processos de sistema operacional
independentes (`curl` concorrente contra a API real em Docker) na
rodada de QA de 17/09/2026 — ver `docs/QA_LOG.md`. A validação com
processos de SO reais não está automatizada em CI.

---

## ADR-005: Keycloak como IdP, JWT validado localmente via JWKS

### Context
A seção 2 do desafio exige integração com um IdP externo OAuth
2.0/OIDC, recomendando Keycloak e `client_credentials` para
comunicação entre serviços. Emissão própria de token está fora do
escopo.

### Decision
Usar Keycloak (`quay.io/keycloak/keycloak:25.0`, modo `start-dev`) no
Docker Compose, com dois clients de service account por papel
(`provider`, `internal`), provisionados automaticamente por
`scripts/keycloak-bootstrap.sh`. A API valida tokens localmente: busca
o JWKS do realm, faz cache das chaves por `kid`
(`internal/infra/idp/jwks.go`), e valida assinatura RS256 + issuer a
cada requisição (`internal/infra/idp/middleware.go`), sem chamar o
Keycloak em cada request.

### Reason
Validação local evita que o IdP vire um ponto de latência/falha por
requisição, e é o padrão usual para OIDC resource servers. Roles de
realm (`realm_access.roles` no claim) mapeiam diretamente para a
distinção que o desafio pede entre operações de provedor e operações
internas — `RequireRole("internal")` protege `POST /wallets`, por
exemplo.

### Result
9 testes de middleware cobrem token válido, header ausente/malformado,
token expirado, assinatura de outra chave, issuer errado, e as duas
roles. Testes HTTP E2E (`wallets_test.go`) confirmam 401 sem token,
403 com role errada, 201/200 com role correta.

### Limitação
POST de aposta valida role provider e correspondência de providerId
com azp; GET de carteira e GET de transação exigem role `internal`. O
middleware exige `exp` e valida `aud` contra `OIDC_AUDIENCE`, com o
client scope de audience provisionado automaticamente no bootstrap do
Keycloak. Os testes de unidade continuam usando JWKS simulado; a
integração com o Keycloak real (emissão de token real, `aud` correto,
fluxo HTTP completo autenticado) foi validada manualmente via
`docker compose` na rodada de QA de 17/09/2026 (`docs/QA_LOG.md`), mas
não está automatizada como teste executável em CI.

---

## ADR-006: Transação SQL delimitada por Unit of Work + context

### Context
A seção 4 do desafio exige que a delimitação da transação SQL entre
repositórios seja documentada e verificável, e a seção 6.5 exige que
inbox/outbox e mudanças de domínio compartilhem a mesma transação.

### Decision
Um único `PgUnitOfWork.Execute(ctx, fn)` (implementando
`ports.UnitOfWork`) abre uma transação `pgx.Tx`, injeta-a no
`context.Context` via chave privada, e todo repositório Postgres
resolve `querierFrom(ctx, pool)`: usa a transação se presente no
context, senão usa o pool diretamente. Isso permite que múltiplos
repositórios (`wallet`, `wagertx`, `ledger`, `outbox` e futuramente inbox) participem da mesma transação SQL sem acoplamento direto
entre eles — nenhum repositório recebe outro como dependência.

### Reason
Alternativas descartadas: passar `pgx.Tx` explicitamente por parâmetro
em cada método de repositório (polui as interfaces de `ports`, que
devem ficar livres de detalhes do driver Postgres) ou usar uma
transação global por request (contraria a exigência de que a
publicação de eventos só ocorra após o commit da transação que os
originou — outbox e domínio precisam poder ser confirmados juntos, mas
cada operação de negócio deve ter seu próprio limite).

### Result
`internal/infra/postgres/txmanager.go`. Testado em
`TestFullFlow_Debit_And_LedgerEntry_WithinSameTx` (débito + ledger +
atualização de status, tudo confirmado ou revertido junto) e
`TestFullFlow_RollbackOnError` (erro após o débito reverte também o
`Save` do saldo). `OpenWallet` usa o mesmo padrão para carteira +
`OPENING` + lançamento de abertura + eventos de outbox.

### Limitação
`OutboxRepository` e `InboxRepository` participam do mesmo contexto
transacional. `OpenWallet` e `ProcessWagerTransaction` usam a outbox;
`sqs.Consumer` usa a inbox dentro da mesma transação do efeito
financeiro (`ExecuteWithin`), garantindo que o `DeleteMessage` do SQS só
ocorra após o commit. Testes de recuperação/publicação concorrente
(dois publishers, reentrega SQS real) foram adicionados na rodada de QA
de 17/09/2026 — ver `internal/infra/outbox/publisher_integration_test.go`
e `internal/infra/sqs/consumer_integration_test.go`. Essa mesma rodada
encontrou e corrigiu um bug real: `InboxRepository.Create` deixava a
transação Postgres abortada ao capturar a violação de unicidade em uma
duplicidade esperada, fazendo o `COMMIT` subsequente falhar mesmo
quando a duplicidade deveria ser um no-op silencioso (ver
`internal/infra/postgres/inbox_repository.go` e ARCHITECTURE.md).

---

## ADR-007: Idempotência e retry otimista em ProcessWagerTransaction

### Context
A seção 9 exige: mesma `idempotencyKey` + mesmo conteúdo → replay do
resultado persistido; mesma `idempotencyKey` + conteúdo diferente →
conflito; `(providerId, externalTransactionId)` já registrado sob
outra chave → conflito. A seção 8 exige coordenação por carteira sem
locks globais, com o cenário obrigatório de duas apostas de 80.00
sobre um saldo de 100.00.

### Decision
Hash de idempotência: SHA-256 sobre um `map[string]any` serializado
via `encoding/json` (que ordena chaves de mapas alfabeticamente),
cobrindo os campos de negócio e excluindo a própria `idempotencyKey`
(`internal/application/usecase/idempotency.go`).

`ProcessWagerTransaction.Execute` roda em um loop de até 5 tentativas.
Cada tentativa é um `uow.Execute` completo: checagem de idempotência,
leitura da carteira, criação da transação, débito/crédito, lançamento
de ledger e eventos de outbox, tudo na mesma transação SQL. Um retorno
`ports.ErrOptimisticLock` (conflito de versão) ou `ports.ErrAlreadyExists`
(duas requisições tentando criar a mesma transação ao mesmo tempo)
descarta a tentativa inteira (rollback) e tenta de novo — a releitura
seguinte enxerga o estado já commitado pela tentativa concorrente.

A checagem de idempotência (`resolveExisting`) busca primeiro por
`idempotencyKey`; se não encontrar, busca por
`(providerId, externalTransactionId)`. Nesse segundo caso, só é
replay se a chave E o hash também baterem — caso contrário é conflito.
Essa dupla verificação (não apenas "achou, logo é conflito") é
necessária porque, sob `READ COMMITTED`, cada `SELECT` da mesma
transação vê o snapshot mais recente committed *daquele instante*:
é possível a primeira busca (por chave) não encontrar nada porque a
transação concorrente ainda não tinha commitado, e a segunda busca
(por operação) já encontrar o registro dela, já commitado — sem essa
comparação extra, esse caso de leitura defasada virava um falso
conflito.

### Result
`internal/application/usecase/process_wager_transaction_test.go`
cobre replay, conflito (mesma chave/conteúdo diferente e mesma
operação/chave diferente), rejeição por saldo insuficiente,
incompatibilidade de moeda e retry após `ErrOptimisticLock` (com fakes
que agora clonam objetos e revertem estado no erro, replicando
transação real — ver limitação abaixo).

Testes de integração reais contra Postgres
(`internal/infra/postgres/process_wager_transaction_integration_test.go`)
cobrem os cenários abaixo. A anotação original relata `-race` e dez
execuções consecutivas; isso é histórico, não uma validação repetida
na revisão de 17/09/2026:
- duas apostas de 80.00 concorrentes sobre 100.00 → uma `PROCESSED`,
  uma `REJECTED` por `INSUFFICIENT_BALANCE`, saldo final 20.00, um
  único lançamento de ledger;
- 50 requisições paralelas da mesma aposta → uma `PROCESSED`, 49
  replays, um único débito.

### Limitação
Os fakes de teste unitário (`open_wallet_test.go`) originalmente
compartilhavam ponteiros entre "repositório" e objeto de domínio em
uso, e o `fakeUOW` não revertia nada em caso de erro — isso mascarava
bugs de concorrência que só apareciam contra o Postgres real. Foram
corrigidos para clonar em leitura/escrita e reverter no erro,
replicando parte da semântica transacional; qualquer novo fake de
repositório deve seguir esse padrão, sem substituir testes reais.

A checagem por chave não tem UNIQUE no banco: operações distintas
concorrentes podem usar a mesma chave. Replay de REJECTED usa saldo
atual. Esses casos não são cobertos pelos dois testes concorrentes
citados e impedem considerar a idempotência completa.

---

## ADR-008: Ambiente de desenvolvimento 100% em Docker, com hot-reload

### Context
O desafio exige que a solução rode via `docker compose up --build` a
partir de um checkout limpo. Até aqui, `docker-compose.yml` só subia
as dependências (Postgres, Keycloak, LocalStack); a API rodava fora do
Docker, e migrations/provisionamento do Keycloak eram passos manuais.
Também era necessário um ciclo de desenvolvimento rápido — editar
código e ver o efeito sem rebuildar a imagem manualmente a cada vez
(equivalente ao `nodemon` do Node.js).

### Decision
`docker/api.Dockerfile` passa a ter quatro stages: `base` (download de
dependências, compartilhado), `dev` (imagem `golang:1.22-bookworm`
completa com `air` instalado, roda `air -c .air.toml`), `build` (compilação estática) e `runtime`
(distroless). `docker-compose.yml` builda
o stage `dev` para o serviço `api`, com o código-fonte montado como
volume (`.:/src`) — qualquer alteração em um arquivo `.go` faz o `air`
recompilar e reiniciar o processo dentro do container automaticamente.
Volumes nomeados cacheiam `$GOPATH/pkg/mod` e `$GOCACHE` entre
reinícios.

Dois serviços one-shot novos rodam antes da `api` (via `depends_on` com
`condition: service_completed_successfully`): `migrate` (aplica as
migrations) e `keycloak-bootstrap` (roda `scripts/keycloak-bootstrap.sh`
contra o Keycloak do compose). `OIDC_JWKS_URL` da `api` usa o hostname
interno (`keycloak:8080`); `OIDC_ISSUER_URL` continua apontando para
`localhost:8081` porque é o valor que aparece no claim `iss` dos
tokens emitidos para quem testa a API a partir do host.

### Reason
Um único `Dockerfile` com múltiplos `target` evita duplicar a lógica de
build entre um Dockerfile de dev e outro de produção, e garante que a
imagem de produção (distroless, sem shell, sem toolchain Go) continua
exatamente como era — o stage `dev` nunca é usado fora do
`docker-compose.yml`. A imagem oficial `migrate/migrate:v4.17.0`
produziu saída binária corrompida e travou de forma reproduzível neste
ambiente (Docker Desktop em macOS); rodar `golang-migrate` via
`go install` dentro de uma imagem `golang` já usada pelo resto do
projeto evitou a dependência nessa imagem e é igualmente confiável.

### Result
O Compose está configurado para subir Postgres, Keycloak (com realm/roles/
clients provisionados), LocalStack e a API, todos dependendo
corretamente uns dos outros, sem passo manual de bootstrap. O registro original relata teste
editando `internal/infra/http/server.go` com o container rodando: o
`air` detectou a mudança, recompilou e reiniciou o servidor em
segundos, com o novo log aparecendo automaticamente — sem
`docker compose restart` nem rebuild da imagem. Esse relato histórico não foi reexecutado nesta revisão.

### Limitação
Durante a configuração, o Docker Desktop travou (múltiplos processos
`docker buildx`/`docker compose`/`docker ps` pendurados por mais de 30
minutos) e precisou ser reiniciado manualmente
(`killall` + reabrir o app) antes do build funcionar — causa raiz não
identificada, possivelmente uma imagem parcialmente corrompida em
cache de uma tentativa anterior interrompida. Se o build travar de
forma parecida no futuro, reiniciar o Docker Desktop e rodar
`docker compose build --no-cache --pull` costuma resolver.
