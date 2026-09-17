#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

fail() { printf '%s\n' "$*" >&2; exit 1; }
[[ $# == 2 ]] || fail 'Uso: bash scripts/deploy-vps.sh API_IMAGE MIGRATION_IMAGE'
[[ $1 =~ ^ghcr\.io/[a-z0-9._/-]+@sha256:[0-9a-f]{64}$ ]] || fail 'API_IMAGE deve usar GHCR e digest sha256 completo.'
[[ $2 =~ ^ghcr\.io/[a-z0-9._/-]+@sha256:[0-9a-f]{64}$ ]] || fail 'MIGRATION_IMAGE deve usar GHCR e digest sha256 completo.'
export API_IMAGE=$1 MIGRATION_IMAGE=$2
DEPLOY_ROOT=${DEPLOY_ROOT:-/opt/wager-ledger}
[[ $DEPLOY_ROOT == /* ]] || fail 'DEPLOY_ROOT deve ser absoluto.'
shared="$DEPLOY_ROOT/shared"
compose_file="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)/docker-compose.yml"
export PRODUCTION_ENV_FILE="$shared/.env.production"
for executable in docker curl flock; do
  command -v "$executable" >/dev/null || fail "Dependência ausente: $executable"
done
[[ -r $PRODUCTION_ENV_FILE ]] || fail "Configure $PRODUCTION_ENV_FILE antes do deploy."
[[ -f $compose_file ]] || fail 'Compose de produção ausente.'
exec 9>"$shared/deploy.lock"
flock -n 9 || fail 'Outro deploy está em execução na VPS.'
docker compose version >/dev/null
compose() { docker compose --project-name wager-ledger-production --file "$compose_file" --profile production "$@"; }
compose config --quiet

previous_api=''
previous_migration=''
previous_compose=''
if [[ -f $shared/current-release ]]; then
  { IFS= read -r previous_api; IFS= read -r previous_migration; IFS= read -r previous_compose; } < "$shared/current-release"
  [[ $previous_api =~ ^ghcr\.io/[a-z0-9._/-]+@sha256:[0-9a-f]{64}$ && $previous_migration =~ ^ghcr\.io/[a-z0-9._/-]+@sha256:[0-9a-f]{64}$ && -f $previous_compose ]] || fail 'Estado anterior inválido; corrija antes de publicar.'
fi

healthy() {
  local attempt
  for ((attempt=0; attempt<30; attempt++)); do
    if curl --fail --silent --output /dev/null --max-time 2 http://127.0.0.1:8080/health/live &&
       curl --fail --silent --output /dev/null --max-time 2 http://127.0.0.1:8080/health/ready; then
      return 0
    fi
    sleep 2
  done
  return 1
}

rollback() {
  if [[ -n $previous_api ]]; then
    printf '%s\n' 'Restaurando a imagem anterior da API; migrations não serão revertidas.' >&2
    API_IMAGE=$previous_api MIGRATION_IMAGE=$previous_migration
    compose_file=$previous_compose
    if compose up -d --no-deps api-production && healthy; then
      printf '%s\n' 'API anterior restaurada.' >&2
    else
      printf '%s\n' 'Rollback falhou: intervenção na VPS necessária.' >&2
    fi
  else
    compose stop api-production >/dev/null 2>&1 || true
    printf '%s\n' 'Primeiro deploy falhou; API interrompida, sem versão anterior para restaurar.' >&2
  fi
}

printf '%s\n' 'Baixando imagens e aplicando migrations.'
compose pull api-production migrate-production
# Não publique a saída do migrator: erros podem incluir a URL do banco.
if ! compose run --rm --no-deps migrate-production >/dev/null 2>&1; then
  fail 'Migration falhou; API anterior preservada. Investigue o banco na VPS sem expor credenciais.'
fi
if ! compose up -d --no-deps api-production || ! healthy; then
  rollback
  fail 'Deploy reprovado pela inicialização ou pelos health checks.'
fi
printf '%s\n' "$API_IMAGE" "$MIGRATION_IMAGE" "$compose_file" > "$shared/current-release.tmp"
mv -- "$shared/current-release.tmp" "$shared/current-release"
printf '%s\n' 'Deploy concluído; estado da versão atualizado.'
