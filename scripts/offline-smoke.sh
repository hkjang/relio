#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 3 ]; then
  echo "usage: $0 <base-url> <bootstrap-admin> <bootstrap-password>" >&2
  exit 2
fi

relio_url="${1%/}"
relio_admin="$2"
relio_initial_password="$3"
relio_cookie_file="$(mktemp)"
relio_support_file="$(mktemp)"
trap 'rm -f "$relio_cookie_file" "$relio_support_file"' EXIT

for attempt in $(seq 1 60); do
  if curl --fail --silent "$relio_url/health/ready" >/dev/null; then break; fi
  if [ "$attempt" -eq 60 ]; then echo "Relio readiness timed out" >&2; exit 1; fi
  sleep 2
done

login_body="$(curl --fail --silent --show-error -c "$relio_cookie_file" \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"$relio_admin\",\"password\":\"$relio_initial_password\"}" \
  "$relio_url/api/v1/auth/login")"
csrf_token="$(printf '%s' "$login_body" | jq -r '.user.csrfToken')"
if [ -z "$csrf_token" ] || [ "$csrf_token" = null ]; then echo "Missing CSRF token" >&2; exit 1; fi

new_password="Relio-Smoke-Password-2026"
curl --fail --silent --show-error -b "$relio_cookie_file" -H "X-CSRF-Token: $csrf_token" \
  -H 'Content-Type: application/json' \
  -d "{\"currentPassword\":\"$relio_initial_password\",\"newPassword\":\"$new_password\"}" \
  "$relio_url/api/v1/me/password" >/dev/null

customer_body="$(curl --fail --silent --show-error -b "$relio_cookie_file" -H "X-CSRF-Token: $csrf_token" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Offline Verification Customer","customerType":"PROSPECT","industry":"Verification","customFields":{}}' \
  "$relio_url/api/v1/customers")"
customer_id="$(printf '%s' "$customer_body" | jq -r '.id')"
stage_id="$(curl --fail --silent --show-error -b "$relio_cookie_file" "$relio_url/api/v1/pipeline" | jq -r '.items[0].stages[0].id')"

curl --fail --silent --show-error -b "$relio_cookie_file" -H "X-CSRF-Token: $csrf_token" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"Offline Verification Deal\",\"customerId\":\"$customer_id\",\"stageId\":\"$stage_id\",\"expectedAmount\":100000000,\"customFields\":{}}" \
  "$relio_url/api/v1/opportunities" >/dev/null

key_body="$(curl --fail --silent --show-error -b "$relio_cookie_file" -H "X-CSRF-Token: $csrf_token" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Offline MCP Verification","scopes":["mcp:use","customer:read","opportunity:read","forecast:read"],"channels":["REST","MCP"]}' \
  "$relio_url/api/v1/me/keys")"
personal_key="$(printf '%s' "$key_body" | jq -r '.secret')"

curl --fail --silent --show-error -H "Authorization: Bearer $personal_key" \
  "$relio_url/api/v1/customers?limit=1" | jq -e '.items | type == "array"' >/dev/null
curl --fail --silent --show-error -H "Authorization: Bearer $personal_key" \
  -H 'Accept: application/json, text/event-stream' -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"offline-smoke","version":"1"}}}' \
  "$relio_url/mcp" | jq -e '.result.serverInfo.name == "Relio"' >/dev/null
curl --fail --silent --show-error -H "Authorization: Bearer $personal_key" \
  -H 'MCP-Protocol-Version: 2025-11-25' -H 'Accept: application/json, text/event-stream' -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_customers","arguments":{"query":"Offline Verification"}}}' \
  "$relio_url/mcp" | jq -e '.result.isError == false' >/dev/null

operations_body="$(curl --fail --silent --show-error -b "$relio_cookie_file" "$relio_url/api/v1/admin/operations")"
printf '%s' "$operations_body" | jq -e '
  .readinessScore >= 0 and .readinessScore <= 100 and
  (.diagnostics | map(.key) | index("postgresql") != null) and
  (.diagnostics | map(.key) | index("master-key") != null) and
  (.features.api == true) and (.features.mcp == true) and
  (.counts.activeUsers >= 1)' >/dev/null

curl --fail --silent --show-error -b "$relio_cookie_file" \
  "$relio_url/api/v1/admin/operations/support-bundle" > "$relio_support_file"
jq -e '.product == "Relio" and .operations.application.version != null and (.operations.diagnostics | length) >= 10' "$relio_support_file" >/dev/null
if grep -Fq "$relio_initial_password" "$relio_support_file"; then
  echo "Bootstrap password leaked into support bundle" >&2
  exit 1
fi

curl --fail --silent --show-error -b "$relio_cookie_file" \
  "$relio_url/api/v1/admin/audit?channel=ADMIN&q=SUPPORT_BUNDLE&limit=10" \
  | jq -e '.items | map(.action) | index("SUPPORT_BUNDLE_EXPORT") != null' >/dev/null

curl --fail --silent --show-error "$relio_url/api/openapi.json" \
  | jq -e '.paths["/admin/operations/support-bundle"].get != null' >/dev/null

if curl --fail --silent "$relio_url/app/dashboard" | grep -Eiq "<(script|link|img)[^>]+(src|href)=['\"]https?://"; then
  echo "External static asset reference detected" >&2
  exit 1
fi

curl --fail --silent "$relio_url/api/v1/system/version" | jq -e '.name == "Relio"' >/dev/null
echo "Relio offline smoke test passed"
