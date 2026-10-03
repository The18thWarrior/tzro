import unittest
from handler import handle_request

class TestHandler(unittest.TestCase):
    def test_independence(self):
        res1 = handle_request("req1")
        res2 = handle_request("req2")
        self.assertEqual(res1["tags"], ["processed"])
        self.assertEqual(res2["tags"], ["processed"])
