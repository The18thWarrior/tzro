package templates

// NodeTypeReferenceCard is a compact reference string listing all available
// node types and key schema fields. It replaces the verbose ~150-line system
// prompt in planWithBackend() when templates are active (ADR-0048).
//
// The local model receives this alongside the serialized template so it knows
// what node types and fields are available beyond what the template demonstrates.
const NodeTypeReferenceCard = `## Node Type Reference

### Node Types
| Type | When to Use | Key Fields |
|------|------------|------------|
| probe | Open-ended exploration (codebase, logs, directories). Internal Thought Chain. | probeConfig (goal, allowedTools, stepBudget, compactEvery, sourceHint) |
| analyze | Structured/tabular data operations (CSV, database, cached data). Auto-provisioned tools. | instructions (natural language analysis goal) |
| action | Single known tool call with predetermined parameters. | action (tool name), allowedTools, dynamicBindings |
| conditional | Branch execution based on upstream output. | conditions, branchTrue, branchFalse |
| loop | Repeat a subgraph until a condition is met. | loopCondition, loopBody |

### Key Schema Fields
- instructions: Natural language goal for the node. Include ALL static values from the user's prompt.
- dynamicBindings: For values from upstream nodes, use {"param": "upstream_node_id.output.property"}. Do NOT bake upstream values into instructions.
- allowedTools: Restrict to only the 1-2 tools needed. Must reference tools from the inventory.
- activationThreshold: 0.0 = disabled (default). 0.7 = enable Edge Thoughts for neural traversal.
- probeConfig.sourceHint: "web" for internet research, "filesystem" for local files (default).
- probeConfig.stepBudget: Max exploration steps (default 20).

### Critical Rules
1. Edges represent BOTH data flow AND procedural ordering. If step A must complete before step B, emit an edge A→B.
2. For code generation tasks, use an action node calling "tzro_code" — never write code through write_file action nodes.
3. For exploration tasks where the next step depends on what you discover, ALWAYS use a probe node.
4. Do NOT reference tools not in the Available Tool Inventory.
5. Keep graphs concise (typically 2-6 nodes).
6. For compound tasks requesting 3+ distinct deliverables (e.g., Quickstart + API Reference + Architecture), decompose into multiple focused sub-nodes or sub-graphs feeding into the final action/synthesis node rather than a single monolithic probe.`
