#!/usr/bin/env python3
"""Render native installation evidence and evaluate a conservative release gate."""
import argparse
from collections import Counter, defaultdict
import json
import math
from pathlib import Path
import statistics


TOKEN_FIELDS = ("input", "output", "cache_read", "cache_write")
RELEASE_MINIMUM_SAVINGS = .33


def total_tokens(result):
    return sum(result.get("usage", {}).get(k, 0) for k in TOKEN_FIELDS)


def succeeded(result):
    return result.get("task_success") is True and result.get("status") == "completed"


def aggregate(results):
    usage = [r.get("usage", {}) for r in results]
    return {
        "count": len(results), "passed": sum(succeeded(r) for r in results),
        "tokens": sum(total_tokens(r) for r in results),
        "requests": sum(u.get("requests", 0) for u in usage),
        "cost": sum(u.get("estimated_cost_usd") or 0 for u in usage),
        "complete": bool(results) and all(u.get("complete") is True and u.get("estimated_cost_usd") is not None for u in usage),
        "agent_seconds": sum(r.get("agent_ms", 0) for r in results) / 1000,
        **{k: sum(u.get(k, 0) for u in usage) for k in TOKEN_FIELDS},
    }


def valid_usage(usage):
    tokens = [usage.get(k) for k in TOKEN_FIELDS]
    cost = usage.get("estimated_cost_usd")
    return (all(type(n) is int and n >= 0 for n in tokens)
            and type(usage.get("requests")) is int and usage["requests"] > 0
            and sum(tokens) > 0 and type(cost) in (int, float)
            and math.isfinite(cost) and cost >= 0)


def savings(baseline, treatment):
    return 1 - treatment / baseline if baseline > 0 else None


def evaluate_gate(data, minimum_savings=RELEASE_MINIMUM_SAVINGS, minimum_repeats=3):
    """A missing/incomplete measurement cannot authorize a release."""
    reasons = []
    cells = {}
    for r in data.get("results", []):
        if r["profile"] not in ("baseline", "standard"):
            continue
        key = (r["task"], r.get("repeat", 1), r["profile"])
        if key in cells:
            reasons.append(f"Duplicate measurement: {key}")
        cells[key] = r
    tasks = sorted({key[0] for key in cells})
    repeats = sorted({key[1] for key in cells})
    declared_repeats = data.get("metadata", {}).get("repeats")
    if type(declared_repeats) is not int or declared_repeats < minimum_repeats or repeats != list(range(1, declared_repeats + 1)):
        reasons.append("Every declared repetition must be present, numbered from one.")
    if len(tasks) < 3:
        reasons.append("At least three distinct tasks are required.")
    if len(repeats) < minimum_repeats:
        reasons.append(f"At least {minimum_repeats} complete repetitions are required.")
    expected = data.get("metadata", {}).get("task_ids")
    if not expected or set(tasks) != set(expected):
        reasons.append("A declared, complete task matrix is required.")
    if not data.get("client_version") or not data.get("metadata", {}).get("source_snapshot_sha256"):
        reasons.append("Client version and immutable source snapshot are required.")
    for task in tasks:
        for repeat in repeats:
            pair = [cells.get((task, repeat, p)) for p in ("baseline", "standard")]
            if any(r is None for r in pair):
                reasons.append(f"Missing pair: {task}, repeat {repeat}.")
                continue
            b, s = pair
            for field in ("fixture_sha256", "prompt_sha256", "grading_sha256"):
                if not b.get(field) or b.get(field) != s.get(field):
                    reasons.append(f"Unmatched {field}: {task}, repeat {repeat}.")
            if succeeded(b) and not succeeded(s):
                reasons.append(f"Standard quality regression: {task}, repeat {repeat}.")
            for r in pair:
                if not valid_usage(r.get("usage", {})):
                    reasons.append(f"Invalid usage values: {r['profile']}/{task}/{repeat}.")
                if not aggregate([r])["complete"] or not r.get("evidence_complete") or not r.get("trace_sha256"):
                    reasons.append(f"Incomplete usage or evidence: {r['profile']}/{task}/{repeat}.")
                if r.get("status") in ("not_run", "setup_incomplete", "ready"):
                    reasons.append(f"Unexecuted cell: {r['profile']}/{task}/{repeat}.")
    measurements = []
    for repeat in repeats:
        b = aggregate([r for key, r in cells.items() if key[1:] == (repeat, "baseline")])
        s = aggregate([r for key, r in cells.items() if key[1:] == (repeat, "standard")])
        reduction = savings(b["tokens"], s["tokens"])
        measurements.append({"repeat": repeat, "token_savings": reduction, "cost_savings": savings(b["cost"], s["cost"])})
        if reduction is None or reduction + 1e-12 < minimum_savings:
            reasons.append(f"Repeat {repeat} does not reach {minimum_savings:.0%} token savings.")
        if s["cost"] >= b["cost"]:
            reasons.append(f"Repeat {repeat} does not reduce estimated cost.")
        if s["passed"] < b["passed"] or not s["passed"]:
            reasons.append(f"Repeat {repeat} does not preserve task success.")
    return {"passed": not reasons, "minimum_token_savings": minimum_savings,
            "minimum_repeats": minimum_repeats, "reasons": list(dict.fromkeys(reasons)), "repetitions": measurements}


def cell_text(value):
    return str(value).replace("|", "\\|").replace("\n", "<br>")


def table(headers, rows):
    return ["| " + " | ".join(headers) + " |", "| " + " | ".join("---" for _ in headers) + " |", *[
        "| " + " | ".join(cell_text(c) for c in row) + " |" for row in rows], ""]


def generate_report(json_path, md_path, minimum_savings=RELEASE_MINIMUM_SAVINGS, minimum_repeats=3):
    data = json.loads(Path(json_path).read_text())
    meta, results = data.get("metadata", {}), data.get("results", [])
    groups = defaultdict(list)
    for r in results:
        groups[r["profile"]].append(r)
    profiles = sorted(groups, key=lambda p: ({"baseline": 0, "standard": 1, "full": 2}.get(p, 3), p))
    gate = evaluate_gate(data, minimum_savings, minimum_repeats)
    stats = {p: aggregate(groups[p]) for p in profiles}
    md = [f"# Developer workflow benchmark: {meta.get('timestamp', 'unknown date')}", "",
          f"Recipe: `{data.get('schema', 'unknown')}`. Model: `{meta.get('model', 'unknown')}`.", "",
          f"Evidence: [{Path(json_path).name}]({Path(json_path).name}). All {len(results)} cells are included.", "",
          "Total tokens include uncached input, cache reads, cache writes, and output. Costs use the recorded prices.", "",
          "## Standard installation release gate", "",
          "**Validated**" if gate["passed"] else "**not validated**", "",
          f"Required: {minimum_savings:.0%} token savings in each of at least {minimum_repeats} complete repetitions, lower estimated cost, and no paired quality regression.", ""]
    md += [f"- {reason}" for reason in gate["reasons"]] + ["", "## Aggregate results", ""]
    md += table(["Profile", "Success", "Requests", "Total tokens", "Uncached", "Output", "Cache read", "Estimated cost", "Agent time", "Usage"], [
        [p.title(), f"{s['passed']}/{s['count']} ({100*s['passed']/s['count']:.1f}%)", s["requests"], f"{s['tokens']:,}", f"{s['input']:,}", f"{s['output']:,}", f"{s['cache_read']:,}", f"${s['cost']:.8f}" + (" known subtotal" if not s["complete"] else ""), f"{s['agent_seconds']:.3f}s", "complete" if s["complete"] else "incomplete"] for p, s in stats.items()])
    md += ["## Matched successful tasks", "", "This descriptive subset excludes failed pairs. The complete matrix remains authoritative for quality and the release gate.", ""]
    baseline = {(r["task"], r.get("repeat", 1)): r for r in groups.get("baseline", [])}
    rows = []
    for p in profiles:
        if p == "baseline":
            continue
        pairs = [(baseline.get((r["task"], r.get("repeat", 1))), r) for r in groups[p]]
        pairs = [(b, r) for b, r in pairs if b and succeeded(b) and succeeded(r)]
        if pairs:
            b, s = aggregate([b for b, _ in pairs]), aggregate([r for _, r in pairs])
            rows.append([p, len(pairs), f"{savings(b['tokens'], s['tokens']):.1%}", f"{savings(b['cost'], s['cost']):.1%}" if b["complete"] and s["complete"] and b["cost"] else "unknown"])
    md += table(["Profile", "Successful pairs", "Token savings", "Cost savings"], rows)
    md += ["## Repetition and variation", ""]
    rows = []
    for p in profiles:
        by_repeat = defaultdict(list)
        for r in groups[p]:
            by_repeat[r.get("repeat", 1)].append(r)
        totals = [aggregate(rs)["tokens"] for rs in by_repeat.values()]
        durations = sorted(r.get("agent_ms", 0) / 1000 for r in groups[p])
        rows.append([p, len(totals), f"{statistics.mean(totals):,.0f}", f"{min(totals):,}–{max(totals):,}", f"{statistics.stdev(totals):,.0f}" if len(totals)>1 else "unknown (one repetition)", f"{statistics.median(durations):.3f}s", f"{durations[max(0,math.ceil(.9*len(durations))-1)]:.3f}s"])
    md += table(["Profile", "Repetitions", "Mean suite tokens", "Range", "Sample standard deviation", "Cell latency p50", "Cell latency p90"], rows)
    md += ["## Per-task comparison", "", "Token totals include all attempts, including failures. Savings are meaningful only alongside success and complete usage.", ""]
    rows = []
    for task in sorted({r["task"] for r in results}):
        b = aggregate([r for r in groups.get("baseline", []) if r["task"] == task])
        for profile in profiles:
            rs = [r for r in groups[profile] if r["task"] == task]
            if not rs:
                continue
            s = aggregate(rs)
            reduction = savings(b["tokens"], s["tokens"])
            rows.append([task, profile, f"{s['passed']}/{s['count']}", s["requests"], f"{s['tokens']:,}",
                         f"{reduction:.1%}" if reduction is not None and s["complete"] and b["complete"] else "unknown"])
    md += table(["Task", "Profile", "Success", "Requests", "Total tokens", "Savings vs Baseline"], rows)
    md += ["No causal cache claim follows from different tool sequences. OS and provider caches remain uncontrolled.", "", "## Per-cell evidence", ""]
    md += table(["Profile", "Task", "Repeat", "Status", "Success", "Tokens", "Requests", "Cost", "Tools/errors", "Skill read", "Trace"], [
        [r["profile"], r["task"], r.get("repeat", 1), r["status"], "PASS" if succeeded(r) else "FAIL", total_tokens(r), r.get("usage", {}).get("requests", 0), r.get("usage", {}).get("estimated_cost_usd", "unknown"), f"{r.get('tool_calls', 0)}/{r.get('tool_errors', 0)}", r.get("skill_read", "unknown"), r.get("trace_path", "not recorded")] for r in results])
    md += ["## Tool selection and hook transformations", ""]
    for p in profiles:
        calls = Counter(t["name"] for r in groups[p] for t in r.get("tools", []))
        commands = Counter(t.get("arguments", {}).get("command", "unknown") for r in groups[p] for t in r.get("tools", []) if t["name"] == "tzro")
        activity = Counter(a.get("runtime", "unknown") for r in groups[p] for a in (r.get("activity") or []))
        hooks = [a for r in groups[p] for a in (r.get("activity") or []) if a.get("runtime") == "hook:post-tool"]
        md += [f"### {p.title()}", "", "Native tools: " + (", ".join(f"`{k}` × {v}" for k,v in sorted(calls.items())) or "not recorded"), "",
               "Local activity: " + (", ".join(f"`{k}` × {v}" for k,v in sorted(activity.items())) or "none recorded"), ""]
        md += ["Native tzro commands: " + (", ".join(f"`{k}` × {v}" for k,v in sorted(commands.items())) or "none recorded"), "",
               f"Observed skill reads: {sum(bool(r.get('skill_read')) for r in groups[p])}/{len(groups[p])} cells. Resource discovery alone does not count as a read.", ""]
        if hooks:
            md += [f"Hook bytes: {sum(h.get('input_bytes',0) for h in hooks):,} input → {sum(h.get('output_bytes',0) for h in hooks):,} output. Outcomes: {dict(Counter(h.get('status','unknown') for h in hooks))}.", ""]
        else:
            md += ["Hook transformation sizes and outcomes: not recorded.", ""]
    md += ["## Failures", ""]
    failures = [r for r in results if not succeeded(r)]
    for r in failures:
        md += [f"### {r['profile']} / {r['task']} / repeat {r.get('repeat',1)}", "", "```text", r.get("error", "No diagnostic recorded."), "```", ""]
    if not failures:
        md += ["No failed cells.", ""]
    dirty = bool(meta.get("source_status")) or meta.get("binary_vcs.modified") == "true"
    md += ["## Reproducibility", ""]
    md += table(["Field", "Recorded value"], [["Client version", data.get("client_version") or "unknown"], ["Source state", "dirty" if dirty else "clean" if "source_status" in meta else "unknown"], *[[k, f"{len(v)} shared host executables; complete inventory in JSON" if k == "shared_host_tools" else json.dumps(v, sort_keys=True) if isinstance(v, (dict,list)) else v] for k,v in sorted(meta.items())]])
    md += ["Runtime readiness and task invocation are separate measurements. Setup and preflight are recorded separately from agent time.", "",
           "The cost guard acts after reported usage. In-flight requests can exceed it. Missing usage is not zero cost.", ""]
    Path(md_path).write_text("\n".join(md))
    return gate


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("input_json")
    parser.add_argument("output_md")
    parser.add_argument("--minimum-savings", type=float, default=RELEASE_MINIMUM_SAVINGS)
    parser.add_argument("--minimum-repeats", type=int, default=3)
    parser.add_argument("--gate-output")
    parser.add_argument("--require-gate", action="store_true")
    args = parser.parse_args()
    if not 0 < args.minimum_savings < 1 or args.minimum_repeats < 2:
        parser.error("minimum savings must be between zero and one; at least two repetitions are required")
    gate = generate_report(args.input_json, args.output_md, args.minimum_savings, args.minimum_repeats)
    if args.gate_output:
        Path(args.gate_output).write_text(json.dumps(gate, indent=2) + "\n")
    print(f"Report: {args.output_md}; Standard release gate: {'PASS' if gate['passed'] else 'NOT VALIDATED'}")
    raise SystemExit(1 if args.require_gate and not gate["passed"] else 0)
