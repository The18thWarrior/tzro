const test = require('node:test');
const assert = require('node:assert');
import { formatCurrency } from './utils';
import { displayCartTotal } from './display';

test('grading formatCurrency', () => {
    assert.strictEqual(formatCurrency(100), '$100.00');
    assert.strictEqual(displayCartTotal(100), 'Total: $100.00');
});
