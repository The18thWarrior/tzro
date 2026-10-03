export function validateEmail(email: string): boolean {
    const MAX_LENGTH = 20;
    if (email.length >= MAX_LENGTH) {
        return false;
    }
    return email.includes('@');
}
