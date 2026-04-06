#!/usr/bin/env bash

set -euo pipefail

BASE_URL="${BASE_URL:-https://irongate.hiteshsadhwani.xyz}"
BASE_URL="${BASE_URL%/}"

require_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "$1 is required" >&2
        exit 1
    fi
}

extract_token() {
    sed -n 's/.*"token":"\([^"]*\)".*/\1/p'
}

require_cmd curl
require_cmd sed

CURL_ARGS=(--connect-timeout 5 --max-time 20 -fsS)
STATUS_CURL_ARGS=(--connect-timeout 5 --max-time 20 -sS)

echo "GET ${BASE_URL}/health"
curl "${CURL_ARGS[@]}" "${BASE_URL}/health"
printf '\n'

echo "GET ${BASE_URL}/ready"
curl "${CURL_ARGS[@]}" "${BASE_URL}/ready"
printf '\n'

echo "POST ${BASE_URL}/api/users/login"
login_response="$(curl "${CURL_ARGS[@]}" -X POST "${BASE_URL}/api/users/login")"
echo "Login succeeded."

token="$(printf '%s' "${login_response}" | extract_token)"
if [ -z "${token}" ]; then
    echo "Failed to extract JWT from login response." >&2
    exit 1
fi

for path in /api/users /api/orders /api/payments/p-1; do
    echo "GET ${BASE_URL}${path}"
    curl "${CURL_ARGS[@]}" -H "Authorization: Bearer ${token}" "${BASE_URL}${path}"
    printf '\n'
done

metrics_status="$(curl "${STATUS_CURL_ARGS[@]}" -o /dev/null -w '%{http_code}' "${BASE_URL}/metrics")"
echo "GET ${BASE_URL}/metrics -> ${metrics_status}"
if [ "${metrics_status}" != "404" ]; then
    echo "Expected public /metrics to return 404, got ${metrics_status}." >&2
    exit 1
fi

echo "Production health check passed."
