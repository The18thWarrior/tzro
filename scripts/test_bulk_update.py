"""Exercise the exact Simple-condition helper through its public CLI."""
import pathlib
import subprocess
import sys
import tempfile
import unittest

HELPER = pathlib.Path(__file__).with_name('bulk_update.py').resolve()

class BulkUpdateTests(unittest.TestCase):
    def run_helper(self, root, old, new, *files):
        return subprocess.run([sys.executable, str(HELPER), old, new, *files], cwd=root, capture_output=True, text=True)

    def test_multifile_replacement_does_not_run_project_code(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            (root/'a.py').write_text('old old')
            (root/'b.py').write_text('old')
            (root/'test_danger.py').write_text('raise RuntimeError("must not execute")')
            result = self.run_helper(root, 'old', 'new', 'a.py', 'b.py')
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((root/'a.py').read_text(), 'new new')
            self.assertEqual((root/'b.py').read_text(), 'new')
            self.assertIn('3 replacement(s)', result.stdout)

    def test_empty_search_cannot_insert_everywhere(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp); (root/'a').write_text('text')
            result = self.run_helper(root, '', 'x', 'a')
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual((root/'a').read_text(), 'text')

    def test_symlink_and_binary_are_not_edited(self):
        with tempfile.TemporaryDirectory() as tmp, tempfile.TemporaryDirectory() as outside:
            root = pathlib.Path(tmp); target = pathlib.Path(outside)/'file'
            target.write_text('old'); (root/'link').symlink_to(target)
            (root/'binary').write_bytes(b'old\x00text')
            result = self.run_helper(root, 'old', 'new', 'link', 'binary')
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(target.read_text(), 'old')
            self.assertEqual((root/'binary').read_bytes(), b'old\x00text')

if __name__ == '__main__':
    unittest.main()
