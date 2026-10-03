// Package pricing provides discount and tax calculations.
package pricing

// CalcTotal computes the final price after discount and tax.
// The function name CalcTotal should be renamed to CalculateTotal
// across all callers and tests.
func CalcTotal(basePrice float64, discountPercent float64, taxRate float64) float64 {
	discounted := basePrice * (1.0 - discountPercent/100.0)
	return discounted * (1.0 + taxRate/100.0)
}

// FormatPrice formats a price as a string with two decimal places.
func FormatPrice(price float64) string {
	return formatWithCents(price)
}

func formatWithCents(price float64) string {
	// Simple formatting without importing fmt to keep the fixture minimal.
	whole := int(price)
	cents := int((price-float64(whole))*100 + 0.5)
	if cents >= 100 {
		whole++
		cents -= 100
	}
	result := intToString(whole) + "."
	if cents < 10 {
		result += "0"
	}
	result += intToString(cents)
	return result
}

func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
