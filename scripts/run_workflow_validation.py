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


# --- Native Antigravity Gemini API Launch Guard & Persistent Reservation ---


def reserve_native_gemini_cell(
    ledger_path: Path,
    run_id: str,
    cell_id: str,
    model: str,
    contract_hash: str,
    max_launches: int = 27,
    authorized: bool = False,
    is_fake: bool = False,
    *,
    amends_contract: str = "",
    amendment_reason: str = "",
):
    """Reserves a single execution cell in the native Gemini API ledger.
    Raises ValueError on duplicates, missing authorization, exhausted allowance, or contract mismatch.
    """
    if not authorized and not is_fake:
        raise ValueError("Explicit authorization is required for live native Gemini API launches")
    if not run_id:
        raise ValueError("run_id is required")
    if not cell_id:
        raise ValueError("cell_id is required")
    if not contract_hash:
        raise ValueError("contract_hash is required")
    if not model:
        raise ValueError("model is required")

    ledger_path = Path(ledger_path)
    ledger_path.parent.mkdir(parents=True, exist_ok=True)
    lock_path = ledger_path.with_suffix(".lock")

    with lock_path.open("a") as handle:
        fcntl.flock(handle, fcntl.LOCK_EX)
        try:
            if ledger_path.exists():
                ledger = json.loads(ledger_path.read_text())
            else:
                if amends_contract:
                    raise ValueError("Cannot amend a missing ledger or discard prior launches")
                ledger = {
                    "mode": "gemini-native",
                    "contract_hash": contract_hash,
                    "max_launches": max_launches,
                    "model": model,
                    "runs": [],
                }

            # An amendment is explicit, locked, and retains every historical reservation.
            if ledger.get("model") != model:
                raise ValueError("Ledger model mismatch: cannot reuse ledger across different models")
            if ledger.get("contract_hash") != contract_hash:
                if not authorized or not amends_contract or amends_contract != ledger.get("contract_hash") or not amendment_reason.strip():
                    raise ValueError("Ledger contract hash mismatch: an explicit authorized amendment is required")
                if any(r.get("status") != "completed" for r in ledger.get("runs", []) if not r.get("fake", False)):
                    raise ValueError("Cannot amend a ledger with unfinished live reservations")
                if max_launches < len([r for r in ledger.get("runs", []) if not r.get("fake", False)]):
                    raise ValueError("New allowance cannot discard prior launches")
                ledger.setdefault("amendments", []).append({
                    "previous_contract_hash": ledger["contract_hash"], "contract_hash": contract_hash,
                    "previous_max_launches": ledger["max_launches"], "max_launches": max_launches,
                    "reason": amendment_reason, "authorized_at": datetime.now(timezone.utc).isoformat(),
                })
                ledger["contract_hash"] = contract_hash
                ledger["max_launches"] = max_launches
            if ledger.get("max_launches") != max_launches:
                raise ValueError("Launch allowance differs from the frozen ledger")

            # Check for existing reservation with same run_id and cell_id
            for entry in ledger.get("runs", []):
                if entry.get("run_id") == run_id and entry.get("cell_id") == cell_id:
                    raise ValueError(
                        f"Cell {cell_id} in run {run_id} already exists in ledger with status={entry.get('status')}; cannot silently retry"
                    )

            # Check launch allowance (fake client does not consume live provider allowance)
            if not is_fake:
                live_launches = sum(1 for r in ledger.get("runs", []) if not r.get("fake", False))
                if live_launches >= ledger.get("max_launches", max_launches):
                    raise ValueError(f"Launch allowance exhausted ({live_launches}/{max_launches})")

            # Create reservation
            res = {
                "run_id": run_id,
                "cell_id": cell_id,
                "model": model,
                "provider": "gemini",
                "contract_hash": contract_hash,
                "fake": is_fake,
                "status": "reserved",
                "started": datetime.now(timezone.utc).isoformat(),
                "finished": None,
                "exit_code": None,
                "usage": "unknown",
                "charged_usd": "unknown",
            }
            ledger.setdefault("runs", []).append(res)

            # Write ledger atomically
            tmp = ledger_path.with_suffix(".tmp")
            tmp.write_text(json.dumps(ledger, indent=2) + "\n")
            os.replace(tmp, ledger_path)
            return res
        finally:
            fcntl.flock(handle, fcntl.LOCK_UN)


def complete_native_gemini_cell(
    ledger_path: Path,
    run_id: str,
    cell_id: str,
    exit_code: int,
    usage: dict = None,
):
    """Marks a reserved cell as completed in the ledger."""
    ledger_path = Path(ledger_path)
    lock_path = ledger_path.with_suffix(".lock")

    with lock_path.open("a") as handle:
        fcntl.flock(handle, fcntl.LOCK_EX)
        try:
            if not ledger_path.exists():
                raise ValueError("Ledger file does not exist")
            ledger = json.loads(ledger_path.read_text())

            target = None
            for entry in ledger.get("runs", []):
                if entry.get("run_id") == run_id and entry.get("cell_id") == cell_id:
                    target = entry
                    break

            if not target:
                raise ValueError(f"Reservation for cell {cell_id} in run {run_id} not found")

            target["status"] = "completed"
            target["finished"] = datetime.now(timezone.utc).isoformat()
            target["exit_code"] = exit_code
            if usage:
                target["usage"] = usage
            # Native token usage and estimates do not establish actual charges.
            target["charged_usd"] = "unknown"

            tmp = ledger_path.with_suffix(".tmp")
            tmp.write_text(json.dumps(ledger, indent=2) + "\n")
            os.replace(tmp, ledger_path)
            return target
        finally:
            fcntl.flock(handle, fcntl.LOCK_UN)


def launch_guarded_native_cell(
    ledger_path: Path,
    run_id: str,
    cell_id: str,
    model: str,
    contract_hash: str,
    spawn_func,
    max_launches: int = 27,
    authorized: bool = False,
    is_fake: bool = False,
):
    """Guards a single cell execution: reserves before spawn, leaves 'reserved' on interrupt, updates on clean exit."""
    reserve_native_gemini_cell(
        ledger_path, run_id, cell_id, model, contract_hash, max_launches, authorized, is_fake
    )
    result = spawn_func()
    exit_code = result.get("exit_code", 0) if isinstance(result, dict) else 0
    usage = result.get("usage") if isinstance(result, dict) else None
    complete_native_gemini_cell(ledger_path, run_id, cell_id, exit_code, usage)
    return result


def native_guard_main(argv):
    """Expose durable per-cell reservations to the native Go runner."""
    parser = argparse.ArgumentParser(description="Reserve or complete one native benchmark cell")
    parser.add_argument("action", choices=("native-reserve", "native-complete"))
    parser.add_argument("--ledger", type=Path, required=True)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--cell-id", required=True)
    parser.add_argument("--model")
    parser.add_argument("--contract-hash")
    parser.add_argument("--amends-contract", default="")
    parser.add_argument("--amendment-reason", default="")
    parser.add_argument("--max-launches", type=int, default=27)
    parser.add_argument("--authorized", action="store_true")
    parser.add_argument("--fake", action="store_true")
    parser.add_argument("--receipt", type=Path)
    args = parser.parse_args(argv)
    try:
        if args.action == "native-reserve":
            if args.max_launches < 1:
                raise ValueError("max-launches must be positive")
            result = reserve_native_gemini_cell(
                args.ledger, args.run_id, args.cell_id, args.model,
                args.contract_hash, args.max_launches, args.authorized, args.fake,
                amends_contract=args.amends_contract, amendment_reason=args.amendment_reason,
            )
        else:
            if args.receipt is None:
                raise ValueError("receipt is required")
            receipt = json.loads(args.receipt.read_text())
            if "exit_code" not in receipt:
                raise ValueError("receipt must contain exit_code")
            result = complete_native_gemini_cell(
                args.ledger, args.run_id, args.cell_id,
                receipt["exit_code"], receipt.get("usage"),
            )
        print(json.dumps(result))
        return 0
    except (ValueError, OSError, TypeError) as exc:
        print(f"Native launch guard: {exc}", file=sys.stderr)
        return 1


def main():
    if len(sys.argv) > 1 and sys.argv[1] in ("native-reserve", "native-complete"):
        return native_guard_main(sys.argv[1:])
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
