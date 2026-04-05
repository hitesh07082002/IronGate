# circuit-breaker-transition-recovery:recovery

Reset chaos, wait past the breaker timeout, then confirm traffic succeeds again.

## Run Contract

- Command: `IRONGATE_BASE_URL='http://127.0.0.1:8080' IRONGATE_SCENARIO_NAME='circuit-breaker-transition-recovery:recovery' IRONGATE_METHOD='GET' IRONGATE_ROUTE_PATH='/api/payments/p-1' IRONGATE_EXPECTED_STATUSES='200' IRONGATE_VUS='1' IRONGATE_ITERATIONS='5' IRONGATE_AUTH_MODE='pool' IRONGATE_AUTH_POOL_SIZE='32' IRONGATE_LOGIN_SUBJECT_PREFIX='bench-cb-user' IRONGATE_LOGIN_ROLE='user' IRONGATE_TOKEN_POOL_PATH='/Users/hiteshsadhwani/Desktop/Personal_Project/IronGate/benchmarks/results/20260406-033854-d1edb38/circuit-breaker-transition-recovery/tokens.json' 'k6' 'run' '--summary-export' '/Users/hiteshsadhwani/Desktop/Personal_Project/IronGate/benchmarks/results/20260406-033854-d1edb38/circuit-breaker-transition-recovery/recovery/k6-summary.json' '--out' 'json=/Users/hiteshsadhwani/Desktop/Personal_Project/IronGate/benchmarks/results/20260406-033854-d1edb38/circuit-breaker-transition-recovery/recovery/k6-metrics.json' '/Users/hiteshsadhwani/Desktop/Personal_Project/IronGate/benchmarks/route.js'`
- Request: `GET /api/payments/p-1`
- Expected statuses: `200`
- Auth mode: `pool`
- Load: `1 VUs for 5 iterations`
- Git commit: `d1edb38`

## Measured Result

- Throughput: `425.39 req/s` across `5` requests
- Latency: `p50 0.98 ms`, `p95 3.48 ms`, `p99 3.84 ms`
- Status counts: `200=5`, `429=0`, `500=0`, `503=0`, `other=0`
- Unexpected statuses: `0`

## Interpretation

The recorded numbers above come directly from the k6 summary export saved beside this Markdown file.
