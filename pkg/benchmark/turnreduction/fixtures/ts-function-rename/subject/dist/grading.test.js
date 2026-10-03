"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
const test = require('node:test');
const assert = require('node:assert');
const utils_1 = require("./utils");
const display_1 = require("./display");
test('grading formatCurrency', () => {
    assert.strictEqual((0, utils_1.formatCurrency)(100), '$100.00');
    assert.strictEqual((0, display_1.displayCartTotal)(100), 'Total: $100.00');
});
