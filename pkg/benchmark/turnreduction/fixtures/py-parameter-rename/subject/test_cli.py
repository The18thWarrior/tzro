import unittest
from cli import handle_cli_order

class TestCLI(unittest.TestCase):
    def test_cli(self):
        res = handle_cli_order(1, quantity=5)
        self.assertEqual(res["quantity"], 5)
