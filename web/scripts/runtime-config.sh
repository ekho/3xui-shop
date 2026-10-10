#!/bin/sh
set -eu
export LC_ALL=C
output="${WEB_PUBLIC_CONFIG_DIR:-/run/web-public}/config.json"
terms_version="${TERMS_VERSION:-}"
privacy_version="${PRIVACY_VERSION:-}"
terms_url="${TERMS_URL:-}"
privacy_url="${PRIVACY_URL:-}"
support_url="${SUPPORT_URL:-}"
product_name="${PRODUCT_NAME:-}"
if [ "${#terms_version}" -gt 128 ] || [ "${#privacy_version}" -gt 128 ] ||
   [ "${#terms_url}" -gt 2048 ] || [ "${#privacy_url}" -gt 2048 ] || [ "${#support_url}" -gt 2048 ] || [ "${#product_name}" -gt 128 ]; then
  printf '{}\n' > "$output"
else
  jq -n --arg termsVersion "$terms_version" --arg privacyVersion "$privacy_version" \
    --arg termsURL "$terms_url" --arg privacyURL "$privacy_url" --arg supportURL "$support_url" --arg productName "$product_name" \
    '{termsVersion:$termsVersion,privacyVersion:$privacyVersion,termsURL:$termsURL,privacyURL:$privacyURL,supportURL:$supportURL} + (if $productName == "" then {} else {productName:$productName} end)' > "$output"
fi
exec "$@"
