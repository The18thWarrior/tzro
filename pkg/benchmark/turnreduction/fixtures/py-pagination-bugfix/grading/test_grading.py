import unittest
from service import get_total_pages, paginate

class TestGrading(unittest.TestCase):
    def test_zero_items(self):
        self.assertEqual(get_total_pages(0, 5), 0)
        
    def test_less_than_one_page(self):
        self.assertEqual(get_total_pages(3, 5), 1)

    def test_paginate_boundary(self):
        items = list(range(15))
        res = paginate(items, 3, 5)
        self.assertEqual(res["total_pages"], 3)
        self.assertEqual(len(res["items"]), 5)
