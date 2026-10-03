const test = require('node:test');
const assert = require('node:assert');
import { formatPrice } from './utils';

test('formats price correctly', () => {
    assert.strictEqual(formatPrice(10), '$10.00');
});
