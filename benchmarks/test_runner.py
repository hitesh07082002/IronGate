import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock


def load_runner_module():
    path = Path(__file__).with_name("runner.py").resolve()
    spec = importlib.util.spec_from_file_location("benchmarks_runner_under_test", path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


runner = load_runner_module()


def summary_fixture(*, rate: float = 123.456, count: int = 10) -> dict:
    return {
        "metrics": {
            "http_reqs": {"values": {"count": count, "rate": rate}},
            "http_req_failed": {"values": {"count": 0, "rate": 0.0}},
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

    def test_managed_stack_requires_docker_compose(self):
        binaries = {"k6": "/tmp/k6", "python3": "/usr/bin/python3"}

        with mock.patch.object(runner.shutil, "which", side_effect=lambda name: binaries.get(name)):
            with self.assertRaises(SystemExit) as raised:
                runner.ensure_dependencies(skip_stack=False)

        self.assertEqual(str(raised.exception), "missing required tooling: docker-compose")


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

            runner.render_result_directory(result_dir)

            suite = json.loads((result_dir / "suite.json").read_text())
            scenario = suite["scenarios"][0]
            self.assertEqual(scenario["artifacts"], {"summary_json": str(scenario_dir / "k6-summary.json")})
            self.assertEqual(scenario["metrics"]["throughput_rps"], 123.456)
            self.assertTrue((result_dir / "throughput.svg").exists())
            self.assertTrue((result_dir / "latency.svg").exists())

            readme = (result_dir / "README.md").read_text()
            self.assertIn(str(result_dir), readme)
            self.assertIn("baseline-public-routing/summary.md", readme)
            self.assertNotIn("metrics_json", readme)


if __name__ == "__main__":
    unittest.main()
