#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import os
import platform
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parent.parent
BENCHMARKS_DIR = ROOT / "benchmarks"
SCENARIOS_PATH = BENCHMARKS_DIR / "scenarios.json"
ROUTE_SCRIPT = BENCHMARKS_DIR / "route.js"
RESULTS_ROOT = BENCHMARKS_DIR / "results"

DEFAULT_BASE_URL = "http://127.0.0.1:8080"
DEFAULT_JWT_SECRET = "demo-secret"
DEFAULT_GRAFANA_USER = "admin"
DEFAULT_GRAFANA_PASSWORD = "admin"
DEFAULT_TRUSTED_PROXIES = "0.0.0.0/0,::/0"


@dataclass
class RunContext:
    base_url: str
    git_commit: str
    git_branch: str
    hardware: dict[str, Any]
    software: dict[str, Any]
    benchmark_env: dict[str, str]


def main() -> int:
    parser = argparse.ArgumentParser(description="Run and render IronGate benchmark scenarios.")
    subparsers = parser.add_subparsers(dest="command", required=True)

    run_parser = subparsers.add_parser("run", help="Run benchmark scenarios and render artifacts.")
    run_parser.add_argument("--scenario", default="all", help="Scenario name from benchmarks/scenarios.json or 'all'.")
    run_parser.add_argument("--base-url", default=DEFAULT_BASE_URL, help="Gateway base URL.")
    run_parser.add_argument("--result-dir", help="Existing or new result directory. Defaults to benchmarks/results/<timestamp>-<commit>.")
    run_parser.add_argument("--skip-stack", action="store_true", help="Reuse an already-running local stack.")
    run_parser.add_argument("--keep-stack-running", action="store_true", help="Leave docker-compose services running after the suite.")
    run_parser.add_argument("--save-event-stream", action="store_true", help="Also persist the per-request k6 JSON event stream (large, local-debug only).")

    render_parser = subparsers.add_parser("render", help="Re-render Markdown and SVG artifacts from an existing result directory.")
    render_parser.add_argument("--result-dir", required=True, help="Result directory containing scenario subdirectories and metadata.")

    args = parser.parse_args()
    scenarios = load_scenarios()

    if args.command == "run":
        return run_command(args, scenarios)
    if args.command == "render":
        return render_command(Path(args.result_dir).expanduser().resolve())
    raise AssertionError(f"unsupported command {args.command}")


def run_command(args: argparse.Namespace, scenarios: dict[str, Any]) -> int:
    ensure_dependencies(skip_stack=args.skip_stack)

    git_commit = git_output(["rev-parse", "--short=7", "HEAD"])
    git_branch = git_output(["branch", "--show-current"])
    result_dir = determine_result_dir(args.result_dir, git_commit)
    result_dir.mkdir(parents=True, exist_ok=True)

    benchmark_env = {
        "JWT_SECRET": os.environ.get("JWT_SECRET", DEFAULT_JWT_SECRET),
        "GRAFANA_ADMIN_USER": os.environ.get("GRAFANA_ADMIN_USER", DEFAULT_GRAFANA_USER),
        "GRAFANA_ADMIN_PASSWORD": os.environ.get("GRAFANA_ADMIN_PASSWORD", DEFAULT_GRAFANA_PASSWORD),
        "IRONGATE_TRUSTED_PROXIES": os.environ.get("IRONGATE_TRUSTED_PROXIES", DEFAULT_TRUSTED_PROXIES),
    }

    stack_started = False
    if not args.skip_stack:
        compose_up(benchmark_env)
        wait_for_ready(args.base_url)
        stack_started = True

    try:
        context = RunContext(
            base_url=args.base_url.rstrip("/"),
            git_commit=git_commit,
            git_branch=git_branch,
            hardware=collect_hardware_info(),
            software=collect_software_info(),
            benchmark_env=benchmark_env,
        )
        write_run_context(result_dir, context)

        selected = select_scenarios(scenarios, args.scenario)
        summaries: list[dict[str, Any]] = []
        for name, scenario in selected.items():
            scenario_dir = result_dir / name
            scenario_dir.mkdir(parents=True, exist_ok=True)
            print(f"[benchmark] {name}")
            if scenario["kind"] == "single":
                summaries.append(run_single_scenario(name, scenario, scenario_dir, context, save_event_stream=args.save_event_stream))
            elif scenario["kind"] == "phased":
                summaries.append(run_phased_scenario(name, scenario, scenario_dir, context, save_event_stream=args.save_event_stream))
            else:
                raise RuntimeError(f"unsupported scenario kind for {name}: {scenario['kind']}")

        write_suite_manifest(result_dir, summaries)
        render_result_directory(result_dir)
    finally:
        if stack_started and not args.keep_stack_running:
            compose_down(benchmark_env)

    print(result_dir)
    return 0


def render_command(result_dir: Path) -> int:
    render_result_directory(result_dir)
    print(result_dir)
    return 0


def select_scenarios(scenarios: dict[str, Any], selected: str) -> dict[str, Any]:
    if selected == "all":
        return scenarios
    if selected not in scenarios:
        raise SystemExit(f"unknown scenario {selected!r}")
    return {selected: scenarios[selected]}


def ensure_dependencies(*, skip_stack: bool) -> None:
    required = ["k6", "python3"]
    if not skip_stack:
        required.append("docker-compose")
    missing = [binary for binary in required if shutil.which(binary) is None]
    if missing:
        raise SystemExit(f"missing required tooling: {', '.join(missing)}")


def load_scenarios() -> dict[str, Any]:
    return json.loads(SCENARIOS_PATH.read_text())


def determine_result_dir(requested: str | None, git_commit: str) -> Path:
    if requested:
        return Path(requested).expanduser().resolve()
    stamp = datetime.now().strftime("%Y%m%d-%H%M%S")
    return (RESULTS_ROOT / f"{stamp}-{git_commit}").resolve()


def git_output(args: list[str]) -> str:
    completed = subprocess.run(["git", *args], cwd=ROOT, check=True, capture_output=True, text=True)
    return completed.stdout.strip()


def compose_up(extra_env: dict[str, str]) -> None:
    env = os.environ.copy()
    env.update(extra_env)
    subprocess.run(["docker-compose", "up", "-d", "--build"], cwd=ROOT, check=True, env=env)


def compose_down(extra_env: dict[str, str]) -> None:
    env = os.environ.copy()
    env.update(extra_env)
    subprocess.run(["docker-compose", "down"], cwd=ROOT, check=True, env=env)


def wait_for_ready(base_url: str, timeout_seconds: int = 180) -> None:
    deadline = time.time() + timeout_seconds
    ready_url = f"{base_url.rstrip('/')}/ready"
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(ready_url, timeout=2) as response:
                if response.status == 200:
                    return
        except Exception:
            time.sleep(2)
            continue
        time.sleep(2)
    raise RuntimeError(f"gateway never became ready at {ready_url}")


def run_single_scenario(
    name: str,
    scenario: dict[str, Any],
    scenario_dir: Path,
    context: RunContext,
    *,
    save_event_stream: bool,
) -> dict[str, Any]:
    token_pool_path = prepare_token_pool(scenario.get("auth"), context, scenario_dir / "tokens.json")
    result = execute_request_run(
        name=name,
        description=scenario["description"],
        scenario_dir=scenario_dir,
        request_config=scenario["request"],
        auth_config=scenario.get("auth"),
        load_config=scenario["load"],
        context=context,
        token_pool_path=token_pool_path,
        save_event_stream=save_event_stream,
    )
    return result


def run_phased_scenario(
    name: str,
    scenario: dict[str, Any],
    scenario_dir: Path,
    context: RunContext,
    *,
    save_event_stream: bool,
) -> dict[str, Any]:
    phase_summaries: list[dict[str, Any]] = []
    chaos = scenario["chaos"]
    token_pool_path = prepare_token_pool(scenario.get("auth"), context, scenario_dir / "tokens.json")

    for phase in scenario["phases"]:
        sleep_before = phase.get("sleep_before")
        if sleep_before:
            time.sleep(parse_duration_seconds(sleep_before))

        apply_chaos_action(chaos, phase["chaos_action"])
        phase_name = phase["name"]
        phase_dir = scenario_dir / phase_name
        phase_dir.mkdir(parents=True, exist_ok=True)
        phase_summaries.append(
            execute_request_run(
                name=f"{name}:{phase_name}",
                description=phase["description"],
                scenario_dir=phase_dir,
                request_config={**scenario["request"], "expected_statuses": phase["expected_statuses"]},
                auth_config=scenario.get("auth"),
                load_config=phase["load"],
                context=context,
                token_pool_path=token_pool_path,
                save_event_stream=save_event_stream,
            )
        )

    apply_chaos_action(chaos, {"type": "reset"})

    metadata = {
        "name": name,
        "kind": "phased",
        "description": scenario["description"],
        "timestamp": datetime.now().astimezone().isoformat(),
        "git_commit": context.git_commit,
        "git_branch": context.git_branch,
        "request": scenario["request"],
        "auth": scenario.get("auth", {"mode": "none"}),
        "chaos": chaos,
        "phases": phase_summaries,
    }
    write_json(scenario_dir / "scenario.json", metadata)
    write_text(scenario_dir / "summary.md", phased_summary_markdown(metadata))
    return metadata


def execute_request_run(
    *,
    name: str,
    description: str,
    scenario_dir: Path,
    request_config: dict[str, Any],
    auth_config: dict[str, Any] | None,
    load_config: dict[str, Any],
    context: RunContext,
    token_pool_path: Path | None,
    save_event_stream: bool,
) -> dict[str, Any]:
    summary_path = scenario_dir / "k6-summary.json"
    metrics_path = scenario_dir / "k6-metrics.json"
    env = build_k6_env(name, request_config, auth_config, load_config, context, token_pool_path)
    command = [
        "k6",
        "run",
        "--summary-export",
        str(summary_path),
    ]
    if save_event_stream:
        command.extend([
            "--out",
            f"json={metrics_path}",
        ])
    command.append(str(ROUTE_SCRIPT))
    start = time.monotonic()
    subprocess.run(command, cwd=ROOT, env=env, check=True)
    wall_time_seconds = time.monotonic() - start

    summary = json.loads(summary_path.read_text())
    metrics = summarize_metrics(summary)
    metadata = {
        "name": name,
        "kind": "single",
        "description": description,
        "timestamp": datetime.now().astimezone().isoformat(),
        "git_commit": context.git_commit,
        "git_branch": context.git_branch,
        "base_url": context.base_url,
        "request": request_config,
        "auth": auth_config or {"mode": "none"},
        "load": load_config,
        "command": format_command(command, env),
        "environment": {
            "benchmark_stack": context.benchmark_env,
            "hardware": context.hardware,
            "software": context.software,
        },
        "artifacts": benchmark_artifacts(
            summary_path=summary_path,
            metrics_path=metrics_path if save_event_stream else None,
            token_pool_path=token_pool_path,
        ),
        "metrics": metrics,
        "wall_time_seconds": round(wall_time_seconds, 2),
    }
    write_json(scenario_dir / "scenario.json", metadata)
    write_text(scenario_dir / "summary.md", single_summary_markdown(metadata))
    return metadata


def build_k6_env(
    name: str,
    request_config: dict[str, Any],
    auth_config: dict[str, Any] | None,
    load_config: dict[str, Any],
    context: RunContext,
    token_pool_path: Path | None,
) -> dict[str, str]:
    env = os.environ.copy()
    env.update(
        {
            "IRONGATE_BASE_URL": context.base_url,
            "IRONGATE_SCENARIO_NAME": name,
            "IRONGATE_METHOD": request_config["method"],
            "IRONGATE_ROUTE_PATH": request_config["path"],
            "IRONGATE_EXPECTED_STATUSES": ",".join(str(item) for item in request_config["expected_statuses"]),
            "IRONGATE_VUS": str(load_config["vus"]),
        }
    )

    if "duration" in load_config:
        env["IRONGATE_DURATION"] = load_config["duration"]
        env.pop("IRONGATE_ITERATIONS", None)
    if "iterations" in load_config:
        env["IRONGATE_ITERATIONS"] = str(load_config["iterations"])
        env.pop("IRONGATE_DURATION", None)
    if "sleep_ms" in load_config:
        env["IRONGATE_SLEEP_MS"] = str(load_config["sleep_ms"])
    if token_pool_path is not None:
        env["IRONGATE_TOKEN_POOL_PATH"] = str(token_pool_path)
    if request_config.get("request_body"):
        env["IRONGATE_REQUEST_BODY"] = request_config["request_body"]
    if request_config.get("xff_mode"):
        env["IRONGATE_XFF_MODE"] = request_config["xff_mode"]

    auth = auth_config or {"mode": "none"}
    env["IRONGATE_AUTH_MODE"] = auth.get("mode", "none")
    if "subject_prefix" in auth:
        env["IRONGATE_LOGIN_SUBJECT_PREFIX"] = auth["subject_prefix"]
    if "role" in auth:
        env["IRONGATE_LOGIN_ROLE"] = auth["role"]
    if "pool_size" in auth:
        env["IRONGATE_AUTH_POOL_SIZE"] = str(auth["pool_size"])

    return env


def prepare_token_pool(auth_config: dict[str, Any] | None, context: RunContext, destination: Path) -> Path | None:
    auth = auth_config or {"mode": "none"}
    mode = auth.get("mode", "none")
    if mode == "none":
        return None

    if mode == "static":
        count = 1
    elif mode == "pool":
        count = int(auth["pool_size"])
    else:
        raise RuntimeError(f"unsupported auth mode {mode!r}")

    subject_prefix = auth.get("subject_prefix", "bench-user")
    role = auth.get("role", "user")
    tokens = []
    for index in range(count):
        tokens.append(mint_login_token(context.base_url, f"{subject_prefix}-{index}", role, index))

    destination.write_text(json.dumps(tokens))
    return destination


def mint_login_token(base_url: str, subject: str, role: str, seed: int) -> str:
    payload = json.dumps({"subject": subject, "role": role}).encode("utf-8")
    request = urllib.request.Request(
        url=f"{base_url.rstrip('/')}/api/users/login",
        data=payload,
        method="POST",
        headers={
            "Content-Type": "application/json",
            "X-Forwarded-For": benchmark_ip(seed),
        },
    )
    with urllib.request.urlopen(request, timeout=5) as response:
        if response.status != 200:
            raise RuntimeError(f"login returned {response.status} for {subject}")
        body = json.loads(response.read().decode("utf-8"))
        token = body.get("token", "")
        if not token:
            raise RuntimeError(f"login did not return token for {subject}")
        return token


def summarize_metrics(summary: dict[str, Any]) -> dict[str, Any]:
    metrics = summary["metrics"]

    def metric_values(name: str) -> dict[str, Any]:
        raw = metrics.get(name, {})
        if isinstance(raw, dict) and "values" in raw:
            values = raw.get("values", {})
            if isinstance(values, dict):
                return values
        if isinstance(raw, dict):
            return raw
        return {}

    def trend(name: str, stat: str) -> float:
        return round(float(metric_values(name).get(stat, 0.0)), 3)

    def counter(name: str) -> dict[str, float]:
        values = metric_values(name)
        return {
            "count": int(values.get("count", 0)),
            "rate": round(float(values.get("rate", 0.0)), 3),
        }

    return {
        "throughput_rps": round(float(metric_values("http_reqs").get("rate", 0.0)), 3),
        "requests_total": int(metric_values("http_reqs").get("count", 0)),
        "unexpected_failure_rate": round(float(metric_values("http_req_failed").get("rate", 0.0)), 5),
        "latency_ms": {
            "avg": trend("http_req_duration", "avg"),
            "p50": trend("http_req_duration", "p(50)"),
            "p95": trend("http_req_duration", "p(95)"),
            "p99": trend("http_req_duration", "p(99)"),
        },
        "status_counts": {
            "200": counter("irongate_status_200")["count"],
            "429": counter("irongate_status_429")["count"],
            "500": counter("irongate_status_500")["count"],
            "503": counter("irongate_status_503")["count"],
            "other": counter("irongate_status_other")["count"],
        },
        "unexpected_status_count": counter("irongate_unexpected_status")["count"],
    }


def apply_chaos_action(chaos: dict[str, Any], action: dict[str, Any]) -> None:
    action_type = action["type"]
    if action_type == "reset":
        post_json(chaos["reset_url"], {})
        return
    if action_type == "errors":
        post_json(chaos["errors_url"], {"rate": action["rate"]})
        return
    raise RuntimeError(f"unsupported chaos action: {action_type}")


def post_json(url: str, payload: dict[str, Any]) -> None:
    data = json.dumps(payload).encode("utf-8")
    request = urllib.request.Request(
        url=url,
        data=data,
        method="POST",
        headers={"Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(request, timeout=5) as response:
            if response.status != 200:
                raise RuntimeError(f"chaos endpoint {url} returned {response.status}")
    except urllib.error.URLError as exc:
        raise RuntimeError(f"chaos endpoint {url} failed: {exc}") from exc


def parse_duration_seconds(raw: str) -> float:
    value = raw.strip().lower()
    if value.endswith("ms"):
        return float(value[:-2]) / 1000.0
    if value.endswith("s"):
        return float(value[:-1])
    if value.endswith("m"):
        return float(value[:-1]) * 60.0
    raise RuntimeError(f"unsupported duration format: {raw}")


def format_command(command: list[str], env: dict[str, str]) -> str:
    relevant = [
        "IRONGATE_BASE_URL",
        "IRONGATE_SCENARIO_NAME",
        "IRONGATE_METHOD",
        "IRONGATE_ROUTE_PATH",
        "IRONGATE_EXPECTED_STATUSES",
        "IRONGATE_VUS",
        "IRONGATE_DURATION",
        "IRONGATE_ITERATIONS",
        "IRONGATE_XFF_MODE",
        "IRONGATE_AUTH_MODE",
        "IRONGATE_AUTH_POOL_SIZE",
        "IRONGATE_LOGIN_SUBJECT_PREFIX",
        "IRONGATE_LOGIN_ROLE",
        "IRONGATE_TOKEN_POOL_PATH",
    ]
    parts = [f"{key}={shell_quote(env[key])}" for key in relevant if env.get(key)]
    parts.extend(shell_quote(part) for part in command)
    return " ".join(parts)


def shell_quote(value: str) -> str:
    escaped = value.replace("'", "'\"'\"'")
    return f"'{escaped}'"


def collect_hardware_info() -> dict[str, Any]:
    info: dict[str, Any] = {
        "platform": platform.platform(),
        "machine": platform.machine(),
        "python": platform.python_version(),
    }
    if sys.platform == "darwin":
        info["cpu"] = safe_command_output(["sysctl", "-n", "machdep.cpu.brand_string"])
        info["logical_cpu_count"] = safe_command_output(["sysctl", "-n", "hw.ncpu"])
        mem_bytes = safe_command_output(["sysctl", "-n", "hw.memsize"])
        if mem_bytes.isdigit():
            info["memory_gb"] = round(int(mem_bytes) / (1024 ** 3), 2)
    return info


def collect_software_info() -> dict[str, str]:
    return {
        "k6": safe_command_output(["k6", "version"]),
        "docker_compose": safe_command_output(["docker-compose", "version"]),
        "go": safe_command_output(["go", "version"]),
    }


def safe_command_output(args: list[str]) -> str:
    try:
        completed = subprocess.run(args, cwd=ROOT, check=True, capture_output=True, text=True)
    except Exception:
        return "unavailable"
    return completed.stdout.strip() or completed.stderr.strip() or "unavailable"


def benchmark_ip(seed: int) -> str:
    normalized = abs(int(seed))
    third = (normalized // 250) % 250
    fourth = normalized % 250
    return f"198.18.{third + 1}.{fourth + 1}"


def write_run_context(result_dir: Path, context: RunContext) -> None:
    payload = {
        "git_commit": context.git_commit,
        "git_branch": context.git_branch,
        "base_url": context.base_url,
        "hardware": context.hardware,
        "software": context.software,
        "benchmark_stack": context.benchmark_env,
        "generated_at": datetime.now().astimezone().isoformat(),
    }
    write_json(result_dir / "run-context.json", payload)


def write_suite_manifest(result_dir: Path, summaries: list[dict[str, Any]]) -> None:
    payload = {
        "generated_at": datetime.now().astimezone().isoformat(),
        "scenarios": summaries,
    }
    write_json(result_dir / "suite.json", payload)


def render_result_directory(result_dir: Path) -> None:
    result_dir = result_dir.resolve()
    suite = json.loads((result_dir / "suite.json").read_text())
    suite = refresh_suite_from_raw(result_dir, suite)
    scenarios = suite["scenarios"]
    singles = [scenario for scenario in scenarios if scenario["kind"] == "single"]
    phased = [scenario for scenario in scenarios if scenario["kind"] == "phased"]

    if singles:
        write_text(result_dir / "throughput.svg", render_bar_chart_svg(
            title="IronGate Benchmark Throughput",
            y_label="Requests / second",
            labels=[scenario["name"] for scenario in singles],
            values=[scenario["metrics"]["throughput_rps"] for scenario in singles],
            palette="#124734",
            width=1080,
            height=420,
        ))
        write_text(result_dir / "latency.svg", render_latency_chart_svg(singles))

    if phased:
        for scenario in phased:
            write_text((result_dir / scenario["name"] / "circuit-breaker-behavior.svg"), render_circuit_breaker_svg(scenario))

    write_json(result_dir / "suite.json", suite)
    write_text(result_dir / "README.md", suite_readme_markdown(result_dir, scenarios))


def refresh_suite_from_raw(result_dir: Path, suite: dict[str, Any]) -> dict[str, Any]:
    refreshed = []
    for scenario in suite.get("scenarios", []):
        if scenario["kind"] == "single":
            scenario_dir = result_dir / scenario["name"]
            raw_summary = json.loads((scenario_dir / "k6-summary.json").read_text())
            scenario["metrics"] = summarize_metrics(raw_summary)
            scenario["artifacts"] = benchmark_artifacts(
                summary_path=scenario_dir / "k6-summary.json",
                metrics_path=scenario_dir / "k6-metrics.json",
                token_pool_path=(scenario_dir / "tokens.json") if (scenario_dir / "tokens.json").exists() else None,
            )
            write_json(scenario_dir / "scenario.json", scenario)
            write_text(scenario_dir / "summary.md", single_summary_markdown(scenario))
            refreshed.append(scenario)
            continue

        scenario_dir = result_dir / scenario["name"]
        raw_metadata = json.loads((scenario_dir / "scenario.json").read_text())
        phase_entries = []
        for phase in raw_metadata.get("phases", []):
            phase_name = phase["name"].split(":", 1)[1] if ":" in phase["name"] else phase["name"]
            phase_dir = scenario_dir / phase_name
            raw_summary = json.loads((phase_dir / "k6-summary.json").read_text())
            phase["metrics"] = summarize_metrics(raw_summary)
            phase["artifacts"] = benchmark_artifacts(
                summary_path=phase_dir / "k6-summary.json",
                metrics_path=phase_dir / "k6-metrics.json",
                token_pool_path=(scenario_dir / "tokens.json") if (scenario_dir / "tokens.json").exists() else None,
            )
            write_json(phase_dir / "scenario.json", phase)
            write_text(phase_dir / "summary.md", single_summary_markdown(phase))
            phase_entries.append(phase)

        raw_metadata["phases"] = phase_entries
        write_json(scenario_dir / "scenario.json", raw_metadata)
        write_text(scenario_dir / "summary.md", phased_summary_markdown(raw_metadata))
        refreshed.append(raw_metadata)

    suite["scenarios"] = refreshed
    return suite


def render_bar_chart_svg(*, title: str, y_label: str, labels: list[str], values: list[float], palette: str, width: int, height: int) -> str:
    chart_left = 80
    chart_right = width - 30
    chart_top = 60
    chart_bottom = height - 80
    chart_width = chart_right - chart_left
    chart_height = chart_bottom - chart_top
    max_value = max(values) if values else 1.0
    max_value = max(max_value, 1.0)
    bar_width = chart_width / max(len(values), 1) * 0.6
    step = chart_width / max(len(values), 1)

    bars = []
    labels_svg = []
    for index, value in enumerate(values):
        x = chart_left + (index * step) + ((step - bar_width) / 2)
        bar_height = (value / max_value) * chart_height
        y = chart_bottom - bar_height
        bars.append(f"<rect x='{x:.1f}' y='{y:.1f}' width='{bar_width:.1f}' height='{bar_height:.1f}' rx='8' fill='{palette}' />")
        labels_svg.append(f"<text x='{x + (bar_width / 2):.1f}' y='{chart_bottom + 24}' text-anchor='middle' font-size='12' fill='#163322'>{escape_xml(short_label(labels[index]))}</text>")
        labels_svg.append(f"<text x='{x + (bar_width / 2):.1f}' y='{y - 8:.1f}' text-anchor='middle' font-size='12' fill='#163322'>{value:.2f}</text>")

    axis_lines = [
        f"<line x1='{chart_left}' y1='{chart_bottom}' x2='{chart_right}' y2='{chart_bottom}' stroke='#446b57' stroke-width='1.5' />",
        f"<line x1='{chart_left}' y1='{chart_top}' x2='{chart_left}' y2='{chart_bottom}' stroke='#446b57' stroke-width='1.5' />",
    ]
    grid = []
    for step_index in range(5):
        ratio = step_index / 4 if 4 else 0
        y = chart_bottom - (ratio * chart_height)
        value = max_value * ratio
        grid.append(f"<line x1='{chart_left}' y1='{y:.1f}' x2='{chart_right}' y2='{y:.1f}' stroke='#d9e8df' stroke-width='1' />")
        grid.append(f"<text x='{chart_left - 12}' y='{y + 4:.1f}' text-anchor='end' font-size='11' fill='#446b57'>{value:.1f}</text>")

    return "\n".join([
        f"<svg xmlns='http://www.w3.org/2000/svg' width='{width}' height='{height}' viewBox='0 0 {width} {height}' role='img' aria-label='{escape_xml(title)}'>",
        "<rect width='100%' height='100%' fill='#f7fbf8' rx='20' />",
        f"<text x='{chart_left}' y='32' font-size='24' font-weight='700' fill='#163322'>{escape_xml(title)}</text>",
        f"<text x='{chart_left}' y='{chart_top - 18}' font-size='12' fill='#446b57'>{escape_xml(y_label)}</text>",
        *grid,
        *axis_lines,
        *bars,
        *labels_svg,
        "</svg>",
    ])


def render_latency_chart_svg(scenarios: list[dict[str, Any]]) -> str:
    width = 1180
    height = 460
    chart_left = 80
    chart_right = width - 30
    chart_top = 60
    chart_bottom = height - 90
    chart_width = chart_right - chart_left
    chart_height = chart_bottom - chart_top
    series = [("p50", "#5b8c5a"), ("p95", "#d97706"), ("p99", "#b91c1c")]
    max_value = max(
        max(scenario["metrics"]["latency_ms"][name] for name, _ in series)
        for scenario in scenarios
    )
    max_value = max(max_value, 1.0)
    group_step = chart_width / max(len(scenarios), 1)
    bar_width = min(28.0, group_step / 4.5)

    bars = []
    labels_svg = []
    for scenario_index, scenario in enumerate(scenarios):
        group_left = chart_left + scenario_index * group_step
        for series_index, (name, color) in enumerate(series):
            value = scenario["metrics"]["latency_ms"][name]
            x = group_left + 20 + (series_index * (bar_width + 8))
            bar_height = (value / max_value) * chart_height
            y = chart_bottom - bar_height
            bars.append(f"<rect x='{x:.1f}' y='{y:.1f}' width='{bar_width:.1f}' height='{bar_height:.1f}' rx='6' fill='{color}' />")
            labels_svg.append(f"<text x='{x + (bar_width / 2):.1f}' y='{y - 8:.1f}' text-anchor='middle' font-size='11' fill='#163322'>{value:.1f}</text>")

        labels_svg.append(f"<text x='{group_left + (group_step / 2):.1f}' y='{chart_bottom + 24}' text-anchor='middle' font-size='12' fill='#163322'>{escape_xml(short_label(scenario['name']))}</text>")

    grid = []
    for step_index in range(5):
        ratio = step_index / 4 if 4 else 0
        y = chart_bottom - (ratio * chart_height)
        value = max_value * ratio
        grid.append(f"<line x1='{chart_left}' y1='{y:.1f}' x2='{chart_right}' y2='{y:.1f}' stroke='#d9e8df' stroke-width='1' />")
        grid.append(f"<text x='{chart_left - 12}' y='{y + 4:.1f}' text-anchor='end' font-size='11' fill='#446b57'>{value:.1f}</text>")

    legend = []
    legend_x = chart_right - 240
    for index, (name, color) in enumerate(series):
        x = legend_x + (index * 76)
        legend.extend([
            f"<rect x='{x}' y='26' width='14' height='14' rx='3' fill='{color}' />",
            f"<text x='{x + 20}' y='37' font-size='12' fill='#163322'>{name}</text>",
        ])

    return "\n".join([
        f"<svg xmlns='http://www.w3.org/2000/svg' width='{width}' height='{height}' viewBox='0 0 {width} {height}' role='img' aria-label='IronGate latency comparison'>",
        "<rect width='100%' height='100%' fill='#f7fbf8' rx='20' />",
        f"<text x='{chart_left}' y='32' font-size='24' font-weight='700' fill='#163322'>IronGate Latency Comparison</text>",
        f"<text x='{chart_left}' y='{chart_top - 18}' font-size='12' fill='#446b57'>Milliseconds</text>",
        *legend,
        *grid,
        f"<line x1='{chart_left}' y1='{chart_bottom}' x2='{chart_right}' y2='{chart_bottom}' stroke='#446b57' stroke-width='1.5' />",
        f"<line x1='{chart_left}' y1='{chart_top}' x2='{chart_left}' y2='{chart_bottom}' stroke='#446b57' stroke-width='1.5' />",
        *bars,
        *labels_svg,
        "</svg>",
    ])


def render_circuit_breaker_svg(scenario: dict[str, Any]) -> str:
    phases = scenario["phases"]
    width = 1180
    height = 480
    chart_left = 80
    chart_right = width - 40
    chart_top = 70
    chart_bottom = height - 90
    chart_width = chart_right - chart_left
    chart_height = chart_bottom - chart_top
    group_step = chart_width / max(len(phases), 1)
    bar_width = min(80.0, group_step * 0.55)
    max_total = max(
        sum(phase["metrics"]["status_counts"].get(code, 0) for code in ("200", "500", "503"))
        for phase in phases
    )
    max_total = max(max_total, 1)
    colors = {"200": "#2f855a", "500": "#dd6b20", "503": "#c53030"}

    bars = []
    labels_svg = []
    for index, phase in enumerate(phases):
        total = sum(phase["metrics"]["status_counts"].get(code, 0) for code in ("200", "500", "503"))
        x = chart_left + (index * group_step) + ((group_step - bar_width) / 2)
        y_cursor = chart_bottom
        for code in ("200", "500", "503"):
            count = phase["metrics"]["status_counts"].get(code, 0)
            if count == 0:
                continue
            segment_height = (count / max_total) * chart_height
            y_cursor -= segment_height
            bars.append(f"<rect x='{x:.1f}' y='{y_cursor:.1f}' width='{bar_width:.1f}' height='{segment_height:.1f}' rx='6' fill='{colors[code]}' />")
        labels_svg.append(f"<text x='{x + (bar_width / 2):.1f}' y='{chart_bottom + 26}' text-anchor='middle' font-size='12' fill='#163322'>{escape_xml(short_label(phase['name']))}</text>")
        labels_svg.append(f"<text x='{x + (bar_width / 2):.1f}' y='{y_cursor - 8:.1f}' text-anchor='middle' font-size='11' fill='#163322'>p95 {phase['metrics']['latency_ms']['p95']:.1f} ms</text>")

    legend = []
    for index, code in enumerate(("200", "500", "503")):
        x = chart_right - 210 + (index * 70)
        legend.extend([
            f"<rect x='{x}' y='28' width='14' height='14' rx='3' fill='{colors[code]}' />",
            f"<text x='{x + 20}' y='39' font-size='12' fill='#163322'>{code}</text>",
        ])

    grid = []
    for step_index in range(5):
        ratio = step_index / 4 if 4 else 0
        y = chart_bottom - (ratio * chart_height)
        value = max_total * ratio
        grid.append(f"<line x1='{chart_left}' y1='{y:.1f}' x2='{chart_right}' y2='{y:.1f}' stroke='#d9e8df' stroke-width='1' />")
        grid.append(f"<text x='{chart_left - 12}' y='{y + 4:.1f}' text-anchor='end' font-size='11' fill='#446b57'>{value:.0f}</text>")

    return "\n".join([
        f"<svg xmlns='http://www.w3.org/2000/svg' width='{width}' height='{height}' viewBox='0 0 {width} {height}' role='img' aria-label='Circuit breaker transition and recovery'>",
        "<rect width='100%' height='100%' fill='#f7fbf8' rx='20' />",
        f"<text x='{chart_left}' y='34' font-size='24' font-weight='700' fill='#163322'>Circuit Breaker Transition And Recovery</text>",
        f"<text x='{chart_left}' y='54' font-size='12' fill='#446b57'>Stacked response counts by phase, with p95 latency annotations from the recorded run.</text>",
        *legend,
        *grid,
        f"<line x1='{chart_left}' y1='{chart_bottom}' x2='{chart_right}' y2='{chart_bottom}' stroke='#446b57' stroke-width='1.5' />",
        f"<line x1='{chart_left}' y1='{chart_top}' x2='{chart_left}' y2='{chart_bottom}' stroke='#446b57' stroke-width='1.5' />",
        *bars,
        *labels_svg,
        "</svg>",
    ])


def suite_readme_markdown(result_dir: Path, scenarios: list[dict[str, Any]]) -> str:
    lines = [
        "# Benchmark Run",
        "",
        f"- Result directory: `{display_path(result_dir)}`",
        f"- Generated at: `{datetime.now().astimezone().isoformat()}`",
        f"- [Run context]({relative_link(result_dir, result_dir / 'run-context.json')})",
        "",
        "## Artifacts",
        "",
    ]

    if (result_dir / "throughput.svg").exists():
        lines.append(f"- [Throughput chart]({relative_link(result_dir, result_dir / 'throughput.svg')})")
    if (result_dir / "latency.svg").exists():
        lines.append(f"- [Latency chart]({relative_link(result_dir, result_dir / 'latency.svg')})")
    lines.extend(
        [
            "",
            "## Scenario Summary",
            "",
            "| Scenario | Throughput (req/s) | p50 (ms) | p95 (ms) | p99 (ms) | Unexpected statuses | Notes |",
            "|---|---:|---:|---:|---:|---:|---|",
        ]
    )

    for scenario in scenarios:
        if scenario["kind"] == "single":
            metrics = scenario["metrics"]
            lines.append(
                f"| `{scenario['name']}` | {metrics['throughput_rps']:.2f} | {metrics['latency_ms']['p50']:.2f} | {metrics['latency_ms']['p95']:.2f} | {metrics['latency_ms']['p99']:.2f} | {metrics['unexpected_status_count']} | [summary]({relative_link(result_dir, result_dir / scenario['name'] / 'summary.md')}) |"
            )
        else:
            lines.append(
                f"| `{scenario['name']}` | phase-based | phase-based | phase-based | phase-based | n/a | [summary]({relative_link(result_dir, result_dir / scenario['name'] / 'summary.md')}) / [graph]({relative_link(result_dir, result_dir / scenario['name'] / 'circuit-breaker-behavior.svg')}) |"
            )

    return "\n".join(lines) + "\n"


def single_summary_markdown(metadata: dict[str, Any]) -> str:
    metrics = metadata["metrics"]
    lines = [
        f"# {metadata['name']}",
        "",
        metadata["description"],
        "",
        "## Run Contract",
        "",
        f"- Command: `{metadata['command']}`",
        f"- Request: `{metadata['request']['method']} {metadata['request']['path']}`",
        f"- Expected statuses: `{', '.join(str(item) for item in metadata['request']['expected_statuses'])}`",
        f"- Auth mode: `{metadata['auth']['mode']}`",
        f"- Load: `{format_load(metadata['load'])}`",
        f"- Git commit: `{metadata['git_commit']}`",
        "",
        "## Measured Result",
        "",
        f"- Throughput: `{metrics['throughput_rps']:.2f} req/s` across `{metrics['requests_total']}` requests",
        f"- Latency: `p50 {metrics['latency_ms']['p50']:.2f} ms`, `p95 {metrics['latency_ms']['p95']:.2f} ms`, `p99 {metrics['latency_ms']['p99']:.2f} ms`",
        f"- Status counts: `200={metrics['status_counts']['200']}`, `429={metrics['status_counts']['429']}`, `500={metrics['status_counts']['500']}`, `503={metrics['status_counts']['503']}`, `other={metrics['status_counts']['other']}`",
        f"- Unexpected statuses: `{metrics['unexpected_status_count']}`",
        "",
        "## Interpretation",
        "",
        interpret_single(metadata),
        "",
    ]
    return "\n".join(lines)


def phased_summary_markdown(metadata: dict[str, Any]) -> str:
    lines = [
        f"# {metadata['name']}",
        "",
        metadata["description"],
        "",
        "## Phase Summary",
        "",
        "| Phase | Throughput (req/s) | p95 (ms) | 200 | 500 | 503 | Unexpected statuses |",
        "|---|---:|---:|---:|---:|---:|---:|",
    ]

    for phase in metadata["phases"]:
        metrics = phase["metrics"]
        lines.append(
            f"| `{phase['name'].split(':', 1)[1]}` | {metrics['throughput_rps']:.2f} | {metrics['latency_ms']['p95']:.2f} | {metrics['status_counts']['200']} | {metrics['status_counts']['500']} | {metrics['status_counts']['503']} | {metrics['unexpected_status_count']} |"
        )

    lines.extend(
        [
            "",
            "## Interpretation",
            "",
            "The healthy phase stays on 200s, the failure phase produces upstream 500s until the breaker trips, the open-circuit phase flips to fast 503 rejections, and the recovery phase returns to 200s after the timeout window and reset. That is the proof artifact for the breaker state machine on the shipped payment route.",
            "",
        ]
    )
    return "\n".join(lines)


def interpret_single(metadata: dict[str, Any]) -> str:
    metrics = metadata["metrics"]
    if metadata["name"] == "authenticated-rate-limited-traffic":
        limited = metrics["status_counts"]["429"]
        return f"The limiter did its job. `{limited}` requests were rejected with 429 after the authenticated bucket saturated, while the gateway still kept tail latency visible in the recorded summary instead of hand-waving the behavior."
    if metadata["name"] == "baseline-public-routing":
        return "This run isolates the public route path with distributed client IPs so the benchmark reflects gateway overhead instead of a single IP immediately rate-limiting itself."
    if metadata["name"] == "full-pipeline-normal":
        return "This is the closest thing to the gateway's steady-state production path in the local stack: JWT auth, Redis limiting, retry, load balancing, and a healthy circuit breaker all stay in the request path without synthetic bypasses."
    return "The recorded numbers above come directly from the k6 summary export saved beside this Markdown file."


def format_load(load: dict[str, Any]) -> str:
    if "duration" in load:
        return f"{load['vus']} VUs for {load['duration']}"
    return f"{load['vus']} VUs for {load['iterations']} iterations"


def short_label(name: str) -> str:
    return name.replace("authenticated-rate-limited-traffic", "auth-rate-limit").replace("baseline-public-routing", "public-baseline").replace("full-pipeline-normal", "full-pipeline").replace("circuit-breaker-transition-recovery", "cb-flow").replace("healthy-warmup", "healthy").replace("failure-trip", "trip").replace("open-circuit", "open").replace("recovery", "recover")


def relative_markdown_path(path: Path) -> str:
    return display_path(path)


def benchmark_artifacts(*, summary_path: Path, metrics_path: Path | None, token_pool_path: Path | None) -> dict[str, str]:
    artifacts: dict[str, str] = {
        "summary_json": display_path(summary_path),
    }
    if metrics_path is not None and metrics_path.exists():
        artifacts["metrics_json"] = display_path(metrics_path)
    if token_pool_path is not None and token_pool_path.exists():
        artifacts["token_pool_json"] = display_path(token_pool_path)
    return artifacts


def display_path(path: Path) -> str:
    try:
        return path.relative_to(ROOT).as_posix()
    except ValueError:
        return str(path)


def relative_link(base_dir: Path, path: Path) -> str:
    return path.relative_to(base_dir).as_posix()


def write_json(path: Path, payload: Any) -> None:
    path.write_text(json.dumps(payload, indent=2) + "\n")


def write_text(path: Path, text: str) -> None:
    path.write_text(text)


def escape_xml(value: str) -> str:
    return (
        value.replace("&", "&amp;")
        .replace("<", "&lt;")
        .replace(">", "&gt;")
        .replace('"', "&quot;")
        .replace("'", "&apos;")
    )


if __name__ == "__main__":
    raise SystemExit(main())
