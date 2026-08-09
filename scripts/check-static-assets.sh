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
echo "Static assets are self-contained"
