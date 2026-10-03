"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
const test = require('node:test');
const assert = require('node:assert');
const validator_1 = require("./validator");
test('boundary minus one', () => {
    assert.strictEqual((0, validator_1.validateEmail)('12345678901@abc.com'), true);
});
test('boundary plus one', () => {
    assert.strictEqual((0, validator_1.validateEmail)('1234567890123@abc.com'), false);
});
