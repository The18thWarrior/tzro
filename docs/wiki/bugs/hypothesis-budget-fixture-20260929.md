# Registry test budget coupled to live campaign

Date: 2026-09-29
Status: fixed

## Symptom

Two registry tests failed after the user authorized a fresh $20 campaign allowance.

## Cause

The readiness fixture copied the live campaign cap but created a temporary ledger with a fixed $20 cap. The mismatch invalidated the fixture. It also prevented the confirmation-reserve check from reaching its intended boundary.

## Correction

The fixture now sets both caps to $20. Production cap validation and spending reservations are unchanged.

## Evidence

The original suite produced two failures. All 12 tests passed after the fixture correction. Command: `python3 -m unittest discover -s scripts -p 'test_hypothesis_registry.py'`.

## Prevention

Tests of fixed budget boundaries must define their complete budget fixture. Live campaign settings must not supply one side of the comparison.

Source: [registry tests](../../../scripts/test_hypothesis_registry.py).
