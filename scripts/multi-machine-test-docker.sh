#!/usr/bin/env bash
# Roda scripts/multi-machine-test.sh distribuído de verdade por SSH, usando
# 3 containers Docker isolados (processo, rede e filesystem próprios) como
# hosts — sem exigir máquinas físicas extras nem configuração manual de SSH.
# É o caminho "clonou o projeto, já funciona" citado no README; para
# distribuir em máquinas físicas de verdade, use scripts/multi-machine-test.sh
# diretamente com HOSTS apontando para SSH reais.
#
# Requer a stack base já no ar (docker compose up -d) e Docker Compose v2.
#
# Uso:
#   scripts/multi-machine-test-docker.sh [idempotency|dispute|both] [N]
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

fail() { printf '[multi-machine-test-docker] ERRO: %s\n' "$*" >&2; exit 1; }
log() { printf '[multi-machine-test-docker] %s\n' "$*" >&2; }

command -v docker >/dev/null || fail "docker não encontrado"
docker compose version >/dev/null 2>&1 || fail "docker compose (v2) não encontrado"

KEY_DIR=".mmt-ssh"
KEY_FILE="$KEY_DIR/id_ed25519"

if [ ! -f "$KEY_FILE" ]; then
  log "gerando par de chaves SSH efêmero em $KEY_DIR (git-ignorado, só para este teste local)"
  mkdir -p "$KEY_DIR"
  ssh-keygen -t ed25519 -N '' -C 'multi-machine-test' -f "$KEY_FILE" -q
  cp "$KEY_FILE.pub" "$KEY_DIR/authorized_keys"
fi

log "subindo os 3 hosts simulados (profile multi-machine)..."
docker compose --profile multi-machine up -d --build mmt-host1 mmt-host2 mmt-host3

log "aguardando sshd nos 3 hosts..."
for host in mmt-host1 mmt-host2 mmt-host3; do
  for _ in $(seq 1 30); do
    docker compose exec -T "$host" true >/dev/null 2>&1 && break
    sleep 1
  done
done

API_URL="${API_URL:-http://localhost:8080}"
KEYCLOAK_URL="${KEYCLOAK_URL:-http://localhost:8081}"

export API_URL KEYCLOAK_URL
export REMOTE_API_URL="http://api:8080"
# ssh:// com porta explícita porque os 3 hosts estão todos em 127.0.0.1,
# cada um numa porta publicada diferente (ssh não aceita "user@host:porta"
# como destino posicional — só a forma de URI abaixo, ou -p separado, que
# não daria pra variar por host dentro de um único SSH_OPTS global).
export HOSTS="ssh://tester@localhost:2221 ssh://tester@localhost:2222 ssh://tester@localhost:2223"
export SSH_OPTS="-i $KEY_FILE -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR"

log "hosts prontos, delegando para scripts/multi-machine-test.sh $*"
exec ./scripts/multi-machine-test.sh "$@"
