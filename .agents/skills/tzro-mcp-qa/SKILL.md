---
name: tzro-mcp-qa
description: Relentlessly perform QA and verify the tzro-mcp integration, ensuring task execution harness handles pauses, client tool submissions, and Probe node thought chains correctly. Use when validating the tzro integration harness, debugging dynamic task loops or Probe node execution, running relentless QA checkups, or when the user requests verification of the tzro-mcp server and engine.
---

# tzro MCP QA & Verification Skill

## Quick Start

1. Build/ensure the `tzro-mcp` binary is available:
   ```bash
   go build -o bin/tzro-mcp cmd/mcp/main.go
   ```
2. Run the QA harness check script to verify the connection and loop handling:
   ```bash
   python3 .agents/skills/tzro-mcp-qa/scripts/qa_harness_check.py
   ```
3. Check status of tasks in the SQLite database:
   ```bash
   sqlite3 tzro.db "SELECT cache_id, created_at FROM disk_cache WHERE cache_id LIKE 'graph_%';"
   ```

## Workflows

### 1. Harness Setup Verification
- Verify the local `tzro-mcp` server starts correctly via stdio.
- Check that the host client registers all client-side tools successfully with `tzro_register_client_tools`.
- Ensure tool calls dispatched by tzro are successfully intercepted and executed in the host agent.

### 2. Probe Node & Thought Chain Validation
- Trigger a task that includes a Probe Node to explore a mock/real path.
- Monitor `tzro_status` to ensure it transitions cleanly through the thought steps.
- Verify that rolling compaction compiles intermediate thoughts (every 3 steps) to control context pressure.
- Verify that terminal synthesis is produced and returned to the parent graph.

### 3. Relentless Verification Loop (The /goal Pattern)
- When failures occur (e.g., timeouts, DB locks, malformed JSON outputs):
  1. Locate the exact step from SQLite `thought_steps` or task logs.
  2. Implement local code adjustments or path configuration fixes.
  3. Re-run tests and assert clean execution.
  4. Iterate continuously until all test cases pass without errors.
  5. Only request user input for core architectural decisions.

## Advanced Features
- Detailed checklists & diagnostic queries: See [REFERENCE.md](REFERENCE.md)
- Complete task execution examples: See [EXAMPLES.md](EXAMPLES.md)
