# baseline-public-routing

Public login traffic routed through the gateway with distributed client IPs so the public bucket does not dominate the latency profile.

## Run Contract

- Command: `IRONGATE_BASE_URL='http://127.0.0.1:8080' IRONGATE_SCENARIO_NAME='baseline-public-routing' IRONGATE_METHOD='POST' IRONGATE_ROUTE_PATH='/api/users/login' IRONGATE_EXPECTED_STATUSES='200' IRONGATE_VUS='24' IRONGATE_DURATION='20s' IRONGATE_XFF_MODE='per_request' IRONGATE_AUTH_MODE='none' 'k6' 'run' '--summary-export' './benchmarks/results/20260406-033854-d1edb38/baseline-public-routing/k6-summary.json' '--out' 'json=./benchmarks/results/20260406-033854-d1edb38/baseline-public-routing/k6-metrics.json' './benchmarks/route.js'`
- Request: `POST /api/users/login`
- Expected statuses: `200`
- Auth mode: `none`
- Load: `24 VUs for 20s`
- Git commit: `d1edb38`

## Measured Result

- Throughput: `3799.53 req/s` across `76016` requests
- Latency: `p50 4.82 ms`, `p95 12.90 ms`, `p99 31.96 ms`
- Status counts: `200=76016`, `429=0`, `500=0`, `503=0`, `other=0`
- Unexpected statuses: `0`

## Interpretation

This run isolates the public route path with distributed client IPs so the benchmark reflects gateway overhead instead of a single IP immediately rate-limiting itself.
