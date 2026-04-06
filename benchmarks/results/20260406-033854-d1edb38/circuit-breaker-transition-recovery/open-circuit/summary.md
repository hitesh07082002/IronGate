# circuit-breaker-transition-recovery:open-circuit

Requests after the breaker opens and the gateway fast-rejects the dead upstream.

## Run Contract

- Command: `IRONGATE_BASE_URL='http://127.0.0.1:8080' IRONGATE_SCENARIO_NAME='circuit-breaker-transition-recovery:open-circuit' IRONGATE_METHOD='GET' IRONGATE_ROUTE_PATH='/api/payments/p-1' IRONGATE_EXPECTED_STATUSES='503' IRONGATE_VUS='1' IRONGATE_ITERATIONS='4' IRONGATE_AUTH_MODE='pool' IRONGATE_AUTH_POOL_SIZE='32' IRONGATE_LOGIN_SUBJECT_PREFIX='bench-cb-user' IRONGATE_LOGIN_ROLE='user' 'k6' 'run' '--summary-export' './benchmarks/results/20260406-033854-d1edb38/circuit-breaker-transition-recovery/open-circuit/k6-summary.json' '--out' 'json=./benchmarks/results/20260406-033854-d1edb38/circuit-breaker-transition-recovery/open-circuit/k6-metrics.json' './benchmarks/route.js'`
- Request: `GET /api/payments/p-1`
- Expected statuses: `503`
- Auth mode: `pool`
- Load: `1 VUs for 4 iterations`
- Git commit: `d1edb38`

## Measured Result

- Throughput: `738.69 req/s` across `4` requests
- Latency: `p50 0.74 ms`, `p95 1.78 ms`, `p99 1.92 ms`
- Status counts: `200=0`, `429=0`, `500=0`, `503=4`, `other=0`
- Unexpected statuses: `0`

## Interpretation

The recorded numbers above come directly from the k6 summary export saved beside this Markdown file.
