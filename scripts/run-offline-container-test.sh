#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 1 ]; then echo "usage: $0 <image>" >&2; exit 2; fi
relio_image="$1"
relio_suffix="${GITHUB_RUN_ID:-local}-$$"
relio_network="relio-offline-$relio_suffix"
relio_postgres="relio-postgres-$relio_suffix"
relio_app="relio-app-$relio_suffix"
relio_volume="relio-data-$relio_suffix"
relio_workspace="$(pwd)"
relio_wrong_volume="relio-wrong-data-$relio_suffix"
relio_restart_state="$(mktemp /tmp/relio-restart-state.XXXXXX)"
# A fixed value keeps the failure output reproducible; production deployments
# generate this once with "openssl rand -hex 32".
relio_encryption_key="72656c696f2d6f66666c696e652d746573742d656e6372797074696f6e2d6b6579"

cleanup() {
  docker rm -f "$relio_app" "$relio_postgres" >/dev/null 2>&1 || true
  docker network rm "$relio_network" >/dev/null 2>&1 || true
  docker volume rm "$relio_volume" "$relio_wrong_volume" >/dev/null 2>&1 || true
  rm -f "$relio_restart_state"
}
trap cleanup EXIT

# Images are pulled before the internal network is created. Both application
# containers then execute on a network with no external route.
docker pull postgres:17-bookworm >/dev/null
docker pull python:3.13-slim >/dev/null
docker network create --internal "$relio_network" >/dev/null
docker run -d --name "$relio_postgres" --network "$relio_network" \
  -e POSTGRES_DB=relio -e POSTGRES_USER=relio -e POSTGRES_PASSWORD=offline-test-password \
  postgres:17-bookworm >/dev/null

for attempt in $(seq 1 60); do
  if docker exec "$relio_postgres" pg_isready -U relio -d relio >/dev/null 2>&1; then break; fi
  if [ "$attempt" -eq 60 ]; then docker logs "$relio_postgres"; exit 1; fi
  sleep 1
done

docker volume create "$relio_volume" >/dev/null
docker run -d --name "$relio_app" --network "$relio_network" \
  -e POSTGRES_DSN="postgres://relio:offline-test-password@$relio_postgres:5432/relio?sslmode=disable" \
  -e BOOTSTRAP_ADMIN="admin" \
  -e BOOTSTRAP_ADMIN_PASSWORD="ChangeMe-Relio-2026" \
  -v "$relio_volume:/var/lib/relio" \
  "$relio_image" >/dev/null

for attempt in $(seq 1 60); do
  if docker exec "$relio_app" /usr/local/bin/relio healthcheck >/dev/null 2>&1; then break; fi
  if [ "$attempt" -eq 60 ]; then docker logs "$relio_app"; exit 1; fi
  sleep 1
done

if ! docker run --rm --network "$relio_network" \
  --mount "type=bind,src=$relio_workspace/scripts/offline-smoke.py,dst=/offline-smoke.py,readonly" \
  python:3.13-slim python /offline-smoke.py "http://$relio_app:8080" admin ChangeMe-Relio-2026; then
  docker logs "$relio_app"
  exit 1
fi

if docker logs "$relio_app" 2>&1 | grep -Eqi 'download|telemetry|license check'; then
  echo "Unexpected runtime download/telemetry activity" >&2
  exit 1
fi

# Store an encrypted OIDC Client Secret and an HMAC Personal Key, replace the
# application container, and prove both remain usable with the same volume.
docker run --rm --network "$relio_network" \
  --mount "type=bind,src=$relio_workspace/scripts/restart-persistence.py,dst=/restart-persistence.py,readonly" \
  --mount "type=bind,src=$relio_restart_state,dst=/restart-state.json" \
  python:3.13-slim python /restart-persistence.py setup "http://$relio_app:8080" admin Relio-Smoke-Password-2026 /restart-state.json
docker rm -f "$relio_app" >/dev/null
docker run -d --name "$relio_app" --network "$relio_network" \
  -e POSTGRES_DSN="postgres://relio:offline-test-password@$relio_postgres:5432/relio?sslmode=disable" \
  -e BOOTSTRAP_ADMIN="admin" \
  -e BOOTSTRAP_ADMIN_PASSWORD="ChangeMe-Relio-2026" \
  -v "$relio_volume:/var/lib/relio" \
  "$relio_image" >/dev/null
for attempt in $(seq 1 60); do
  if docker exec "$relio_app" /usr/local/bin/relio healthcheck >/dev/null 2>&1; then break; fi
  if [ "$attempt" -eq 60 ]; then docker logs "$relio_app"; exit 1; fi
  sleep 1
done
docker run --rm --network "$relio_network" \
  --mount "type=bind,src=$relio_workspace/scripts/restart-persistence.py,dst=/restart-persistence.py,readonly" \
  --mount "type=bind,src=$relio_restart_state,dst=/restart-state.json,readonly" \
  python:3.13-slim python /restart-persistence.py verify "http://$relio_app:8080" admin Relio-Smoke-Password-2026 /restart-state.json

# Reusing PostgreSQL with a different/empty volume must fail closed instead of
# generating a replacement key that silently invalidates every credential.
docker rm -f "$relio_app" >/dev/null
docker volume create "$relio_wrong_volume" >/dev/null
docker run -d --name "$relio_app" --network "$relio_network" \
  -e POSTGRES_DSN="postgres://relio:offline-test-password@$relio_postgres:5432/relio?sslmode=disable" \
  -e BOOTSTRAP_ADMIN="admin" \
  -e BOOTSTRAP_ADMIN_PASSWORD="ChangeMe-Relio-2026" \
  -v "$relio_wrong_volume:/var/lib/relio" \
  "$relio_image" >/dev/null
for attempt in $(seq 1 20); do
  relio_state="$(docker inspect "$relio_app" --format '{{.State.Status}}')"
  if [ "$relio_state" = "exited" ] || [ "$relio_state" = "dead" ]; then break; fi
  if [ "$attempt" -eq 20 ]; then echo "Relio accepted a mismatched data volume" >&2; docker logs "$relio_app"; exit 1; fi
  sleep 1
done
if ! docker logs "$relio_app" 2>&1 | grep -q 'instance master key recovery required'; then
  echo "Missing actionable Master Key recovery error" >&2
  docker logs "$relio_app"
  exit 1
fi

# A non-empty volume with another valid 32-byte key must also be rejected by
# the DB fingerprint comparison, not merely by the missing-file guard.
docker rm -f "$relio_app" >/dev/null
docker run --rm --entrypoint sh -v "$relio_wrong_volume:/var/lib/relio" "$relio_image" \
  -c 'head -c 32 /dev/urandom > /var/lib/relio/secrets/master.key && chmod 600 /var/lib/relio/secrets/master.key'
docker run -d --name "$relio_app" --network "$relio_network" \
  -e POSTGRES_DSN="postgres://relio:offline-test-password@$relio_postgres:5432/relio?sslmode=disable" \
  -e BOOTSTRAP_ADMIN="admin" \
  -e BOOTSTRAP_ADMIN_PASSWORD="ChangeMe-Relio-2026" \
  -v "$relio_wrong_volume:/var/lib/relio" \
  "$relio_image" >/dev/null
for attempt in $(seq 1 20); do
  relio_state="$(docker inspect "$relio_app" --format '{{.State.Status}}')"
  if [ "$relio_state" = "exited" ] || [ "$relio_state" = "dead" ]; then break; fi
  if [ "$attempt" -eq 20 ]; then echo "Relio accepted a different Master Key" >&2; docker logs "$relio_app"; exit 1; fi
  sleep 1
done
if ! docker logs "$relio_app" 2>&1 | grep -q 'presented key cannot open it'; then
  echo "Missing actionable Master Key mismatch error" >&2
  docker logs "$relio_app"
  exit 1
fi

# Adopting ENCRYPTION_KEY while the original volume is still attached must
# re-wrap the same data key, so nothing has to be re-issued.
docker rm -f "$relio_app" >/dev/null
docker run -d --name "$relio_app" --network "$relio_network" \
  -e POSTGRES_DSN="postgres://relio:offline-test-password@$relio_postgres:5432/relio?sslmode=disable" \
  -e BOOTSTRAP_ADMIN="admin" \
  -e BOOTSTRAP_ADMIN_PASSWORD="ChangeMe-Relio-2026" \
  -e ENCRYPTION_KEY="$relio_encryption_key" \
  -v "$relio_volume:/var/lib/relio" \
  "$relio_image" >/dev/null
for attempt in $(seq 1 60); do
  if docker exec "$relio_app" /usr/local/bin/relio healthcheck >/dev/null 2>&1; then break; fi
  if [ "$attempt" -eq 60 ]; then docker logs "$relio_app"; exit 1; fi
  sleep 1
done
if ! docker logs "$relio_app" 2>&1 | grep -q '"adopted":true'; then
  echo "ENCRYPTION_KEY adoption was not recorded" >&2
  docker logs "$relio_app"
  exit 1
fi
docker run --rm --network "$relio_network" \
  --mount "type=bind,src=$relio_workspace/scripts/restart-persistence.py,dst=/restart-persistence.py,readonly" \
  --mount "type=bind,src=$relio_restart_state,dst=/restart-state.json,readonly" \
  python:3.13-slim python /restart-persistence.py verify "http://$relio_app:8080" admin Relio-Smoke-Password-2026 /restart-state.json portable

# The point of ENCRYPTION_KEY: destroy the data volume entirely and start with a
# brand new one. The Personal Key and the SSO Client Secret must still work.
docker rm -f "$relio_app" >/dev/null
docker volume rm "$relio_volume" >/dev/null
docker volume create "$relio_volume" >/dev/null
docker run -d --name "$relio_app" --network "$relio_network" \
  -e POSTGRES_DSN="postgres://relio:offline-test-password@$relio_postgres:5432/relio?sslmode=disable" \
  -e BOOTSTRAP_ADMIN="admin" \
  -e BOOTSTRAP_ADMIN_PASSWORD="ChangeMe-Relio-2026" \
  -e ENCRYPTION_KEY="$relio_encryption_key" \
  -v "$relio_volume:/var/lib/relio" \
  "$relio_image" >/dev/null
for attempt in $(seq 1 60); do
  if docker exec "$relio_app" /usr/local/bin/relio healthcheck >/dev/null 2>&1; then break; fi
  if [ "$attempt" -eq 60 ]; then echo "Relio refused to start from ENCRYPTION_KEY alone" >&2; docker logs "$relio_app"; exit 1; fi
  sleep 1
done
docker run --rm --network "$relio_network" \
  --mount "type=bind,src=$relio_workspace/scripts/restart-persistence.py,dst=/restart-persistence.py,readonly" \
  --mount "type=bind,src=$relio_restart_state,dst=/restart-state.json,readonly" \
  python:3.13-slim python /restart-persistence.py verify "http://$relio_app:8080" admin Relio-Smoke-Password-2026 /restart-state.json portable

# A wrong ENCRYPTION_KEY must fail closed rather than mint a replacement key.
docker rm -f "$relio_app" >/dev/null
docker run -d --name "$relio_app" --network "$relio_network" \
  -e POSTGRES_DSN="postgres://relio:offline-test-password@$relio_postgres:5432/relio?sslmode=disable" \
  -e BOOTSTRAP_ADMIN="admin" \
  -e BOOTSTRAP_ADMIN_PASSWORD="ChangeMe-Relio-2026" \
  -e ENCRYPTION_KEY="0000000000000000000000000000000000000000000000000000000000000000" \
  -v "$relio_volume:/var/lib/relio" \
  "$relio_image" >/dev/null
for attempt in $(seq 1 20); do
  relio_state="$(docker inspect "$relio_app" --format '{{.State.Status}}')"
  if [ "$relio_state" = "exited" ] || [ "$relio_state" = "dead" ]; then break; fi
  if [ "$attempt" -eq 20 ]; then echo "Relio accepted a wrong ENCRYPTION_KEY" >&2; docker logs "$relio_app"; exit 1; fi
  sleep 1
done
if ! docker logs "$relio_app" 2>&1 | grep -q 'presented key cannot open it'; then
  echo "Missing actionable ENCRYPTION_KEY mismatch error" >&2
  docker logs "$relio_app"
  exit 1
fi

# Dropping ENCRYPTION_KEY once the data key is wrapped by it must also fail
# closed, because the volume no longer holds anything that can open the key.
docker rm -f "$relio_app" >/dev/null
docker run -d --name "$relio_app" --network "$relio_network" \
  -e POSTGRES_DSN="postgres://relio:offline-test-password@$relio_postgres:5432/relio?sslmode=disable" \
  -e BOOTSTRAP_ADMIN="admin" \
  -e BOOTSTRAP_ADMIN_PASSWORD="ChangeMe-Relio-2026" \
  -v "$relio_volume:/var/lib/relio" \
  "$relio_image" >/dev/null
for attempt in $(seq 1 20); do
  relio_state="$(docker inspect "$relio_app" --format '{{.State.Status}}')"
  if [ "$relio_state" = "exited" ] || [ "$relio_state" = "dead" ]; then break; fi
  if [ "$attempt" -eq 20 ]; then echo "Relio started without the ENCRYPTION_KEY that wraps its data key" >&2; docker logs "$relio_app"; exit 1; fi
  sleep 1
done
if ! docker logs "$relio_app" 2>&1 | grep -q 'wrapped by ENCRYPTION_KEY'; then
  echo "Missing actionable missing-ENCRYPTION_KEY error" >&2
  docker logs "$relio_app"
  exit 1
fi

echo "Relio credential continuity test passed"
