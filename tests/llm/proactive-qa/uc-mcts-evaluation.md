# Use Case: Multi-Branch MCTS Evaluation Pipeline

**Actor**: Developer running complex DAG tasks where edge thought evaluation explores multiple candidate approaches.
**Route**: MCP (`tzro_run`) / CLI (`tzro chat`)
**Backend**: http://localhost:36888
**Priority**: P1

---

## Intent

A developer submits a complex task where the engine needs to evaluate multiple candidate approaches at a decision point in the DAG. Instead of committing to a single edge thought, the engine generates K candidate actions in a single inference call, evaluates each through speculative rollouts (executing real or simulated tool calls), scores them with a heuristic value function, and selects the highest-scoring candidate. This improves execution quality by exploring the action space before committing, while the speculation fence prevents dangerous side effects during evaluation.

## Preconditions

- The `tzro` daemon is running with a local model loaded.
- The task graph contains at least one node with `MCTSBranches > 0`.
- The speculation ceiling is configured (default: L2/Suggest level).
- The `MutationBudget.MaxDepth` is set to limit recursive spawning.

## Success Criteria

- [ ] When a node has `MCTSBranches > 0`, the engine generates K candidate actions in a single Local Model inference call using a GBNF-constrained JSON schema.
- [ ] Each candidate includes an action description, tool name, arguments, reasoning, and a self-assessed score.
- [ ] Candidates are evaluated through speculative rollouts — each candidate's tool is actually executed or simulated based on the speculation fence.
- [ ] The speculation fence classifies tools by proactivity level: tools at or below the ceiling execute for real (`SpecReal`), tools above the ceiling but at or below L3 are simulated (`SpecImagined`), and tools above L3 are blocked (`SpecBlocked`).
- [ ] Candidates requiring blocked tools are pruned from the evaluation set before rollout.
- [ ] `ImagineToolOutput` generates plausible simulated output for `SpecImagined` tools using the Local Model, with a template-based fallback when inference is unavailable.
- [ ] The heuristic value function scores candidates using four weighted signals: output quality (0.3), key term coverage (0.3), anti-hallucination check (0.2), and dampened self-assessment (0.2).
- [ ] The model's self-assessed score is dampened by 0.7x to prevent overconfident candidates from always winning.
- [ ] The highest-scoring candidate is selected for real execution on the node.
- [ ] Spawned nodes (those with `spawned_` prefix) always use single-shot mode, never multi-branch, to prevent recursive branching.
- [ ] Spawn depth tracking correctly counts `spawned_` prefix nesting levels.
- [ ] `canSpawnAtDepth` enforces `MutationBudget.MaxDepth`, preventing infinite spawn recursion.
- [ ] The PreFlect hook injects corrective micro-skills (SOPs) into node instructions before execution when matching skills exist in the skill store.
- [ ] PreFlect skill injection is logged with the skill name, node ID, and tool name.

## Edge Cases to Probe

- Node has `MCTSBranches = 3` but the model generates fewer than 3 candidates — verify graceful handling with fewer candidates.
- All K candidates require blocked tools — verify the node falls back to single-shot execution.
- Two candidates score identically — verify deterministic tie-breaking.
- Speculation fence ceiling is set to 0 (most restrictive) — verify most tools are blocked or imagined.
- `ImagineToolOutput` inference fails — verify template-based fallback produces a usable output.
- A spawned node at maximum spawn depth attempts to spawn another node — verify `canSpawnAtDepth` prevents it.
- PreFlect finds 3 corrective skills matching a tool — verify all 3 SOPs are concatenated and prepended to instructions.
- PreFlect `SkillFinder` is nil — verify no crash; returns `ActionContinue` immediately.

## Anti-Patterns to Watch For

- [ ] Multi-branch evaluation generates K separate inference calls instead of a single call with K candidates.
- [ ] Speculation fence allows irreversible tools (L4+) to execute during speculative rollout.
- [ ] `ImagineToolOutput` produces outputs that cause the value function to always score simulated candidates higher than real ones.
- [ ] Self-assessment dampening is not applied, causing overconfident candidates to always win.
- [ ] Anti-hallucination check misses obvious premature success claims in tool output.
- [ ] Spawned nodes inherit `MCTSBranches > 0` from their parent, causing exponential branching.
- [ ] Spawn depth counting fails when node IDs contain "spawned_" as a substring rather than prefix.
- [ ] PreFlect SOP injection corrupts the original node instructions by overwriting instead of prepending.
