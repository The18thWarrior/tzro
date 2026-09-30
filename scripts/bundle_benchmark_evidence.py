#!/usr/bin/env python3
"""Copy a run's immutable evidence into a portable local bundle; never publish."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil


def bundle(source, destination):
    if destination.exists():
        raise ValueError("Destination already exists")
    data = json.loads(source.read_text())
    root = destination.parent / (destination.stem + "-evidence")
    root.mkdir(parents=True, exist_ok=False)

    def retain(path, checksum, name):
        original = source.resolve().parent / path
        if hashlib.sha256(original.read_bytes()).hexdigest() != checksum:
            raise ValueError(f"Evidence checksum mismatch: {original}")
        target = root / name
        shutil.copyfile(original, target)
        return str(target.relative_to(destination.parent))

    meta = data["metadata"]
    meta["original_report"] = str(source.resolve())
    meta["source_snapshot_path"] = retain(meta["source_snapshot_path"], meta["source_snapshot_sha256"], "source.tar.gz")
    for index, cell in enumerate(data["results"]):
        if cell.get("trace_path"):
            cell["trace_path"] = retain(cell["trace_path"], cell["trace_sha256"], f"{index:03d}-events.jsonl")
    with destination.open("x") as handle:
        handle.write(json.dumps(data, indent=2) + "\n")
    return destination


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path)
    parser.add_argument("destination", type=Path)
    args = parser.parse_args()
    print(bundle(args.source, args.destination))
