#!/usr/bin/env bash
set -euo pipefail

KEYCLOAK_URL="${KEYCLOAK_URL:-http://localhost:8081}"
ADMIN_USER="${KEYCLOAK_ADMIN:-admin}"
ADMIN_PASSWORD="${KEYCLOAK_ADMIN_PASSWORD:-admin}"
REALM="wager-ledger"

echo "Aguardando Keycloak ficar disponível em $KEYCLOAK_URL ..."
until curl -sf "$KEYCLOAK_URL/realms/master" > /dev/null 2>&1; do
  sleep 2
done
echo "Keycloak disponível."

get_admin_token() {
  curl -sf -X POST "$KEYCLOAK_URL/realms/master/protocol/openid-connect/token" \
    -d "client_id=admin-cli" \
    -d "username=$ADMIN_USER" \
    -d "password=$ADMIN_PASSWORD" \
    -d "grant_type=password" \
    | jq -r '.access_token'
}

TOKEN="$(get_admin_token)"

api() {
  local method="$1" path="$2" data="${3:-}"
  if [ -n "$data" ]; then
    curl -sf -X "$method" "$KEYCLOAK_URL/admin$path" \
      -H "Authorization: Bearer $TOKEN" \
      -H "Content-Type: application/json" \
      -d "$data"
  else
    curl -sf -X "$method" "$KEYCLOAK_URL/admin$path" \
      -H "Authorization: Bearer $TOKEN"
  fi
}

echo "Criando realm '$REALM' (ignora erro se já existir)..."
api POST "/realms" "{\"realm\":\"$REALM\",\"enabled\":true}" > /dev/null 2>&1 || true

echo "Criando roles de realm 'provider' e 'internal'..."
api POST "/realms/$REALM/roles" '{"name":"provider"}' > /dev/null 2>&1 || true
api POST "/realms/$REALM/roles" '{"name":"internal"}' > /dev/null 2>&1 || true

echo "Criando client scope de audience 'wager-ledger-api-audience'..."
api POST "/realms/$REALM/client-scopes" '{
  "name": "wager-ledger-api-audience",
  "protocol": "openid-connect",
  "attributes": {"include.in.token.scope":"true","display.on.consent.screen":"false"}
}' > /dev/null 2>&1 || true

AUDIENCE_SCOPE_ID="$(api GET "/realms/$REALM/client-scopes" | jq -r '.[] | select(.name=="wager-ledger-api-audience") | .id')"

api POST "/realms/$REALM/client-scopes/$AUDIENCE_SCOPE_ID/protocol-mappers/models" '{
  "name": "wager-ledger-api-audience-mapper",
  "protocol": "openid-connect",
  "protocolMapper": "oidc-audience-mapper",
  "config": {
    "included.custom.audience": "wager-ledger-api",
    "id.token.claim": "false",
    "access.token.claim": "true"
  }
}' > /dev/null 2>&1 || true

echo "Tornando 'wager-ledger-api-audience' escopo padrão do realm..."
curl -sf -X PUT "$KEYCLOAK_URL/admin/realms/$REALM/default-default-client-scopes/$AUDIENCE_SCOPE_ID" \
  -H "Authorization: Bearer $TOKEN" > /dev/null 2>&1 || true

create_service_client() {
  local client_id="$1" secret="$2" role="$3"

  echo "Criando client '$client_id'..."
  api POST "/realms/$REALM/clients" "{
    \"clientId\": \"$client_id\",
    \"secret\": \"$secret\",
    \"serviceAccountsEnabled\": true,
    \"publicClient\": false,
    \"standardFlowEnabled\": false,
    \"directAccessGrantsEnabled\": false,
    \"protocol\": \"openid-connect\"
  }" > /dev/null 2>&1 || true

  local internal_id
  internal_id="$(api GET "/realms/$REALM/clients?clientId=$client_id" | jq -r '.[0].id')"

  local service_account_user_id
  service_account_user_id="$(api GET "/realms/$REALM/clients/$internal_id/service-account-user" | jq -r '.id')"

  local role_repr
  role_repr="$(api GET "/realms/$REALM/roles/$role")"

  echo "Atribuindo role '$role' ao client '$client_id'..."
  api POST "/realms/$REALM/users/$service_account_user_id/role-mappings/realm" "[$role_repr]" > /dev/null 2>&1 || true
}

create_service_client "provider-a" "provider-a-secret" "provider"
create_service_client "provider-b" "provider-b-secret" "provider"
create_service_client "wager-internal" "wager-internal-secret" "internal"

echo ""
echo "Provisionamento concluído. Exemplo de obtenção de token:"
echo ""
echo "curl -s -X POST $KEYCLOAK_URL/realms/$REALM/protocol/openid-connect/token \\"
echo "  -d client_id=provider-a \\"
echo "  -d client_secret=provider-a-secret \\"
echo "  -d grant_type=client_credentials | jq"