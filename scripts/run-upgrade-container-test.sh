#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 2 ]; then echo "usage: $0 <old-image> <new-image>" >&2; exit 2; fi
relio_old_image="$1"
relio_new_image="$2"
relio_upgrade_suffix="${GITHUB_RUN_ID:-local}-$$"
relio_upgrade_network="relio-upgrade-$relio_upgrade_suffix"
relio_upgrade_postgres="relio-upgrade-postgres-$relio_upgrade_suffix"
relio_upgrade_old="relio-upgrade-old-$relio_upgrade_suffix"
relio_upgrade_new="relio-upgrade-new-$relio_upgrade_suffix"
relio_upgrade_volume="relio-upgrade-data-$relio_upgrade_suffix"

relio_expected_new_schema="$(find migrations -maxdepth 1 -type f -name '*.sql' -printf '%f\n' | sort | tail -n 1)"
if [ -z "$relio_expected_new_schema" ]; then echo "no migration found" >&2; exit 2; fi

cleanup() {
  docker rm -f "$relio_upgrade_old" "$relio_upgrade_new" "$relio_upgrade_postgres" >/dev/null 2>&1 || true
  docker network rm "$relio_upgrade_network" >/dev/null 2>&1 || true
  docker volume rm "$relio_upgrade_volume" >/dev/null 2>&1 || true
}
trap cleanup EXIT

wait_for_app() {
  local container="$1"
  for attempt in $(seq 1 60); do
    if docker exec "$container" /usr/local/bin/relio healthcheck >/dev/null 2>&1; then return; fi
    if [ "$attempt" -eq 60 ]; then docker logs "$container"; exit 1; fi
    sleep 1
  done
}

docker pull postgres:17-bookworm >/dev/null
docker network create --internal "$relio_upgrade_network" >/dev/null
docker volume create "$relio_upgrade_volume" >/dev/null
docker run -d --name "$relio_upgrade_postgres" --network "$relio_upgrade_network" \
  -e POSTGRES_DB=relio -e POSTGRES_USER=relio -e POSTGRES_PASSWORD=upgrade-test-password \
  postgres:17-bookworm >/dev/null

for attempt in $(seq 1 60); do
  if docker exec "$relio_upgrade_postgres" pg_isready -U relio -d relio >/dev/null 2>&1; then break; fi
  if [ "$attempt" -eq 60 ]; then docker logs "$relio_upgrade_postgres"; exit 1; fi
  sleep 1
done

common_args=(
  --network "$relio_upgrade_network"
  -e "POSTGRES_DSN=postgres://relio:upgrade-test-password@$relio_upgrade_postgres:5432/relio?sslmode=disable"
  -e "BOOTSTRAP_ADMIN=admin"
  -e "BOOTSTRAP_ADMIN_PASSWORD=ChangeMe-Relio-Upgrade-2026"
  -v "$relio_upgrade_volume:/var/lib/relio"
)

docker run -d --name "$relio_upgrade_old" "${common_args[@]}" "$relio_old_image" >/dev/null
wait_for_app "$relio_upgrade_old"
relio_old_schema="$(docker exec "$relio_upgrade_postgres" psql -U relio -d relio -Atc "SELECT max(version) FROM schema_migrations")"
test -n "$relio_old_schema"
test "$(docker exec "$relio_upgrade_postgres" psql -U relio -d relio -Atc "SELECT count(*) FROM users WHERE is_bootstrap=true")" = "1"
docker rm -f "$relio_upgrade_old" >/dev/null

docker run -d --name "$relio_upgrade_new" "${common_args[@]}" "$relio_new_image" >/dev/null
wait_for_app "$relio_upgrade_new"
relio_new_schema="$(docker exec "$relio_upgrade_postgres" psql -U relio -d relio -Atc "SELECT max(version) FROM schema_migrations")"
test "$relio_new_schema" = "$relio_expected_new_schema"
test "$(docker exec "$relio_upgrade_postgres" psql -U relio -d relio -Atc "SELECT count(*) FROM users WHERE is_bootstrap=true")" = "1"
test "$(docker exec "$relio_upgrade_postgres" psql -U relio -d relio -Atc "SELECT count(*) FROM deal_health_rules WHERE active=true")" -ge 9
test "$(docker exec "$relio_upgrade_postgres" psql -U relio -d relio -Atc "SELECT count(*) FROM information_schema.columns WHERE table_name='contacts' AND column_name='relationship_role'")" = "1"

if [ "$relio_expected_new_schema" = "003_relationship_intelligence.sql" ]; then
  test "$(docker exec "$relio_upgrade_postgres" psql -U relio -d relio -Atc "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('contact_relationships','account_plans','opportunity_members')")" = "3"
  test "$(docker exec "$relio_upgrade_postgres" psql -U relio -d relio -Atc "SELECT count(*) FROM system_settings WHERE namespace='relationship_intelligence'")" = "3"
fi

echo "Relio upgrade test passed: $relio_old_schema -> $relio_new_schema"
