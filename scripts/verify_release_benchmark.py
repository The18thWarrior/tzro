#!/usr/bin/env python3
"""Fail closed unless saved measurements and their exact product source pass."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import tarfile

from generate_benchmark_report import evaluate_gate, TOKEN_FIELDS, RELEASE_MINIMUM_SAVINGS


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def product_path(name):
    return name.startswith(("pkg/", "cmd/")) or name in ("go.mod", "go.sum", "install.sh")


def verify_evidence(data, report_dir, source_root):
    reasons = []
    meta = data.get("metadata", {})
    snapshot = report_dir / meta.get("source_snapshot_path", "missing-source")
    if not snapshot.is_file() or digest(snapshot) != meta.get("source_snapshot_sha256"):
        reasons.append("Source snapshot is missing or has a different checksum.")
    else:
        archived = {}
        with tarfile.open(snapshot, "r:gz") as archive:
            for member in archive:
                if not product_path(member.name):
                    continue
                if not member.isfile() or ".." in Path(member.name).parts:
                    reasons.append(f"Unsupported source archive entry: {member.name}")
                    continue
                archived[member.name] = hashlib.sha256(archive.extractfile(member).read()).hexdigest()
        names = subprocess.check_output(["git", "-C", str(source_root), "ls-files", "-z", "--cached", "--others", "--exclude-standard"]).decode().split("\0")
        current = {name: digest(source_root / name) for name in names if product_path(name) and (source_root / name).is_file()}
        for name in sorted(set(current) | set(archived)):
            if current.get(name) != archived.get(name):
                reasons.append(f"Product source differs from measured source: {name}")
        if not archived:
            reasons.append("Snapshot contains no product source.")
    for cell in data.get("results", []):
        if cell.get("profile") not in ("baseline", "standard"):
            continue
        label = f"{cell['profile']}/{cell['task']}/{cell.get('repeat', 1)}"
        path = report_dir / cell.get("trace_path", "missing-trace")
        if not path.is_file() or digest(path) != cell.get("trace_sha256"):
            reasons.append(f"Missing or changed native trace: {label}")
            continue
        totals = dict.fromkeys(TOKEN_FIELDS, 0)
        requests = 0
        for line in path.read_text().splitlines():
            event = json.loads(line)
            message = event.get("message", {})
            if event.get("type") != "message_end" or message.get("role") != "assistant":
                continue
            requests += 1
            usage = message.get("usage", {})
            for field, native in (("input", "input"), ("output", "output"), ("cache_read", "cacheRead"), ("cache_write", "cacheWrite")):
                value = usage.get(native)
                if type(value) is not int or value < 0:
                    reasons.append(f"Invalid native usage: {label}")
                else:
                    totals[field] += value
        reported = cell.get("usage", {})
        if requests != reported.get("requests") or any(totals[k] != reported.get(k) for k in totals):
            reasons.append(f"Reported usage does not match native trace: {label}")
        prices = meta.get("prices", {})
        if all(k + "_per_million" in prices for k in TOKEN_FIELDS):
            cost = sum(totals[k] * prices[k + "_per_million"] for k in TOKEN_FIELDS) / 1e6
            if abs(cost - (reported.get("estimated_cost_usd") or 0)) > 1e-9:
                reasons.append(f"Reported cost does not match native usage and prices: {label}")
        else:
            reasons.append("Explicit price metadata is required.")
    return reasons


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("report", type=Path)
    parser.add_argument("--source-root", type=Path, default=Path(__file__).resolve().parents[1])
    args = parser.parse_args()
    if not args.report.is_file():
        print(f"Release blocked: validation report missing: {args.report}")
        return 1
    data = json.loads(args.report.read_text())
    reasons = evaluate_gate(data, minimum_savings=RELEASE_MINIMUM_SAVINGS, minimum_repeats=3)["reasons"]
    reasons += verify_evidence(data, args.report.resolve().parent, args.source_root.resolve())
    if data.get("metadata", {}).get("suite") != "representative":
        reasons.append("Release validation requires the complete representative suite.")
    expected = {"control_cache", "control_rate_limiter", "billing_discount", "authentication_discovery", "revenue_256", "revenue_8192", "incident_diagnosis"}
    if set(data.get("metadata", {}).get("task_ids", [])) != expected:
        reasons.append("The preregistered seven-task battery is incomplete.")
    for reason in dict.fromkeys(reasons):
        print("Release blocked: " + reason)
    if not reasons:
        print("Standard installation release gate passed; source and native evidence verified.")
    return bool(reasons)


if __name__ == "__main__":
    raise SystemExit(main())
