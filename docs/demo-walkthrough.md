# Manual Demo Walkthrough

This is the longer, manual evaluation path for IronGate.

If you just want the fastest happy path, use [`./demo.sh`](../demo.sh) from the repo root.

## Full Local Walkthrough

```bash
export JWT_SECRET=demo-secret
export GRAFANA_ADMIN_USER=admin
export GRAFANA_ADMIN_PASSWORD=admin
docker compose up -d --build
until curl -fsS http://127.0.0.1:8080/ready; do sleep 2; done
TOKEN="$(curl -fsS -X POST http://127.0.0.1:8080/api/users/login | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')"
curl -fsS -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8080/api/users
curl -fsS -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8080/api/orders
curl -fsS -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8080/api/payments/p-1
curl -fsS http://127.0.0.1:8080/health
curl -fsS http://127.0.0.1:8080/metrics | sed -n '1,20p'
docker compose down
```

If your machine only has the legacy `docker-compose` binary, substitute `docker-compose`
for `docker compose`.

## Keep The Stack Running

If you want the services left up for inspection:

```bash
./demo.sh --keep-stack
```

Useful local URLs after that:

- gateway: `http://127.0.0.1:8080`
- Prometheus: `http://127.0.0.1:9090`
- Grafana: `http://127.0.0.1:3000` with `admin/admin`

When you are done:

```bash
./demo.sh --teardown
```

## Smoke Load Test

With the stack running locally:

```bash
make load-test
```

If you want demo capture instructions, use [`artifacts/demo/README.md`](../artifacts/demo/README.md).
