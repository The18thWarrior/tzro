# TDD Loop Template

Use this template for each slice in the implementation plan.

## Per-Slice Format

```
### Slice N: [Short descriptive name]

**Interface changes:**
- [New or modified public types/functions — signatures only]

**RED — Write the test:**
- Test name: `Test[Behavior]_[Scenario]`
- Setup: [What state to create]
- Action: [What to call through the public interface]
- Assert: [What observable behavior to verify]

**GREEN — Minimal implementation:**
- [What code to write — just enough to pass the test]
- [No speculative features, no optimization]

**Verify:**
- [ ] Test fails before implementation (confirms test is sensitive)
- [ ] Test passes after implementation
- [ ] All previous tests still pass
```

## Rules

1. **One test per RED→GREEN cycle.** Don't batch.
2. **Test behavior, not implementation.** "User can checkout" not "checkout calls validateCart."
3. **Use public interfaces only.** No testing private methods or internal state.
4. **Minimal GREEN.** Write the least code that passes. Refactor later.
5. **Never refactor while RED.** Get to GREEN first, then clean up.

## Example

```
### Slice 1: Token tracker records prompt and completion counts

**Interface changes:**
- New: `type TokenTracker struct`
- New: `func NewTokenTracker() *TokenTracker`
- New: `func (t *TokenTracker) Record(prompt, completion int)`
- New: `func (t *TokenTracker) Total() (prompt, completion int)`

**RED — Write the test:**
- Test name: `TestTokenTracker_RecordsAndTotals`
- Setup: `tracker := NewTokenTracker()`
- Action: `tracker.Record(100, 50); tracker.Record(200, 75)`
- Assert: `total prompt == 300, total completion == 125`

**GREEN — Minimal implementation:**
- Struct with two int fields, Record adds, Total returns them

**Verify:**
- [ ] Test fails before implementation
- [ ] Test passes after implementation
- [ ] No other tests affected
```
