#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

DEPLOY_HOST="${DEPLOY_HOST:-irongate@168.144.80.152}"
DEPLOY_DOMAIN="${DEPLOY_DOMAIN:-irongate.hiteshsadhwani.xyz}"
REMOTE_APP_ROOT="${REMOTE_APP_ROOT:-/opt/irongate}"
REMOTE_RELEASE_ROOT="${REMOTE_APP_ROOT}/releases"
REMOTE_ENV_FILE="${REMOTE_APP_ROOT}/shared/production.env"
BASE_URL="${BASE_URL:-https://${DEPLOY_DOMAIN}}"
EXPECTED_BRANCH="${EXPECTED_BRANCH:-main}"
SKIP_REMOTE_SYNC_CHECK="${SKIP_REMOTE_SYNC_CHECK:-false}"

if ! command -v git >/dev/null 2>&1; then
    echo "git is required" >&2
    exit 1
fi

if ! command -v ssh >/dev/null 2>&1; then
    echo "ssh is required" >&2
    exit 1
fi

release_id="$(date -u +%Y%m%d%H%M%S)-$(git -C "${REPO_ROOT}" rev-parse --short HEAD)"

current_branch="$(git -C "${REPO_ROOT}" branch --show-current)"
if [ "${current_branch}" != "${EXPECTED_BRANCH}" ]; then
    echo "Refusing to deploy branch ${current_branch}. Check out ${EXPECTED_BRANCH} first, or override EXPECTED_BRANCH if this is intentional." >&2
    exit 1
fi

if [ "${SKIP_REMOTE_SYNC_CHECK}" != "true" ]; then
    git -C "${REPO_ROOT}" fetch origin "${EXPECTED_BRANCH}" --quiet
    local_sha="$(git -C "${REPO_ROOT}" rev-parse HEAD)"
    remote_sha="$(git -C "${REPO_ROOT}" rev-parse "origin/${EXPECTED_BRANCH}")"
    if [ "${local_sha}" != "${remote_sha}" ]; then
        echo "Refusing to deploy ${local_sha}. Local HEAD does not match origin/${EXPECTED_BRANCH} (${remote_sha})." >&2
        exit 1
    fi
fi

echo "Deploying ${release_id} from ${EXPECTED_BRANCH} to ${DEPLOY_HOST}..."

remote_cmd=$(
    cat <<EOF
set -euo pipefail
release_dir="${REMOTE_RELEASE_ROOT}/${release_id}"
archive_path="/tmp/${release_id}.tar"
mkdir -p "\${release_dir}"
cat >"\${archive_path}"
tar -xf "\${archive_path}" -C "\${release_dir}"
rm -f "\${archive_path}"

if [ ! -f "${REMOTE_ENV_FILE}" ]; then
    echo "Missing ${REMOTE_ENV_FILE}. Run ./scripts/bootstrap-production-host.sh first." >&2
    exit 1
fi

cd "\${release_dir}"
docker compose \
    --project-name irongate \
    --env-file "${REMOTE_ENV_FILE}" \
    -f docker-compose.yml \
    -f deploy/docker-compose.prod.yml \
    up -d --build --remove-orphans

for attempt in \$(seq 1 60); do
    if curl -fsS http://127.0.0.1:8080/ready >/dev/null 2>&1; then
        ln -sfn "\${release_dir}" "${REMOTE_APP_ROOT}/current"
        echo "Remote readiness check passed."
        exit 0
    fi
    sleep 5
done

echo "Gateway did not become ready on the droplet within 300 seconds." >&2
exit 1
EOF
)

git -C "${REPO_ROOT}" archive --format=tar HEAD | ssh -o StrictHostKeyChecking=accept-new "${DEPLOY_HOST}" "bash -lc $(printf '%q' "${remote_cmd}")"

echo "Remote deploy finished. Verifying ${BASE_URL}..."
BASE_URL="${BASE_URL}" "${SCRIPT_DIR}/check-production-health.sh"

echo "Production deploy complete."
