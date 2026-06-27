# Interface Design for Testability

Good interfaces make testing natural. Design for these during planning, not after.

## Principles

1. **Accept dependencies, don't create them**

   ```go
   // Testable — dependency injected
   func NewProcessor(store Store) *Processor {}

   // Hard to test — dependency created internally
   func NewProcessor() *Processor {
       store := postgres.Connect(os.Getenv("DB_URL"))
   }
   ```

2. **Return results, don't produce side effects**

   ```go
   // Testable — caller decides what to do with result
   func CalculateDiscount(cart Cart) Discount {}

   // Hard to test — mutates input, no return value
   func ApplyDiscount(cart *Cart) {}
   ```

3. **Small surface area**
   - Fewer methods = fewer tests needed
   - Fewer params = simpler test setup
   - One responsibility per interface

## Planning Checklist

When defining interfaces in the plan, verify:

- [ ] Dependencies are parameters, not internal construction
- [ ] Functions return values instead of mutating state
- [ ] Each interface has ≤5 methods (ideally ≤3)
- [ ] A test for this interface needs ≤2 lines of setup
