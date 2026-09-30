import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from workflow_write_boundary import confined_command, require_write_boundary


class WriteBoundaryTest(unittest.TestCase):
    def test_unsupported_backend_has_no_unconfined_fallback(self):
        with patch('workflow_write_boundary.sys.platform', 'linux'):
            with self.assertRaisesRegex(ValueError, 'no unconfined fallback'):
                require_write_boundary()

    @unittest.skipUnless(sys.platform == 'darwin', 'macOS kernel backend')
    def test_allowed_outputs_and_descendant_escape_denied(self):
        with tempfile.TemporaryDirectory(prefix='tzro-boundary-"') as directory:
            root = Path(directory)
            work = root / 'work'; work.mkdir()
            reports = root / 'reports'; reports.mkdir()
            outside = root / 'outside'; outside.mkdir()
            protected = outside / 'keep.txt'; protected.write_text('unchanged')
            (work / 'alias').symlink_to(outside, target_is_directory=True)
            script = '''import json, pathlib, subprocess, sys
work, report, outside = map(pathlib.Path, sys.argv[1:])
(work/'allowed.txt').write_text('allowed')
report.write_text('report')
checks = []
for path in [outside/'new.txt', outside/'keep.txt', work/'alias'/'new.txt', report.parent/'other.json']:
    try: path.write_text('escaped'); checks.append(False)
    except PermissionError: checks.append(True)
child = subprocess.run(['/bin/sh','-c','printf escaped > "$1"','sh',str(outside/'child.txt')],capture_output=True)
print(json.dumps({'denied':checks,'child_denied':child.returncode != 0}))
'''
            command, metadata = confined_command([sys.executable, '-c', script, str(work), str(reports/'result.json'), str(outside)], work, reports/'result.json')
            result = subprocess.run(command, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            import json
            checks = json.loads(result.stdout)
            self.assertTrue(all(checks['denied']), checks)
            self.assertTrue(checks['child_denied'])
            self.assertEqual(protected.read_text(), 'unchanged')
            self.assertFalse((outside/'new.txt').exists())
            self.assertFalse((outside/'child.txt').exists())
            self.assertEqual((work/'allowed.txt').read_text(), 'allowed')
            self.assertEqual((reports/'result.json').read_text(), 'report')
            self.assertEqual(metadata['kind'], 'macos-run-write-boundary-v1')


if __name__ == '__main__':
    unittest.main()
