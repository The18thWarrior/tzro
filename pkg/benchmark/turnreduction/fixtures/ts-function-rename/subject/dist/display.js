"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.displayCartTotal = displayCartTotal;
const utils_1 = require("./utils");
function displayCartTotal(total) {
    return `Total: ${(0, utils_1.formatPrice)(total)}`;
}
