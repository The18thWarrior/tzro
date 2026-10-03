"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
const test = require('node:test');
const assert = require('node:assert');
const utils_1 = require("./utils");
test('formats price correctly', () => {
    assert.strictEqual((0, utils_1.formatPrice)(10), '$10.00');
});
