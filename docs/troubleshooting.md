# Troubleshooting

This page covers the common local and demo-path failures.

For production bootstrap, deploy, and live-host issues, use
[`deploy/README.md`](../deploy/README.md).

## Local Stack

- If `./demo.sh` says Docker is not reachable, start Docker Desktop or Docker Engine first.
- If `http://127.0.0.1:8080` is already in use, stop the conflicting service before running the demo.
- If `./demo.sh` says `k6` is required, run `mise install` in the repo root and rerun it.

## Inspection And Teardown

- If you want to inspect Prometheus or Grafana after the walkthrough, rerun `./demo.sh --keep-stack`.
- Stop the kept stack with `./demo.sh --teardown`.

## Load Testing

- If `make load-test` says the gateway is not reachable, start the local stack first and rerun it.

## Production-Specific Note

- If you are deploying to production, keep `IRONGATE_GATEWAY_BIND_HOST=127.0.0.1` so the gateway is only reachable through Caddy.
- The full production operator flow lives in [`deploy/README.md`](../deploy/README.md).
