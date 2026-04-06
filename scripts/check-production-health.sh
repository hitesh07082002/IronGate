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

echo "GET ${BASE_URL}/health"
curl -fsS "${BASE_URL}/health"
printf '\n'

echo "GET ${BASE_URL}/ready"
curl -fsS "${BASE_URL}/ready"
printf '\n'

echo "POST ${BASE_URL}/api/users/login"
login_response="$(curl -fsS -X POST "${BASE_URL}/api/users/login")"
printf '%s\n' "${login_response}"

token="$(printf '%s' "${login_response}" | extract_token)"
if [ -z "${token}" ]; then
    echo "Failed to extract JWT from login response." >&2
    exit 1
fi

for path in /api/users /api/orders /api/payments/p-1; do
    echo "GET ${BASE_URL}${path}"
    curl -fsS -H "Authorization: Bearer ${token}" "${BASE_URL}${path}"
    printf '\n'
done

metrics_status="$(curl -sS -o /dev/null -w '%{http_code}' "${BASE_URL}/metrics")"
echo "GET ${BASE_URL}/metrics -> ${metrics_status}"
if [ "${metrics_status}" = "200" ]; then
    echo "Expected public /metrics to be blocked, but it returned 200." >&2
    exit 1
fi

echo "Production health check passed."
