# Use Case: GLiNER Zero-Shot Span Extraction

**Actor**: The graph execution engine that needs to extract structured entities (file paths, function names, parameters) from unstructured text without hallucination.
**Route**: Internal — `pkg/extractor` consumed by `pkg/executor` via the `Extractor` interface
**Backend**: `pkg/extractor` — WorkerClient, GLiNERAdapter
**Priority**: P1

---

## Intent

During graph execution, extract nodes need to identify and locate specific entities within unstructured text — file paths mentioned in error logs, function names referenced in user queries, or parameter values embedded in natural language instructions. The GLiNER worker (150M params, ONNX Runtime) performs zero-shot span extraction locally: given a text and a set of semantic labels, it returns the matched substrings with byte offsets and confidence scores. Unlike generative models, it cannot hallucinate entities that are not present in the input text.

## Preconditions

- GLiNER worker binary (Python ONNX Runtime) is installed and accessible
- ONNX model weights are available at the expected path
- Graph execution engine is configured with an `Extractor` via `WithExtractor`

## Success Criteria

- [ ] Worker starts on first `Extract()` call and emits a `"ready"` banner
- [ ] Extraction request with a text containing a file path and label `"file_path"` returns the correct span with start/end offsets
- [ ] Multiple labels in a single request return spans for each matched label type
- [ ] Spans that do not exist in the text are not returned (no hallucination)
- [ ] Confidence scores are between 0.0 and 1.0
- [ ] Start and end byte offsets accurately delimit the matched text substring
- [ ] Worker auto-restarts on write failure (broken pipe) and retries the request
- [ ] `Close()` terminates the worker process cleanly
- [ ] Adapter correctly maps between `extractor.ExtractedSpan` and `executor.ExtractedSpan`
- [ ] Latency is reported in the response (`latency_ms` field)

## Edge Cases to Probe

- Empty text string — should return zero spans, not error
- Labels that match nothing in the text — should return empty spans list
- Very long text (10K+ chars) — should complete without timeout or truncation
- Worker process crashes mid-extraction — should restart and retry
- Unicode text with multi-byte characters — byte offsets should be correct
- Overlapping entity spans (e.g., nested paths) — should return both spans

## Anti-Patterns to Watch For

- [ ] Worker hangs during startup with no timeout
- [ ] Extraction returns spans with text that does not match the byte offsets
- [ ] Self-healing restart leaves orphaned worker processes
- [ ] Concurrent extraction calls corrupt shared stdin/stdout buffers
- [ ] Adapter silently swallows extraction errors instead of propagating them
- [ ] Worker restart loop with no backoff on persistent failures
