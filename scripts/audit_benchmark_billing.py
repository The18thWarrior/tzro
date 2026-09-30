#!/usr/bin/env python3
"""Fetch billing metadata for recorded generations; never generate completions."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import json
from pathlib import Path
import urllib.parse
import urllib.request

from run_workflow_validation import api_key


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("reports", nargs="+", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    result = json.loads(args.output.read_text()) if args.output.exists() else {"schema": "tzro.generation-billing.v1", "generations": {}}
    cells = []
    for report in args.reports:
        data = json.loads(report.read_text())
        for cell in data["results"]:
            if not cell.get("trace_path"):
                continue
            ids = []
            for line in (report.resolve().parent / cell["trace_path"]).read_text().splitlines():
                event = json.loads(line)
                message = event.get("message", {})
                if event.get("type") == "message_end" and message.get("role") == "assistant" and message.get("responseId"):
                    ids.append(message["responseId"])
            cells.append({"report": str(report), "profile": cell["profile"], "task": cell["task"], "repeat": cell.get("repeat", 1),
                          "native_usage_complete": cell["usage"]["complete"], "reported_requests": cell["usage"]["requests"], "generation_ids": ids})
    key = api_key()
    pending = sorted({identifier for cell in cells for identifier in cell["generation_ids"] if identifier not in result["generations"] or "error" in result["generations"][identifier]})

    def fetch(identifier):
        try:
            url = "https://openrouter.ai/api/v1/generation?" + urllib.parse.urlencode({"id": identifier})
            request = urllib.request.Request(url, headers={"Authorization": "Bearer " + key})
            with urllib.request.urlopen(request, timeout=30) as response:
                data = json.load(response)["data"]
            # Retain only accounting/provenance fields, excluding account metadata.
            fields = ("id", "model", "provider_name", "total_cost", "native_tokens_prompt", "native_tokens_completion", "native_tokens_cached", "native_tokens_reasoning", "finish_reason", "cancelled")
            return identifier, {field: data.get(field) for field in fields}
        except Exception as error:
            return identifier, {"error": type(error).__name__}

    with ThreadPoolExecutor(max_workers=3) as pool:
        for identifier, data in pool.map(fetch, pending):
            result["generations"][identifier] = data
    result["cells"] = cells
    result["known_provider_cost_usd"] = sum(row.get("total_cost") or 0 for row in result["generations"].values())
    result["unknown_metadata_count"] = sum("error" in row or row.get("total_cost") is None for row in result["generations"].values())
    result["limitations"] = "Only recorded generation IDs can be reconciled. Missing/unfinished responses may have additional cost; their spending reservations remain in the shared ledger."
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + "\n")
    print(f"Recorded generations: {len(result['generations'])}; unknown metadata: {result['unknown_metadata_count']}; known provider cost ${result['known_provider_cost_usd']:.8f}")


if __name__ == "__main__":
    main()
