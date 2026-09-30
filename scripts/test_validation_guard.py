import hashlib
import io
import json
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest

from run_workflow_validation import reservation
from verify_release_benchmark import verify_evidence


class SpendingTest(unittest.TestCase):
    def test_reservations_include_unfinished_runs_and_in_flight_margin(self):
        ledger = {"runs": [{"charged_usd": .4, "status": "reserved"}]}
        amount, guard = reservation(ledger, 2, .5, (.75, 3, .75))
        self.assertEqual(amount, .5)
        self.assertAlmostEqual(guard, .5 - .120576)
        with self.assertRaises(ValueError):
            reservation(ledger, .5, .5, (.75, 3, .75))
        with self.assertRaises(ValueError):
            reservation(ledger, float("nan"), .5, (.75, 3, .75))


class EvidenceTest(unittest.TestCase):
    def test_changed_source_trace_and_accounting_block_release(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            subprocess.run(["git", "init", "-q", str(root)], check=True)
            (root / "pkg").mkdir()
            product = root / "pkg/source.go"
            product.write_text("package example\n")
            snapshot = root / "source.tar.gz"
            with tarfile.open(snapshot, "w:gz") as archive:
                body = product.read_bytes()
                info = tarfile.TarInfo("pkg/source.go")
                info.size = len(body)
                archive.addfile(info, io.BytesIO(body))
            event = {"type": "message_end", "message": {"role": "assistant", "usage": {
                "input": 10, "output": 2, "cacheRead": 3, "cacheWrite": 0}}}
            trace = root / "events.jsonl"
            trace.write_text(json.dumps(event) + "\n")
            digest = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
            data = {"metadata": {"source_snapshot_path": "source.tar.gz", "source_snapshot_sha256": digest(snapshot),
                    "prices": {k + "_per_million": 1 for k in ("input", "output", "cache_read", "cache_write")}},
                    "results": [{"task": "example", "profile": "standard", "trace_path": "events.jsonl",
                    "trace_sha256": digest(trace), "usage": {"requests": 1, "input": 10, "output": 2,
                    "cache_read": 3, "cache_write": 0, "estimated_cost_usd": .000015}}]}
            self.assertEqual(verify_evidence(data, root, root), [])
            product.write_text("package changed\n")
            self.assertTrue(any("source differs" in e for e in verify_evidence(data, root, root)))
            product.write_text("package example\n")
            data["results"][0]["usage"]["input"] = 9
            self.assertTrue(any("usage does not match" in e for e in verify_evidence(data, root, root)))
            data["results"][0]["usage"]["input"] = 10
            trace.write_text("{}\n")
            self.assertTrue(any("changed native trace" in e for e in verify_evidence(data, root, root)))


if __name__ == "__main__":
    unittest.main()
