"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
const test = require('node:test');
const assert = require('node:assert');
const display_1 = require("./display");
// @ts-ignore - this will fail until utils is renamed
const utils_1 = require("./utils");
test('displays total', () => {
    assert.strictEqual((0, display_1.displayCartTotal)(25.5), 'Total: $25.50');
});
test('calls formatCurrency', () => {
    assert.strictEqual((0, utils_1.formatCurrency)(10), '$10.00');
});
