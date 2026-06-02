# Bug Post-Mortem: Numeric Parameter Coercion and Resolution Failures

## Symptom
- **Description**: 
  - GBNF extracted positive numbers (e.g., `110.0`) for arguments like `adjustment_amount` in `inventory.update_ledger` when the instruction explicitly specified a negative ledger adjustment (e.g., `-110.0`).
  - Upstream dynamic numeric parameters (e.g., `amount: 12500.0`) were not resolved downstream, resulting in uncoerced `0` values.
  - This led to 13 Parameter Mismatch failures out of 80 cases in the `tzro_dag` benchmark run.
- **Reproduction**: Run `tzro_dag` consolidated benchmark cases containing negative number literals or dynamic float64 variables propagated from preceding nodes.

## Diagnosis
- **Hypotheses**:
  1. The GBNF grammar parser fails to reliably extract the negative prefix, returning the absolute positive value.
  2. The numeric coercion pipeline is completely bypassed for any non-zero extracted value.
  3. Upstream dynamic variable interpolation resolver only evaluates string types, ignoring floats and integers.
- **Root Cause**:
  1. **Strict Zero-Check in Coercion**: `coerceNumericArguments` in `internal/executor/executor.go` was designed to only apply natural language coercion if the GBNF-extracted value was exactly `0.0`. If a positive integer was extracted, the coercion was bypassed, preventing correction of sign flips.
  2. **String-Only Type Assertion**: `resolveInterpolatedArguments` in `internal/executor/executor.go` asserted the parameter value strictly as `val.(string)`. When evaluated against numeric arguments like `amount`, it failed the type assertion and skipped matching, leaving numeric arguments unresolved.

## Resolution
- **Fix**:
  1. **Extended Coercion**: Modified `coerceNumericArguments` to check for sign mismatches. The function now applies coercion if `numVal == 0` OR if a sign mismatch is detected relative to the instruction literal (e.g. `(numVal > 0 && bestNum < 0) || (numVal < 0 && bestNum > 0)`).
  2. **Numeric Interpolation Support**: Extended `resolveInterpolatedArguments` with a switch statement evaluating multiple numeric type footprints (`float64`, `int`, `int64`, `float32`). If a match is found and is numeric, it parses the upstream resolved output string using `strconv.ParseFloat` and correctly overrides the mismatched value.
  3. **Verification**: Executed Go integration tests (`TestBenchmarkRunTzroDagConsolidated`) which completed successfully (100% pass rate).

## Long-term Prevention
- Ensure all post-extraction parameter correction engines support mixed numeric/string type coercion and dynamic type conversions to safely bridge the semantic boundary between natural language instruction sets and structured execution tools.
