#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${ROOT_DIR}"

KEEP_STACK_RUNNING=false
SKIP_BUILD=false
ACTION="run"
BASE_URL="${IRONGATE_BASE_URL:-http://127.0.0.1:8080}"
PROMETHEUS_URL="${IRONGATE_PROMETHEUS_URL:-http://127.0.0.1:9090}"
GRAFANA_URL="${IRONGATE_GRAFANA_URL:-http://127.0.0.1:3000}"

usage() {
  cat <<'EOF'
Usage: ./demo.sh [--keep-stack] [--skip-build] [--teardown] [--help]

Options:
  --keep-stack  Leave Docker Compose services running after the walkthrough.
  --skip-build  Reuse existing images instead of rebuilding them.
  --teardown    Stop the local demo stack and exit.
  --help        Show this help text.
EOF
}

section() {
  printf '\n==> %s\n' "$1"
}

require_command() {
  local binary="$1"
  if ! command -v "${binary}" >/dev/null 2>&1; then
    echo "${binary} is required for ./demo.sh" >&2
    exit 1
  fi
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --keep-stack)
      KEEP_STACK_RUNNING=true
      ;;
    --skip-build)
      SKIP_BUILD=true
      ;;
    --teardown)
      ACTION="teardown"
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      echo >&2
      usage >&2
      exit 1
      ;;
  esac
  shift
done

export JWT_SECRET="${JWT_SECRET:-demo-secret}"
export GRAFANA_ADMIN_USER="${GRAFANA_ADMIN_USER:-admin}"
export GRAFANA_ADMIN_PASSWORD="${GRAFANA_ADMIN_PASSWORD:-admin}"

if docker compose version >/dev/null 2>&1; then
  COMPOSE_CMD=(docker compose)
  COMPOSE_DISPLAY="docker compose"
elif command -v docker-compose >/dev/null 2>&1; then
  COMPOSE_CMD=(docker-compose)
  COMPOSE_DISPLAY="docker-compose"
else
  echo "Docker Compose is required. Install Docker Desktop or the Docker Compose plugin." >&2
  exit 1
fi

if command -v docker >/dev/null 2>&1 && ! docker info >/dev/null 2>&1; then
  echo "Docker is installed but the daemon is not reachable. Start Docker Desktop or Docker Engine and try again." >&2
  exit 1
fi

if [[ "${ACTION}" == "teardown" ]]; then
  section "Stopping Stack"
  "${COMPOSE_CMD[@]}" down
  echo "IronGate demo stack stopped."
  exit 0
fi

require_command curl

cleanup() {
  if [[ "${KEEP_STACK_RUNNING}" != "true" ]]; then
    "${COMPOSE_CMD[@]}" down >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

UP_ARGS=(-d)
if [[ "${SKIP_BUILD}" != "true" ]]; then
  UP_ARGS+=(--build)
fi

section "IronGate Demo"
echo "Compose command: ${COMPOSE_DISPLAY}"
echo "Gateway URL: ${BASE_URL}"
if [[ "${KEEP_STACK_RUNNING}" == "true" ]]; then
  echo "Stack cleanup: disabled, services will stay up after the walkthrough"
else
  echo "Stack cleanup: enabled, services will be stopped when the walkthrough exits"
fi

section "Starting Stack"
"${COMPOSE_CMD[@]}" up "${UP_ARGS[@]}"

section "Waiting For Readiness"
ready=false
for _ in $(seq 1 60); do
  if curl -fsS "${BASE_URL}/ready" >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 2
done

if [[ "${ready}" != "true" ]]; then
  echo "Gateway not ready after 120s at ${BASE_URL}/ready" >&2
  exit 1
fi

section "Gateway Health"
echo "GET ${BASE_URL}/health"
curl -fsS "${BASE_URL}/health"

echo
echo "GET ${BASE_URL}/ready"
curl -fsS "${BASE_URL}/ready"

section "Demo Login"
echo "POST ${BASE_URL}/api/users/login"
TOKEN="$(
  curl -fsS -X POST "${BASE_URL}/api/users/login" \
    | sed -n 's/.*"token":"\([^"]*\)".*/\1/p'
)"

if [[ -z "${TOKEN}" ]]; then
  echo "Failed to extract demo token" >&2
  exit 1
fi

AUTH_HEADER="Authorization: Bearer ${TOKEN}"

section "Protected Routes"
echo "GET ${BASE_URL}/api/users"
curl -fsS -H "${AUTH_HEADER}" "${BASE_URL}/api/users"

echo
echo "GET ${BASE_URL}/api/orders"
curl -fsS -H "${AUTH_HEADER}" "${BASE_URL}/api/orders"

echo
echo "GET ${BASE_URL}/api/payments/p-1"
curl -fsS -H "${AUTH_HEADER}" "${BASE_URL}/api/payments/p-1"

section "Metrics Sample"
echo "GET ${BASE_URL}/metrics"
METRICS_SAMPLE="$(curl -fsS "${BASE_URL}/metrics")"
printf '%s\n' "${METRICS_SAMPLE}" | sed -n '1,10p'

section "Optional Smoke Benchmark"
if command -v k6 >/dev/null 2>&1 && command -v make >/dev/null 2>&1; then
  echo "Running k6 smoke test..."
  IRONGATE_BASE_URL="${BASE_URL}" make load-test
elif command -v k6 >/dev/null 2>&1; then
  echo "Skipping k6 smoke test because make is not installed."
  echo "Install make, then run 'IRONGATE_BASE_URL=${BASE_URL} make load-test'."
else
  echo "Skipping k6 smoke test because k6 is not installed."
  echo "Run 'mise x k6@1.7.1 -- make load-test' later for the optional benchmark smoke."
fi

section "Done"
echo "Demo completed successfully."
echo
echo "Gateway: ${BASE_URL}"
if [[ "${KEEP_STACK_RUNNING}" == "true" ]]; then
  echo "Prometheus: ${PROMETHEUS_URL}"
  echo "Grafana: ${GRAFANA_URL} (default local creds: ${GRAFANA_ADMIN_USER}/${GRAFANA_ADMIN_PASSWORD})"
  echo "The stack is still running."
  echo "When you are done, stop it with: ./demo.sh --teardown"
else
  echo "The stack will now be stopped."
  echo "Run './demo.sh --keep-stack' if you want to inspect Prometheus and Grafana after the walkthrough."
fi
