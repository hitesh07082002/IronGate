# circuit-breaker-transition-recovery

Payment route across healthy traffic, upstream failure, open-circuit fast rejection, and recovery after the breaker timeout.

## Phase Summary

| Phase | Throughput (req/s) | p95 (ms) | 200 | 500 | 503 | Unexpected statuses |
|---|---:|---:|---:|---:|---:|---:|
| `healthy-warmup` | 807.02 | 1.87 | 8 | 0 | 0 | 0 |
| `failure-trip` | 798.16 | 2.10 | 0 | 5 | 3 | 0 |
| `open-circuit` | 738.69 | 1.78 | 0 | 0 | 4 | 0 |
| `recovery` | 425.39 | 3.48 | 5 | 0 | 0 | 0 |

## Interpretation

The healthy phase stays on 200s, the failure phase produces upstream 500s until the breaker trips, the open-circuit phase flips to fast 503 rejections, and the recovery phase returns to 200s after the timeout window and reset. That is the proof artifact for the breaker state machine on the shipped payment route.
