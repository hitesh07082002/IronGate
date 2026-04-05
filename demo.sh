#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${ROOT_DIR}"

export JWT_SECRET="${JWT_SECRET:-demo-secret}"
export GRAFANA_ADMIN_USER="${GRAFANA_ADMIN_USER:-admin}"
export GRAFANA_ADMIN_PASSWORD="${GRAFANA_ADMIN_PASSWORD:-admin}"

cleanup() {
  docker-compose down >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "Starting IronGate demo stack..."
docker-compose up -d --build

echo "Waiting for gateway readiness..."
ready=false
for _ in $(seq 1 60); do
  if curl -fsS http://127.0.0.1:8080/ready >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 2
done

if [[ "${ready}" != "true" ]]; then
  echo "Gateway not ready after 120s" >&2
  exit 1
fi

echo
echo "Gateway liveness:"
curl -fsS http://127.0.0.1:8080/health

echo
echo "Gateway readiness:"
curl -fsS http://127.0.0.1:8080/ready

echo
echo "Issuing demo login token..."
TOKEN="$(
  curl -fsS -X POST http://127.0.0.1:8080/api/users/login \
    | sed -n 's/.*"token":"\([^"]*\)".*/\1/p'
)"

if [[ -z "${TOKEN}" ]]; then
  echo "Failed to extract demo token" >&2
  exit 1
fi

AUTH_HEADER="Authorization: Bearer ${TOKEN}"

echo
echo "Protected users request:"
curl -fsS -H "${AUTH_HEADER}" http://127.0.0.1:8080/api/users

echo
echo "Protected orders request:"
curl -fsS -H "${AUTH_HEADER}" http://127.0.0.1:8080/api/orders

echo
echo "Protected payment lookup:"
curl -fsS -H "${AUTH_HEADER}" http://127.0.0.1:8080/api/payments/p-1

echo
echo "Gateway metrics sample:"
METRICS_SAMPLE="$(curl -fsS http://127.0.0.1:8080/metrics)"
printf '%s\n' "${METRICS_SAMPLE}" | sed -n '1,10p'

echo
echo "Running k6 smoke test..."
IRONGATE_BASE_URL="http://127.0.0.1:8080" make load-test

echo
echo "Demo completed successfully."
