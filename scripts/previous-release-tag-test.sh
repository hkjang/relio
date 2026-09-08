#!/usr/bin/env bash
set -euo pipefail

# Covers previous-release-tag.sh, which decides which image the release workflow
# upgrades from. The case that broke four releases in a row is "the previous tag
# was never released": the answer has to be the newest tag that really was.

script="$(cd "$(dirname "$0")" && pwd)/previous-release-tag.sh"
workspace="$(mktemp -d /tmp/relio-previous-release.XXXXXX)"
trap 'rm -rf "$workspace"' EXIT

export GITHUB_REPOSITORY="hkjang/relio"
export PATH="$workspace/bin:$PATH"
mkdir -p "$workspace/bin" "$workspace/releases"

# A stand-in for `gh release view` that answers from one file per released tag,
# exactly as GitHub does: no file means the tag has no release.
cat >"$workspace/bin/gh" <<'STUB'
#!/usr/bin/env bash
tag="$3"
if [ ! -f "$RELIO_RELEASES/$tag" ]; then
  echo "release not found" >&2
  exit 1
fi
if [ "$(cat "$RELIO_RELEASES/$tag")" = "UNREACHABLE" ]; then
  echo "HTTP 503: the GitHub API is unavailable" >&2
  exit 1
fi
cat "$RELIO_RELEASES/$tag"
STUB
chmod +x "$workspace/bin/gh"
export RELIO_RELEASES="$workspace/releases"

repo="$workspace/repo"
git init -q "$repo"
git -C "$repo" -c user.email=test@relio.invalid -c user.name=test commit -q --allow-empty -m "release history"
for tag in v1.11.13 v1.11.14 v1.11.15 v1.11.16 v1.11.17 v1.11.18 v1.11.19; do
  git -C "$repo" tag "$tag"
done
cd "$repo"

failures=0
release() { printf 'relio-%s.tar.gz\n' "$1" >"$RELIO_RELEASES/$1"; }
unrelease() { rm -f "$RELIO_RELEASES/$1"; }

check() {
  local name="$1" expected="$2" current="$3" actual status=0
  actual="$("$script" "$current" 2>/dev/null)" || status=$?
  if [ "$status" -ne 0 ]; then
    echo "FAIL $name: exited $status, expected the tag ${expected:-<none>}" >&2
    failures=$((failures + 1))
    return
  fi
  if [ "$actual" != "$expected" ]; then
    echo "FAIL $name: chose ${actual:-<none>}, expected ${expected:-<none>}" >&2
    failures=$((failures + 1))
    return
  fi
  echo "ok   $name"
}

check_refuses() {
  local name="$1" current="$2" status=0
  "$script" "$current" >/dev/null 2>&1 || status=$?
  if [ "$status" -eq 0 ]; then
    echo "FAIL $name: succeeded where it had to refuse" >&2
    failures=$((failures + 1))
    return
  fi
  echo "ok   $name"
}

for tag in v1.11.13 v1.11.14 v1.11.15 v1.11.16 v1.11.17 v1.11.18; do release "$tag"; done
check "the previous release is the previous tag when it was published" v1.11.18 v1.11.19

# The state that failed four releases in a row: v1.11.15 stopped before
# publishing and v1.11.16 onwards inherited the failure, so every tag after
# v1.11.14 exists without a release.
for tag in v1.11.15 v1.11.16 v1.11.17 v1.11.18; do unrelease "$tag"; done
check "an unreleased tag is skipped for the newest tag that was released" v1.11.14 v1.11.19
check "the upgrade source of the first broken release is still found" v1.11.14 v1.11.16

# A release that exists but published no image is no better an upgrade source
# than a tag with no release at all.
printf 'checksums.txt\n' >"$RELIO_RELEASES/v1.11.18"
check "a release without the image asset is skipped" v1.11.14 v1.11.19
unrelease v1.11.18

# Re-running an older tag must not upgrade from a release made after it.
check "a later release is never chosen as the upgrade source" v1.11.13 v1.11.14

for tag in v1.11.13 v1.11.14; do unrelease "$tag"; done
check "the very first release has nothing to upgrade from" "" v1.11.19

# An unreadable lookup must stop the release rather than quietly pick an older
# image or claim there is no previous release at all.
release v1.11.14
printf 'UNREACHABLE\n' >"$RELIO_RELEASES/v1.11.18"
check_refuses "an unreadable release stops the search" v1.11.19
unrelease v1.11.18

check_refuses "a tag missing from the checkout is refused" v9.9.9

if [ "$failures" -ne 0 ]; then
  echo "$failures previous release selection test(s) failed" >&2
  exit 1
fi
echo "Previous release selection verified: the upgrade source is the newest tag that was actually released"
