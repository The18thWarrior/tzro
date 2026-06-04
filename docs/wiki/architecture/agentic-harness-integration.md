# Architecture: Agentic Harness Integration (MCP vs. Native Plugin vs. Sidecar)

When integrating `tzro` with agentic frameworks (such as Anthropic's Claude Code, Google's Antigravity SDK, or Nous Research's Hermes Agent), the communication boundary between the orchestrator (harness) and the execution runner (`tzro`) plays a critical role in token cost and execution latency. This document evaluates the pros, cons, and mechanics of the three integration paradigms: MCP Server, Native Plugin, and Sidecar.

---

## 1. The Core Problem: The Cloud Execution Round-Trip Tax

When running `tzro` as a standard **Model Context Protocol (MCP) Server** over stdio, the protocol limits communication to client-initiated Request-Response cycles. Because standard stdio MCP does not support server-initiated tool calls, if the `tzro` Kahn Compiler schedules a step requiring a tool hosted on the client environment (like running terminal commands or modifying files), the system must execute a multi-step suspend/resume loop:

1. **DAG Execution Interception**: The Go executor reaches a node requiring a client tool (e.g., `run_command`).
2. **Notification & Pause**: The engine writes a `client_tool_request` notification to `tzro.db` and pauses the execution thread.
3. **Polling/Subscription Notification**: The client harness must poll `tzro_client_tool_list` (or subscribe to events) to discover that a client tool is pending.
4. **Local Execution & Submission**: The parent agent's LLM is prompted to execute the tool locally in the harness and submit the output via `tzro_client_tool_submit`.
5. **Resume Task**: The engine resumes task execution, moving to the next node.

For a DAG with multiple client tool steps, this requires **separate round-trips back to the cloud LLM**, adding latency and consuming a massive number of cloud tokens.

---

## 2. Architectural Comparison

```
┌──────────────────────────────────────────────────────────────────────────────────────────────┐
│                                   INTEGRATION PARADIGMS                                      │
├────────────────────────────────┬───────────────────────────────┬─────────────────────────────┤
│      1. MCP Server Mode        │      2. Native Plugin Mode     │     3. Sidecar / SSE        │
│                                │                               │                             │
│       ┌──────────────┐         │       ┌──────────────┐        │       ┌──────────────┐      │
│       │  Parent LLM  │         │       │  Parent LLM  │        │       │  Parent LLM  │      │
│       └──────┬───────┘         │       └──────┬───────┘        │       └──────┬───────┘      │
│     Tools    │ (Resume loops   │              │ (Runs once)    │              │ (Runs once)  │
│     calls    │  via Cloud LLM) │              ▼                │              ▼              │
│              ▼                 │       ┌──────────────┐        │       ┌──────────────┐      │
│       ┌──────────────┐         │       │ Native Tool  │        │       │ Sidecar      │      │
│       │  tzro Server │         │       │ Dispatcher   │        │       │ Mediator     │      │
│       └──────────────┘         │       └──────▲───────┘        │       └──────▲───────┘      │
│                                │              │ (Direct        │              │ (Outbound    │
│                                │              │  in-process)   │              │  JSON-RPC)   │
│                                │       ┌──────┴───────┐        │       ┌──────┴───────┐      │
│                                │       │ tzro Engine  │        │       │ tzro Daemon  │      │
│                                │       └──────────────┘        │       └──────────────┘      │
└────────────────────────────────┴───────────────────────────────┴─────────────────────────────┘
```

### 2.1 Option 1: MCP Server (Current)
*   **Transport**: Stdio or HTTP/SSE JSON-RPC 2.0.
*   **Pros**: 
    *   Universal compatibility with any host supporting MCP (Cursor, Claude Desktop, Claude Code, Zed).
    *   Strong process isolation.
*   **Cons**:
    *   High round-trip latency.
    *   Requires parent LLM context loops to execute client tools.

### 2.2 Option 2: Native Plugin (Hermes / Antigravity SDK)
*   **Transport**: Direct, in-process Python/Go API bindings.
*   **Pros**:
    *   **Direct Tool Dispatching**: When `tzro` encounters a node requiring a client tool (e.g., `run_command` or `write_file`), the plugin programmatically invokes the harness's local execution API in-process (e.g., `ctx.dispatch_tool` in Hermes) without yielding control back to the cloud LLM.
    *   **Zero Cloud Step Loops**: Bypasses the cloud LLM entirely for intermediate client execution steps.
    *   **Rich Environment Access**: The plugin has full access to the harness's configured backends (SSH, Docker, local shell, etc.).
*   **Cons**:
    *   Tightly coupled. Requires maintaining specific plugin packages for each harness.

### 2.3 Option 3: Sidecar Daemon with Bidirectional IPC
*   **Transport**: Local WebSocket or SSE/HTTP bidirectional connection.
*   **Pros**:
    *   Maintains process separation while allowing `tzro` to invoke client tools asynchronously over the socket connection.
*   **Cons**:
    *   Standard agent CLIs (e.g., Claude Code) do not support inbound tool execution calls from servers, making this model difficult to run in closed environments without custom wrappers.

---

## 3. Concrete Integration Maps

### 3.1 Hermes Agent Plugin Integration
In Hermes, a custom Python plugin resides under `~/.hermes/plugins/`. An integrated `tzro` plugin registers its tools and intercepts execution:

*   **Initialization**: The plugin starts `tzrod` in the background and registers the `tzro_run` tool.
*   **Direct Dispatch Hook**: The plugin registers a callback that intercepts `tzro` node execution. If a node needs a client tool, instead of pausing:
    ```python
    # Programmatic tool execution inside Hermes runtime
    result = ctx.dispatch_tool(
        "run_command", 
        {"command": node_instruction}
    )
    # Feed result directly back to the local executor
    ```
*   **Result**: The parent LLM invokes `tzro_run` once, and the local Go/WASM executor runs the entire graph to completion, only returning the final consolidated Markdown report to the parent agent.

### 3.2 Google Antigravity SDK Integration
In the Antigravity SDK, `tzro` integrates via the connection and policy layers:

*   **Connection Binding**: Packaged as a custom Python connection client (`TzroConnection`), allowing developers to configure the agent to compile prompts into DAGs.
*   **Safety Integration**: Plugs into the SDK's `policy` configuration (e.g., [safety_policies.md](../references/safety_policies.md)), allowing the SDK to approve or deny commands dynamically based on predicates before `tzro` runs them.

---

## 4. Architectural Recommendation

To support both portability and efficiency, `tzro` adopts a **Layered Hybrid Architecture**:

1.  **Portability Layer (MCP)**: Maintain the standard stdio MCP server for IDEs/CLIs (Cursor, Claude Desktop, Claude Code) where plugin installation is restricted.
2.  **Performance Layer (Native Plugin)**: Build and publish first-class Python plugins for **Hermes Agent** and the **Google Antigravity SDK**. These plugins hook directly into the harness tool context to execute client-side tasks programmatically in-process, bypassing the cloud LLM tax entirely.
