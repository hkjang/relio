#!/usr/bin/env bash
set -euo pipefail

allowed='POSTGRES_DSN|BOOTSTRAP_ADMIN|BOOTSTRAP_ADMIN_PASSWORD'
unexpected_files="$(grep -RIlE --include='*.go' 'os\.(Getenv|LookupEnv)\(' . | grep -v '^./internal/config/config.go$' || true)"
if [ -n "$unexpected_files" ]; then
  echo "Environment access is only allowed in internal/config/config.go:" >&2
  printf '%s\n' "$unexpected_files" >&2
  exit 1
fi
found="$(grep -RohE --include='*.go' 'os\.Getenv\("[A-Z][A-Z0-9_]*"\)|os\.LookupEnv\("[A-Z][A-Z0-9_]*"\)' . || true)"
if [ -n "$found" ]; then
  invalid="$(printf '%s\n' "$found" | sed -E 's/.*\("([A-Z][A-Z0-9_]*)"\).*/\1/' | grep -Ev "^($allowed)$" || true)"
  if [ -n "$invalid" ]; then
    echo "Unsupported application environment variables:" >&2
    printf '%s\n' "$invalid" >&2
    exit 1
  fi
fi

for required in POSTGRES_DSN BOOTSTRAP_ADMIN BOOTSTRAP_ADMIN_PASSWORD; do
  grep -q "\"$required\"" internal/config/config.go || { echo "Missing $required" >&2; exit 1; }
done
echo "Application environment contract verified: exactly three configuration variables"
