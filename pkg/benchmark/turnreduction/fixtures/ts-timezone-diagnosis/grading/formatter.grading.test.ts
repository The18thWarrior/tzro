const test = require('node:test');
const assert = require('node:assert');
import { formatDate } from './formatter';

test('formats date for negative timezone', () => {
    const d = new Date('2023-01-01T12:00:00Z');
    // For UTC-05:00, time should be 07:00:00
    assert.strictEqual(formatDate(d, '-05:00'), '2023-01-01T07:00:00.000-05:00');
});

test('formats date for zero timezone', () => {
    const d = new Date('2023-01-01T12:00:00Z');
    assert.strictEqual(formatDate(d, '+00:00'), '2023-01-01T12:00:00.000+00:00');
});
