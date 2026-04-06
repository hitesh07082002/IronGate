# Benchmark Run

- Result directory: `benchmarks/results/20260406-033854-d1edb38`
- Generated at: `2026-04-06T11:49:08.126662+05:30`
- [Run context](run-context.json)

## Artifacts

- [Throughput chart](throughput.svg)
- [Latency chart](latency.svg)

## Scenario Summary

| Scenario | Throughput (req/s) | p50 (ms) | p95 (ms) | p99 (ms) | Unexpected statuses | Notes |
|---|---:|---:|---:|---:|---:|---|
| `baseline-public-routing` | 3799.53 | 4.82 | 12.90 | 31.96 | 0 | [summary](baseline-public-routing/summary.md) |
| `authenticated-rate-limited-traffic` | 5552.44 | 1.19 | 2.46 | 4.15 | 0 | [summary](authenticated-rate-limited-traffic/summary.md) |
| `full-pipeline-normal` | 230.15 | 2.92 | 6.47 | 11.08 | 0 | [summary](full-pipeline-normal/summary.md) |
| `circuit-breaker-transition-recovery` | phase-based | phase-based | phase-based | phase-based | n/a | [summary](circuit-breaker-transition-recovery/summary.md) / [graph](circuit-breaker-transition-recovery/circuit-breaker-behavior.svg) |
