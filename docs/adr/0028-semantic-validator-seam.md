# 28. Semantic Validator Seam and XML Schema Generation

Date: 2026-06-09

## Status

Accepted (Supersedes [0002-local-gbnf-constraints.md](0002-local-gbnf-constraints.md))

## Context

Historically, tzro relied on strict GBNF grammar constraints during Local Model inference to guarantee 100% syntactically perfect JSON tool execution payloads (ADR-0002). However, profiling our benchmark runs revealed two major issues with this approach:
1. **Speed Bottleneck:** Computing grammar masks token-by-token for deeply nested JSON schemas limited the model's generation speed to ~29 t/s.
2. **Reliability (The Straitjacket Effect):** Strict enforcement stripped the small local models of their decoding freedom, causing them to hallucinate invalid default parameters, truncate outputs, or enter endless loops just to satisfy the syntax constraints, resulting in a 50% parameter extraction failure rate.

We needed a way to restore decoding speed and reliability while still guaranteeing that the execution engine receives exact JSON structures matching the MCP tool schemas.

## Decision

We are deprecating deep JSON GBNF constraints in favor of a **Semantic Validator** seam and **XML-based generation**.

1. **XML Generation & Schema Translation:** The Local Model will now output tool calls as loose XML tags (`<tool>...</tool><args>...</args>`). To minimize cognitive load, the engine will intercept JSON Schemas at registration and translate them into XML structures in the system prompt. GBNF is restricted to shallow XML wrapper tag validation merely to ensure parsability.
2. **Explicit Validator Node:** The Kahn Compiler will inject an explicit `semantic_validator` node into the DAG prior to execution. This node is durable and checkpointed to SQLite.
3. **Deterministic Coercion & Retry Loop:** The Semantic Validator is responsible for type coercion, default imputation, and fuzzy matching. If an argument is un-coercible or missing, the validator will immediately throw a deterministic error string back to the Local Model in a retry loop (e.g., "Validation failed: 'latest' cannot be converted to integer for orderId"), rather than escalating or silently failing.

## Consequences

*   **Positive:** Generation speeds will increase significantly due to the removal of deep token grammar masking.
*   **Positive:** Parameter accuracy will increase as the model generates simpler formats it is naturally trained on, allowing the deterministic seam to handle the complexity of JSON coercion.
*   **Negative:** Added complexity to the Kahn Compiler and prompt generation, as we now maintain translation layers between JSON schema and XML schema.
*   **Positive:** Durable retry loops mean that validation failures are gracefully handled and visible in the telemetry dashboard.
