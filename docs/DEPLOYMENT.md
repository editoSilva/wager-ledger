# Deploy em VPS com Docker

A publicação usa GitHub Actions, imagens no GHCR e Docker Compose na VPS.
O único `docker-compose.yml` contém os serviços locais e o profile `production`,
com `api-production` e `migrate-production`. O script executa explicitamente
somente esses dois serviços. PostgreSQL e o
provedor OIDC precisam estar disponíveis separadamente. Nenhuma VPS é provisionada pelo workflow.

## Preparação da VPS

Use Linux com Docker Engine, plugin Docker Compose v2.24 ou superior, Bash, `curl` e `flock`
(util-linux). A arquitetura da VPS deve corresponder à imagem construída no
workflow. O usuário SSH precisa executar Docker sem prompt de sudo e escrever
em `/opt/wager-ledger`; acesso ao socket Docker equivale a acesso administrativo.

Crie `/opt/wager-ledger/releases` e `/opt/wager-ledger/shared`, pertencentes ao
usuário de deploy. Guarde a configuração somente na VPS em
`/opt/wager-ledger/shared/.env.production`, com permissão `600`:

```dotenv
DATABASE_URL=postgres://USER:PASSWORD@DATABASE_HOST:5432/wager_ledger?sslmode=verify-full
OIDC_ISSUER_URL=https://identity.example.com/realms/wager-ledger
OIDC_JWKS_URL=https://identity.example.com/realms/wager-ledger/protocol/openid-connect/certs
SHUTDOWN_TIMEOUT=15s
```

Substitua os exemplos por endpoints e credenciais reais, incluindo a configuração
TLS exigida pelo PostgreSQL. Os hosts precisam ser acessíveis de dentro dos
containers; `localhost` aponta para o próprio container. O arquivo segue a sintaxe
`env_file` do Compose: use aspas simples quando precisar preservar `$` literal.
O Compose fixa `APP_ENV=production` e `HTTP_PORT=8080`. O `env_file` dos
serviços de produção usa `required: false` para permitir o uso local sem esse
arquivo; o script exige sua presença antes de qualquer deploy. Não execute
`docker compose --profile production up` sem selecionar serviços: isso também
ativaria os serviços locais sem profile. Use sempre o script de deploy.

Se o pacote GHCR for privado, autentique previamente o usuário de deploy na VPS
com `docker login ghcr.io`, usando uma credencial com `read:packages`. Prefira
`--password-stdin`; não coloque tokens no histórico de comandos. O pipeline não
transfere credenciais do registry para o servidor.

A API é publicada apenas em `127.0.0.1:8080`. Configure um proxy reverso na VPS
(por exemplo Nginx ou Caddy) para encaminhar o domínio com HTTPS a esse endereço.
Restrinja o firewall às portas necessárias de SSH e HTTPS. O banco e o IdP não
são publicados por esta stack.

## Configuração do GitHub

Crie o environment `production` e configure os secrets:

| Secret | Conteúdo |
| --- | --- |
| `VPS_HOST` | IP ou hostname da VPS |
| `VPS_USER` | Usuário SSH de deploy |
| `VPS_SSH_KEY` | Chave privada SSH dedicada |
| `VPS_KNOWN_HOSTS` | Entrada conhecida e verificada da chave SSH do servidor |
| `VPS_PORT` | Porta SSH; opcional, padrão 22 |

Verifique o fingerprint da chave do servidor por um canal confiável antes de
cadastrar `VPS_KNOWN_HOSTS`. Para porta diferente de 22, a entrada normalmente
identifica `[host]:porta`. O workflow mantém a verificação de identidade SSH.

Defina a variável **do repositório** `DEPLOY_ENABLED=true` depois de preparar a
VPS. A variável habilita o job antes de ele entrar no environment; cadastrá-la
apenas no environment não habilita o deploy. Configure revisores obrigatórios no
environment se desejar uma aprovação de produção. Habilite a permissão de
publicação de pacotes GHCR pelo `GITHUB_TOKEN` conforme a política da organização.

Execute o workflow manualmente em `main` quando necessário. As verificações de CI
precisam passar antes da publicação. As tags `sha-<SHA completo>` e
`migrate-sha-<SHA completo>` identificam o commit, mas o deploy recebe os dois
**digests** retornados pelo build, garantindo que a referência não mude.

## Protocolo de deploy e rollback

O workflow transfere o Compose e o script para uma pasta de release:
`/opt/wager-ledger/releases/<commit>-<run_id>-<run_attempt>`. A interface remota é:

```bash
bash scripts/deploy-vps.sh \
  ghcr.io/OWNER/REPOSITORY@sha256:DIGEST_DA_API \
  ghcr.io/OWNER/REPOSITORY@sha256:DIGEST_DO_MIGRATOR
```

Os placeholders devem ser substituídos por nomes em minúsculas e digests de 64
dígitos hexadecimais. `DEPLOY_ROOT` permite alterar a raiz em execução manual;
o workflow utiliza `/opt/wager-ledger`.

O script trava `shared/deploy.lock`, valida o Compose, baixa as imagens, aplica
`migrate up` e recria apenas a API. O nome do projeto Compose permanece
`wager-ledger-production` em todas as releases. O migrator é uma imagem dedicada
com `/bin/sh`, o binário `migrate` e os arquivos em `/migrations`.

Falha na migration interrompe a publicação antes de substituir a API. A saída
do migrator é suprimida porque erros podem conter a URL do banco; investigue na
VPS em uma sessão restrita, sem copiar credenciais para logs públicos. Examine
também o estado de migrations antes de tentar novamente; não aplique `force`
sem entender a falha e validar o schema.

Depois da atualização, o script tenta `/health/live` e `/health/ready` até 30
vezes, com pausa de 2 segundos e timeout de 2 segundos por requisição. Falha ao
subir a API ou ao validar HTTP restaura a imagem e o Compose da última release
bem-sucedida, quando houver. No primeiro deploy, uma API reprovada é interrompida.
O workflow continua marcando falha mesmo que o rollback seja bem-sucedido.
O arquivo `shared/current-release` só muda após sucesso e guarda os dois digests
e o caminho do Compose. Preserve a release apontada e suas imagens no registry;
não execute limpeza indiscriminada de pastas ou imagens usadas no rollback.

**Rollback não reverte migrations.** Toda migration publicada precisa manter
compatibilidade com a API anterior (expansão primeiro, remoção em release
posterior). Faça backup e teste a restauração do banco antes de alterações de
schema. Uma reversão manual consiste em republicar uma versão de aplicação
compatível com o schema atual, após avaliar suas migrations.

Atualmente `/health/ready` não registra verificadores de dependências. Os checks
do deploy comprovam resposta HTTP, mas não validam banco, OIDC ou operações de
carteira. Faça um smoke test funcional autenticado depois da publicação. A troca
de um único container pode causar breve indisponibilidade; não há blue/green.

## Verificações locais

```bash
bash -n scripts/deploy-vps.sh
API_IMAGE=ghcr.io/example/wager-ledger:validation \
MIGRATION_IMAGE=ghcr.io/example/wager-ledger:validation \
PRODUCTION_ENV_FILE=/caminho/para/arquivo-de-exemplo \
docker compose -f docker-compose.yml --profile production config --quiet
```

Use um arquivo de exemplo sem segredos para validar a estrutura. Essa validação
não publica imagens, não aplica migrations e não acessa a VPS.
