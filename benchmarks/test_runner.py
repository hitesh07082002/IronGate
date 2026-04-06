import importlib.util
import json
import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock


def load_runner_module():
    path = Path(__file__).with_name("runner.py").resolve()
    spec = importlib.util.spec_from_file_location("benchmarks_runner_under_test", path)
    if spec is None or spec.loader is None:
        raise ImportError(f"unable to load benchmark runner module from {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


runner = load_runner_module()


def summary_fixture(*, rate: float = 123.456, count: int = 10) -> dict:
    return {
        "setup_data": {
            "tokens": ["token-a", "token-b"],
        },
        "metrics": {
            "http_reqs": {"values": {"count": count, "rate": rate}},
            "http_req_failed": {"value": 0.125, "passes": 7, "fails": 1},
            "http_req_duration": {
                "values": {
                    "avg": 5.123,
                    "p(50)": 4.0,
                    "p(95)": 8.0,
                    "p(99)": 9.0,
                }
            },
            "irongate_status_200": {"values": {"count": count, "rate": rate}},
            "irongate_status_429": {"values": {"count": 0, "rate": 0.0}},
            "irongate_status_500": {"values": {"count": 0, "rate": 0.0}},
            "irongate_status_503": {"values": {"count": 0, "rate": 0.0}},
            "irongate_status_other": {"values": {"count": 0, "rate": 0.0}},
            "irongate_unexpected_status": {"values": {"count": 0, "rate": 0.0}},
        }
    }


def write_json(path: Path, payload: dict) -> None:
    path.write_text(json.dumps(payload, indent=2) + "\n")


class DisplayPathTests(unittest.TestCase):
    def test_display_path_returns_repo_relative_path_for_repo_files(self):
        path = runner.ROOT / "benchmarks" / "scenarios.json"

        self.assertEqual(runner.display_path(path), "benchmarks/scenarios.json")

    def test_display_path_returns_absolute_path_for_external_files(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            path = Path(tmpdir) / "external.json"
            path.write_text("{}\n")

            self.assertEqual(runner.display_path(path), str(path))


class EnsureDependenciesTests(unittest.TestCase):
    def test_skip_stack_does_not_require_docker_compose(self):
        binaries = {"k6": "/tmp/k6", "python3": "/usr/bin/python3"}

        with mock.patch.object(runner.shutil, "which", side_effect=lambda name: binaries.get(name)):
            runner.ensure_dependencies(skip_stack=True)

    def test_skip_stack_accepts_k6_via_mise_exec(self):
        binaries = {"k6": None, "mise": "/usr/bin/mise", "python3": "/usr/bin/python3"}

        with mock.patch.object(runner.shutil, "which", side_effect=lambda name: binaries.get(name)):
            with mock.patch.object(runner.subprocess, "run") as run_mock:
                run_mock.return_value = mock.Mock(returncode=0)

                runner.ensure_dependencies(skip_stack=True)

        run_mock.assert_called_once_with(
            ["/usr/bin/mise", "exec", "--", "k6", "version"],
            cwd=runner.ROOT,
            capture_output=True,
            text=True,
            check=False,
        )

    def test_managed_stack_requires_compose_plugin_or_legacy_binary(self):
        binaries = {"k6": "/tmp/k6", "python3": "/usr/bin/python3", "docker": None, "docker-compose": None}

        with mock.patch.object(runner.shutil, "which", side_effect=lambda name: binaries.get(name)):
            with self.assertRaises(SystemExit) as raised:
                runner.ensure_dependencies(skip_stack=False)

        self.assertEqual(str(raised.exception), runner.MISSING_COMPOSE_TOOLING)


class ComposeDetectionTests(unittest.TestCase):
    def test_detect_compose_command_prefers_docker_compose_plugin(self):
        binaries = {"docker": "/usr/bin/docker", "docker-compose": "/usr/local/bin/docker-compose"}

        with mock.patch.object(runner.shutil, "which", side_effect=lambda name: binaries.get(name)):
            with mock.patch.object(runner.subprocess, "run") as run_mock:
                run_mock.return_value = mock.Mock(returncode=0)

                self.assertEqual(runner.detect_compose_command(), ["/usr/bin/docker", "compose"])

        run_mock.assert_called_once_with(
            ["/usr/bin/docker", "compose", "version"],
            capture_output=True,
            text=True,
            check=False,
        )

    def test_detect_compose_command_falls_back_to_legacy_binary(self):
        binaries = {"docker": "/usr/bin/docker", "docker-compose": "/usr/local/bin/docker-compose"}

        with mock.patch.object(runner.shutil, "which", side_effect=lambda name: binaries.get(name)):
            with mock.patch.object(runner.subprocess, "run") as run_mock:
                run_mock.return_value = mock.Mock(returncode=1)

                self.assertEqual(runner.detect_compose_command(), ["/usr/local/bin/docker-compose"])


class BenchmarkEnvTests(unittest.TestCase):
    def test_build_benchmark_env_uses_managed_stack_defaults(self):
        with mock.patch.dict(
            os.environ,
            {
                "JWT_SECRET": "managed-secret",
                "IRONGATE_ALLOW_LOGIN_OVERRIDES": "false",
            },
            clear=False,
        ):
            env = runner.build_benchmark_env(skip_stack=False)

        self.assertEqual(env["JWT_SECRET"], "managed-secret")
        self.assertEqual(env["GRAFANA_ADMIN_USER"], runner.DEFAULT_GRAFANA_USER)
        self.assertEqual(env["IRONGATE_ALLOW_LOGIN_OVERRIDES"], "false")
        self.assertEqual(env["IRONGATE_TRUSTED_PROXIES"], runner.DEFAULT_TRUSTED_PROXIES)

    def test_build_benchmark_env_does_not_invent_skip_stack_defaults(self):
        with mock.patch.dict(
            os.environ,
            {
                "JWT_SECRET": "external-secret",
                "IRONGATE_ALLOW_LOGIN_OVERRIDES": "true",
                "UNRELATED": "keep-out",
            },
            clear=True,
        ):
            env = runner.build_benchmark_env(skip_stack=True)

        self.assertEqual(
            env,
            {
                "JWT_SECRET": "external-secret",
                "IRONGATE_ALLOW_LOGIN_OVERRIDES": "true",
            },
        )


class BuildK6EnvTests(unittest.TestCase):
    def test_build_k6_env_strips_inherited_irongate_keys(self):
        context = runner.RunContext(
            base_url="http://127.0.0.1:8080",
            git_commit="d1edb38",
            git_branch="feat/test",
            hardware={},
            software={},
            benchmark_env={},
        )
        request_config = {
            "method": "GET",
            "path": "/api/orders",
            "expected_statuses": [200],
        }
        auth_config = {"mode": "pool", "pool_size": 4, "subject_prefix": "bench", "role": "user"}
        load_config = {"vus": 4, "duration": "10s", "sleep_ms": 100}

        with mock.patch.dict(os.environ, {"IRONGATE_STALE": "yes", "PATH": os.environ.get("PATH", "")}, clear=False):
            env = runner.build_k6_env(
                "full-pipeline-normal",
                request_config,
                auth_config,
                load_config,
                context,
                None,
            )

        self.assertNotIn("IRONGATE_STALE", env)
        self.assertEqual(env["IRONGATE_SLEEP_MS"], "100")
        self.assertEqual(env["IRONGATE_DURATION"], "10s")


class FormattingTests(unittest.TestCase):
    def test_format_command_normalizes_repo_paths_and_includes_sleep_ms(self):
        summary_path = runner.ROOT / "benchmarks" / "results" / "demo" / "k6-summary.json"
        metrics_path = runner.ROOT / "benchmarks" / "results" / "demo" / "k6-metrics.json"
        token_path = runner.ROOT / "benchmarks" / "results" / "demo" / "tokens.json"
        env = {
            "IRONGATE_BASE_URL": "http://127.0.0.1:8080",
            "IRONGATE_SCENARIO_NAME": "full-pipeline-normal",
            "IRONGATE_METHOD": "GET",
            "IRONGATE_ROUTE_PATH": "/api/orders",
            "IRONGATE_EXPECTED_STATUSES": "200",
            "IRONGATE_VUS": "24",
            "IRONGATE_DURATION": "20s",
            "IRONGATE_SLEEP_MS": "100",
            "IRONGATE_REQUEST_BODY": '{"username":"demo"}',
            "IRONGATE_AUTH_MODE": "pool",
            "IRONGATE_AUTH_POOL_SIZE": "32",
            "IRONGATE_LOGIN_SUBJECT_PREFIX": "bench-order-user",
            "IRONGATE_LOGIN_ROLE": "user",
            "IRONGATE_TOKEN_POOL_PATH": str(token_path),
        }
        command = [
            "k6",
            "run",
            "--summary-export",
            str(summary_path),
            "--out",
            f"json={metrics_path}",
            str(runner.ROUTE_SCRIPT),
        ]

        formatted = runner.format_command(command, env)

        self.assertIn("IRONGATE_SLEEP_MS='100'", formatted)
        self.assertIn("IRONGATE_REQUEST_BODY='{\"username\":\"demo\"}'", formatted)
        self.assertIn("IRONGATE_TOKEN_POOL_PATH='./benchmarks/results/demo/tokens.json'", formatted)
        self.assertIn("'--summary-export' './benchmarks/results/demo/k6-summary.json'", formatted)
        self.assertIn("'--out' 'json=./benchmarks/results/demo/k6-metrics.json'", formatted)
        self.assertIn("'./benchmarks/route.js'", formatted)
        self.assertNotIn(str(runner.ROOT), formatted)

    def test_sanitize_command_string_preserves_repo_relative_json_output_paths(self):
        command = (
            "IRONGATE_BASE_URL='http://127.0.0.1:8080' "
            "'k6' 'run' '--summary-export' './benchmarks/results/demo/k6-summary.json' "
            "'--out' 'json=./benchmarks/results/demo/k6-metrics.json' './benchmarks/route.js'"
        )

        sanitized = runner.sanitize_command_string(command)

        self.assertIn("'--summary-export' './benchmarks/results/demo/k6-summary.json'", sanitized)
        self.assertIn("'--out' 'json=./benchmarks/results/demo/k6-metrics.json'", sanitized)


class SanitizationTests(unittest.TestCase):
    def test_summarize_metrics_reads_http_req_failed_value_field(self):
        metrics = runner.summarize_metrics(summary_fixture())

        self.assertEqual(metrics["unexpected_failure_rate"], 0.125)

    def test_sanitize_benchmark_stack_redacts_sensitive_values(self):
        sanitized = runner.sanitize_benchmark_stack(
            {
                "JWT_SECRET": "demo-secret",
                "GRAFANA_ADMIN_USER": "admin",
                "GRAFANA_ADMIN_PASSWORD": "admin",
                "IRONGATE_TRUSTED_PROXIES": "0.0.0.0/0,::/0",
                "IRONGATE_ALLOW_LOGIN_OVERRIDES": "true",
            }
        )

        self.assertEqual(sanitized["JWT_SECRET"], "<redacted>")
        self.assertEqual(sanitized["GRAFANA_ADMIN_USER"], "<redacted>")
        self.assertEqual(sanitized["GRAFANA_ADMIN_PASSWORD"], "<redacted>")
        self.assertEqual(sanitized["IRONGATE_TRUSTED_PROXIES"], "<configure-at-runtime>")
        self.assertEqual(sanitized["IRONGATE_ALLOW_LOGIN_OVERRIDES"], "true")


class RenderResultDirectoryTests(unittest.TestCase):
    def test_render_result_directory_supports_external_results_without_event_stream(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            result_dir = Path(tmpdir).resolve()
            scenario_dir = result_dir / "baseline-public-routing"
            scenario_dir.mkdir(parents=True)

            write_json(
                result_dir / "suite.json",
                {
                    "generated_at": "2026-04-06T00:00:00+05:30",
                    "scenarios": [
                        {
                            "name": "baseline-public-routing",
                            "kind": "single",
                            "description": "Public route baseline.",
                            "request": {
                                "method": "POST",
                                "path": "/api/users/login",
                                "expected_statuses": [200],
                            },
                            "auth": {"mode": "none"},
                            "load": {"vus": 4, "duration": "5s"},
                            "command": "k6 run",
                            "metrics": {},
                            "artifacts": {},
                            "git_commit": "d1edb38",
                        }
                    ],
                },
            )
            write_json(result_dir / "run-context.json", {"git_commit": "d1edb38"})
            write_json(scenario_dir / "k6-summary.json", summary_fixture())
            write_json(
                scenario_dir / "scenario.json",
                {
                    "name": "baseline-public-routing",
                    "kind": "single",
                    "description": "Public route baseline.",
                    "request": {
                        "method": "POST",
                        "path": "/api/users/login",
                        "expected_statuses": [200],
                    },
                    "auth": {"mode": "none"},
                    "load": {"vus": 4, "duration": "5s"},
                    "command": (
                        f"'k6' 'run' '--summary-export' '{result_dir / 'baseline-public-routing' / 'k6-summary.json'}' "
                        f"'--out' 'json=./k6-metrics.json' '{runner.ROUTE_SCRIPT}'"
                    ),
                    "environment": {
                        "benchmark_stack": {
                            "JWT_SECRET": "demo-secret",
                            "GRAFANA_ADMIN_USER": "admin",
                            "GRAFANA_ADMIN_PASSWORD": "admin",
                            "IRONGATE_TRUSTED_PROXIES": "0.0.0.0/0,::/0",
                        }
                    },
                    "metrics": {},
                    "artifacts": {"token_pool_json": "tokens.json"},
                    "git_commit": "d1edb38",
                },
            )
            write_json(
                result_dir / "run-context.json",
                {
                    "git_commit": "d1edb38",
                    "benchmark_stack": {
                        "JWT_SECRET": "demo-secret",
                        "GRAFANA_ADMIN_USER": "admin",
                        "GRAFANA_ADMIN_PASSWORD": "admin",
                        "IRONGATE_TRUSTED_PROXIES": "0.0.0.0/0,::/0",
                    },
                },
            )

            runner.render_result_directory(result_dir)

            suite = json.loads((result_dir / "suite.json").read_text())
            scenario = suite["scenarios"][0]
            self.assertEqual(scenario["artifacts"], {"summary_json": str(scenario_dir / "k6-summary.json")})
            self.assertEqual(scenario["metrics"]["throughput_rps"], 123.456)
            self.assertEqual(scenario["environment"]["benchmark_stack"]["JWT_SECRET"], "<redacted>")
            self.assertNotIn("token_pool_json", scenario["artifacts"])
            self.assertNotIn(str(runner.ROOT), scenario["command"])
            self.assertIn("baseline-public-routing/k6-metrics.json", scenario["command"])
            self.assertTrue((result_dir / "throughput.svg").exists())
            self.assertTrue((result_dir / "latency.svg").exists())

            raw_summary = json.loads((scenario_dir / "k6-summary.json").read_text())
            self.assertEqual(raw_summary["setup_data"]["tokens"], [])
            self.assertEqual(raw_summary["setup_data"]["token_count"], 2)

            runner.render_result_directory(result_dir)
            rerendered_summary = json.loads((scenario_dir / "k6-summary.json").read_text())
            self.assertEqual(rerendered_summary["setup_data"]["token_count"], 2)

            run_context = json.loads((result_dir / "run-context.json").read_text())
            self.assertEqual(run_context["benchmark_stack"]["JWT_SECRET"], "<redacted>")

            readme = (result_dir / "README.md").read_text()
            self.assertIn(str(result_dir), readme)
            self.assertIn("baseline-public-routing/summary.md", readme)
            self.assertNotIn("metrics_json", readme)


if __name__ == "__main__":
    unittest.main()
