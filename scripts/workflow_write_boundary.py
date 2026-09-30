"""Confine benchmark subprocess writes on macOS; reads and networking stay native."""
import hashlib
import json
from pathlib import Path
import sys

SANDBOX = Path('/usr/bin/sandbox-exec')


def require_write_boundary():
    if sys.platform != 'darwin' or not SANDBOX.is_file():
        raise ValueError('Guarded paid workflows require the verified macOS write-boundary backend; no unconfined fallback is allowed')


def confined_command(command, work_root, report):
    require_write_boundary()
    root = Path(work_root).resolve(strict=True)
    output = Path(report).resolve()
    if not root.is_dir() or not output.parent.is_dir():
        raise ValueError('Write-boundary roots must exist before launching the benchmark')
    # Kernel path checks apply to descendants and resolved symlink targets.
    # Permit only this run's state, its exact report file, and the null device.
    quote = lambda value: json.dumps(str(value), ensure_ascii=False)
    policy = '\n'.join([
        '(version 1)', '(allow default)', '(deny file-write*)',
        f'(allow file-write* (subpath {quote(root)}))',
        f'(allow file-write* (literal {quote(output)}))',
        '(allow file-write* (literal "/dev/null"))',
    ])
    boundary = {'kind': 'macos-run-write-boundary-v1', 'work_root': str(root),
                'report_file': str(output), 'policy_sha256': hashlib.sha256(policy.encode()).hexdigest(),
                'limitations': 'Write confinement for the complete run, not per-task isolation, read isolation, or network isolation.'}
    return [str(SANDBOX), '-p', policy, *command], boundary
