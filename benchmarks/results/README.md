# Benchmark Results

Each recorded run lands in a timestamped directory under `benchmarks/results/`.

Latest committed benchmark bundle:

- [`20260406-033854-d1edb38`](./20260406-033854-d1edb38/README.md)

The contract for every run is:

- `run-context.json` captures the machine note, software versions, git commit, and benchmark stack environment.
- `<scenario>/k6-summary.json` stores the machine-readable k6 summary export.
- `<scenario>/scenario.json` stores the exact command, request contract, load shape, and extracted key metrics.
- `<scenario>/summary.md` is the brief human-readable interpretation tied to that run's data.
- top-level SVG files compare throughput and latency across the recorded scenarios.

The optional per-request `k6-metrics.json` event stream is intentionally not committed by default because it is large. Generate it locally with `python3 benchmarks/runner.py run --save-event-stream ...` when you need low-level debugging.

Committed result directories are versioned evidence, not placeholders. Regenerate them with `make benchmark`.
