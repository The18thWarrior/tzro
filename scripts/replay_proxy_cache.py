#!/usr/bin/env python3
"""Replay identical client requests directly and through the installed proxy.

This component diagnostic has no agent loop or prepared answers. Shared provider
caches remain a confound; outbound prefix identity is observed, not assumed.
"""
import argparse
import fcntl
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import threading
import time
import urllib.request

from run_workflow_validation import api_key


def encoded(value):
    return json.dumps(value, separators=(",", ":"), sort_keys=True).encode()


def sha(value):
    return hashlib.sha256(value).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--ledger", type=Path, default=Path(".scratch/standard-savings/spend.json"))
    parser.add_argument("--total-cap", type=float, required=True)
    parser.add_argument("--binary", type=Path, default=Path("bin/tzro"))
    args = parser.parse_args()
    if args.output.exists():
        raise ValueError("Output already exists")
    key = api_key()
    requests = []
    # A stable, non-secret source prefix. Distinct repetitions have distinct
    # prefixes; the two routes receive exactly the same bytes within each pair.
    reference = "\n".join(f"Record {i:03d}: a task has an identifier, status, duration, and result. Preserve order when reporting results." for i in range(100))
    tool_names = ["read", "search", "write"]
    for repeat in range(3):
        for turn in range(4):
            names = tool_names[turn % 3:] + tool_names[:turn % 3]
            payload = {"model": "minimax/minimax-m3", "stream": False, "max_tokens": 32,
                       "temperature": 0, "reasoning": {"enabled": False}, "tool_choice": "none",
                       "provider": {"only": ["Minimax"], "allow_fallbacks": False},
                       "messages": [{"role": "system", "content": f"Cache replay repetition {repeat}. Reference only:\n" + reference},
                                    {"role": "user", "content": f"Acknowledgment {turn}: reply only OK."}],
                       "tools": [{"type": "function", "function": {"name": name, "description": name + " a local document", "parameters": {"type": "object", "properties": {}}}} for name in names]}
            body = encoded(payload)
            routes = ["direct", "proxy"] if (repeat + turn) % 2 == 0 else ["proxy", "direct"]
            requests.extend((repeat + 1, turn + 1, route, body) for route in routes)
    # A byte is an upper bound on this text's tokens; allow per-message overhead.
    # These prices exceed the pinned provider's published prices.
    reserve = sum(((len(body) + 4096) * .75 + 32 * 3) / 1e6 for _, _, _, body in requests)
    with args.ledger.with_suffix(".lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        ledger = json.loads(args.ledger.read_text())
        if ledger["total_cap_usd"] != args.total_cap or sum(r["charged_usd"] for r in ledger["runs"]) + reserve > args.total_cap:
            raise ValueError("Replay exceeds the shared authorized budget")
        run = {"run_id": args.output.stem, "status": "reserved", "reserved_usd": reserve, "charged_usd": reserve, "report": str(args.output.resolve())}
        ledger["runs"].append(run)

        def save_ledger():
            temporary = args.ledger.with_suffix(".tmp")
            temporary.write_text(json.dumps(ledger, indent=2) + "\n")
            os.replace(temporary, args.ledger)

        save_ledger()
        observed = []

        class Relay(BaseHTTPRequestHandler):
            def log_message(self, *values):
                pass

            def do_POST(self):
                body = self.rfile.read(int(self.headers["Content-Length"]))
                record = {"body_sha256": sha(body), "body": json.loads(body)}
                observed.append(record)
                try:
                    request = urllib.request.Request("https://openrouter.ai/api/v1/chat/completions", data=body,
                              headers={"Authorization": "Bearer " + key, "Content-Type": "application/json"})
                    with urllib.request.urlopen(request, timeout=90) as response:
                        result = response.read()
                    record["response"] = json.loads(result)
                    self.send_response(200)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(result)))
                    self.end_headers()
                    self.wfile.write(result)
                except Exception as error:
                    record["error"] = type(error).__name__
                    self.send_error(502, "Upstream replay failed; reservation retained")

        server = ThreadingHTTPServer(("127.0.0.1", 0), Relay)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        upstream = f"http://127.0.0.1:{server.server_port}"
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        root = Path(tempfile.mkdtemp(prefix="tzro-cache-replay-"))
        proxy = subprocess.Popen([str(args.binary.resolve()), "start", "--port", str(port), "--upstream-openai", upstream],
                                 env={**os.environ, "TZRO_DB_PATH": str(root / "store.db"), "TZRO_UPSTREAM_LOCAL": upstream},
                                 cwd=root,
                                 stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        results = []
        report = {"schema": "tzro.fixed-request-replay.v1", "model": "minimax/minimax-m3", "provider": "Minimax",
                  "binary_sha256": sha(args.binary.read_bytes()), "requests_per_route": 12, "results": results,
                  "limitations": "Provider caches are shared between routes. Order is counterbalanced. Cache/cost differences are descriptive, not an isolated causal estimate. No agent task success is measured."}
        args.output.parent.mkdir(parents=True, exist_ok=True)
        try:
            for _ in range(100):
                try:
                    urllib.request.urlopen(urllib.request.Request(f"http://127.0.0.1:{port}/v1/chat/completions", method="OPTIONS"), timeout=.2).close()
                    break
                except OSError:
                    if proxy.poll() is not None:
                        raise RuntimeError("Proxy exited before readiness")
                    time.sleep(.05)
            for repeat, turn, route, body in requests:
                base = upstream if route == "direct" else f"http://127.0.0.1:{port}"
                started = time.monotonic()
                with urllib.request.urlopen(urllib.request.Request(base + "/v1/chat/completions", data=body, headers={"Content-Type": "application/json"}), timeout=100) as response:
                    value = json.load(response)
                sent = observed[-1]["body"]
                if not value.get("usage", {}).get("prompt_tokens"):
                    raise RuntimeError("Provider usage unavailable; reservation retained")
                result = {"repeat": repeat, "turn": turn, "route": route, "client_payload_sha256": sha(body),
                          "upstream_payload_sha256": observed[-1]["body_sha256"], "upstream_payload": sent,
                          "prefix_sha256": sha(encoded({"system": sent["messages"][0], "tools": sent["tools"]})),
                          "usage": value["usage"], "elapsed_ms": round((time.monotonic() - started) * 1000)}
                results.append(result)
                args.output.write_text(json.dumps(report, indent=2) + "\n")
                print(f"{repeat}/{turn} {route}: {result['usage']}", flush=True)
            charged = sum((r["usage"]["prompt_tokens"] * .75 + r["usage"]["completion_tokens"] * 3) / 1e6 for r in results)
            run.update(status="accounted", charged_usd=charged,
                       provider_reported_usd=sum(r["usage"].get("cost", 0) for r in results))
            save_ledger()
        finally:
            proxy.terminate()
            proxy.wait(timeout=10)
            server.shutdown()
        print(f"Replay saved: {args.output}; conservative budget charge ${run['charged_usd']:.6f}")


if __name__ == "__main__":
    main()
