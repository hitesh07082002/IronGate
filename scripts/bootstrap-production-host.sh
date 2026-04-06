#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

DEPLOY_HOST="${DEPLOY_HOST:-root@168.144.80.152}"
DEPLOY_DOMAIN="${DEPLOY_DOMAIN:-irongate.hiteshsadhwani.xyz}"
REMOTE_APP_ROOT="${REMOTE_APP_ROOT:-/opt/irongate}"
REMOTE_ENV_FILE="${REMOTE_APP_ROOT}/shared/production.env"
DEPLOY_USER="${DEPLOY_USER:-irongate}"

if ! command -v ssh >/dev/null 2>&1; then
    echo "ssh is required" >&2
    exit 1
fi

if ! command -v sed >/dev/null 2>&1; then
    echo "sed is required" >&2
    exit 1
fi

rendered_caddyfile="$(mktemp)"
cleanup() {
    rm -f "${rendered_caddyfile}"
}
trap cleanup EXIT

sed "s/{{DOMAIN}}/${DEPLOY_DOMAIN}/g" "${REPO_ROOT}/deploy/Caddyfile.template" >"${rendered_caddyfile}"

ssh -o StrictHostKeyChecking=accept-new "${DEPLOY_HOST}" \
    "DEPLOY_DOMAIN=$(printf '%q' "${DEPLOY_DOMAIN}") REMOTE_APP_ROOT=$(printf '%q' "${REMOTE_APP_ROOT}") REMOTE_ENV_FILE=$(printf '%q' "${REMOTE_ENV_FILE}") DEPLOY_USER=$(printf '%q' "${DEPLOY_USER}") bash -s" <<EOF
set -euo pipefail

export DEBIAN_FRONTEND=noninteractive

apt-get update
apt-get install -y --no-install-recommends \
    ca-certificates \
    caddy \
    curl \
    docker.io \
    docker-compose-v2 \
    git \
    openssl \
    ufw

systemctl enable --now docker
systemctl enable --now caddy

mkdir -p "\${REMOTE_APP_ROOT}/shared" "\${REMOTE_APP_ROOT}/releases"

if ! id -u "\${DEPLOY_USER}" >/dev/null 2>&1; then
    useradd --create-home --shell /bin/bash --groups docker "\${DEPLOY_USER}"
else
    usermod -aG docker "\${DEPLOY_USER}"
fi

mkdir -p "/home/\${DEPLOY_USER}/.ssh"
if [ -f /root/.ssh/authorized_keys ] && [ ! -f "/home/\${DEPLOY_USER}/.ssh/authorized_keys" ]; then
    cp /root/.ssh/authorized_keys "/home/\${DEPLOY_USER}/.ssh/authorized_keys"
fi
chmod 700 "/home/\${DEPLOY_USER}/.ssh"
[ -f "/home/\${DEPLOY_USER}/.ssh/authorized_keys" ] && chmod 600 "/home/\${DEPLOY_USER}/.ssh/authorized_keys"
chown -R "\${DEPLOY_USER}:\${DEPLOY_USER}" "/home/\${DEPLOY_USER}/.ssh"
chown -R "\${DEPLOY_USER}:docker" "\${REMOTE_APP_ROOT}"

if [ ! -f "\${REMOTE_ENV_FILE}" ]; then
    jwt_secret="\$(openssl rand -hex 32)"
    grafana_password="\$(openssl rand -base64 24 | tr -d '\n')"
    cat >"\${REMOTE_ENV_FILE}" <<ENVFILE
JWT_SECRET=\${jwt_secret}
GRAFANA_ADMIN_USER=admin
GRAFANA_ADMIN_PASSWORD=\${grafana_password}
IRONGATE_GATEWAY_BIND_HOST=127.0.0.1
IRONGATE_TRUSTED_PROXIES=127.0.0.1/32,::1/128
IRONGATE_ALLOW_LOGIN_OVERRIDES=false
ENVFILE
fi
chown "\${DEPLOY_USER}:\${DEPLOY_USER}" "\${REMOTE_ENV_FILE}"
chmod 600 "\${REMOTE_ENV_FILE}"

mkdir -p /var/log/caddy
chown caddy:caddy /var/log/caddy
cat >/etc/caddy/Caddyfile <<'CADDYFILE'
$(cat "${rendered_caddyfile}")
CADDYFILE

caddy fmt --overwrite /etc/caddy/Caddyfile
caddy validate --config /etc/caddy/Caddyfile
systemctl reload caddy || systemctl restart caddy

ufw allow OpenSSH
ufw allow 80/tcp
ufw allow 443/tcp
ufw --force enable

docker --version
docker compose version
ufw status
systemctl is-active docker
systemctl is-active caddy
EOF

echo "Bootstrap complete for ${DEPLOY_HOST} (${DEPLOY_DOMAIN})."
echo "Server env file: ${REMOTE_ENV_FILE}"
echo "Deploy user: ${DEPLOY_USER}"
