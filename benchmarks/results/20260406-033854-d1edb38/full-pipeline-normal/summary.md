# full-pipeline-normal

Protected order traffic with a pool of authenticated demo users to exercise auth, rate limiting, retry, load balancing, and healthy circuit breakers together.

## Run Contract

- Command: `IRONGATE_BASE_URL='http://127.0.0.1:8080' IRONGATE_SCENARIO_NAME='full-pipeline-normal' IRONGATE_METHOD='GET' IRONGATE_ROUTE_PATH='/api/orders' IRONGATE_EXPECTED_STATUSES='200' IRONGATE_VUS='24' IRONGATE_DURATION='20s' IRONGATE_AUTH_MODE='pool' IRONGATE_AUTH_POOL_SIZE='1024' IRONGATE_LOGIN_SUBJECT_PREFIX='bench-order-user' IRONGATE_LOGIN_ROLE='user' IRONGATE_TOKEN_POOL_PATH='/Users/hiteshsadhwani/Desktop/Personal_Project/IronGate/benchmarks/results/20260406-033854-d1edb38/full-pipeline-normal/tokens.json' 'k6' 'run' '--summary-export' '/Users/hiteshsadhwani/Desktop/Personal_Project/IronGate/benchmarks/results/20260406-033854-d1edb38/full-pipeline-normal/k6-summary.json' '--out' 'json=/Users/hiteshsadhwani/Desktop/Personal_Project/IronGate/benchmarks/results/20260406-033854-d1edb38/full-pipeline-normal/k6-metrics.json' '/Users/hiteshsadhwani/Desktop/Personal_Project/IronGate/benchmarks/route.js'`
- Request: `GET /api/orders`
- Expected statuses: `200`
- Auth mode: `pool`
- Load: `24 VUs for 20s`
- Git commit: `d1edb38`

## Measured Result

- Throughput: `230.15 req/s` across `4624` requests
- Latency: `p50 2.92 ms`, `p95 6.47 ms`, `p99 11.08 ms`
- Status counts: `200=4624`, `429=0`, `500=0`, `503=0`, `other=0`
- Unexpected statuses: `0`

## Interpretation

This is the closest thing to the gateway's steady-state production path in the local stack: JWT auth, Redis limiting, retry, load balancing, and a healthy circuit breaker all stay in the request path without synthetic bypasses.
