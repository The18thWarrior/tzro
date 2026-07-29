# Use Case: Local Code Generation via tzro_code

**Actor**: AI coding agent delegating file-level code generation to the local engine.
**Route**: MCP (`tzro_code`)
**Backend**: http://localhost:36888
**Priority**: P0

---

## Intent

An AI coding agent wants to generate or modify a single source file by providing a natural language spec and a target file path. The engine gathers context from the target file and its siblings, classifies complexity, routes to either direct generation or a two-phase draft mode, applies compilation quality gates, and writes the result — all locally without cloud API calls.

## Preconditions

- The `tzro` daemon is running with a local GGUF model loaded.
- The target file path is within the allowed filesystem roots.
- For compilation quality gates: Go, TypeScript, Python, or JavaScript toolchains are available on the host.

## Success Criteria

- [ ] Agent can invoke `tzro_code` with a spec and file path, and receives generated code written to the target file.
- [ ] Context gathering reads the target file (if it exists) and up to 5 sibling files from the same directory, prioritizing same-extension files.
- [ ] Large existing files are truncated using content-aware truncation (preserving signatures, not cutting mid-function).
- [ ] Binary files are rejected with a clear error message.
- [ ] Language detection correctly infers the language from file extension for all supported languages (Go, TypeScript, Python, Rust, etc.).
- [ ] Complexity classification routes simple tasks to direct single-pass generation and complex tasks to two-phase draft mode.
- [ ] In draft mode, a first pass generates a draft and a second pass refines it — both visible as DAG nodes.
- [ ] The compilation quality gate runs language-appropriate checks (go build, tsc, python -m py_compile) against the generated output.
- [ ] If compilation fails, a structured repair prompt is generated containing the error output and injected into a spawned repair node.
- [ ] The repair node receives the original code, compilation errors, and language-specific reference patterns to guide the fix.
- [ ] Diff mode generates a structured diff patch instead of a full-file rewrite when updating large existing files.
- [ ] Diff patches are applied correctly, preserving unchanged sections of the file.
- [ ] Module context extraction provides package-level type signatures and exports to the generation prompt for Go files.
- [ ] The max line cap is enforced — generated output exceeding the cap is truncated with a warning.
- [ ] Markdown fence stripping removes spurious code fences from LLM output before writing.
- [ ] Hot-swappable model management allows the engine to temporarily swap to a code-specialized GGUF model for generation, then lazily restore the default model after completion.
- [ ] Model swap is transparent to the caller — `tzro_code` returns generated code regardless of which model served the request.
- [ ] If the code-specialized model fails to load, generation falls back to the current active model without error.
- [ ] Spec compliance gate evaluates compiled code against spec requirements after compilation pass.
- [ ] If compliance evaluation finds missing requirements, regeneration prompt instructs full implementation of all requirements.

## Edge Cases to Probe

- Generating a file in a directory that doesn't exist yet — verify parent directories are created.
- Target file is a 20K-line file with a small edit spec — verify diff mode is selected and only the relevant section changes.
- Compilation gate fails 3 times in a row — verify failure dampening prevents infinite repair loops.
- Spec requests code in a language that doesn't have a compilation gate (e.g., Lua) — verify the gate is skipped gracefully.
- Two concurrent `tzro_code` calls writing to the same file — verify no data corruption.
- Target path outside allowed roots — verify rejection before any file I/O.

## Anti-Patterns to Watch For

- [ ] Generated code includes markdown fences (```go ... ```) that corrupt the output file.
- [ ] Compilation repair enters an infinite loop, spawning nodes beyond the mutation budget.
- [ ] Diff mode produces a patch that silently deletes code not mentioned in the spec.
- [ ] Context gathering reads binary files (images, compiled artifacts) and injects garbage into the prompt.
- [ ] Large sibling files blow up the prompt context without truncation.
- [ ] The quality gate runs destructive commands (e.g., `go clean`) that affect the user's project state.
- [ ] Error messages from compilation failures are swallowed — the user sees "generation failed" with no detail.
- [ ] Module context extraction fails silently, causing the generator to miss type information.
