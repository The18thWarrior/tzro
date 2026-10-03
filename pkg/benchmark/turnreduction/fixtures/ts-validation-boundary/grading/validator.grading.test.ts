const test = require('node:test');
const assert = require('node:assert');
import { validateEmail } from './validator';

test('accepts exact maximum length', () => {
    const address = 'a'.repeat(12) + '@abc.com';
    assert.strictEqual(address.length, 20);
    assert.strictEqual(validateEmail(address), true);
});

test('rejects missing at sign', () => {
    assert.strictEqual(validateEmail('abcdefghij'), false);
});

test('boundary minus one', () => {
    assert.strictEqual(validateEmail('12345678901@abc.com'), true);
});

test('boundary plus one', () => {
    assert.strictEqual(validateEmail('1234567890123@abc.com'), false);
});
