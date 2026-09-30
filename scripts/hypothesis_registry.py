#!/usr/bin/env python3
"""Inspect the hypothesis registry locally. Never launch runs or modify spending."""

import argparse
from collections import Counter
import hashlib
import json
import math
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[1]
DEFAULT = ROOT / "docs/experiments/registry.json"
FAMILIES = {
    "interface", "context_selection", "context_lifecycle", "execution_batching",
    "graph_delegation", "cache", "local_decisions", "architecture",
}
VERDICTS = {"promising", "confirmed", "rejected", "inconclusive", "superseded"}
STATES = {"queued", "active", "complete"}
PHASES = {"capability", "local", "screen", "confirmation", "validation"}
REQUIRED_CONTRACT = (
    "parent_snapshot", "native_control", "treatment", "control",
    "prediction", "falsifier", "tasks", "task_weights", "grading", "model_route",
    "client_version", "limits", "order_and_cache_policy", "primary_metric",
    "quality_requirements", "sampling_plan", "acceptance_rule", "stop_rules",
    "rollback", "implementation_effort_limit", "local_resource_limits",
)


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def read_catalog(path, root=ROOT):
    """Read only individual catalog entries, preserving multiline mechanisms."""
    content = (root / path).read_text()
    pattern = r"^#### ([HT]\d{2}): ([^\n]+)\n(.*?)(?=^#{1,4} |\Z)"
    records = []
    for match in re.finditer(pattern, content, re.M | re.S):
        identifier, title, body = match.groups()
        fields = dict(re.findall(
            r"^\* \*\*([^*]+)\*\*: ?(.*?)(?=^\* \*\*|^---\s*$|\Z)",
            body, re.M | re.S,
        ))
        fields = {key: value.strip() for key, value in fields.items()}
        required = {"Type", "Target Bottleneck", "Mechanism", "Implementation Surface"}
        if not required <= fields.keys():
            raise ValueError(f"Incomplete catalog entry {identifier} in {path}")
        impact = fields.get("Token & Quality Impact", fields.get("Turn & Quality Impact"))
        if not impact:
            raise ValueError(f"Missing predicted impact for {identifier}")
        records.append({
            "id": identifier, "title": title, "catalog": path,
            "line": content.count("\n", 0, match.start()) + 1,
            "type": fields["Type"], "bottleneck": fields["Target Bottleneck"],
            "mechanism": fields["Mechanism"], "predicted_impact": impact,
            "claim_status": "unmeasured_prediction",
            "implementation_surface": fields["Implementation Surface"],
        })
    return records


def unique(rows, label, errors):
    identifiers = [row["id"] for row in rows]
    if len(set(identifiers)) != len(identifiers):
        errors.append(f"Duplicate {label} IDs")
    return set(identifiers)


def validate(data, root=ROOT):
    """Check integrity and evidence requirements; not a release or spending gate."""
    errors = []
    if data.get("schema_version") != 1:
        errors.append("Unsupported registry schema")
    expected = []
    for catalog in data["catalogs"]:
        path = root / catalog["path"]
        if not path.is_file():
            errors.append(f"Missing catalog: {catalog['path']}")
            continue
        if digest(path) != catalog["sha256"]:
            errors.append(f"Catalog changed: {catalog['path']}; review and update its imported records")
        expected.extend(read_catalog(catalog["path"], root))
    source_ids = unique(data["sources"], "source", errors)
    if {row["id"] for row in expected} != source_ids:
        errors.append("Catalog ID coverage differs from registry sources")
    by_id = {row["id"]: row for row in data["sources"]}
    for row in expected:
        if row != by_id.get(row["id"]):
            errors.append(f"Imported source differs from catalog: {row['id']}")
    group_ids = unique(data["mechanisms"], "mechanism", errors)
    memberships = []
    for group in data["mechanisms"]:
        memberships.extend(group["source_ids"])
        if group["family"] not in FAMILIES:
            errors.append(f"Unknown family for {group['id']}")
        if not group["source_ids"] or not group["grouping_rationale"]:
            errors.append(f"Empty mechanism or grouping rationale: {group['id']}")
        if group["track"] not in {"incremental", "architectural"}:
            errors.append(f"Unknown exploration track: {group['id']}")
    if Counter(memberships) != Counter({identifier: 1 for identifier in source_ids}):
        errors.append("Each catalog ID must belong to exactly one mechanism group")
    experiment_ids = unique(data["experiments"], "experiment", errors)
    active = []
    for trial in data["experiments"]:
        identifier = trial["id"]
        if trial["state"] not in STATES or trial["phase"] not in PHASES:
            errors.append(f"Invalid state/phase: {identifier}")
        if not trial["mechanism_ids"] or not set(trial["mechanism_ids"]) <= group_ids:
            errors.append(f"Unknown or empty mechanism references: {identifier}")
        if not set(trial["related_source_ids"]) <= source_ids:
            errors.append(f"Unknown source references: {identifier}")
        if not set(trial["depends_on"]) <= experiment_ids or identifier in trial["depends_on"]:
            errors.append(f"Invalid experiment dependencies: {identifier}")
        verdict = trial["verdict"]
        if verdict is not None and verdict not in VERDICTS:
            errors.append(f"Invalid verdict: {identifier}")
        if trial["state"] == "complete":
            if verdict is None or not trial["result"]:
                errors.append(f"Completed experiment needs verdict and result: {identifier}")
            else:
                result = trial["result"]
                for key in ("summary", "evidence_paths", "limitations", "revisit_condition"):
                    if not result.get(key):
                        errors.append(f"Result missing {key}: {identifier}")
                for path in result.get("evidence_paths", []):
                    if not (root / path).is_file():
                        errors.append(f"Missing result evidence: {path}")
                if verdict == "confirmed":
                    if trial["phase"] not in {"confirmation", "validation"}:
                        errors.append(f"Confirmed verdict requires confirmation phase: {identifier}")
                    if result.get("quality_passed") is not True or not result.get("matched_measurements"):
                        errors.append(f"Confirmed verdict needs quality and matched measurements: {identifier}")
        elif verdict is not None or trial["result"] is not None:
            errors.append(f"Unfinished experiment cannot carry a result verdict: {identifier}")
        if trial["state"] == "active":
            active.append(identifier)
            errors.extend(contract_errors(trial))
    if len(active) > 1:
        errors.append("This campaign supports one active experiment at a time")
    if data["campaign"]["active_experiment"] != (active[0] if len(active) == 1 else None):
        errors.append("Campaign active_experiment does not match the active trial")
    campaign = data["campaign"]
    if not positive(campaign["total_cap_usd"]):
        errors.append("Campaign cap must be finite and positive")
    if campaign["paid_profiles"] != ["baseline", "standard"]:
        errors.append("Current campaign permits only baseline and standard paid profiles")
    return errors


def positive(value):
    return isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value) and value > 0


def contract_errors(trial):
    return [f"{trial['id']}: missing contract field {key}" for key in REQUIRED_CONTRACT
            if not trial["contract"].get(key)] + (
        [] if trial["contract"].get("preregistration_path") else [f"{trial['id']}: missing preregistration_path"]
    )


def ready_errors(data, trial, root=ROOT):
    """Read-only paid-screen preflight. The paid runner still owns reservations."""
    errors = contract_errors(trial)
    campaign, contract = data["campaign"], trial["contract"]
    if trial["phase"] not in {"screen", "confirmation", "validation"}:
        errors.append("Paid preflight requires screen, confirmation, or validation phase")
    if trial["state"] == "complete":
        errors.append("Completed trials cannot be rerun; create a new experiment")
    if trial["scope"] not in campaign["allowed_scopes"]:
        errors.append(f"Scope not authorized by this campaign: {trial['scope']}")
    completed = {row["id"] for row in data["experiments"] if row["state"] == "complete"}
    if not set(trial["depends_on"]) <= completed:
        errors.append("Experiment dependencies are unfinished")
    for name in ("parent_snapshot", "candidate_snapshot"):
        snapshot = (contract if name == "parent_snapshot" else trial).get(name) or {}
        path = root / snapshot.get("path", "")
        if not path.is_file() or digest(path) != snapshot.get("sha256"):
            errors.append(f"Missing or changed {name}")
    registration = root / (contract.get("preregistration_path") or "")
    if not registration.is_file() or json.loads(registration.read_text()) != contract:
        errors.append("Preregistration is missing or differs from the current contract")
    reserve, run_cap = campaign.get("confirmation_reserve_usd"), contract.get("run_cap_usd")
    if not positive(reserve) or not positive(run_cap):
        errors.append("Set positive confirmation reserve and run cap before paid work")
    ledger_path = root / campaign["ledger_path"]
    if not ledger_path.is_file():
        errors.append("Existing campaign ledger is missing; do not reset it")
        return errors
    ledger = json.loads(ledger_path.read_text())
    if ledger["total_cap_usd"] != campaign["total_cap_usd"]:
        errors.append("Ledger authorization differs from campaign cap")
    charges = [row["charged_usd"] for row in ledger["runs"]]
    if any(not isinstance(n, (int, float)) or isinstance(n, bool) or not math.isfinite(n) or n < 0 for n in charges):
        errors.append("Invalid spending ledger charges")
    elif positive(reserve) and positive(run_cap):
        protected = 0 if trial["phase"] in {"confirmation", "validation"} else reserve
        if sum(charges) + run_cap + protected > campaign["total_cap_usd"]:
            errors.append("Run cap would exceed remaining budget or consume the confirmation reserve")
    return errors


def render(data):
    groups = {row["id"]: row for row in data["mechanisms"]}
    sources = {row["id"]: row for row in data["sources"]}
    lines = ["# Hypothesis experiment registry", "", "Generated from [registry.json](registry.json). Edit JSON, then run `python3 scripts/hypothesis_registry.py render`.", "",
             f"{len(sources)} catalog ideas; {len(groups)} mechanism groups; {len(data['experiments'])} experiment cards.", "",
             "Catalog impact estimates are unmeasured predictions. Queued cards are not scheduled runs.", "",
             "See [promising trial follow-ups](promising-followups.md) for later evidence and the next gate for retained candidates.", "",
             "Use the local `hypothesis-loop` skill. Protocol: [.agents/skills/hypothesis-loop/SKILL.md](../../.agents/skills/hypothesis-loop/SKILL.md).", "",
             "## Campaign", "", f"- Provider cap: ${data['campaign']['total_cap_usd']:.2f} aggregate; reuse `{data['campaign']['ledger_path']}`.",
             "- Paid profiles: Baseline and Standard. Standard remains model-free.",
             f"- Release gate: {data['campaign']['release_gate']['minimum_token_savings']:.0%} token savings in each of {data['campaign']['release_gate']['minimum_repeats']} complete matched repetitions, lower estimated cost, no task-success loss.",
             "- A component experiment does not need to meet the entire release target.", "",
             "## Experiment cards", "", "| ID | Experiment | Mechanisms | Phase | State | Verdict |", "| --- | --- | --- | --- | --- | --- |"]
    for trial in data["experiments"]:
        lines.append(f"| {trial['id']} | {trial['title']} | {', '.join(trial['mechanism_ids'])} | {trial['phase']} | {trial['state']} | {trial['verdict'] or 'unmeasured'} |")
    lines += ["", "## Mechanism coverage", "", "| Family | Groups | Experiment cards | Completed |", "| --- | ---: | ---: | ---: |"]
    for family in sorted(FAMILIES):
        ids = {g["id"] for g in groups.values() if g["family"] == family}
        trials = [t for t in data["experiments"] if ids.intersection(t["mechanism_ids"])]
        lines.append(f"| {family} | {len(ids)} | {len(trials)} | {sum(t['state'] == 'complete' for t in trials)} |")
    lines += ["", "## Mechanisms", "", "Grouped ideas share a mechanism, not necessarily an identical implementation. Preserve their variants when designing trials.", "",
              "| Group | Catalog ideas | Family | Capability review |", "| --- | --- | --- | --- |"]
    for group in groups.values():
        links = [f"[{identifier}](../{Path(sources[identifier]['catalog']).name})" for identifier in group["source_ids"]]
        lines.append(f"| {group['id']}: {group['title'].replace('|', '/')} | {', '.join(links)} | {group['family']} | {group['capability']['state']} |")
    lines += ["", "## Results", ""]
    results = [t for t in data["experiments"] if t["state"] == "complete"]
    if not results:
        lines.append("No registered experiments have completed. Historical runs are contextual evidence, not component-isolation results.")
    for trial in results:
        lines.append(f"- **{trial['id']} ({trial['verdict']}):** {trial['result']['summary']}")
    return "\n".join(lines) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["check", "summary", "render"])
    parser.add_argument("--registry", type=Path, default=DEFAULT)
    parser.add_argument("--ready", metavar="EXPERIMENT", help="Read-only paid-screen preflight; never reserves or runs")
    parser.add_argument("--output", type=Path, help="Markdown destination for render")
    args = parser.parse_args()
    try:
        data = json.loads(args.registry.read_text())
        errors = validate(data)
        if args.ready:
            trial = next((row for row in data["experiments"] if row["id"] == args.ready), None)
            errors += ready_errors(data, trial) if trial else [f"Unknown experiment {args.ready}"]
        if errors:
            print("\n".join(errors), file=sys.stderr)
            return 1
        if args.command == "render":
            output = args.output or args.registry.with_name("README.md")
            output.write_text(render(data))
            print(f"Wrote {output}")
        else:
            print(f"Registry valid: {len(data['sources'])} ideas, {len(data['mechanisms'])} mechanisms, {len(data['experiments'])} experiments")
            if args.command == "summary":
                for trial in data["experiments"]:
                    print(f"{trial['id']} {trial['state']}/{trial['phase']} {trial['verdict'] or 'unmeasured'}: {trial['title']}")
                print("Paid readiness is separate: check --ready E001. This command never runs experiments.")
        return 0
    except (OSError, ValueError, KeyError, TypeError, AttributeError) as error:
        print(f"Registry error: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
