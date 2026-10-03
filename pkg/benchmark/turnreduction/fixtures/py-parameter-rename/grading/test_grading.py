import unittest
from service import process_order
from cli import handle_cli_order

class TestGrading(unittest.TestCase):
    def test_service_kwargs(self):
        res = process_order(order_id=2, quantity=10)
        self.assertEqual(res["quantity"], 10)
        
    def test_cli_kwargs(self):
        res = handle_cli_order(order_id=3, quantity=20)
        self.assertEqual(res["quantity"], 20)
