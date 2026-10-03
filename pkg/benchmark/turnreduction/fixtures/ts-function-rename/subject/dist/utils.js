"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.formatPrice = formatPrice;
function formatPrice(amount) {
    return `$${amount.toFixed(2)}`;
}
