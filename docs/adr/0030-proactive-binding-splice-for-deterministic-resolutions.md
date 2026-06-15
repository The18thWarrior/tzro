# 30. Proactive Binding Splice for Deterministic Resolutions

When the Response Resolver (ADR-0029) deterministically resolves a DynamicBinding via `recursive_key` or `fuzzy_key`, the resolved value is stripped from the tool schema before inference and spliced back into the final JSON after inference. The model never sees or generates these parameters — it can't get them wrong.

Previously, resolved values were injected into the prompt as "RESOLVED UPSTREAM VALUES" hints, and the model was asked to generate all parameters including ones we already knew. A post-extraction override only fired when the model output was null/empty. In practice, the model frequently ignored the hint and generated plausible-but-wrong values (e.g., outputting `/receipt_code_path` instead of the resolved `/receipts/rcpt_XXX.pdf`), accounting for 8 of 14 GBNF parameter failures in benchmark debug9.

## Considered Options

| Option | Why Rejected |
|---|---|
| Widen the post-extraction override to always replace (not just null/empty) | Still reactive — depends on the override running after the model. Also risks clobbering correct model outputs with bad `semantic_fallback` resolutions |
| Inject resolved values more aggressively into the prompt | Still depends on the model obeying instructions, which is the failure mode we're fixing |
| Proactive strip & splice (chosen) | Eliminates the model from the path entirely for known values. Zero inference cost for these params, zero chance of getting them wrong |

## Consequences

- **Positive:** Eliminates an entire class of parameter mismatch failures (~57% of GBNF failures in debug9) without additional inference calls.
- **Positive:** Reduces the validator's inference task complexity — fewer params to extract means simpler, faster, more accurate extraction for the remaining params.
- **Positive:** `resolveDynamicBindings` now returns tier metadata (`ResolvedBinding{Value, Tier}`), making resolution confidence explicit in the type system rather than implicit in log output.
- **Negative:** Schema modification logic adds complexity to the validator node path — we parse, modify, and re-serialize the JSON schema before inference.
- **Negative:** `semantic_fallback` resolutions are deliberately excluded from the splice (too unreliable), creating a bifurcated override path: proactive for high-confidence tiers, prompt-hint for low-confidence.
