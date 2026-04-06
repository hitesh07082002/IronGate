# Production Deploy

This document is the operational reference for the IronGate production environment behind `https://irongate.hiteshsadhwani.xyz`.

## Operator Checklist

1. Run [`scripts/bootstrap-production-host.sh`](../scripts/bootstrap-production-host.sh) once as `root` on a new host.
2. Commit and push the release you want to ship.
3. Run [`scripts/deploy-production.sh`](../scripts/deploy-production.sh) from a local checkout of `main`.
4. Use [`scripts/check-production-health.sh`](../scripts/check-production-health.sh) for a live smoke check when you want verification without a redeploy.

## Architecture

- Host-level Caddy terminates TLS on `80/443`
- Caddy proxies to the gateway on `127.0.0.1:8080`
- Public `/metrics` is blocked at the edge
- Prometheus, Grafana, Redis, and the backing services stay private
- The gateway trusts forwarded headers only from localhost via `IRONGATE_TRUSTED_PROXIES=127.0.0.1/32,::1/128`

## Roles

- `root` is used only for one-time host bootstrap tasks such as package installation, firewall changes, and Caddy configuration.
- `irongate` is the day-to-day deploy user. It owns `/opt/irongate`, can talk to Docker, and is the default SSH user for [`scripts/deploy-production.sh`](../scripts/deploy-production.sh).

## One-Time Bootstrap

Run:

```bash
./scripts/bootstrap-production-host.sh
```

That script:

- installs Docker, Compose, Caddy, UFW, and the small host dependencies
- creates a dedicated `irongate` deploy user with the existing SSH key copied from `root`
- creates `/opt/irongate/{shared,releases}`
- creates `/opt/irongate/shared/production.env` if it does not already exist
- configures UFW to allow only `OpenSSH`, `80/tcp`, and `443/tcp`
- writes `/etc/caddy/Caddyfile` from [`deploy/Caddyfile.template`](./Caddyfile.template)

The generated env file is server-local, contains production secrets, and is never committed.

## Deploy

Run:

```bash
./scripts/deploy-production.sh
```

That script packages the committed local `HEAD`, uploads it over SSH, runs:

```bash
docker compose \
  --project-name irongate \
  --env-file /opt/irongate/shared/production.env \
  -f docker-compose.yml \
  -f deploy/docker-compose.prod.yml \
  up -d --build --remove-orphans
```

It then waits for the local `/ready` endpoint on the droplet and runs [`scripts/check-production-health.sh`](../scripts/check-production-health.sh) against the public URL.
On the first successful deploy it also creates `/opt/irongate/current` as a symlink to the active release.

Safety rails:

- the script refuses to deploy from a non-`main` branch by default
- the script refuses to deploy if local `HEAD` does not match `origin/main`
- the default SSH target is the least-privilege deploy user `irongate`
- both checks can be overridden explicitly, but the default path is safe for production

## Health Check

Run:

```bash
./scripts/check-production-health.sh
```

The health script verifies:

- `GET /health`
- `GET /ready`
- `POST /api/users/login`
- authenticated `GET /api/users`
- authenticated `GET /api/orders`
- authenticated `GET /api/payments/p-1`
- public `/metrics` is not exposed through Caddy

## Overrides

All scripts accept environment overrides:

```bash
DEPLOY_HOST=root@203.0.113.10 \
DEPLOY_DOMAIN=api.example.com \
REMOTE_APP_ROOT=/opt/irongate \
./scripts/deploy-production.sh
```
