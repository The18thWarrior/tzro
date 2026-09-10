**Tzro capability evaluation — September 8, 2026**

This assessment reviews `astra-changes` at `4820318`, relative to local `main` at `1e60244`. The branch contains two commits and changes 37 files. The working tree was clean before this assessment. These five recommendations are proposals, not approved implementation decisions.

**The strongest direction is a dependable local context layer that improves task completion across agent clients.** The current architecture has useful foundations for that direction. Its next challenge is to connect those foundations into complete user workflows.

The assessment uses repository history, current source, design records, and primary external references. Historical wiki entries describe their contemporary architecture. The current [domain model](../../../CONTEXT.md) and [solution approach](../../../SOLUTION_APPROACH.md) define the present boundaries.

| Period | Evidence from Git history | Product lesson |
| --- | --- | --- |
| May–June 2026 | Initial commit on May 21. Early releases added durable execution, MCP, local inference, plugins, and client tool dispatch. | The original goal combined local control, persistent work, and cooperation with cloud agents. |
| July–August 2026 | Releases added code generation, tabular SQL, verification gates, plan templates, and neural embeddings. | Reliability required increasing amounts of execution scaffolding. |
| August 26–27 | The wiki records the architecture reset. Commit `1e60244` released the v2 replacement. | Deterministic discovery and compaction became the core product. |
| September 8 | Commits `30596b6` and `4820318` added context, artifact, session, policy, and compatibility capabilities. | Tzro now has the foundations for context continuity and evidence management. |

The documented inspirations have distinct roles. [ADR-0015](../../adr/0015-pristal-architecture-alignment.md) explicitly attributes the earlier Strategist–Compiler–Translator design to Pristal v2. The [wiki log](../log.md) records research into RadixAttention, prefix reuse, symbolic dictionaries, and lossless prefill optimization. The v2 [PRD](../../PRD_TZRO_V2.md) positions lightweight native processing against heavyweight context compressors, including Headroom. This review does not independently establish its competitor resource comparisons.

The older challenges explain the current boundaries. The [planner post-mortem](../bugs/cloud-planner-timeout-and-heuristic-fallback-pollution.md) records timeouts and fallback plans that introduced unavailable tools. The [benchmark post-mortem](../bugs/benchmark-dataset-corruption-and-label-shifting.md) records problems with locally converted labels and turn alignment. Those records do not establish defects in every upstream benchmark dataset.

Later wiki entries describe context corruption during synthesis, weak navigation loops, and benchmark-specific scaffolding. The August 26 record explicitly removes task-ID branches from the evaluation harness. These lessons favor deterministic evidence processing and independent outcome checks. All five proposals preserve the current zero-model-sidecar default.

**The branch adds substantial foundations, with uneven integration.** The [Astra capability page](../features/astra-context-capabilities.md) lists the original scope. Source inspection confirms these additions:

- Provider normalization preserves unknown fields and tool identifiers. Protocol fixtures cover the expanded routes, and usage parsing records provider-reported values.
- The privacy engine adds workspace rules, custom detectors, preview, and audit storage.
- Context packs combine symbol search, path ranking, test filename hints, and TS/JS import relationships. The latest commit adds path aliases and lexical fallback when FTS5 is unavailable.
- The artifact store adds full-content identities, range expansion, SQL access, and an LRU eviction API.
- Session manifests add objectives, constraints, decisions, checks, file snapshots, pending work, and artifact references. Import includes workspace and freshness checks.

Several boundaries still need end-to-end work. These are observations from the reviewed source, not a complete security or correctness audit.

| Boundary | Current evidence | Consequence |
| --- | --- | --- |
| Session capture | [`session save`](../../../cmd/tzro/main.go) immediately serializes a new manifest. It defaults the branch to `main`. | Changed files, checks, decisions, pending tasks, and artifact references remain empty through this CLI path. |
| Context selection | [`Assembler.Assemble`](../../../pkg/context/context.go) often expands a symbol candidate into a whole-file skeleton. It always admits the first candidate. | A pack can exceed its requested budget. The current import expansion does not supply general reverse references. |
| Workspace identity | [`symbol_index` and `file_index_state`](../../../pkg/store/store.go) lack workspace keys. The CLI opens one home-directory database. | Multiple repositories and worktrees need stronger index isolation before automatic cross-client use. |
| Artifact retention | [`EvictArtifactsLRU`](../../../pkg/store/store.go) exists and has tests. No production caller invokes eviction or its quota alias. | Automatic storage limits still need configuration and lifecycle wiring. |
| Privacy coverage | [`tzro start`](../../../cmd/tzro/main.go) does not load the workspace policy into the proxy configuration. Context assembly and artifact capture lack equivalent policy calls. | A policy engine alone does not establish uniform enforcement across all entry points. |
| Diagnostics | [`doctor`](../../../cmd/tzro/main.go) prints a hardcoded route matrix alongside local checks. | A `VERIFIED` label does not establish a live provider or client handshake. |

The performance evidence also needs careful interpretation. The updated [saved benchmark](../../../pkg/hooks/testdata/kvcache_e2e_benchmark_results.json) records 43,035 direct prompt tokens and 32,859 proxied prompt tokens. That is a 23.6% reduction in this run. Recorded elapsed time falls from 42,520 ms to 37,040 ms, a 12.9% reduction.

However, recorded warm cache hits fall from 82.3% to 68.1%. The direct run has 19 turns and the proxied run has 10. This artifact does not include an independent task-completion grade. These results describe one saved run and do not isolate the effect of any individual branch change.

The [context holdout test](../../../pkg/context/holdout_test.go) provides a small synthetic retrieval fixture. Its pack query includes expected path terms, while its probe query differs. It does not enforce equal context budgets or measure downstream task success. It reports Go heap allocation with an RSS label. This is useful test coverage, but it does not establish general task quality or process memory limits.

The following proposals extend the branch. Illustrative command names describe possible interfaces and are not available commands.

1. **Context packs that explain the impact of a change**

   A developer changes a shared interface. Tzro returns its callers, implementations, affected tests, configuration dependencies, and relevant design constraints. The agent can see the likely consequences before it edits another file.

   The branch already finds symbols and follows selected TS/JS imports. The addition is reverse dependency traversal anchored to a Git diff or symbol. It also needs repository and worktree identity, bounded traversal, and relationships with explicit precision levels.

   The first slice supports Go and TS/JS changes. It combines changed-symbol detection, reverse references, related test selection, and exact source spans under a hard budget. Later slices can import existing compiler indexes through SCIP. Optional index imports preserve the small default runtime.

   The module boundary is an impact index behind `pkg/context`, `pkg/ast`, and `pkg/store`. A caller supplies a revision range or symbol and a budget. The result contains ranked evidence and an explanation for each relationship.

   [Aider repository maps](https://aider.chat/docs/repomap.html) demonstrate dependency ranking within context budgets. [Sourcegraph precise navigation](https://sourcegraph.com/docs/code-navigation/precise-code-navigation) demonstrates compiler-derived references with search fallback. These are design references, not evidence that tzro historically copied those systems.

   Success means better recall of affected consumers and tests on unseen repositories at the same context budget. Additional checks cover branch changes, renames, unresolved imports, latency, and process memory. Static relationships must remain distinguishable from inferred test associations.

2. **Automatic task continuity across agent clients**

   A developer stops one agent and resumes the task in another. The next agent receives the objective, current constraints, pending work, file changes, and check results. It also sees which evidence became stale after intervening edits.

   The branch already supplies a session manifest format and import checks. The addition is automatic capture, task selection, artifact retention, and restoration at client lifecycle boundaries. The current empty-manifest CLI path makes this a large practical opportunity.

   The first slice connects two supported clients through their documented hooks. Git supplies revision and file state. Tool events supply commands, exit codes, timestamps, and output artifacts. The host agent supplies explicit decisions and pending work. Tzro stores those statements without pretending to infer intent deterministically.

   Repository, worktree, and task identities prevent unrelated sessions from mixing. Active sessions pin required artifacts. Cross-machine export later needs an artifact bundle and deliberate path remapping, since references to a local store are insufficient.

   This extends `pkg/session`, `pkg/hooks`, and `pkg/store`. [Claude lifecycle hooks](https://code.claude.com/docs/en/hooks#sessionstart) provide one concrete integration pattern. Each client needs its own capability check because lifecycle events differ.

   Success means a receiving agent resumes a seeded interrupted task without repeating completed checks or losing constraints. Checks must also invalidate stale evidence and detect absent artifacts. Task continuity preserves state for the host agent and introduces no execution scheduler.

3. **Compaction with explicit evidence guarantees**

   A test command produces thousands of lines and one failure. The compact result retains that failure, the command exit code, its source location, and exact retrieval instructions. A dependency stack frame remains available when that frame explains the error.

   The branch already retains originals in artifact-aware paths. The addition is a machine-readable result contract that identifies retained evidence and omitted spans. Retrieval alone cannot help an agent that has no indication it missed something important.

   The first slice adds adapters for Go test JSON, JUnit, and common compiler diagnostics. Every adapter preserves outcome fields, failure identifiers, and artifact-backed source spans. If parsing or original retention fails, the adapter returns the original with an explicit status. A structured result also preserves null values, missing fields, numeric types, and multiline cells.

   This deepens `pkg/compactor` through a small result type. Host adapters supply exit status because a text pipe alone cannot recover it reliably. Retention follows workspace policy and session pins.

   The [MCP tool-result specification](https://modelcontextprotocol.io/specification/2025-06-18/server/tools#tool-result) separates structured results, text, and resource references. Tzro can use that contract pattern within existing CLI and hook surfaces.

   Success means zero lost seeded failures, no false-success output, and exact recovery of retained source spans. This guarantee concerns known output fields and provenance. It does not guarantee that a model interprets every result correctly.

4. **One local evidence search across code, documents, and tool output**

   A developer asks why an integration fails. One context pack contains the implementation, an OpenAPI constraint, a relevant design decision, and the latest matching CI error. Each excerpt identifies its source and revision.

   The branch already stores artifacts and finds some documents through path keywords. The addition is content search and common provenance across documents and captured results. It turns logs and specifications into reusable evidence alongside source code.

   The first slice indexes Markdown sections, HTML text, OpenAPI/JSON Schema paths, JUnit output, and captured logs. Each entry records origin, content hash, revision or timestamp, and line or section anchors. Optional PDF and Office extractors can add page anchors later. Local exports can supply issue and support records without mandatory account connectors.

   A source-adapter interface feeds the existing Content-Hash Store and Context Pack Assembler. Native text formats keep the default installation small. [Ripgrep-all adapters](https://github.com/phiresky/ripgrep-all#available-adapters) provide a concrete reference for optional extraction and cached local search.

   Success means higher evidence recall on integration, incident, and onboarding tasks at fixed budgets. Every excerpt needs a valid origin. Privacy checks precede indexing and export. Historical documents remain distinguishable from current requirements.

5. **A local context inspector with replay and quality profiles**

   A developer sees that an agent missed a requirement. Tzro shows whether retrieval omitted it, compaction removed it, or the selected pack included an outdated version. The developer can compare a proposed context profile against saved local inputs.

   The branch already records provider usage and includes retrieval tests. The addition is a user-facing explanation of context decisions, deterministic replay, and repository-specific quality profiles. This connects token reduction to evidence retention and task outcomes.

   The first slice records a bounded trace of candidate selection, inclusion reasons, excluded spans, transformations, policy decisions, and artifact references. Replay compares alternative budgets and compaction profiles without provider calls. The report distinguishes measured usage, estimates, and unavailable measurements.

   Optional host-agent runs can compare task completion, repeated reads, artifact expansions, retries, and elapsed time. Those runs use fixed revisions and independent outcome checks. A replay of saved inputs alone cannot establish how an agent will behave after its context changes.

   The boundary is a context trace and replay module that consumes events from existing packages. It does not own task execution. [Anthropic's evaluation guidance](https://www.anthropic.com/engineering/demystifying-evals-for-ai-agents) supports separate treatment of transcripts, outcomes, and graders.

   Success means users can reproduce a known omission and identify a better profile on unseen tasks. A profile recommendation requires preserved quality at the same or smaller budget. Trace storage remains local, bounded, and subject to privacy policy.

**Recommended delivery order:** complete the existing workspace, policy, budget, and retention boundaries first. Then build change-impact context and automatic continuity as the first visible capabilities. Evidence contracts strengthen both. Broader source retrieval follows that provenance work. A minimal replay recorder starts early, while profile tuning follows a representative task corpus.

This order prioritizes missed dependencies, repeated work, and lost evidence. The architectural tradeoff is deliberate: more useful context services within the current deterministic core. Each proposal has a small interface and owns its indexing, capture, extraction, or replay complexity.

**Validation performed for this assessment:** `go test -short ./pkg/... ./cmd/tzro` passed in every package except the sandbox-blocked proxy suite. The proxy failure was a denied loopback bind. An unrestricted `go test -short ./pkg/proxy` rerun passed. Verbose output passed through `tzro compact`, and shell `pipefail` preserved the test status.

Live provider benchmarks were not rerun. The saved benchmark supplied the reported run statistics. No runtime source files changed during this assessment.
