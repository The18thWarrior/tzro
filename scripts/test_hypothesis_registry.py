"""Registry integrity and paid-preflight tests; no provider calls."""

import json
from pathlib import Path
import tempfile
import unittest

from hypothesis_registry import ROOT, DEFAULT, REQUIRED_CONTRACT, digest, read_catalog, ready_errors, render, validate


def fixture():
    # Campaign results are mutable data. Tests must survive later real trials.
    data = json.loads(DEFAULT.read_text())
    data["campaign"]["active_experiment"] = None
    data["campaign"]["confirmation_reserve_usd"] = None
    data["experiments"] = data["experiments"][:4]
    for trial in data["experiments"]:
        trial.update(state="queued", phase="capability", verdict=None, result=None, depends_on=[])
        trial["contract"] = {key: None for key in (*REQUIRED_CONTRACT, "preregistration_path", "run_cap_usd")}
    return data


class RegistryTests(unittest.TestCase):
    def setUp(self):
        self.data = fixture()

    def test_seed_covers_both_catalogs_without_claiming_results(self):
        self.assertEqual(validate(self.data), [])
        self.assertEqual({row["id"] for row in self.data["sources"]},
                         {row["id"] for catalog in self.data["catalogs"] for row in read_catalog(catalog["path"])})
        self.assertTrue(all(row["claim_status"] == "unmeasured_prediction" for row in self.data["sources"]))
        self.assertTrue(all(row["state"] == "queued" and row["result"] is None for row in self.data["experiments"]))
        self.assertIn("No registered experiments have completed", render(self.data))

    def test_multiline_mechanism_does_not_swallow_next_heading(self):
        rows = read_catalog("docs/token-optimization-hypotheses.md")
        entry = next(row for row in rows if row["id"] == "H48")
        self.assertIn("Public Semantic Tape", entry["mechanism"])
        self.assertIn("Ephemeral Tool Buffer", entry["mechanism"])
        self.assertNotIn("Prioritization", entry["mechanism"])
        self.assertNotIn("Token & Quality Impact", entry["mechanism"])

    def test_source_loss_edits_and_duplicate_membership_are_rejected(self):
        self.data["sources"].pop()
        self.assertTrue(any("coverage" in error for error in validate(self.data)))
        self.data = fixture()
        self.data["sources"][0]["predicted_impact"] = "proven 90%"
        self.assertTrue(any("source differs" in error for error in validate(self.data)))
        self.data = fixture()
        self.data["mechanisms"][1]["source_ids"].append("H01")
        self.assertTrue(any("exactly one" in error for error in validate(self.data)))

    def test_catalog_drift_requires_review(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for catalog in self.data["catalogs"]:
                path = root / catalog["path"]
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text((ROOT / catalog["path"]).read_text() + "\nAn editorial change.\n")
            self.assertTrue(any("Catalog changed" in error for error in validate(self.data, root)))

    def test_active_trial_requires_a_contract_and_matching_campaign_pointer(self):
        self.data["experiments"][0]["state"] = "active"
        errors = validate(self.data)
        self.assertTrue(any("missing contract field" in error for error in errors))
        self.assertTrue(any("active_experiment" in error for error in errors))

    def test_false_confirmation_is_rejected(self):
        trial = self.data["experiments"][0]
        trial.update(state="complete", verdict="confirmed", result={"summary": "Looks good"})
        errors = validate(self.data)
        self.assertTrue(any("confirmation phase" in error for error in errors))
        self.assertTrue(any("matched measurements" in error for error in errors))
        self.assertTrue(any("evidence_paths" in error for error in errors))

    def test_unknown_reference_and_verdict_are_rejected(self):
        trial = self.data["experiments"][0]
        trial.update(mechanism_ids=["M-unknown"], verdict="victory")
        errors = validate(self.data)
        self.assertTrue(any("mechanism references" in error for error in errors))
        self.assertTrue(any("Invalid verdict" in error for error in errors))


class ReadinessTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.data = fixture()
        self.trial = self.data["experiments"][0]
        self.trial["phase"] = "screen"
        contract = self.trial["contract"]
        for key in REQUIRED_CONTRACT:
            contract[key] = "specified in this local fixture"
        for name in ("parent", "candidate"):
            path = self.root / f"{name}.tar"
            path.write_text(name)
            target = contract if name == "parent" else self.trial
            target[f"{name}_snapshot"] = {"path": path.name, "sha256": digest(path)}
        contract.update(preregistration_path="contract.json", run_cap_usd=2)
        self.data["campaign"].update(ledger_path="ledger.json", total_cap_usd=20, confirmation_reserve_usd=3)
        self.write_contract()
        self.write_ledger(14)

    def write_contract(self):
        (self.root / "contract.json").write_text(json.dumps(self.trial["contract"]))

    def write_ledger(self, charged):
        (self.root / "ledger.json").write_text(json.dumps({"total_cap_usd": 20,
            "runs": [{"charged_usd": charged, "status": "reserved"}]}))

    def test_complete_preflight_is_read_only(self):
        before = (self.root / "ledger.json").read_bytes()
        self.assertEqual(ready_errors(self.data, self.trial, self.root), [])
        self.assertEqual((self.root / "ledger.json").read_bytes(), before)

    def test_unfinished_reservations_and_confirmation_budget_are_counted(self):
        self.write_ledger(16)
        self.assertTrue(any("confirmation reserve" in error for error in ready_errors(self.data, self.trial, self.root)))
        self.trial["phase"] = "confirmation"
        self.assertEqual(ready_errors(self.data, self.trial, self.root), [])
        self.write_ledger(19)
        self.assertTrue(any("remaining budget" in error for error in ready_errors(self.data, self.trial, self.root)))

    def test_missing_ledger_changed_plan_and_changed_snapshot_block_paid_work(self):
        (self.root / "ledger.json").unlink()
        self.assertTrue(any("ledger is missing" in error for error in ready_errors(self.data, self.trial, self.root)))
        self.write_ledger(14)
        self.trial["contract"]["prediction"] = "a different prediction"
        self.assertTrue(any("Preregistration" in error for error in ready_errors(self.data, self.trial, self.root)))
        self.write_contract()
        (self.root / "candidate.tar").write_text("changed")
        self.assertTrue(any("changed candidate_snapshot" in error for error in ready_errors(self.data, self.trial, self.root)))

    def test_unapproved_scope_and_nonfinite_caps_block_paid_work(self):
        self.trial["scope"] = "local_worker_prototype"
        self.assertTrue(any("Scope not authorized" in error for error in ready_errors(self.data, self.trial, self.root)))
        self.trial["scope"] = "standard_model_free"
        self.trial["contract"]["run_cap_usd"] = float("nan")
        self.write_contract()
        self.assertTrue(any("positive confirmation reserve" in error for error in ready_errors(self.data, self.trial, self.root)))

    def test_completed_trial_cannot_be_silently_rerun(self):
        self.trial["state"] = "complete"
        self.assertTrue(any("cannot be rerun" in error for error in ready_errors(self.data, self.trial, self.root)))


if __name__ == "__main__":
    unittest.main()
