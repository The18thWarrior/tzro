// Package order uses pricing to compute order totals.
package pricing

// OrderTotal computes the total for an order with the given items.
func OrderTotal(items []float64, discountPercent float64, taxRate float64) float64 {
	var sum float64
	for _, price := range items {
		sum += price
	}
	return CalcTotal(sum, discountPercent, taxRate)
}
