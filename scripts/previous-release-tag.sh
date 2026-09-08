#!/usr/bin/env bash
set -euo pipefail

# Prints the tag of the newest release the current tag can be upgraded from, or
# nothing at all when no earlier release was ever published.
#
# The previous tag is not the previous release. A tag whose release workflow
# stopped before "Publish GitHub Release" leaves a tag behind that has no
# release and no image to load, so treating it as the upgrade source asks for a
# download that can never succeed. That failure then skips publishing in turn,
# which is why one broken release blocks every release after it: v1.11.15 failed
# in "Test source", and v1.11.16, v1.11.17 and v1.11.18 each failed one second
# into "Verify upgrade from previous release" on an asset that does not exist.
#
# Walking back to the newest tag that really was released keeps the upgrade test
# running against a real image instead of skipping it.

if [ "$#" -ne 1 ]; then echo "usage: $0 <current-tag>" >&2; exit 2; fi
current_tag="$1"
repository="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY must name the repository that holds the releases}"

if ! git tag --sort=-v:refname | grep -qxF "$current_tag"; then
  echo "$current_tag is not a tag in this checkout, so the releases before it cannot be identified" >&2
  exit 1
fi

# published_assets prints the asset names of the release for a tag. Exit code 1
# means the release does not exist and is the only reason to keep looking; any
# other failure is reported rather than read as a missing release, because
# silently falling back to an older release would hide a broken lookup.
published_assets() {
  local tag="$1" output status
  set +e
  output="$(gh release view "$tag" --repo "$repository" --json assets --jq '.assets[].name' 2>&1)"
  status=$?
  set -e
  if [ "$status" -eq 0 ]; then
    printf '%s\n' "$output"
    return 0
  fi
  case "$output" in
  *"release not found"* | *"Not Found"* | *"HTTP 404"*) return 1 ;;
  esac
  printf '%s\n' "$output" >&2
  return 2
}

# Tags older than the current one, newest first. Anything sorted above the
# current tag is a later release and must never become the upgrade source.
older_tags="$(git tag --sort=-v:refname | awk -v current="$current_tag" 'found { print } $0 == current { found = 1 }')"

previous_tag=""
while IFS= read -r tag; do
  [ -n "$tag" ] || continue
  status=0
  assets="$(published_assets "$tag")" || status=$?
  if [ "$status" -eq 1 ]; then
    echo "$tag was tagged but never released; looking further back" >&2
    continue
  fi
  if [ "$status" -ne 0 ]; then
    echo "cannot read the release of $tag" >&2
    exit 1
  fi
  if printf '%s\n' "$assets" | grep -qxF "relio-${tag}.tar.gz"; then
    previous_tag="$tag"
    break
  fi
  echo "the release of $tag carries no relio-${tag}.tar.gz image; looking further back" >&2
done <<<"$older_tags"

if [ -n "$previous_tag" ]; then
  printf '%s\n' "$previous_tag"
fi
