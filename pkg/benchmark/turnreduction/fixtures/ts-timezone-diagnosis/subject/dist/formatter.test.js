"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
const test = require('node:test');
const assert = require('node:assert');
const formatter_1 = require("./formatter");
test('formats date for positive timezone', () => {
    const d = new Date('2023-01-01T12:00:00Z');
    // For UTC+02:00, time should be 14:00:00
    // But bug subtracts, so it will be 10:00:00
    assert.strictEqual((0, formatter_1.formatDate)(d, '+02:00'), '2023-01-01T14:00:00.000+02:00');
});
