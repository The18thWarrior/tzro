import hashlib
import io
import json
from pathlib import Path
import subprocess
import sys
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


class TestNativeValidationGuard_InterruptedCellCannotBeSilentlyRelaunched(unittest.TestCase):
    def test_native_guard_cli_reserves_then_records_unknown_charges(self):
        script = Path(__file__).with_name("run_workflow_validation.py")
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ledger = root / "native.json"
            base = [sys.executable, str(script)]
            identity = ["--ledger", str(ledger), "--run-id", "r1", "--cell-id", "c1"]
            reserve = base + ["native-reserve"] + identity + [
                "--model", "gemini-3.8-flash-low", "--contract-hash", "contract-one",
                "--max-launches", "1", "--fake",
            ]
            result = subprocess.run(reserve, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            receipt = root / "receipt.json"
            receipt.write_text(json.dumps({"exit_code": 0, "usage": {"total_tokens": 12, "estimated_cost_usd": 3}}))
            result = subprocess.run(base + ["native-complete"] + identity + ["--receipt", str(receipt)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            saved = json.loads(ledger.read_text())["runs"][0]
            self.assertEqual(saved["status"], "completed")
            self.assertEqual(saved["usage"]["total_tokens"], 12)
            self.assertEqual(saved["charged_usd"], "unknown")
            repeated = subprocess.run(reserve, capture_output=True, text=True)
            self.assertNotEqual(repeated.returncode, 0)
            amended = base + ["native-reserve", "--ledger", str(ledger), "--run-id", "amended", "--cell-id", "c1",
                              "--model", "gemini-3.8-flash-low", "--contract-hash", "contract-two", "--max-launches", "2",
                              "--authorized", "--fake", "--amends-contract", "contract-one", "--amendment-reason", "explicit test amendment"]
            result = subprocess.run(amended, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            current = json.loads(ledger.read_text())
            self.assertEqual(current["runs"][0], saved)
            self.assertEqual(len(current["amendments"]), 1)

    def test_interrupted_cell_cannot_be_silently_relaunched(self):
        from run_workflow_validation import launch_guarded_native_cell, reserve_native_gemini_cell

        with tempfile.TemporaryDirectory() as directory:
            ledger = Path(directory) / "spend.json"
            run_id = "test-run-1"
            cell_id = "go-pricing-boundary_tzro"
            model = "gemini-3.8-flash-low"
            contract_hash = "abc123hash"

            # 1. Simulate an interrupted cell execution
            launch_count = 0

            def interrupted_spawn():
                nonlocal launch_count
                launch_count += 1
                raise KeyboardInterrupt("Simulated task interruption")

            with self.assertRaises(KeyboardInterrupt):
                launch_guarded_native_cell(
                    ledger_path=ledger,
                    run_id=run_id,
                    cell_id=cell_id,
                    model=model,
                    contract_hash=contract_hash,
                    spawn_func=interrupted_spawn,
                    is_fake=True,
                )

            self.assertEqual(launch_count, 1)

            # Assert the unfinished reservation remains in the ledger
            data = json.loads(ledger.read_text())
            self.assertEqual(len(data["runs"]), 1)
            self.assertEqual(data["runs"][0]["run_id"], run_id)
            self.assertEqual(data["runs"][0]["cell_id"], cell_id)
            self.assertEqual(data["runs"][0]["status"], "reserved")
            self.assertEqual(data["runs"][0]["usage"], "unknown")
            self.assertIsNone(data["runs"][0]["finished"])

            # 2. Attempt another launch with the same run/cell ID
            replacement_launched = False

            def replacement_spawn():
                nonlocal replacement_launched
                replacement_launched = True
                return {"exit_code": 0}

            with self.assertRaises(ValueError) as ctx:
                launch_guarded_native_cell(
                    ledger_path=ledger,
                    run_id=run_id,
                    cell_id=cell_id,
                    model=model,
                    contract_hash=contract_hash,
                    spawn_func=replacement_spawn,
                    is_fake=True,
                )

            # Assert no replacement launch occurred
            self.assertFalse(replacement_launched)
            self.assertIn("cannot silently retry", str(ctx.exception))

            # Ledger still has exactly 1 entry, still in 'reserved' state
            after_data = json.loads(ledger.read_text())
            self.assertEqual(len(after_data["runs"]), 1)
            self.assertEqual(after_data["runs"][0]["status"], "reserved")

    def test_guard_validations(self):
        from run_workflow_validation import reserve_native_gemini_cell

        with tempfile.TemporaryDirectory() as directory:
            ledger = Path(directory) / "spend.json"
            # Requires authorization for live
            with self.assertRaises(ValueError):
                reserve_native_gemini_cell(
                    ledger, "r1", "c1", "gemini-3.8-flash-low", "hash1",
                    max_launches=2, authorized=False, is_fake=False
                )

            # Allows fake without authorization
            res1 = reserve_native_gemini_cell(
                ledger, "r1", "c1", "gemini-3.8-flash-low", "hash1",
                max_launches=2, is_fake=True
            )
            self.assertEqual(res1["status"], "reserved")

            # Contract hash mismatch
            with self.assertRaises(ValueError):
                reserve_native_gemini_cell(
                    ledger, "r1", "c2", "gemini-3.8-flash-low", "different_hash",
                    max_launches=2, is_fake=True
                )

            # Model mismatch
            with self.assertRaises(ValueError):
                reserve_native_gemini_cell(
                    ledger, "r1", "c2", "different-model", "hash1",
                    max_launches=2, is_fake=True
                )

            # Live launch allowance exhaustion with max_launches=1
            ledger_capped = Path(directory) / "capped.json"
            reserve_native_gemini_cell(
                ledger_capped, "r1", "c1", "gemini-3.8-flash-low", "hash1",
                max_launches=1, authorized=True, is_fake=False
            )
            with self.assertRaises(ValueError) as ctx:
                reserve_native_gemini_cell(
                    ledger_capped, "r1", "c2", "gemini-3.8-flash-low", "hash1",
                    max_launches=1, authorized=True, is_fake=False
                )
            self.assertIn("Launch allowance exhausted", str(ctx.exception))


class NativeContractAmendmentTest(unittest.TestCase):
    def test_amendment_preserves_spending_and_requires_explicit_authority(self):
        from run_workflow_validation import reserve_native_gemini_cell, complete_native_gemini_cell
        with tempfile.TemporaryDirectory() as directory:
            ledger = Path(directory)/"native.json"
            with self.assertRaises(ValueError):
                reserve_native_gemini_cell(ledger,"new","one","model","newhash",2,True,False,amends_contract="oldhash",amendment_reason="missing ledger")
            reserve_native_gemini_cell(ledger,"old","one","model","oldhash",1,True,False)
            original=json.loads(ledger.read_text())["runs"][0]
            with self.assertRaises(ValueError):
                reserve_native_gemini_cell(ledger,"new","one","model","newhash",2,True,False,amends_contract="oldhash",amendment_reason="authorized client freeze")
            complete_native_gemini_cell(ledger,"old","one",0,{"total_tokens":12})
            original=json.loads(ledger.read_text())["runs"][0]
            for authorization, previous, reason in [(False,"oldhash","reason"),(True,"wrong","reason"),(True,"oldhash","")]:
                with self.assertRaises(ValueError):
                    reserve_native_gemini_cell(ledger,"new","one","model","newhash",2,authorization,False,amends_contract=previous,amendment_reason=reason)
                self.assertEqual(json.loads(ledger.read_text())["contract_hash"],"oldhash")
            reserve_native_gemini_cell(ledger,"new","one","model","newhash",2,True,False,amends_contract="oldhash",amendment_reason="authorized client freeze")
            current=json.loads(ledger.read_text())
            self.assertEqual(current["runs"][0],original)
            self.assertEqual(current["max_launches"],2)
            self.assertEqual(len(current["amendments"]),1)
            with self.assertRaises(ValueError):
                reserve_native_gemini_cell(ledger,"new","two","model","newhash",2,True,False)


if __name__ == "__main__":
    unittest.main()
