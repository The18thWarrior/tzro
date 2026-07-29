# Spec Compliance Gate for Codegen

Benchmark run `results-full-6` exposed a gap between the Compilation Gate and functional correctness: `create_query_builder` (T5, quality 2.0) compiled successfully after cloud repair but was missing implementations for SELECT, JOIN, GROUP BY, HAVING, ORDER BY, and LIMIT in `Build()`. The cloud repair prompt (ADR-0057) includes the original spec but its instructions say "Fix ALL compilation errors" and "Do not add new features or change the API surface" — Rule 4 actively prevents the repair model from adding the missing functionality. Separately, `update_add_error_handling` (T2, quality 2.75) compiled but had runtime `NameError` bugs that a syntax-only gate can't catch.

These are two instances of the same gap: code that compiles but doesn't implement the spec. The Compilation Gate handles syntax correctness. The new **Spec Compliance Gate** handles functional completeness. They are distinct concerns with distinct mechanisms.

The gate runs after the Compilation Gate passes. It feeds the generated code + original task spec to the **Local Model** with a structured evaluation prompt: "For each requirement in the spec, classify as IMPLEMENTED or MISSING with a one-line reason." If any requirements are MISSING, the gate triggers full regeneration — not targeted patching. The regeneration prompt includes the checklist of missing requirements as explicit remediation context, giving the model a focused second attempt.

Full regeneration over targeted patching: the failed code's structure may be fundamentally wrong for the missing requirements (e.g., `Build()` organized around INSERT/UPDATE/DELETE with no extensibility for SELECT). Patching Frankenstein code onto a wrong structure produces worse results than a clean regeneration with complete requirements visibility.

Budget: 1 local regeneration attempt. If the regenerated code still fails spec compliance, escalate to the cloud model for a single attempt — same tiered escalation pattern as ADR-0057 (Compilation Gate cloud repair). The regenerated code runs through the Compilation Gate normally — no special nesting. If it doesn't compile, the compilation repair loop handles it independently.

This subsumes the originally proposed P2 fix of adding mypy/pyflakes post-compilation linting. The `update_add_error_handling` runtime bugs (undefined variables, broken retry logic) are spec compliance failures, not syntax failures — the spec says "retry failed requests up to 3 times" and the code doesn't do that. A spec-aware gate catches this class of error without adding language-specific linter infrastructure.

## Considered Options

- **Widen the Compilation Gate repair prompt to include spec**: The repair prompt already includes the spec (line 53-54 of `codegen_repair.go`). The problem is the framing instructions, not missing context. Weakening Rule 4 ("don't add features") would break the repair prompt's primary use case in `CompilationGateHook` where you genuinely want narrow compilation fixes.
- **Targeted patching instead of full regeneration**: Cheaper but risks producing incoherent code where additions don't integrate with the existing structure. Local inference cost for a full regeneration is ~30 seconds — acceptable given the quality improvement (2.0 → 4.0+).
- **Language-specific linters (mypy, pyflakes, go vet)**: Catches a subset of runtime bugs but requires per-language toolchain management. The Spec Compliance Gate is language-agnostic and catches functional gaps that no linter would flag. Linters may be added later as a complementary pre-flight check but are not the primary mechanism.
- **Cloud-only evaluation**: More reliable but adds cloud cost to every codegen task. The hybrid approach (local evaluation, cloud escalation on failure) is consistent with the local-first philosophy.

## Consequences

- Codegen pipeline gains a second gate: Compilation Gate → Spec Compliance Gate. Both must pass before the output is accepted.
- The Spec Compliance Gate uses the **Local Model** for evaluation — evaluation is easier than generation, so the 4B model is expected to reliably identify missing requirements even when it couldn't implement them from scratch.
- Full regeneration doubles inference cost on spec-compliance failures (~30s additional wall clock). Accepted — quality improvement from 2.0 to 4.0+ justifies the cost.
- `BuildRepairPrompt` and `BuildRepairDAG` remain unchanged — they stay scoped to compilation repair. A new `BuildRegenerationPrompt` is needed for the spec compliance remediation path.
- The Spec Compliance Gate only fires for codegen tasks (nodes with `OutputFormat: "source_code"`). Documentation and data analysis tasks are not affected.
