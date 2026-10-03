import { formatPrice } from './utils';
export function displayCartTotal(total: number): string {
    return `Total: ${formatPrice(total)}`;
}
