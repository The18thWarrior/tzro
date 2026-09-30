#!/usr/bin/env python3
"""Run a paid matrix with a persistent aggregate spending reservation.

Prices must conservatively bound all possible provider routes. An unfinished or
unaccounted attempt keeps its entire reservation; it cannot silently retry.
"""
import argparse
import fcntl
import json
import math
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
from datetime import datetime, timezone
from workflow_write_boundary import confined_command, require_write_boundary


def api_key():
    key = os.environ.get("TZRO_BENCH_API_KEY") or os.environ.get("OPENROUTER_API_KEY")
    if key:
        return key
    path = Path(".env")
    if path.is_file():
        for line in path.read_text().splitlines():
            name, separator, value = line.strip().removeprefix("export ").partition("=")
            if separator and name.strip() == "OPENROUTER_API_KEY":
                return value.strip().strip("\"'")
    raise ValueError("TZRO_BENCH_API_KEY or OPENROUTER_API_KEY is required")


def reservation(ledger, total_cap, run_cap, prices):
    if any(not math.isfinite(n) or n <= 0 for n in (total_cap, run_cap, *prices)):
        raise ValueError("Caps and conservative token prices must be finite and positive")
    spent = sum(r["charged_usd"] for r in ledger["runs"])
    available = min(run_cap, total_cap - spent)
    # Native client context/output limits are recorded by the runner. Reserve a
    # full extra request because the runner's stop signal is asynchronous.
    margin = (128000 * max(prices[0], prices[2]) + 8192 * prices[1]) / 1e6
    if available <= margin:
        raise ValueError("Insufficient remaining budget after the in-flight reserve")
    return available, available - margin


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--profiles", default="baseline,standard")
    parser.add_argument("--suite", choices=["representative", "legacy"], default="representative")
    parser.add_argument("--repeats", type=int, default=3)
    parser.add_argument("--model", default="minimax/minimax-m3")
    parser.add_argument("--total-cap", type=float, required=True)
    parser.add_argument("--run-cap", type=float, required=True)
    parser.add_argument("--input-price", type=float, required=True)
    parser.add_argument("--output-price", type=float, required=True)
    parser.add_argument("--cache-read-price", type=float, required=True)
    parser.add_argument("--ledger", type=Path, default=Path(".scratch/standard-savings/spend.json"))
    parser.add_argument("--report-dir", type=Path, default=Path("docs/benchmarks"))
    parser.add_argument("--binary", type=Path, default=Path("bin/tzro"))
    args, extra = parser.parse_known_args()
    if not args.run_id or any(c not in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" for c in args.run_id):
        parser.error("run-id must contain only letters, numbers, hyphens, and underscores")
    args.ledger.parent.mkdir(parents=True, exist_ok=True)
    args.report_dir.mkdir(parents=True, exist_ok=True)
    output = args.report_dir / (args.run_id + ".json")
    if output.exists():
        parser.error("report already exists; choose a new run-id")
    require_write_boundary()
    key = api_key()
    lock = args.ledger.with_suffix(".lock")
    with lock.open("a") as handle:
        fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
        ledger = json.loads(args.ledger.read_text()) if args.ledger.exists() else {"total_cap_usd": args.total_cap, "runs": []}
        if ledger["total_cap_usd"] != args.total_cap:
            raise ValueError("Existing ledger has a different authorized cap")
        available, guard = reservation(ledger, args.total_cap, args.run_cap, (args.input_price, args.output_price, args.cache_read_price))
        work_parent = Path(tempfile.mkdtemp(prefix="tzro-validation-"))
        work = work_parent / "profiles"
        run = {"run_id": args.run_id, "started": datetime.now(timezone.utc).isoformat(),
               "report": str(output.resolve()), "work_dir": str(work), "status": "reserved",
               "reserved_usd": available, "charged_usd": available,
               "price_basis": "conservative provider route ceiling; cache reads charged at the supplied ceiling"}
        ledger["runs"].append(run)

        def save():
            temporary = args.ledger.with_suffix(".tmp")
            temporary.write_text(json.dumps(ledger, indent=2) + "\n")
            os.replace(temporary, args.ledger)

        save()
        command = [str(args.binary.resolve()), "bench", "workflows", "--run", "--model", args.model,
                   "--profiles", args.profiles, "--suite", args.suite, "--repeats", str(args.repeats),
                   "--max-cost", str(guard), "--input-price", str(args.input_price),
                   "--output-price", str(args.output_price), "--cache-read-price", str(args.cache_read_price),
                   "--cache-write-price", str(args.input_price), "--work-dir", str(work), "--output", str(output.resolve())]
        if "full" in args.profiles.split(","):
            command += ["--decision-bin", str(Path("bin/jev-score").resolve()),
                        "--decision-model", str(Path("models/decision/Jev-Style-0.8B-Decision-v3-Q4_K_M.gguf").resolve()),
                        "--decision-version", "JEV v3 (libllama 9770, Qwen3.5)",
                        "--extractor-bin", shutil.which("python3"), "--extractor-arg", str(Path("bin/gliner_worker.py").resolve()),
                        "--extractor-model", str(Path.home() / ".cache/huggingface/hub/models--fastino--gliner2.5-base-v1/snapshots/1a8bc24e00dc7300b9017c81d63e3dcdabb26596"),
                        "--extractor-version", "GLiNER 2.5 (gliner2 2.0.0, torch 2.8.0)"]
        # Allow runtime paths/timeouts only; never override prices, profiles or budget.
        allowed = {"--timeout", "--setup-timeout", "--max-turns", "--decision-bin", "--decision-model",
                   "--decision-version", "--extractor-bin", "--extractor-arg", "--extractor-model", "--extractor-version"}
        if len(extra) % 2 or any(extra[i] not in allowed for i in range(0, len(extra), 2)):
            raise ValueError("Unsupported extra runner arguments")
        command += extra
        command, boundary = confined_command(command, work_parent, output)
        run["write_boundary"] = boundary
        save()
        print(f"Run {args.run_id}: reserved ${available:.4f}, stop threshold ${guard:.4f}; work {work}", flush=True)
        completed = subprocess.run(command, env={**os.environ, "TZRO_BENCH_API_KEY": key})
        source = output if output.exists() else work / "progress.json"
        if source.exists():
            data = json.loads(source.read_text())
            ran = [r for r in data["results"] if r.get("trace_path")]
            known = sum(r["usage"].get("estimated_cost_usd") or 0 for r in ran)
            accounted = all(r["usage"].get("complete") for r in ran) and source == output
            run.update(status="accounted" if accounted else "usage_unknown", known_subtotal_usd=known)
            if accounted:
                run["charged_usd"] = known
        run["exit_code"] = completed.returncode
        run["finished"] = datetime.now(timezone.utc).isoformat()
        save()
        print(f"Aggregate budget charged/reserved: ${sum(r['charged_usd'] for r in ledger['runs']):.6f} / ${args.total_cap:.2f}", flush=True)
        if output.exists():
            subprocess.run([sys.executable, "scripts/generate_benchmark_report.py", str(output), str(output.with_suffix(".md")), "--gate-output", str(output.with_suffix(".gate.json"))], check=True)
        return completed.returncode


if __name__ == "__main__":
    raise SystemExit(main())
