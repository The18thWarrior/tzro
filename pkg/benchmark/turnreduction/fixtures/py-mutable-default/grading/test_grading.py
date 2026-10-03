import unittest
from handler import handle_request

class TestGrading(unittest.TestCase):
    def test_five_calls(self):
        for i in range(5):
            res = handle_request(f"req{i}")
            self.assertEqual(len(res["tags"]), 1)
            self.assertEqual(res["tags"], ["processed"])
            
    def test_custom_tags(self):
        res = handle_request("req", tags=["custom"])
        self.assertEqual(res["tags"], ["custom", "processed"])
