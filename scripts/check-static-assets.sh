#!/usr/bin/env bash
set -euo pipefail

if grep -RInE "<(script|link|img)[^>]+(src|href)=['\"]https?://" web/src web/index.html internal/webui/dist; then
  echo "External runtime asset reference detected" >&2
  exit 1
fi
if grep -RInE 'google-analytics|googletagmanager|cdnjs|jsdelivr|unpkg\.com|fonts\.googleapis' web/src web/index.html; then
  echo "Forbidden CDN, font, or analytics reference detected" >&2
  exit 1
fi
# Visitor analytics must stay opt-in: the loader is served from this origin and is
# empty until an administrator enables a provider.
if ! grep -q 'src="/analytics.js"' web/index.html; then
  echo "Analytics loader reference is missing from web/index.html" >&2
  exit 1
fi
# The frontend must never reference a tracker host itself. Placeholder text using
# reserved example domains is fine; a real vendor host is not. The vendor URL is
# assembled server-side in internal/analytics from admin configuration.
if grep -RInoE 'https?://[A-Za-z0-9.-]+' web/src web/index.html |
   grep -vE '(example\.(com|org|net)|example\.invalid|localhost|127\.0\.0\.1|www\.w3\.org|schema\.org)' |
   grep -E 'googletagmanager|google-analytics|plausible\.io|matomo\.|umami\.|segment\.|hotjar|mixpanel|amplitude'; then
  echo "A tracking vendor host is referenced by the frontend; it must come from admin configuration" >&2
  exit 1
fi
# `go:embed dist/*` must match a file before the web build has run, so one anchor
# is committed under dist/. The build empties that directory and copies web/public/
# back in, so the anchor has to be identical there or a build dirties the checkout.
anchor=internal/webui/dist/README
if ! git ls-files --error-unmatch "$anchor" >/dev/null 2>&1; then
  echo "$anchor must be committed so go:embed dist/* matches before the web build" >&2
  exit 1
fi
if ! cmp -s "$anchor" web/public/README; then
  echo "$anchor and web/public/README differ; the web build would rewrite the committed copy" >&2
  exit 1
fi
echo "Static assets are self-contained"
