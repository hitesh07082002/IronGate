# Benchmarks

IronGate ships both benchmark tooling and a committed benchmark snapshot.

## Commands

Run the benchmark test contract:

```bash
make benchmark-test
```

Run the full benchmark suite:

```bash
make benchmark
```

`make benchmark` writes a timestamped result bundle under `benchmarks/results/`.

## Recorded Snapshot

Recorded benchmark bundle:
[benchmarks/results/20260406-033854-d1edb38/](../benchmarks/results/20260406-033854-d1edb38/README.md)

Committed run environment:

- Apple M4, 10 logical CPU cores, 16 GB RAM
- `k6 v1.7.1`
- Docker Compose `v2.35.1`
- Go `1.24.4`

Main scenario highlights from that run:

| Scenario | Contract | Result |
|---|---|---:|
| Baseline public routing | `POST /api/users/login`, 24 VUs, 20s, distributed client IPs | `3799.53 req/s`, `p50 4.82 ms`, `p95 12.90 ms`, `p99 31.96 ms` |
| Authenticated + rate-limited traffic | `GET /api/payments/p-1`, 8 VUs, 20s, single authenticated identity | `111,042` rate-limited `429` responses after the first `20` successful requests |
| Full pipeline under normal conditions | `GET /api/orders`, 24 VUs, 20s, 1024 authenticated demo users, 100 ms pacing | `230.15 req/s`, `p50 2.92 ms`, `p95 6.47 ms`, `p99 11.08 ms` |

Circuit-breaker proof artifact:
[circuit-breaker-behavior.svg](../benchmarks/results/20260406-033854-d1edb38/circuit-breaker-transition-recovery/circuit-breaker-behavior.svg)

Benchmark note:

The local benchmark stack sets `IRONGATE_TRUSTED_PROXIES=0.0.0.0/0,::/0` so one host can
emulate many client IPs through `X-Forwarded-For`, and it enables login-claim overrides
only inside the benchmark Compose stack so auth scenarios can mint distinct demo identities.
Those are benchmark-only local settings. The default runtime still trusts no proxies and
rejects login claim overrides unless explicitly configured.
