#!/usr/bin/env bash
# Executa os cenários de concorrência obrigatórios do README (§8/§13) com
# vários processos verdadeiramente independentes — cada um com sua própria
# conexão, memória e (opcionalmente) máquina.
#
# Uso:
#   scripts/multi-machine-test.sh idempotency [N]
#   scripts/multi-machine-test.sh dispute
#   scripts/multi-machine-test.sh both [N]
#
#   idempotency: envia a MESMA aposta N vezes em paralelo (N padrão 3) e
#                comprova um único débito — README §13 item 1/4.
#   dispute:     carteira com 100.00 BRL recebe duas apostas distintas de
#                80.00 BRL ao mesmo tempo — README §8 (teste obrigatório).
#
# Variáveis de ambiente:
#   API_URL                  default http://localhost:8080
#   KEYCLOAK_URL             default http://localhost:8081
#   REALM                    default wager-ledger
#   INTERNAL_CLIENT_ID       default wager-internal
#   INTERNAL_CLIENT_SECRET   default wager-internal-secret
#   PROVIDER_CLIENT_ID       default provider-a
#   PROVIDER_CLIENT_SECRET   default provider-a-secret
#   HOSTS                    lista separada por espaço de destinos SSH
#                            ("user@host1 user@host2 user@host3"), um por
#                            processo concorrente. Se vazio (padrão), cada
#                            processo roda localmente em background — ainda
#                            são processos de SO independentes (memória e
#                            conexão TCP próprias), só não em máquinas
#                            físicas diferentes. Com menos hosts que
#                            processos, os hosts são reaproveitados em
#                            rodízio.
#   SSH_OPTS                 opções extras para o ssh (ex.: "-i chave.pem")
#   REMOTE_API_URL           endpoint da API que os PROCESSOS enxergam ao
#                            enviar a aposta (default: o mesmo de API_URL).
#                            Só precisa ser diferente de API_URL quando o
#                            coordenador e os hosts de HOSTS não alcançam a
#                            API pelo mesmo endereço — ex.: hosts são
#                            containers Docker e falam "http://api:8080"
#                            enquanto o coordenador (no host) usa
#                            "http://localhost:8080".
#
# Requer curl e jq no coordenador; e bash no destino de cada processo
# (local ou, com HOSTS, em cada máquina via SSH). REMOTE_API_URL precisa ser
# alcançável a partir de cada host de HOSTS — para máquinas físicas de
# verdade na mesma rede, o docker-compose.yml deste projeto já expõe as
# portas 8080/8081 em todas as interfaces (não só 127.0.0.1), então
# "http://<ip-do-host-que-roda-docker-compose>:8080" funciona a partir de
# outra máquina na rede, se a porta estiver liberada no firewall.
#
# Não tem 3 máquinas físicas à mão? scripts/multi-machine-test-docker.sh
# sobe 3 containers Linux com sshd próprio (processo, rede e filesystem
# isolados entre si) e roda este script apontando HOSTS para eles — funciona
# só com `git clone` + Docker, sem precisar de hardware extra nem SSH
# configurado manualmente.

set -euo pipefail

fail() { printf '[multi-machine-test] ERRO: %s\n' "$*" >&2; exit 1; }
log() { printf '[multi-machine-test] %s\n' "$*" >&2; }

for bin in curl jq; do
  command -v "$bin" >/dev/null || fail "dependência ausente no coordenador: $bin"
done

SCENARIO="${1:-both}"
case "$SCENARIO" in
  idempotency|dispute|both) ;;
  *) fail "cenário inválido: $SCENARIO (use idempotency, dispute ou both)" ;;
esac

API_URL="${API_URL:-http://localhost:8080}"
KEYCLOAK_URL="${KEYCLOAK_URL:-http://localhost:8081}"
REALM="${REALM:-wager-ledger}"
INTERNAL_CLIENT_ID="${INTERNAL_CLIENT_ID:-wager-internal}"
INTERNAL_CLIENT_SECRET="${INTERNAL_CLIENT_SECRET:-wager-internal-secret}"
PROVIDER_CLIENT_ID="${PROVIDER_CLIENT_ID:-provider-a}"
PROVIDER_CLIENT_SECRET="${PROVIDER_CLIENT_SECRET:-provider-a-secret}"
# Endpoint que os PROCESSOS DISPATCHADOS (local em background, ou remoto via
# SSH em HOSTS) usam para enviar a aposta — pode diferir de API_URL quando o
# coordenador e os hosts não enxergam a API pelo mesmo endereço (ex.: hosts
# são containers na rede do docker compose e falam com "http://api:8080",
# enquanto o coordenador roda no host e usa "http://localhost:8080"). Se não
# for definido, cai no mesmo valor de API_URL (caso comum: tudo na mesma
# máquina).
REMOTE_API_URL="${REMOTE_API_URL:-$API_URL}"
SSH_OPTS="${SSH_OPTS:-}"
read -r -a HOSTS_ARR <<< "${HOSTS:-}"

WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

get_token() {
  local client_id="$1" secret="$2"
  curl -sf -X POST "$KEYCLOAK_URL/realms/$REALM/protocol/openid-connect/token" \
    -d "grant_type=client_credentials" -d "client_id=$client_id" -d "client_secret=$secret" \
    | jq -r .access_token
}

# dispatch idx outfile script
# Executa `script` (corpo de shell já com todas as variáveis substituídas —
# não depende de nada do ambiente remoto) como um processo independente:
# via SSH round-robin em HOSTS_ARR, ou localmente em background se HOSTS
# estiver vazio. A saída (stdout+stderr) é redirecionada para `outfile` no
# coordenador — isso funciona mesmo quando a execução é remota, porque a
# redireção `>` roda no processo `ssh` local, não na máquina remota.
dispatch() {
  local idx="$1" outfile="$2" script="$3"
  if [ "${#HOSTS_ARR[@]}" -eq 0 ]; then
    log "processo $idx -> local (background)"
    bash -c "$script" >"$outfile" 2>&1 &
    return
  fi
  local host="${HOSTS_ARR[$(( idx % ${#HOSTS_ARR[@]} ))]}"
  log "processo $idx -> host $host (ssh)"
  # shellcheck disable=SC2086
  ssh $SSH_OPTS "$host" 'bash -s' <<<"$script" >"$outfile" 2>&1 &
}

gen_uuid() {
  # Sempre minúsculo: o Postgres normaliza o tipo UUID para minúsculo ao
  # persistir, mas POST /wallets ecoa de volta o playerId exatamente como
  # foi enviado (não o valor persistido/normalizado) — enviar um UUID
  # maiúsculo (uuidgen no macOS gera maiúsculo) faria as requisições de
  # aposta seguintes serem rejeitadas com wallet_player_mismatch, porque a
  # comparação de playerId é sensível a caixa.
  (uuidgen 2>/dev/null || cat /proc/sys/kernel/random/uuid) | tr 'A-Z' 'a-z'
}

open_wallet() {
  local internal_token="$1" balance="$2"
  local player_id
  player_id=$(gen_uuid)
  curl -sf -X POST "$API_URL/wallets" \
    -H "Authorization: Bearer $internal_token" -H "Content-Type: application/json" \
    -d "{\"playerId\":\"$player_id\",\"initialBalance\":{\"amount\":\"$balance\",\"currency\":\"BRL\"}}"
}

bet_script() {
  # Monta o corpo do script remoto/local para uma requisição de aposta.
  # Todas as variáveis já são interpoladas aqui no coordenador — o script
  # gerado não referencia nada do ambiente de quem o executa.
  local provider_token="$1" player_id="$2" wallet_id="$3" external_id="$4" idem_key="$5" amount="$6"
  # Sem -f: uma rejeição de negócio (400/409, ex. saldo insuficiente ou
  # conflito de idempotência) ainda é uma resposta JSON válida que os
  # cenários abaixo precisam inspecionar — com -f o curl descartaria o
  # corpo e devolveria só um código de saída, escondendo o motivo real.
  cat <<SCRIPT
curl -s -X POST '$REMOTE_API_URL/wagering/transactions' \
  -H 'Authorization: Bearer $provider_token' -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: $idem_key' \
  -d '{"providerId":"$PROVIDER_CLIENT_ID","externalTransactionId":"$external_id","playerId":"$player_id","walletId":"$wallet_id","roundId":"round-multi-machine","gameId":"game-multi-machine","kind":"BET","money":{"amount":"$amount","currency":"BRL"}}'
SCRIPT
}

wallet_snapshot() {
  local internal_token="$1" wallet_id="$2"
  curl -sf "$API_URL/wallets/$wallet_id" -H "Authorization: Bearer $internal_token"
}

reconcile() {
  local internal_token="$1" wallet_id="$2"
  curl -sf -X POST "$API_URL/wallets/$wallet_id/reconciliation" -H "Authorization: Bearer $internal_token"
}

run_idempotency() {
  local n="${1:-3}"
  log "=== cenário idempotency (N=$n processos, mesma aposta) ==="

  local internal_token provider_token wallet_json wallet_id player_id
  internal_token=$(get_token "$INTERNAL_CLIENT_ID" "$INTERNAL_CLIENT_SECRET") || fail "falha ao obter token interno"
  provider_token=$(get_token "$PROVIDER_CLIENT_ID" "$PROVIDER_CLIENT_SECRET") || fail "falha ao obter token do provedor"

  wallet_json=$(open_wallet "$internal_token" "100.00") || fail "falha ao abrir carteira"
  wallet_id=$(echo "$wallet_json" | jq -r .id)
  player_id=$(echo "$wallet_json" | jq -r .playerId)
  log "carteira $wallet_id (player $player_id) aberta com 100.00 BRL"

  local run_id ext_id idem_key
  run_id=$(gen_uuid)
  ext_id="mmt-idem-$run_id"
  idem_key="$PROVIDER_CLIENT_ID:$ext_id"

  local i outfile script
  for ((i = 0; i < n; i++)); do
    outfile="$WORKDIR/idem-$i.json"
    script=$(bet_script "$provider_token" "$player_id" "$wallet_id" "$ext_id" "$idem_key" "30.00")
    dispatch "$i" "$outfile" "$script"
  done
  wait
  log "todos os $n processos concluídos, coletando resultados"

  local originals=0 replays=0
  for ((i = 0; i < n; i++)); do
    outfile="$WORKDIR/idem-$i.json"
    if ! jq -e . "$outfile" >/dev/null 2>&1; then
      fail "processo $i não retornou JSON válido — saída: $(cat "$outfile")"
    fi
    local replay
    replay=$(jq -r '.idempotentReplay' "$outfile")
    if [ "$replay" = "false" ]; then
      originals=$((originals + 1))
    else
      replays=$((replays + 1))
    fi
  done
  log "originais=$originals replays=$replays (esperado 1 original e $((n - 1)) replays)"

  local final balance
  final=$(wallet_snapshot "$internal_token" "$wallet_id")
  balance=$(echo "$final" | jq -r .balance.amount)
  log "saldo final: $balance (esperado 70.00 — um único débito de 30.00 sobre 100.00)"

  local recon consistent
  recon=$(reconcile "$internal_token" "$wallet_id")
  consistent=$(echo "$recon" | jq -r .consistent)

  if [ "$originals" -eq 1 ] && [ "$balance" = "70.00" ] && [ "$consistent" = "true" ]; then
    log "PASS: idempotency"
  else
    log "FAIL: idempotency (originais=$originals balance=$balance consistent=$consistent)"
    return 1
  fi
}

run_dispute() {
  log "=== cenário dispute (2 processos, apostas de 80.00 sobre saldo de 100.00) ==="

  local internal_token provider_token wallet_json wallet_id player_id
  internal_token=$(get_token "$INTERNAL_CLIENT_ID" "$INTERNAL_CLIENT_SECRET") || fail "falha ao obter token interno"
  provider_token=$(get_token "$PROVIDER_CLIENT_ID" "$PROVIDER_CLIENT_SECRET") || fail "falha ao obter token do provedor"

  wallet_json=$(open_wallet "$internal_token" "100.00") || fail "falha ao abrir carteira"
  wallet_id=$(echo "$wallet_json" | jq -r .id)
  player_id=$(echo "$wallet_json" | jq -r .playerId)
  log "carteira $wallet_id (player $player_id) aberta com 100.00 BRL"

  local run_id
  run_id=$(gen_uuid)

  local outfile_a="$WORKDIR/dispute-0.json" outfile_b="$WORKDIR/dispute-1.json"
  local script_a script_b
  script_a=$(bet_script "$provider_token" "$player_id" "$wallet_id" "mmt-dispute-a-$run_id" "$PROVIDER_CLIENT_ID:mmt-dispute-a-$run_id" "80.00")
  script_b=$(bet_script "$provider_token" "$player_id" "$wallet_id" "mmt-dispute-b-$run_id" "$PROVIDER_CLIENT_ID:mmt-dispute-b-$run_id" "80.00")
  dispatch 0 "$outfile_a" "$script_a"
  dispatch 1 "$outfile_b" "$script_b"
  wait
  log "os 2 processos concluídos, coletando resultados"

  local status_a status_b failure_a failure_b
  jq -e . "$outfile_a" >/dev/null 2>&1 || fail "processo 0 não retornou JSON válido — saída: $(cat "$outfile_a")"
  jq -e . "$outfile_b" >/dev/null 2>&1 || fail "processo 1 não retornou JSON válido — saída: $(cat "$outfile_b")"
  status_a=$(jq -r .status "$outfile_a"); status_b=$(jq -r .status "$outfile_b")
  failure_a=$(jq -r '.failureCode // ""' "$outfile_a"); failure_b=$(jq -r '.failureCode // ""' "$outfile_b")
  log "processo 0: status=$status_a failureCode=$failure_a"
  log "processo 1: status=$status_b failureCode=$failure_b"

  local processed=0 rejected=0
  for s in "$status_a" "$status_b"; do
    [ "$s" = "PROCESSED" ] && processed=$((processed + 1))
    [ "$s" = "REJECTED" ] && rejected=$((rejected + 1))
  done

  local final balance
  final=$(wallet_snapshot "$internal_token" "$wallet_id")
  balance=$(echo "$final" | jq -r .balance.amount)
  log "saldo final: $balance (esperado 20.00)"

  local recon consistent
  recon=$(reconcile "$internal_token" "$wallet_id")
  consistent=$(echo "$recon" | jq -r .consistent)

  if [ "$processed" -eq 1 ] && [ "$rejected" -eq 1 ] && [ "$balance" = "20.00" ] && [ "$consistent" = "true" ]; then
    log "PASS: dispute"
  else
    log "FAIL: dispute (processed=$processed rejected=$rejected balance=$balance consistent=$consistent)"
    return 1
  fi
}

status=0
case "$SCENARIO" in
  idempotency) run_idempotency "${2:-3}" || status=1 ;;
  dispute) run_dispute || status=1 ;;
  both)
    run_idempotency "${2:-3}" || status=1
    run_dispute || status=1
    ;;
esac

exit "$status"
