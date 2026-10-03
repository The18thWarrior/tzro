import unittest
from service import paginate, get_total_pages

class TestPagination(unittest.TestCase):
    def test_paginate_exact_multiple(self):
        items = list(range(10))
        res = paginate(items, 1, 5)
        self.assertEqual(len(res["items"]), 5)
        self.assertEqual(res["total_pages"], 2)

    def test_paginate_inexact(self):
        items = list(range(11))
        res = paginate(items, 1, 5)
        self.assertEqual(res["total_pages"], 3)
