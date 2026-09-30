# Developer workflow benchmark: 2026-09-29T01:06:43Z

Recipe: `tzro.installation-profiles.v1`. Model: `minimax/minimax-m3`.

Evidence: [workflows-20260928.json](workflows-20260928.json). All 24 cells are included.

Total tokens include uncached input, cache reads, cache writes, and output. Costs use the recorded prices.

## Standard installation release gate

**not validated**

Required: 40% token savings in each of at least 3 complete repetitions, lower estimated cost, and no paired quality regression.

- Every declared repetition must be present, numbered from one.
- At least 3 complete repetitions are required.
- A declared, complete task matrix is required.
- Client version and immutable source snapshot are required.
- Unmatched grading_sha256: macro_1_cache_impl, repeat 1.
- Incomplete usage or evidence: baseline/macro_1_cache_impl/1.
- Incomplete usage or evidence: standard/macro_1_cache_impl/1.
- Unmatched grading_sha256: macro_2_rate_bugfix, repeat 1.
- Incomplete usage or evidence: baseline/macro_2_rate_bugfix/1.
- Incomplete usage or evidence: standard/macro_2_rate_bugfix/1.
- Unmatched grading_sha256: macro_3_schema_refactor, repeat 1.
- Incomplete usage or evidence: baseline/macro_3_schema_refactor/1.
- Incomplete usage or evidence: standard/macro_3_schema_refactor/1.
- Unmatched grading_sha256: macro_4_auth_diagnosis, repeat 1.
- Incomplete usage or evidence: baseline/macro_4_auth_diagnosis/1.
- Incomplete usage or evidence: standard/macro_4_auth_diagnosis/1.
- Unmatched grading_sha256: macro_5_large_file_skeleton, repeat 1.
- Incomplete usage or evidence: baseline/macro_5_large_file_skeleton/1.
- Incomplete usage or evidence: standard/macro_5_large_file_skeleton/1.
- Unmatched grading_sha256: macro_6_verbose_log_compact, repeat 1.
- Incomplete usage or evidence: baseline/macro_6_verbose_log_compact/1.
- Incomplete usage or evidence: standard/macro_6_verbose_log_compact/1.
- Unmatched grading_sha256: macro_7_tabular_analysis, repeat 1.
- Incomplete usage or evidence: baseline/macro_7_tabular_analysis/1.
- Incomplete usage or evidence: standard/macro_7_tabular_analysis/1.
- Unmatched grading_sha256: macro_8_multi_pkg_discovery, repeat 1.
- Incomplete usage or evidence: baseline/macro_8_multi_pkg_discovery/1.
- Incomplete usage or evidence: standard/macro_8_multi_pkg_discovery/1.
- Repeat 1 does not reach 40% token savings.
- Repeat 1 does not reduce estimated cost.

## Aggregate results

| Profile | Success | Requests | Total tokens | Uncached | Output | Cache read | Estimated cost | Agent time | Usage |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Baseline | 6/8 (75.0%) | 49 | 145,951 | 42,912 | 5,503 | 97,536 | $0.02532936 | 235.784s | complete |
| Standard | 8/8 (100.0%) | 68 | 219,783 | 54,925 | 7,546 | 157,312 | $0.03497142 | 250.815s | complete |
| Full | 8/8 (100.0%) | 69 | 239,843 | 53,218 | 8,065 | 178,560 | $0.03635700 | 276.107s | complete |

## Matched successful tasks

This descriptive subset excludes failed pairs. The complete matrix remains authoritative for quality and the release gate.

| Profile | Successful pairs | Token savings | Cost savings |
| --- | --- | --- | --- |
| standard | 6 | -40.8% | -24.5% |
| full | 6 | -44.5% | -20.4% |

## Repetition and variation

| Profile | Repetitions | Mean suite tokens | Range | Sample standard deviation | Cell latency p50 | Cell latency p90 |
| --- | --- | --- | --- | --- | --- | --- |
| baseline | 1 | 145,951 | 145,951–145,951 | unknown (one repetition) | 34.165s | 62.190s |
| standard | 1 | 219,783 | 219,783–219,783 | unknown (one repetition) | 27.725s | 61.261s |
| full | 1 | 239,843 | 239,843–239,843 | unknown (one repetition) | 31.726s | 67.865s |

## Per-task comparison

Token totals include all attempts, including failures. Savings are meaningful only alongside success and complete usage.

| Task | Profile | Success | Requests | Total tokens | Savings vs Baseline |
| --- | --- | --- | --- | --- | --- |
| macro_1_cache_impl | baseline | 1/1 | 7 | 16,816 | 0.0% |
| macro_1_cache_impl | standard | 1/1 | 6 | 18,902 | -12.4% |
| macro_1_cache_impl | full | 1/1 | 7 | 17,078 | -1.6% |
| macro_2_rate_bugfix | baseline | 1/1 | 6 | 16,238 | 0.0% |
| macro_2_rate_bugfix | standard | 1/1 | 9 | 25,133 | -54.8% |
| macro_2_rate_bugfix | full | 1/1 | 9 | 24,277 | -49.5% |
| macro_3_schema_refactor | baseline | 0/1 | 3 | 5,779 | 0.0% |
| macro_3_schema_refactor | standard | 1/1 | 7 | 16,301 | -182.1% |
| macro_3_schema_refactor | full | 1/1 | 8 | 20,895 | -261.6% |
| macro_4_auth_diagnosis | baseline | 1/1 | 6 | 16,848 | 0.0% |
| macro_4_auth_diagnosis | standard | 1/1 | 9 | 24,553 | -45.7% |
| macro_4_auth_diagnosis | full | 1/1 | 10 | 28,165 | -67.2% |
| macro_5_large_file_skeleton | baseline | 1/1 | 5 | 25,961 | 0.0% |
| macro_5_large_file_skeleton | standard | 1/1 | 8 | 40,095 | -54.4% |
| macro_5_large_file_skeleton | full | 1/1 | 7 | 42,253 | -62.8% |
| macro_6_verbose_log_compact | baseline | 0/1 | 3 | 5,647 | 0.0% |
| macro_6_verbose_log_compact | standard | 1/1 | 5 | 14,091 | -149.5% |
| macro_6_verbose_log_compact | full | 1/1 | 7 | 24,619 | -336.0% |
| macro_7_tabular_analysis | baseline | 1/1 | 8 | 30,702 | 0.0% |
| macro_7_tabular_analysis | standard | 1/1 | 14 | 54,138 | -76.3% |
| macro_7_tabular_analysis | full | 1/1 | 13 | 53,993 | -75.9% |
| macro_8_multi_pkg_discovery | baseline | 1/1 | 11 | 27,960 | 0.0% |
| macro_8_multi_pkg_discovery | standard | 1/1 | 10 | 26,570 | 5.0% |
| macro_8_multi_pkg_discovery | full | 1/1 | 8 | 28,563 | -2.2% |

No causal cache claim follows from different tool sequences. OS and provider caches remain uncontrolled.

## Per-cell evidence

| Profile | Task | Repeat | Status | Success | Tokens | Requests | Cost | Tools/errors | Skill read | Trace |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| baseline | macro_1_cache_impl | 1 | completed | PASS | 16816 | 7 | 0.00342252 | 6/0 | unknown | not recorded |
| standard | macro_1_cache_impl | 1 | completed | PASS | 18902 | 6 | 0.0038365199999999995 | 7/0 | unknown | not recorded |
| full | macro_1_cache_impl | 1 | completed | PASS | 17078 | 7 | 0.00292818 | 6/0 | unknown | not recorded |
| baseline | macro_2_rate_bugfix | 1 | completed | PASS | 16238 | 6 | 0.00352602 | 7/0 | unknown | not recorded |
| standard | macro_2_rate_bugfix | 1 | completed | PASS | 25133 | 9 | 0.004262879999999999 | 8/0 | unknown | not recorded |
| full | macro_2_rate_bugfix | 1 | completed | PASS | 24277 | 9 | 0.00401004 | 8/0 | unknown | not recorded |
| baseline | macro_3_schema_refactor | 1 | failed | FAIL | 5779 | 3 | 0.0009272999999999999 | 4/0 | unknown | not recorded |
| standard | macro_3_schema_refactor | 1 | completed | PASS | 16301 | 7 | 0.0026353799999999997 | 6/0 | unknown | not recorded |
| full | macro_3_schema_refactor | 1 | completed | PASS | 20895 | 8 | 0.0034637999999999995 | 7/0 | unknown | not recorded |
| baseline | macro_4_auth_diagnosis | 1 | completed | PASS | 16848 | 6 | 0.00339318 | 9/1 | unknown | not recorded |
| standard | macro_4_auth_diagnosis | 1 | completed | PASS | 24553 | 9 | 0.0038653199999999998 | 8/0 | unknown | not recorded |
| full | macro_4_auth_diagnosis | 1 | completed | PASS | 28165 | 10 | 0.0043698 | 9/0 | unknown | not recorded |
| baseline | macro_5_large_file_skeleton | 1 | completed | PASS | 25961 | 5 | 0.0042798 | 6/0 | unknown | not recorded |
| standard | macro_5_large_file_skeleton | 1 | completed | PASS | 40095 | 8 | 0.00688056 | 7/0 | unknown | not recorded |
| full | macro_5_large_file_skeleton | 1 | completed | PASS | 42253 | 7 | 0.005101560000000001 | 8/0 | unknown | not recorded |
| baseline | macro_6_verbose_log_compact | 1 | failed | FAIL | 5647 | 3 | 0.00089502 | 2/0 | unknown | not recorded |
| standard | macro_6_verbose_log_compact | 1 | completed | PASS | 14091 | 5 | 0.0030672000000000004 | 6/0 | unknown | not recorded |
| full | macro_6_verbose_log_compact | 1 | completed | PASS | 24619 | 7 | 0.004582439999999999 | 8/0 | unknown | not recorded |
| baseline | macro_7_tabular_analysis | 1 | completed | PASS | 30702 | 8 | 0.00474996 | 9/0 | unknown | not recorded |
| standard | macro_7_tabular_analysis | 1 | completed | PASS | 54138 | 14 | 0.00667494 | 13/1 | unknown | not recorded |
| full | macro_7_tabular_analysis | 1 | completed | PASS | 53993 | 13 | 0.0069603 | 12/0 | unknown | not recorded |
| baseline | macro_8_multi_pkg_discovery | 1 | completed | PASS | 27960 | 11 | 0.004135560000000001 | 10/0 | unknown | not recorded |
| standard | macro_8_multi_pkg_discovery | 1 | completed | PASS | 26570 | 10 | 0.0037486199999999994 | 9/0 | unknown | not recorded |
| full | macro_8_multi_pkg_discovery | 1 | completed | PASS | 28563 | 8 | 0.00494088 | 10/0 | unknown | not recorded |

## Tool selection and hook transformations

### Baseline

Native tools: not recorded

Local activity: none recorded

Native tzro commands: none recorded

Observed skill reads: 0/8 cells. Resource discovery alone does not count as a read.

Hook transformation sizes and outcomes: not recorded.

### Standard

Native tools: not recorded

Local activity: `cli:tzro hook` × 64, `cli:tzro ingest` × 1, `cli:tzro query` × 2

Native tzro commands: none recorded

Observed skill reads: 0/8 cells. Resource discovery alone does not count as a read.

Hook transformation sizes and outcomes: not recorded.

### Full

Native tools: not recorded

Local activity: `cli:tzro hook` × 68, `cli:tzro ingest` × 1, `cli:tzro query` × 1, `cli:tzro start` × 8

Native tzro commands: none recorded

Observed skill reads: 0/8 cells. Resource discovery alone does not count as a read.

Hook transformation sizes and outcomes: not recorded.

## Failures

### baseline / macro_3_schema_refactor / repeat 1

```text
task tests failed: exit status 1: # acme/model [acme/model.test]
./item_test.go:7:7: item.Tags undefined (type *Item has no field or method Tags)
./item_test.go:8:14: item.Tags undefined (type *Item has no field or method Tags)
./item_test.go:9:48: item.Tags undefined (type *Item has no field or method Tags)
FAIL	acme/model [build failed]
FAIL

```

### baseline / macro_6_verbose_log_compact / repeat 1

```text
task tests failed: exit status 1: --- FAIL: TestProcessBatch (0.00s)
panic: empty item encountered in pipeline stage 10: index out of bounds [recovered, repanicked]

goroutine 20 [running]:
testing.tRunner.func1.2({0x1004b9a40, 0x1004ec960})
	/usr/local/go/src/testing/testing.go:1974 +0x1a0
testing.tRunner.func1()
	/usr/local/go/src/testing/testing.go:1977 +0x318
panic({0x1004b9a40?, 0x1004ec960?})
	/usr/local/go/src/runtime/panic.go:860 +0x12c
acme/pipeline.stage10(...)
	/private/var/folders/sy/zbm6jhg96cg4dl2_9sxv6f8m0000gn/T/tzro-workflows-1590039907/profiles/015-baseline/workspace/pipeline.go:30
acme/pipeline.stage9(...)
	/private/var/folders/sy/zbm6jhg96cg4dl2_9sxv6f8m0000gn/T/tzro-workflows-1590039907/profiles/015-baseline/workspace/pipeline.go:26
acme/pipeline.stage8(...)
	/private/var/folders/sy/zbm6jhg96cg4dl2_9sxv6f8m0000gn/T/tzro-workflows-1590039907/profiles/015-baseline/workspace/pipeline.go:25
acme/pipeline.stage7(...)
	/private/var/folders/sy/zbm6jhg96cg4dl2_9sxv6f8m0000gn/T/tzro-workflows-1590039907/profiles/015-baseline/workspace/pipeline.go:24
acme/pipeline.stage6({0x0?, 0x100468a40?})
	/private/var/folders/sy/zbm6jhg96cg4dl2_9sxv6f8m0000gn/T/tzro-workflows-1590039907/profiles/015-baseline/workspace/pipeline.go:23 +0x50
acme/pipeline.stage5(...)
	/private/var/folders/sy/zbm6jhg96cg4dl2_9sxv6f8m0000gn/T/tzro-workflows-1590039907/profiles/015-baseline/workspace/pipeline.go:22
acme/pipeline.stage4(...)
	/private/var/folders/sy/zbm6jhg96cg4dl2_9sxv6f8m0000gn/T/tzro-workflows-1590039907/profiles/015-baseline/workspace/pipeline.go:21
acme/pipeline.stage3(...)
	/private/var/folders/sy/zbm6jhg96cg4dl2_9sxv6f8m0000gn/T/tzro-workflows-1590039907/profiles/015-baseline/workspace/pipeline.go:20
acme/pipeline.stage2(...)
	/private/var/folders/sy/zbm6jhg96cg4dl2_9sxv6f8m0000gn/T/tzro-workflows-1590039907/profiles/015-baseline/workspace/pipeline.go:19
acme/pipeline.stage1(...)
	/private/var/folders/sy/zbm6jhg96cg4dl2_9sxv6f8m0000gn/T/tzro-workflows-1590039907/profiles/015-baseline/workspace/pipeline.go:18
acme/pipeline.ProcessBatch({0x36ace0f23f08?, 0x100518120?, 0x36ace0f23f08?})
	/private/var/folders/sy/zbm6jhg96cg4dl2_9sxv6f8m0000gn/T/tzro-workflows-1590039907/profiles/015-baseline/workspace/pipeline.go:12 +0xa4
acme/pipeline.TestProcessBatch(0x36ace0f60248)
	/private/var/folders/sy/zbm6jhg96cg4dl2_9sxv6f8m0000gn/T/tzro-workflows-1590039907/profiles/015-baseline/workspace/pipeline_test.go:7 +0x68
testing.tRunner(0x36ace0f60248, 0x1004ebdf8)
	/usr/local/go/src/testing/testing.go:2036 +0xc4
created by testing.(*T).Run in goroutine 1
	/usr/local/go/src/testing/testing.go:2101 +0x3a8
FAIL	acme/pipeline	0.312s
FAIL

```

## Reproducibility

| Field | Recorded value |
| --- | --- |
| Client version | unknown |
| Source state | dirty |
| arch | arm64 |
| binary_vcs.modified | true |
| binary_vcs.revision | e8c16aeac8a03b70f6e78250e4190998492b7ca5 |
| binary_vcs.time | 2026-09-28T22:18:13Z |
| cache_policy | fresh client, workspace, Go cache and tzro store per cell; readiness workers stop before tasks; OS and provider caches uncontrolled |
| client_binary_sha256 | 0e4e408dac67af83dd4431a7780400712da6b9e083f862de568f04ef586c8501 |
| cost_guard | checked after each reported assistant message; in-flight usage can exceed the limit |
| cost_source | estimate from native client token usage and caller-supplied prices; incomplete usage is flagged |
| cpu_model | Apple M2 Pro |
| download_cost | not measured; models must be provisioned before running this recipe |
| go_version | go1.26.0 |
| hardware_model | Mac14,9 |
| installation_source | explicit local binary through install.sh |
| installer_sha256 | 2bb243b92bfbf79e81b8fe1a10fc4a1189b525d1915b9449dede89b485e29ce6 |
| logical_cpus | 10 |
| max_turns | 20 |
| memory_bytes | 34359738368 |
| model | minimax/minimax-m3 |
| model_context_window | 128000 |
| model_max_output_tokens | 8192 |
| os | darwin |
| prices | {"cache_read_per_million": 0.06, "cache_write_per_million": 0, "input_per_million": 0.3, "output_per_million": 1.2} |
| provider_base_url | https://openrouter.ai/api/v1 |
| recipe | tzro.installation-profiles.v1 |
| runtimes | {"decision_binary_sha256": "37fcf9c5110d275a9b3ffda027ad1abe55b6bb21b0d1adc58ab01249eba0cdb0", "decision_model_sha256": "0a19bc29bacc33e0d871146c8612b24dd14c2ed2e61cedeb7a928b0852628bac", "decision_version": "JEV v3 (libllama 9770, Qwen3.5)", "extractor_argument_files_sha256": {"gliner_worker.py": "c9e0031b9ee2c48945cd47ce01c57fca31f020f71cf5eec7ca9fc54f984c1446"}, "extractor_arguments_sha256": "6114d0c7ea766dafe1e22e292613bcd084dcae9ae0de5690e479bc3afa31c13a", "extractor_binary_sha256": "15040e58b17b6417ce2a71a0de166f6bdf4e6e4e1ee61374413d61eb4e361b7f", "extractor_model_sha256": "898ba838a048c7fa4599654405ddef54437e642875a5706613dcceea8cf2ea81", "extractor_version": "GLiNER 2.5 (gliner2 2.0.0, torch 2.8.0)"} |
| source_diff_sha256 | a13a1fe08e0cbcb79aac338054cfdadd7a4dfe8b5df8ea578411a95ad3c443dd |
| source_revision | e8c16aeac8a03b70f6e78250e4190998492b7ca5 |
| source_status | M Makefile<br> M cmd/tzro/bench_workflows.go<br> M docs/benchmarks/workflows-20260928.json<br> M docs/benchmarks/workflows-20260928.md<br> M pkg/benchmark/workflow/execute.go<br> M pkg/benchmark/workflow/runner.go<br> M pkg/benchmark/workflow/tasks.go<br> M pkg/benchmark/workflow/types.go<br> M pkg/compactor/compactor.go<br> M pkg/compactor/compactor_test.go<br> M pkg/dlp/policy.go<br> M pkg/hooks/instructions.go<br> M scripts/generate_benchmark_report.py<br>?? pkg/benchmark/workflow/tasks_realistic.go<br>?? pkg/benchmark/workflow/tasks_realistic_test.go |
| task_timeout_seconds | 180 |
| timestamp | 2026-09-29T01:06:43Z |
| tzro_binary_sha256 | 39694f38671c8d018f2e0bfd309bc3783d3254bfb945d80e85cf30f991bf663e |

Runtime readiness and task invocation are separate measurements. Setup and preflight are recorded separately from agent time.

The cost guard acts after reported usage. In-flight requests can exceed it. Missing usage is not zero cost.
