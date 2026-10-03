import unittest
from service import process_order

class TestService(unittest.TestCase):
    def test_process(self):
        res = process_order(1, qty=5)
        self.assertEqual(res["quantity"], 5)
