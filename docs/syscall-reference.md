# tzro Syscall Reference

This document maps every registered MCP tool in the tzro engine to its closest POSIX/OS syscall analogue, framing the engine as an **Agentic Operating System** where agents interact with system services through a well-defined call interface.

---

## Process Management

These syscalls control task (process) lifecycle — creation, monitoring, termination, and scheduling.

| MCP Tool | OS Analogue | Description |
|:---|:---|:---|
| `tzro_run` | `fork(2)` + `execve(2)` | Spawn a new task. Compiles a DAG from the prompt and begins durable execution. |
| `tzro_status` | `waitpid(2)` / `kill(pid, 0)` | Poll a task's execution state (pending, running, completed, failed). |
| `tzro_list_tasks` | `ps(1)` / `getdents(2)` on `/proc` | List recent tasks with status, node counts, and completion timestamps. |
| `tzro_resume` | `SIGCONT` | Resume a paused task that was waiting on a hook (approval gate or client tool). |
| `tzro_workflow` | `systemd` unit orchestration | Create, trigger, pause, or cancel persistent multi-task workflows with cron scheduling. |

---

## Memory & Storage

Analogues to filesystem and virtual memory operations — reading, writing, and querying persistent state.

| MCP Tool | OS Analogue | Description |
|:---|:---|:---|
| `tzro_memory_query` | `read(2)` / `pread(2)` | Query the relational fact memory store with optional semantic vector search. |
| `tzro_memory_ingest` | `write(2)` / `pwrite(2)` | Persist a new fact memory record with content, context, and confidence. |
| `tzro_kg_neighborhood` | `readdir(2)` + `stat(2)` (graph traversal) | Multi-hop neighborhood traversal of the Relational Knowledge Graph from a seed entity. |
| `tzro_kg_add_entity` | `mkdir(2)` / `mknod(2)` | Create a new node in the Knowledge Graph with typed metadata. |
| `tzro_rag_context` | `mmap(2)` (memory-mapped retrieval) | Retrieve hybrid vector + keyword search results from both memory and KG, assembled into a context block. |

---

## Skill / Shared Library Management

Analogues to dynamic linker operations — loading, listing, and resolving shared procedure libraries.

| MCP Tool | OS Analogue | Description |
|:---|:---|:---|
| `tzro_skills_list` | `ldconfig -p` / `dl_iterate_phdr(3)` | List all registered Procedural Micro-Skills (SOPs). |
| `tzro_skills_get` | `dlsym(3)` | Retrieve a specific micro-skill's full SOP content by ID. |
| `tzro_skills_relevant` | `ld.so` symbol resolution | Find micro-skills relevant to a given task context via trigger matching. |
| `tzro_skills_add` | `dlopen(3)` | Register a new Procedural Micro-Skill from a trigger description and SOP body. |

---

## Tool / Device Driver Management

Analogues to device registration and I/O control — managing the tools available to the execution engine.

| MCP Tool | OS Analogue | Description |
|:---|:---|:---|
| `tzro_configure_tools` | `ioctl(2)` / `sysfs` device config | Configure which tools are enabled/disabled for the current session. |
| `tzro_register_client_tools` | `insmod(8)` / `register_chrdev(9)` | Register external tools from the harness agent into the tool registry at runtime. |
| `tzro_client_tool_list` | `lsmod(8)` / `/proc/devices` | List all client-registered (harness-forwarded) tools. |
| `tzro_client_tool_submit` | `write(2)` to device fd (async I/O completion) | Submit the result of a client tool execution back to a waiting task node. |

---

## Interrupt & Signal Handling

Analogues to signal delivery and approval gates — inter-process communication for task coordination.

| MCP Tool | OS Analogue | Description |
|:---|:---|:---|
| `tzro_hook_list` | `sigpending(2)` | List pending hooks (approval gates, client tool requests) awaiting resolution. |
| `tzro_hook_approve` | `sigaction(2)` / signal handler dispatch | Approve or reject a pending hook, unblocking the waiting task node. |

---

## Inference / Compute

Analogues to compute dispatch — requesting structured work from the local or cloud model.

| MCP Tool | OS Analogue | Description |
|:---|:---|:---|
| `tzro_completion` | `syscall(2)` (generic kernel call) | Request a raw text completion from the active inference backend. |
| `tzro_classification` | `prctl(2)` (process attribute query) | Classify input text into structured categories via the local model. |
| `tzro_model_list` | `sysconf(3)` / `getrlimit(2)` | List available inference backends and their current configuration. |
| `tzro_model_set` | `setrlimit(2)` / `nice(2)` | Switch the active inference backend or model configuration. |

---

## Monitoring & Telemetry

Analogues to system monitoring and proactive diagnostics — background agents and activity tracking.

| MCP Tool | OS Analogue | Description |
|:---|:---|:---|
| `tzro_observer_events` | `dmesg(1)` / `klogctl(2)` | Retrieve recent telemetry events captured by the Observer Agent. |
| `tzro_observer_memories` | `auditctl(8)` / audit log query | Retrieve memories synthesized by the Observer Agent from telemetry reflection. |
| `tzro_activity_report` | `acct(2)` (process accounting) | Report current agent activity for Sentinel Agent correlation. |
| `tzro_sentinel_alerts` | `/dev/watchdog` read | Retrieve proactive alerts generated by the Sentinel Agent's ambient analysis. |
| `tzro_sentinel_wake` | `SIGALRM` / watchdog ping | Manually trigger an immediate Sentinel Agent analysis cycle. |

---

## Dashboard & Introspection

Analogues to system introspection and status reporting.

| MCP Tool | OS Analogue | Description |
|:---|:---|:---|
| `tzro_dashboard` | `uptime(1)` / `sysinfo(2)` | Check system dashboard status, URL, and spec freshness. |
| `tzro_dashboard_regenerate` | `sync(2)` (force flush) | Force immediate regeneration of the system dashboard specification. |
| `tzro_dashboard_spec` | `cat /proc/meminfo` | Return the raw dashboard spec JSON for debugging. |

---

## Package Management

Analogues to OS package management — installing, removing, and listing capability extensions.

| MCP Tool | OS Analogue | Description |
|:---|:---|:---|
| `tzro_apps_list` | `dpkg -l` / `rpm -qa` | List all installed Agent Apps and their current status. |
| `tzro_apps_install` | `dpkg -i` / `rpm -i` | Install an Agent App from a `.tzroapp` archive. |
| `tzro_apps_uninstall` | `dpkg -r` / `rpm -e` | Uninstall an Agent App (soft-disable or purge). |

---

## Network / External

Analogues to network system calls — fetching external data.

| MCP Tool | OS Analogue | Description |
|:---|:---|:---|
| `tzro_web_search` | `connect(2)` + `send(2)` / `recv(2)` | Execute a web search query and return structured results. |

---

## Design Notes

1. **Durable by default**: Unlike POSIX where processes lose state on crash, every `tzro_run` task is checkpointed to SQLite. The closest analogue is a process with CRIU (Checkpoint/Restore In Userspace) enabled by default.

2. **No file descriptors**: The MCP protocol is request/response, not fd-based. Tools like `tzro_client_tool_submit` simulate async I/O completion callbacks rather than persistent file descriptor reads.

3. **Capability-gated**: The Proactivity Ladder (L0–L4) acts as a capability-based security model similar to Linux capabilities (`CAP_NET_ADMIN`, `CAP_SYS_PTRACE`), where each tool has a declared proactivity level that gates execution against the workflow's approved ceiling.

4. **Two-tier execution**: The Local Model / Cloud Model split is analogous to user-space vs kernel-space — routine work stays local (user-space), while privileged operations (planning, world knowledge) escalate to the cloud (kernel).
