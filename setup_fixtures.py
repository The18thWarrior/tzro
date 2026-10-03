import os
import shutil
import subprocess
import json

BASE_DIR = "/Users/jp/Desktop/Repos/tzro/pkg/benchmark/turnreduction/fixtures"

FIXTURES = {
    "py-pagination-bugfix": {
        "type": "python",
        "subject": {
            "service.py": """def get_total_pages(total_items, per_page):
    if total_items == 0:
        return 0
    return total_items // per_page
    
def paginate(items, page, per_page):
    start = (page - 1) * per_page
    end = start + per_page
    return {
        "items": items[start:end],
        "total_pages": get_total_pages(len(items), per_page)
    }
""",
            "test_service.py": """import unittest
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
"""
        },
        "grading": {
            "test_grading.py": """import unittest
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
"""
        },
        "fix": {
            "service.py": """def get_total_pages(total_items, per_page):
    if total_items == 0:
        return 0
    return (total_items + per_page - 1) // per_page
    
def paginate(items, page, per_page):
    start = (page - 1) * per_page
    end = start + per_page
    return {
        "items": items[start:end],
        "total_pages": get_total_pages(len(items), per_page)
    }
"""
        }
    },
    "py-parameter-rename": {
        "type": "python",
        "subject": {
            "service.py": """def process_order(order_id, qty):
    return {"order_id": order_id, "quantity": qty, "status": "processed"}
""",
            "cli.py": """from service import process_order

def handle_cli_order(order_id, qty):
    return process_order(order_id, qty=qty)
""",
            "test_service.py": """import unittest
from service import process_order

class TestService(unittest.TestCase):
    def test_process(self):
        res = process_order(1, qty=5)
        self.assertEqual(res["quantity"], 5)
""",
            "test_cli.py": """import unittest
from cli import handle_cli_order

class TestCLI(unittest.TestCase):
    def test_cli(self):
        res = handle_cli_order(1, quantity=5)
        self.assertEqual(res["quantity"], 5)
"""
        },
        "grading": {
            "test_grading.py": """import unittest
from service import process_order
from cli import handle_cli_order

class TestGrading(unittest.TestCase):
    def test_service_kwargs(self):
        res = process_order(order_id=2, quantity=10)
        self.assertEqual(res["quantity"], 10)
        
    def test_cli_kwargs(self):
        res = handle_cli_order(order_id=3, quantity=20)
        self.assertEqual(res["quantity"], 20)
"""
        },
        "fix": {
            "service.py": """def process_order(order_id, quantity):
    return {"order_id": order_id, "quantity": quantity, "status": "processed"}
""",
            "cli.py": """from service import process_order

def handle_cli_order(order_id, quantity):
    return process_order(order_id, quantity=quantity)
""",
            "test_service.py": """import unittest
from service import process_order

class TestService(unittest.TestCase):
    def test_process(self):
        res = process_order(1, quantity=5)
        self.assertEqual(res["quantity"], 5)
"""
        }
    },
    "py-mutable-default": {
        "type": "python",
        "subject": {
            "handler.py": """def handle_request(data, tags=[]):
    tags.append("processed")
    return {"data": data, "tags": tags}
""",
            "test_handler.py": """import unittest
from handler import handle_request

class TestHandler(unittest.TestCase):
    def test_independence(self):
        res1 = handle_request("req1")
        res2 = handle_request("req2")
        self.assertEqual(res1["tags"], ["processed"])
        self.assertEqual(res2["tags"], ["processed"])
"""
        },
        "grading": {
            "test_grading.py": """import unittest
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
"""
        },
        "fix": {
            "handler.py": """def handle_request(data, tags=None):
    if tags is None:
        tags = []
    tags.append("processed")
    return {"data": data, "tags": tags}
"""
        }
    },
    "ts-validation-boundary": {
        "type": "typescript",
        "subject": {
            "validator.ts": """export function validateEmail(email: string): boolean {
    const MAX_LENGTH = 20;
    if (email.length >= MAX_LENGTH) {
        return false;
    }
    return email.includes('@');
}
""",
            "validator.test.ts": """const test = require('node:test');
const assert = require('node:assert');
import { validateEmail } from './validator';

test('validates normal email', () => {
    assert.strictEqual(validateEmail('a@b.com'), true);
});

test('rejects long email', () => {
    assert.strictEqual(validateEmail('12345678901234567@b.com'), false);
});

test('accepts exactly max length email', () => {
    assert.strictEqual(validateEmail('123456789012@abc.com'), true);
});
"""
        },
        "grading": {
            "validator.grading.test.ts": """const test = require('node:test');
const assert = require('node:assert');
import { validateEmail } from './validator';

test('boundary minus one', () => {
    assert.strictEqual(validateEmail('12345678901@abc.com'), true);
});

test('boundary plus one', () => {
    assert.strictEqual(validateEmail('1234567890123@abc.com'), false);
});
"""
        },
        "fix": {
            "validator.ts": """export function validateEmail(email: string): boolean {
    const MAX_LENGTH = 20;
    if (email.length > MAX_LENGTH) {
        return false;
    }
    return email.includes('@');
}
"""
        }
    },
    "ts-function-rename": {
        "type": "typescript",
        "subject": {
            "utils.ts": """export function formatPrice(amount: number): string {
    return `$${amount.toFixed(2)}`;
}
""",
            "display.ts": """import { formatPrice } from './utils';
export function displayCartTotal(total: number): string {
    return `Total: ${formatPrice(total)}`;
}
""",
            "utils.test.ts": """const test = require('node:test');
const assert = require('node:assert');
import { formatPrice } from './utils';

test('formats price correctly', () => {
    assert.strictEqual(formatPrice(10), '$10.00');
});
""",
            "display.test.ts": """const test = require('node:test');
const assert = require('node:assert');
import { displayCartTotal } from './display';
// @ts-ignore - this will fail until utils is renamed
import { formatCurrency } from './utils';

test('displays total', () => {
    assert.strictEqual(displayCartTotal(25.5), 'Total: $25.50');
});
test('calls formatCurrency', () => {
    assert.strictEqual(formatCurrency(10), '$10.00');
});
"""
        },
        "grading": {
            "grading.test.ts": """const test = require('node:test');
const assert = require('node:assert');
import { formatCurrency } from './utils';
import { displayCartTotal } from './display';

test('grading formatCurrency', () => {
    assert.strictEqual(formatCurrency(100), '$100.00');
    assert.strictEqual(displayCartTotal(100), 'Total: $100.00');
});
"""
        },
        "fix": {
            "utils.ts": """export function formatCurrency(amount: number): string {
    return `$${amount.toFixed(2)}`;
}
""",
            "display.ts": """import { formatCurrency } from './utils';
export function displayCartTotal(total: number): string {
    return `Total: ${formatCurrency(total)}`;
}
""",
            "utils.test.ts": """const test = require('node:test');
const assert = require('node:assert');
import { formatCurrency } from './utils';

test('formats currency correctly', () => {
    assert.strictEqual(formatCurrency(10), '$10.00');
});
"""
        }
    },
    "ts-timezone-diagnosis": {
        "type": "typescript",
        "subject": {
            "formatter.ts": """export function formatDate(date: Date, tzOffsetHours: number): string {
    const localTime = new Date(date.getTime() - (tzOffsetHours * 60 * 60 * 1000)); 
    return localTime.toISOString().replace('Z', '') + (tzOffsetHours >= 0 ? '+' : '-') + Math.abs(tzOffsetHours).toString().padStart(2, '0') + ':00';
}
""",
            "formatter.test.ts": """const test = require('node:test');
const assert = require('node:assert');
import { formatDate } from './formatter';

test('formats date for positive timezone', () => {
    const d = new Date('2023-01-01T12:00:00Z');
    assert.strictEqual(formatDate(d, 2), '2023-01-01T14:00:00.000+02:00');
});
"""
        },
        "grading": {
            "formatter.grading.test.ts": """const test = require('node:test');
const assert = require('node:assert');
import { formatDate } from './formatter';

test('formats date for negative timezone', () => {
    const d = new Date('2023-01-01T12:00:00Z');
    assert.strictEqual(formatDate(d, -5), '2023-01-01T07:00:00.000-05:00');
});

test('formats date for zero timezone', () => {
    const d = new Date('2023-01-01T12:00:00Z');
    assert.strictEqual(formatDate(d, 0), '2023-01-01T12:00:00.000+00:00');
});
"""
        },
        "fix": {
            "formatter.ts": """export function formatDate(date: Date, tzOffsetHours: number): string {
    const localTime = new Date(date.getTime() + (tzOffsetHours * 60 * 60 * 1000)); 
    return localTime.toISOString().replace('Z', '') + (tzOffsetHours >= 0 ? '+' : '-') + Math.abs(tzOffsetHours).toString().padStart(2, '0') + ':00';
}
"""
        }
    }
}

TSCONFIG = {
    "compilerOptions": {
        "module": "commonjs",
        "target": "ES2022",
        "strict": True,
        "outDir": "dist"
    }
}

def run_cmd(cmd, cwd):
    return subprocess.run(cmd, shell=True, cwd=cwd, capture_output=True, text=True)

results = []

for fx_name, fx_data in FIXTURES.items():
    fx_dir = os.path.join(BASE_DIR, fx_name)
    subject_dir = os.path.join(fx_dir, "subject")
    grading_dir = os.path.join(fx_dir, "grading")
    
    os.makedirs(subject_dir, exist_ok=True)
    os.makedirs(grading_dir, exist_ok=True)
    
    for fname, content in fx_data["subject"].items():
        with open(os.path.join(subject_dir, fname), "w") as f:
            f.write(content)
            
    for fname, content in fx_data["grading"].items():
        with open(os.path.join(grading_dir, fname), "w") as f:
            f.write(content)
            
    if fx_data["type"] == "typescript":
        with open(os.path.join(subject_dir, "tsconfig.json"), "w") as f:
            json.dump(TSCONFIG, f, indent=2)
            
    if fx_data["type"] == "python":
        res1 = run_cmd("python3 -m unittest discover -s . -p 'test_*.py'", subject_dir)
    else:
        res1 = run_cmd("npx tsc; node --test dist/*.test.js", subject_dir)
        
    res1_status = "FAIL (expected)" if res1.returncode != 0 else "PASS (UNEXPECTED)"
    
    for fname, content in fx_data["fix"].items():
        with open(os.path.join(subject_dir, fname), "w") as f:
            f.write(content)
            
    for fname in os.listdir(grading_dir):
        shutil.copy(os.path.join(grading_dir, fname), os.path.join(subject_dir, fname))
        
    if fx_data["type"] == "python":
        res2 = run_cmd("python3 -m unittest discover -s . -p 'test_*.py'", subject_dir)
    else:
        res2 = run_cmd("npx tsc; node --test dist/*.test.js", subject_dir)
        
    if res2.returncode == 0:
        res2_status = "PASS (expected)"
    else:
        res2_status = f"FAIL (UNEXPECTED) {res2.stdout} {res2.stderr}"
    
    for fname, content in fx_data["subject"].items():
        with open(os.path.join(subject_dir, fname), "w") as f:
            f.write(content)
    for fname in os.listdir(grading_dir):
        os.remove(os.path.join(subject_dir, fname))
        
    results.append(f"{fx_name}:\\n  Subject tests initially: {res1_status}\\n  After fix (all tests): {res2_status}")

print("=== RESULTS ===")
for r in results:
    print(r)
    print()
