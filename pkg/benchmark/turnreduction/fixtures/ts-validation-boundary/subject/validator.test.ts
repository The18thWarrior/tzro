const test = require('node:test');
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
