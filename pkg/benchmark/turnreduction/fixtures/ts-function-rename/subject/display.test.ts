const test = require('node:test');
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
