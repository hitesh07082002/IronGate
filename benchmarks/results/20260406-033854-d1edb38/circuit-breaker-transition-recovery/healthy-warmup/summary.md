# circuit-breaker-transition-recovery:healthy-warmup

Healthy upstream responses before inducing failures.

## Run Contract

- Command: `IRONGATE_BASE_URL='http://127.0.0.1:8080' IRONGATE_SCENARIO_NAME='circuit-breaker-transition-recovery:healthy-warmup' IRONGATE_METHOD='GET' IRONGATE_ROUTE_PATH='/api/payments/p-1' IRONGATE_EXPECTED_STATUSES='200' IRONGATE_VUS='1' IRONGATE_ITERATIONS='8' IRONGATE_AUTH_MODE='pool' IRONGATE_AUTH_POOL_SIZE='32' IRONGATE_LOGIN_SUBJECT_PREFIX='bench-cb-user' IRONGATE_LOGIN_ROLE='user' IRONGATE_TOKEN_POOL_PATH='/Users/hiteshsadhwani/Desktop/Personal_Project/IronGate/benchmarks/results/20260406-033854-d1edb38/circuit-breaker-transition-recovery/tokens.json' 'k6' 'run' '--summary-export' '/Users/hiteshsadhwani/Desktop/Personal_Project/IronGate/benchmarks/results/20260406-033854-d1edb38/circuit-breaker-transition-recovery/healthy-warmup/k6-summary.json' '--out' 'json=/Users/hiteshsadhwani/Desktop/Personal_Project/IronGate/benchmarks/results/20260406-033854-d1edb38/circuit-breaker-transition-recovery/healthy-warmup/k6-metrics.json' '/Users/hiteshsadhwani/Desktop/Personal_Project/IronGate/benchmarks/route.js'`
- Request: `GET /api/payments/p-1`
- Expected statuses: `200`
- Auth mode: `pool`
- Load: `1 VUs for 8 iterations`
- Git commit: `d1edb38`

## Measured Result

- Throughput: `807.02 req/s` across `8` requests
- Latency: `p50 0.69 ms`, `p95 1.87 ms`, `p99 1.96 ms`
- Status counts: `200=8`, `429=0`, `500=0`, `503=0`, `other=0`
- Unexpected statuses: `0`

## Interpretation

The recorded numbers above come directly from the k6 summary export saved beside this Markdown file.
