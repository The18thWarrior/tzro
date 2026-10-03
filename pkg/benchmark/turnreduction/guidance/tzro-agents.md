# Tzro editing workflow

This workspace has the Tzro MCP server and a verification preset at `.tzro/verification.yaml`.
Use Tzro to combine related edits and their checks in one tool call.
This avoids separate edit, test-launch, and test-result steps in the agent conversation.

After you inspect the relevant code, prefer `tzro_edit_and_verify` on MCP server `tzro` for known literal replacements or file creations.
Batch related edits across files in one request.
For each replacement, supply `kind: "replace"`, `path`, `old_text`, `new_text`, and `expected_matches` with the exact intended match count.
For a new file, supply `kind: "create"`, `path`, and `content`.
Use workspace-relative paths and exact text from the files.

The tool checks the whole edit batch before it writes files, then runs the configured verification preset.
Inspect its application status, verification status, and check diagnostics.
A failed check leaves applied edits available for correction.
If a replacement is rejected, inspect the current text before you retry.
If the tool already reports successful required checks for the final edits, do not repeat those same checks without a reason.
Run additional checks when the task requires coverage outside the preset.

Use Tzro discovery or context tools when they help locate relevant code.
Ordinary reads and native tools remain available for operations that Tzro cannot express or when a Tzro tool is unavailable.
Choose the edits yourself from the task requirements and source code.
