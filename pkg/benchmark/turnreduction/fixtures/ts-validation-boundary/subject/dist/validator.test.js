"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
const test = require('node:test');
const assert = require('node:assert');
const validator_1 = require("./validator");
test('validates normal email', () => {
    assert.strictEqual((0, validator_1.validateEmail)('a@b.com'), true);
});
test('rejects long email', () => {
    assert.strictEqual((0, validator_1.validateEmail)('12345678901234567@b.com'), false);
});
test('accepts exactly max length email', () => {
    assert.strictEqual((0, validator_1.validateEmail)('123456789012@abc.com'), true);
});
