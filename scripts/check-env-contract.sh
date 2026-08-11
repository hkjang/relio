#!/usr/bin/env bash
set -euo pipefail

# Three variables are required to bootstrap and ENCRYPTION_KEY is the optional
# wrapping key that keeps credentials valid when the data volume is recreated.
allowed='POSTGRES_DSN|BOOTSTRAP_ADMIN|BOOTSTRAP_ADMIN_PASSWORD|ENCRYPTION_KEY'
# The contract covers the shipped binary. Test files may read their own opt-in
# variables (for example an integration test DSN) without widening it.
unexpected_files="$(grep -RIlE --include='*.go' --exclude='*_test.go' 'os\.(Getenv|LookupEnv)\(' . | grep -v '^./internal/config/config.go$' || true)"
if [ -n "$unexpected_files" ]; then
  echo "Environment access is only allowed in internal/config/config.go:" >&2
  printf '%s\n' "$unexpected_files" >&2
  exit 1
fi
found="$(grep -RohE --include='*.go' --exclude='*_test.go' 'os\.Getenv\("[A-Z][A-Z0-9_]*"\)|os\.LookupEnv\("[A-Z][A-Z0-9_]*"\)' . || true)"
if [ -n "$found" ]; then
  invalid="$(printf '%s\n' "$found" | sed -E 's/.*\("([A-Z][A-Z0-9_]*)"\).*/\1/' | grep -Ev "^($allowed)$" || true)"
  if [ -n "$invalid" ]; then
    echo "Unsupported application environment variables:" >&2
    printf '%s\n' "$invalid" >&2
    exit 1
  fi
fi

for required in POSTGRES_DSN BOOTSTRAP_ADMIN BOOTSTRAP_ADMIN_PASSWORD ENCRYPTION_KEY; do
  grep -q "\"$required\"" internal/config/config.go || { echo "Missing $required" >&2; exit 1; }
done
echo "Application environment contract verified: three required variables and the optional ENCRYPTION_KEY"
