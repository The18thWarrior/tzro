// Package pricing provides discount-aware price calculation.
package pricing

import "math"

// DiscountedPrice applies a percentage discount to a base price and
// rounds to the nearest cent. The discount must be between 0 and 100.
func DiscountedPrice(basePrice float64, discountPercent float64) float64 {
	if discountPercent < 0 || discountPercent > 100 {
		return basePrice
	}
	// BUG: truncation instead of rounding causes off-by-one cent errors
	// at specific boundaries (e.g., 19.99 * 0.85 = 16.9915 → 16.99 instead of 16.99).
	// The real bug: we use Floor instead of Round, which fails at boundaries like
	// 10.05 * 0.90 = 9.045 → Floor(904.5)/100 = 9.04 instead of 9.05.
	factor := 1.0 - discountPercent/100.0
	return math.Floor(basePrice*factor*100) / 100
}

// BulkDiscount returns the discount percentage for a given quantity.
func BulkDiscount(quantity int) float64 {
	switch {
	case quantity >= 100:
		return 20.0
	case quantity >= 50:
		return 15.0
	case quantity >= 10:
		return 10.0
	default:
		return 0.0
	}
}
