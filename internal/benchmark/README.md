# tzro Durable DAG Benchmarking Suite

This package implements the off-line capable, dynamic benchmarking suite designed to measure the efficacy of the `tzro` durable execution engine against industry standard function-calling datasets.

Unlike traditional chat-loop agents that run turn-by-turn conversational cycles, `tzro` compiles complex tasks into optimal Directed Acyclic Graphs (DAGs) of parallelized and sequential steps. This suite evaluates whether compiling interactions into graphs outperforms traditional conversational loops, while also supporting turn-by-turn conversational simulation.

---

## 1. Benchmarking Evaluation Modes

The runner supports two distinct execution simulation modes:

### A. Consolidated DAG Mode (`--mode consolidated`)
Converts the entire multi-turn user conversation sequence into a single planned task. 
- The Cloud Planner builds a consolidated multi-step DAG with dynamic parameter links (e.g. `{{nodes.node_1.output.flight_id}}`).
- The Go compiler topological-sorts the DAG and runs node executions in parallel layers.
- Evaluates if the framework can successfully compile conversational loops into a deterministic planned pipeline.

### B. Interactive Multi-Turn Mode (`--mode interactive`)
Simulates traditional conversational cycles turn-by-turn.
- At each turn, a sub-DAG of a **maximum of 10 nodes** is planned and executed.
- The outcome of the tool execution is statefully committed as knowledge in `memory.DB` (saving structured key-value memories in standard SQLite tables and mapping entity links in the Relational Knowledge Graph).
- For subsequent turns, the **Hybrid Vector Search** and **Neighborhood Graph-RAG Traversal** (up to 2 hops) retrieve relevant contextual facts and dynamically inject them into the system and user prompts.
- Evaluates tzro's durable memory context loop over back-and-forth interactions.

---

## 2. Supported Datasets

The entire test suites are housed 100% offline within the repository to ensure CI/CD reliability and bypass external network latency:

1. **Berkeley Function Calling Leaderboard (BFCL)** (`--dataset bfcl`):
   - Focuses on complex API parameters, dynamic value selection, and sustained multi-turn conversational tool execution.
   - Located at `internal/benchmark/testdata/bfcl_samples.json`.

2. **ComplexFuncBench** (`--dataset complexfuncbench`):
   - Evaluates multi-step cross-domain booking paths (Flight, Hotel, Car Rental, Taxi, Attraction) and parameter value extraction under constraints.
   - Located at `internal/benchmark/testdata/complexfuncbench_samples.json`.

---

## 3. Command Line Interface Usage

Run the suite using the `tzro benchmark` command.

### Persistent Options
* `-d, --dataset`: The target evaluation dataset. Options: `bfcl` (default) or `complexfuncbench`.
* `-m, --mode`: The simulation mode. Options: `consolidated` (default) or `interactive`.
* `-t, --model-mode`: The model execution tier. Options: `local` (default), `cooperative`, or `cloud`.
* `-j, --json`: Print raw minified JSON output instead of styled tabular console summaries.

---

### Command Examples

#### 1. Run BFCL V3 in Consolidated DAG Mode (Default Local Model)
Evaluates if the local planner compiles flight-booking turns into a clean single DAG run:
```bash
./bin/tzro benchmark run --dataset bfcl --mode consolidated --model-mode local
```

#### 2. Run ComplexFuncBench in Interactive Mode (Cooperative Routing)
Evaluates cross-domain hotel/taxi queries with a 10-node sub-DAG compilation ceiling and SQLite Knowledge Graph context updates:
```bash
./bin/tzro benchmark run --dataset complexfuncbench --mode interactive --model-mode cooperative
```

#### 3. Run cloud-only evaluation for baseline calibration
Enforces remote cloud planner/local node executor completions:
```bash
./bin/tzro benchmark run --dataset bfcl --mode consolidated --model-mode cloud
```

#### 4. JSON Output for Scripting / CI Pipeline integrations
Dump the complete benchmark metrics to a file or stream:
```bash
./bin/tzro benchmark run --dataset bfcl --json > benchmark_run_report.json
```

---

## 4. Evaluated Metrics & Analysis

The analytics report calculates three core metrics:

1. **Successful Runs**: Percentage of test cases where all turns completed successfully, planning the correct sequence of tools and outputting precise parameters.
2. **DAG Planning Accuracy**: Verifies if the planner maps out the correct topological sequence of tools and dependency edges.
3. **GBNF Parameter Accuracy**: Measures the local model's ability to extract 100% syntactically correct parameters according to GBNF dynamic JSON schema constraints.
