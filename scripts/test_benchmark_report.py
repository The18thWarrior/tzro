import json
import copy
from pathlib import Path
import tempfile
import unittest

from generate_benchmark_report import generate_report, evaluate_gate


def passing_matrix():
    data = {"client_version": "test", "metadata": {"task_ids": ["a", "b", "c"], "repeats": 3,
            "source_snapshot_sha256": "source"}, "results": []}
    for repeat in range(1, 4):
        for task in data["metadata"]["task_ids"]:
            for profile, tokens in [("baseline", 100), ("standard", 60)]:
                data["results"].append({"task": task, "profile": profile, "repeat": repeat,
                    "status": "completed", "task_success": True, "fixture_sha256": task,
                    "prompt_sha256": task, "grading_sha256": task, "evidence_complete": True,
                    "trace_sha256": "trace", "usage": {"input": tokens, "output": 0,
                    "cache_read": 0, "cache_write": 0, "requests": 1, "complete": True,
                    "estimated_cost_usd": tokens / 10000}})
    return data


class BenchmarkReportTest(unittest.TestCase):
    def test_release_threshold_includes_33_percent_and_rejects_32_percent(self):
        data = passing_matrix()
        for cell in data["results"]:
            if cell["profile"] == "standard":
                cell["usage"].update(input=67, estimated_cost_usd=.0067)
        gate = evaluate_gate(data)
        self.assertTrue(gate["passed"], gate["reasons"])
        self.assertEqual(gate["minimum_token_savings"], .33)
        for cell in data["results"]:
            if cell["profile"] == "standard":
                cell["usage"].update(input=68, estimated_cost_usd=.0068)
        self.assertFalse(evaluate_gate(data)["passed"])

    def test_gate_requires_complete_matched_repetitions(self):
        self.assertTrue(evaluate_gate(passing_matrix())["passed"])
        mutations = {
            "missing pair": lambda d: d["results"].pop(),
            "duplicate": lambda d: d["results"].append(copy.deepcopy(d["results"][0])),
            "quality loss": lambda d: d["results"][1].update(task_success=False),
            "unmatched input": lambda d: d["results"][1].update(fixture_sha256="different"),
            "incomplete usage": lambda d: d["results"][1]["usage"].update(complete=False),
            "incomplete trace": lambda d: d["results"][1].update(evidence_complete=False),
            "only pooled savings": lambda d: d["results"][1]["usage"].update(input=82),
            "negative usage": lambda d: d["results"][1]["usage"].update(input=-1),
            "nonfinite cost": lambda d: d["results"][1]["usage"].update(estimated_cost_usd=float("nan")),
            "missing declared repeat": lambda d: d["metadata"].update(repeats=4),
        }
        for name, mutate in mutations.items():
            with self.subTest(name=name):
                data = passing_matrix()
                mutate(data)
                self.assertFalse(evaluate_gate(data)["passed"])

    def test_report_uses_measured_success_and_source_state(self):
        source = Path(__file__).resolve().parents[1] / "docs/benchmarks/workflows-20260928.json"
        with tempfile.TemporaryDirectory() as directory:
            destination = Path(directory) / "report.md"
            generate_report(str(source), str(destination))
            report = destination.read_text()
        self.assertIn("6/8 (75.0%)", report)
        self.assertIn("24 cells", report)
        self.assertIn("dirty", report)
        self.assertNotIn("clean tree", report)
        self.assertNotIn("4/4 Baseline", report)
        self.assertNotIn("3,033", report)
        self.assertIn("item.Tags undefined", report)
        self.assertIn("not validated", report)


if __name__ == "__main__":
    unittest.main()
