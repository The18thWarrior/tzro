"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.validateEmail = validateEmail;
function validateEmail(email) {
    const MAX_LENGTH = 20;
    if (email.length >= MAX_LENGTH) {
        return false;
    }
    return email.includes('@');
}
