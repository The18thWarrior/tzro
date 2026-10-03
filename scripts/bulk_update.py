#!/usr/bin/env python3
"""
bulk_update.py — Simple multi-file literal text replacement helper.

Usage:
    python3 bulk_update.py <old_text> <new_text> <file1> [file2] [file3] ...

Replaces every occurrence of old_text with new_text in the specified files.
Reports each file changed. Does not run any tests or verification.

This helper is available only in the Simple Automation condition of the
turn-reduction evaluation. It has no knowledge of fixtures, task IDs,
or reference solutions.
"""

import sys
import os


def main():
    if len(sys.argv) < 4:
        print("Usage: bulk_update.py <old_text> <new_text> <file1> [file2] ...", file=sys.stderr)
        sys.exit(1)

    old_text = sys.argv[1]
    new_text = sys.argv[2]
    files = sys.argv[3:]
    if not old_text:
        print("ERROR: search text must not be empty", file=sys.stderr)
        sys.exit(1)

    total_replacements = 0
    changed_files = []

    for filepath in files:
        # Path safety: reject absolute paths and path traversal.
        if os.path.isabs(filepath):
            print(f"SKIP: absolute path not allowed: {filepath}", file=sys.stderr)
            continue
        if ".." in filepath.split(os.sep):
            print(f"SKIP: path traversal not allowed: {filepath}", file=sys.stderr)
            continue

        if os.path.islink(filepath) or os.path.realpath(filepath) != os.path.abspath(filepath):
            print(f"SKIP: symlink path not allowed: {filepath}", file=sys.stderr)
            continue

        if not os.path.isfile(filepath):
            print(f"SKIP: file not found: {filepath}", file=sys.stderr)
            continue

        # Check if it looks like a binary file.
        try:
            with open(filepath, "r", encoding="utf-8") as f:
                content = f.read()
        except UnicodeDecodeError:
            print(f"SKIP: binary file: {filepath}", file=sys.stderr)
            continue

        if "\x00" in content:
            print(f"SKIP: binary file: {filepath}", file=sys.stderr)
            continue

        count = content.count(old_text)
        if count == 0:
            print(f"NO MATCH: {filepath}", file=sys.stderr)
            continue

        new_content = content.replace(old_text, new_text)
        with open(filepath, "w", encoding="utf-8") as f:
            f.write(new_content)

        total_replacements += count
        changed_files.append(filepath)
        print(f"CHANGED: {filepath} ({count} replacement{'s' if count > 1 else ''})")

    print(f"\nSummary: {len(changed_files)} file(s) changed, {total_replacements} replacement(s) total")
    if not changed_files:
        sys.exit(1)


if __name__ == "__main__":
    main()
