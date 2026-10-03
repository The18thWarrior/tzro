"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
const test = require('node:test');
const assert = require('node:assert');
const formatter_1 = require("./formatter");
test('formats date for negative timezone', () => {
    const d = new Date('2023-01-01T12:00:00Z');
    // For UTC-05:00, time should be 07:00:00
    assert.strictEqual((0, formatter_1.formatDate)(d, '-05:00'), '2023-01-01T07:00:00.000-05:00');
});
test('formats date for zero timezone', () => {
    const d = new Date('2023-01-01T12:00:00Z');
    assert.strictEqual((0, formatter_1.formatDate)(d, '+00:00'), '2023-01-01T12:00:00.000+00:00');
});
