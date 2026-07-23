# Data Analysis Benchmark Category — Design Spec

> **Status**: Ready for implementation
> **Date**: 2026-07-13
> **Related**: [Data Profiler & Cache Bridge Node](data-profiler-and-cache-bridge-node.md)

---

## 1. Problem Statement

The comparison benchmark suite has two task categories — `docgen` (documentation generation) and `codegen` (code generation) — but no category that exercises **structured data access**. The upcoming Data Profiler & Cache Bridge Node feature changes how `read_file` handles tabular files (CSV, TSV, Excel), replacing raw content with a compact profile + cache reference. Without a data-focused benchmark category, there is no way to measure the profiler's impact on token consumption, cost, or answer quality.

## 2. Solution Overview

Add a third benchmark category (`datanal`) that runs 5 data analysis tasks against the [LeadSuccess.csv](../../helpers/LeadSuccess.csv) dataset (~255 rows, 22 columns of Salesforce lead data) under 3 execution conditions: `cloud_react`, `local_only`, and `cooperative`.

Each task asks a concrete, verifiable question. Tasks span:
- **Data retrieval**: filter-and-return queries (find records matching criteria)
- **Data aggregation**: group-and-compute queries (count, percentage, cross-tab)

Quality is scored via ground-truth comparison: each task includes a pre-computed `expectedAnswer` that the LLM judge uses to evaluate factual correctness.

### Relationship to Data Profiler

This benchmark is the **before/after test suite** for the Data Profiler:
- **Before**: `read_file` returns raw CSV (~54KB). All conditions process raw text.
- **After**: `read_file` returns a compact Data Profile (~2KB) with a cacheId. Conditions use `jq_cached_data` for surgical queries.

Run the benchmark before and after implementation to measure impact.

---

## 3. Execution Conditions

| Condition | ID | Pipeline | Model | Purpose |
|---|---|---|---|---|
| Cloud ReAct (Baseline) | `cloud_react` | ReAct loop | Cloud | Baseline: all tokens are cloud, full context accumulation |
| Local Only | `local_only` | tzro DAG | Local | Zero cloud cost, measures local model capability |
| Cooperative | `cooperative` | tzro DAG | Cloud plan + Local exec | Headline number: minimal cloud tokens |

### Fair Tooling

All conditions get the same tool set. When the Data Profiler is active, the ReAct condition's tool set is extended to include `introspect_cache`, `read_cached_data`, and `jq_cached_data` — ensuring the cost delta comes from execution model differences, not tool availability.

---

## 4. Task Suite

### CSV Dataset Schema

Source: `helpers/LeadSuccess.csv` (22 columns)

| Column | Type | Notes |
|--------|------|-------|
| `Saleforce_ID` | string | Unique lead ID |
| `account_name` | string | Company name |
| `name`, `name1`, `name2` | string | Contact name (full, first, last) |
| `email` | string | Contact email |
| `Accout_Owner` | string | Sales rep (⚠️ misspelled in source) |
| `Sector` | enum | eCommerce, Media, Other, SaaS, Social Gaming, or empty |
| `Lead_Source` | enum | Referral - Investor, Inside Sales Direct, Account / Sales Rolodex, etc. |
| `Primary_Incumbent_CDN` | string | Akamai, Edgecast, Amazon CloudFront, None, etc. |
| `Target_Account?` | string | "Yes" or empty |
| `Country` | string | USA, United Kingdom, Germany, Japan, etc. |

### Task Definitions

#### T1: `lead_lookup_by_company` (Retrieval)

**Prompt**: Read the CSV file at helpers/LeadSuccess.csv. Find all leads where the account_name is "Walmart". Return each lead's full name (the "name" column) and email address.

**What it tests**: Simple row filtering — identify column, filter by exact match, extract specific fields.

---

#### T1: `lead_count_by_country` (Aggregation)

**Prompt**: Read the CSV file at helpers/LeadSuccess.csv. Count the total number of leads for each unique value in the Country column. Return the top 5 countries ranked by lead count, including the count for each.

**What it tests**: Single-column GROUP BY with ORDER BY and LIMIT.

---

#### T2: `lead_sector_breakdown` (Aggregation)

**Prompt**: Read the CSV file at helpers/LeadSuccess.csv. Group all leads by the Sector column. For each sector, provide: the sector name, the number of leads, and the percentage of total leads (rounded to 1 decimal place). Include leads with an empty or blank Sector value as "Unspecified". Sort results by count in descending order.

**What it tests**: GROUP BY with computed derived column (percentage), null handling, sorting.

---

#### T2: `lead_source_by_owner` (Cross-tab Aggregation)

**Prompt**: Read the CSV file at helpers/LeadSuccess.csv. For each unique Account Owner (the Accout_Owner column — note the column name is misspelled in the data), count their total number of leads and list the distinct Lead_Source values for their leads. Sort by total lead count descending.

**What it tests**: Multi-column aggregation with nested distinct values, misspelled column handling.

---

#### T3: `lead_target_account_analysis` (Mixed Retrieval + Aggregation)

**Prompt**: Read the CSV file at helpers/LeadSuccess.csv. Find all leads where the Target_Account? column equals "Yes". Group these leads by their Primary_Incumbent_CDN value. For each CDN provider, return: the provider name, the count of target account leads using that CDN, and a comma-separated list of the distinct company names (account_name) associated with those leads. Sort by lead count descending.

**What it tests**: WHERE filter → GROUP BY → nested aggregation with distinct values. Most complex task.

---

## 5. Quality Evaluation

### Judge System Prompt

New `datanalJudgeSystemPrompt` evaluates data correctness:

```
You are a data analysis quality evaluator. You will receive a data analysis result 
produced by an AI model, along with the expected correct answer. Score each criterion 
on a 1-5 scale:
  1 = Completely wrong or missing
  2 = Partially correct but major errors in values or groupings
  3 = Mostly correct but some missing data points or minor calculation errors
  4 = Correct values and groupings with only cosmetic issues
  5 = Exact match with expected answer, clearly formatted

Compare the model's output against the Expected Correct Answer section.
```

### Rubric (All Tasks)

| Criterion | Description |
|-----------|-------------|
| **Correctness** | Values, counts, and groupings match the expected answer |
| **Completeness** | All requested data points present, no missing groups or records |
| **Formatting** | Results clearly formatted and easy to read |
| **Methodology** | Approach to data access and analysis is sound |

### Expected Answer Injection

The expected answer is injected into the judge's user message (no API changes):

```go
judgeOutput := taskResults[i].OutputText
if g.category == CategoryDatanal && task.ExpectedAnswer != "" {
    judgeOutput = fmt.Sprintf("## Model Output\n\n%s\n\n## Expected Correct Answer\n\n%s", 
        taskResults[i].OutputText, task.ExpectedAnswer)
}
```

---

## 6. Implementation Changes

### New Constants (`types.go`)

```go
const CategoryDatanal = "datanal"

func DatanalConditions() []string {
    return []string{ConditionCloudReAct, ConditionLocalOnly, ConditionCooperative}
}
```

### New Field on `ComparisonTask` (`types.go`)

```go
ExpectedAnswer string `json:"expectedAnswer,omitempty"`
```

### Task Loading (`suite.go`)

```go
//go:embed testdata/datanal_tasks.json
var datanalTaskDataFS embed.FS

// In LoadTasksByCategory switch:
case CategoryDatanal:
    data, err = datanalTaskDataFS.ReadFile("testdata/datanal_tasks.json")
```

### Category Loop (`suite.go`)

```go
for _, cat := range []string{CategoryDocgen, CategoryCodegen, CategoryDatanal} {
```

### Condition Routing (`suite.go`)

```go
if category == CategoryDatanal {
    return DatanalConditions()
}
```

### DAG Condition Handler (`conditions.go`)

Datanal follows the docgen path — read from project root, write to testOutputDir:

```go
} else if t.Category == CategoryDatanal {
    taskPrompt = fmt.Sprintf("%s\n\nIMPORTANT: The data file is located in the project directory at: %s/helpers/LeadSuccess.csv", 
        taskPrompt, projectRoot)
}
```

### ReAct System Prompt (`react.go`)

Category-aware system prompt + cache tools for datanal tasks:

```go
const reactDatanalSystemPrompt = `You are a data analyst. You have access to filesystem 
and data tools to read and query structured data files. Read the specified data file, 
analyze it, and answer the question precisely. When working with CSV/tabular data, 
use the appropriate tools to access cached data if a cacheId is provided.`
```

### Judge Category (`judge.go`)

```go
case CategoryDatanal:
    return datanalJudgeSystemPrompt
```

### CLI (`compare.go`)

Add "datanal" to category display and help text.

### New File: `testdata/datanal_tasks.json`

5 task definitions with `expectedAnswer` fields computed from actual CSV data.

---

## 7. Edge Cases

1. **Misspelled column**: `Accout_Owner` — T2 task explicitly calls this out in prompt
2. **Empty sector values**: T2 sector task specifies "Include as 'Unspecified'"
3. **CSV quirks**: Embedded newlines in quoted fields (row 26-28), commas in company names
4. **Pre-profiler baseline**: Works before Data Profiler implementation — all conditions read raw CSV

---

## 8. Verification Plan

### Expected Answer Computation

Run a verification script against the CSV to compute exact expected answers before embedding in task JSON.

### Automated Tests

| Test | What it validates |
|------|-------------------|
| `LoadTasksByCategory("datanal", 0)` | Loads all 5 tasks |
| `LoadTasksByCategory("datanal", 1)` | Returns only T1 tasks (2) |
| `DatanalConditions()` | Returns `[cloud_react, local_only, cooperative]` |
| `conditionsForCategory("datanal", "")` | Returns `DatanalConditions()` |
| `JudgeSystemPromptForCategory("datanal")` | Returns datanal judge prompt |

### Manual Verification

```bash
tzro compare --category datanal --tier 1 --condition cooperative -o /tmp/test
```
