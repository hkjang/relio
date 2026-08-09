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

cleanup() {
  docker rm -f "$relio_app" "$relio_postgres" >/dev/null 2>&1 || true
  docker network rm "$relio_network" >/dev/null 2>&1 || true
  docker volume rm "$relio_volume" >/dev/null 2>&1 || true
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
