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
echo "Static assets are self-contained"
