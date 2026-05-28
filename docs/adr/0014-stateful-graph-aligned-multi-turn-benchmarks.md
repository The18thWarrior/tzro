# Stateful Graph-Aligned Multi-Turn Benchmarks

We transition the Berkeley Function Calling Leaderboard (BFCL) benchmark evaluation model to a stateful, graph-aligned multi-turn paradigm. The compiler script `convert_bfcl.py` will preserve the original conversational turns exactly, grouping expected tool calls per turn into an expected list rather than flattening them into artificial separate turns. The benchmark runner will track and maintain a stateful **Virtual Filesystem State** in-memory, injecting active current working directory (CWD) and tree layouts directly into the agent's planning prompts, and validate execution using sequence-agnostic multiset matching.

## Key Design Rules

1. **Dialogue Turn Preservation**: Conversational turns must align 1:1 with the original dataset question sequences. Turns that expect no tool calls (e.g. Turn 1 missing parameters) must be preserved rather than skipped, preventing dialogue context blindness.
2. **Virtual Filesystem State Simulation**: The benchmark runner must maintain a virtual POSIX-like directory and file state in-memory during GorillaFileSystem evaluations. Every tool execution (such as `cd`, `mkdir`, `rm`) must dynamically mutate this virtual filesystem state at runtime.
3. **Dynamic Prompt Injection**: The simulated working directory and folder tree must be dynamically rendered and appended as a structured environment block to the user prompt at the start of each turn. This isolates simulation context entirely to the benchmark harness, leaving production planning interfaces pristine.
4. **Sequence-Agnostic Multiset Matching**: Because the Go-powered Kahn Compiler schedules tasks into parallel topological sorting layers, validation must evaluate whether all expected tools were successfully called with correct arguments (a multiset check), rather than enforcing strict chronological order.

## Considered Options

- **Strict Turn-by-Turn Isolation**: Flatten expected tool sequences into separate turns and restrict the planning engine to emit exactly one tool node per turn during interactive benchmarks. Rejected — conflicts with our core architectural standard of Graph-RAG planning and Kahn compilation, forcing our execution graph into a slow, sequential conversational loop.
- **Pure Conversational Heuristics**: Avoid stateful filesystem tracking and force the planner to reconstruct the active current working directory purely from historical conversation logs and SQLite text facts. Rejected — regularly leads to semantic drift, context slot thrashing, and safety-fallback directory loops under local model limits.
- **Stateful Graph-Aligned Paradigm (Chosen)**: Group expectations per turn, simulate POSIX folder state dynamically, inject active environment context, and validate using multiset matching, ensuring maximum planning visibility and perfect graph alignment.
