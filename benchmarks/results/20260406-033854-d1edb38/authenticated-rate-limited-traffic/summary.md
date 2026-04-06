# authenticated-rate-limited-traffic

Single authenticated identity hitting the payment status route until the Redis-backed limiter starts returning 429s.

## Run Contract

- Command: `IRONGATE_BASE_URL='http://127.0.0.1:8080' IRONGATE_SCENARIO_NAME='authenticated-rate-limited-traffic' IRONGATE_METHOD='GET' IRONGATE_ROUTE_PATH='/api/payments/p-1' IRONGATE_EXPECTED_STATUSES='200,429' IRONGATE_VUS='8' IRONGATE_DURATION='20s' IRONGATE_AUTH_MODE='static' IRONGATE_LOGIN_SUBJECT_PREFIX='bench-rate-limit-user' IRONGATE_LOGIN_ROLE='user' 'k6' 'run' '--summary-export' './benchmarks/results/20260406-033854-d1edb38/authenticated-rate-limited-traffic/k6-summary.json' '--out' 'json=./benchmarks/results/20260406-033854-d1edb38/authenticated-rate-limited-traffic/k6-metrics.json' './benchmarks/route.js'`
- Request: `GET /api/payments/p-1`
- Expected statuses: `200, 429`
- Auth mode: `static`
- Load: `8 VUs for 20s`
- Git commit: `d1edb38`

## Measured Result

- Throughput: `5552.44 req/s` across `111062` requests
- Latency: `p50 1.19 ms`, `p95 2.46 ms`, `p99 4.15 ms`
- Status counts: `200=20`, `429=111042`, `500=0`, `503=0`, `other=0`
- Unexpected statuses: `0`

## Interpretation

The limiter did its job. `111042` requests were rejected with 429 after the authenticated bucket saturated, while the gateway still kept tail latency visible in the recorded summary instead of hand-waving the behavior.
