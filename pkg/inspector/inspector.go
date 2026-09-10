package inspector

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"tzro/pkg/dlp"
	"tzro/pkg/store"
)

// Epistemological tiers.
const (
	TierMeasured       = "measured"
	TierCounterfactual = "counterfactual"
	TierUnknown        = "unknown"
)

// TraceConfig holds the configuration snapshot of the context assembly.
type TraceConfig struct {
	Budget            int    `json:"budget"`
	CompactionProfile string `json:"compaction_profile,omitempty"`
	PolicyVersion     string `json:"policy_version,omitempty"`
	IncludeGenCode    bool   `json:"include_gen_code"`
}

// CandidateTrace records a discovered candidate.
type CandidateTrace struct {
	Path       string  `json:"path"`
	Hash       string  `json:"hash"`
	SourceKind string  `json:"source_kind"`
	Score      float64 `json:"score"`
	Tokens     int     `json:"tokens"`
}

// FilterDecision records whether a candidate passed filtering and why.
type FilterDecision struct {
	Path   string `json:"path"`
	Passed bool   `json:"passed"`
	Stage  string `json:"stage"` // privacy | gitignore | generated_code | allow
	Reason string `json:"reason,omitempty"`
}

// RankedCandidate records candidate ranking metrics.
type RankedCandidate struct {
	Path         string  `json:"path"`
	Rank         int     `json:"rank"`
	Score        float64 `json:"score"`
	Precision    string  `json:"precision,omitempty"`
	Relationship string  `json:"relationship,omitempty"`
}

// PackedItem records an included candidate in context packing.
type PackedItem struct {
	Path       string `json:"path"`
	TokensUsed int    `json:"tokens_used"`
	Tier       string `json:"tier"` // measured | counterfactual | unknown
}

// PackingStage records packing decisions and budget utilization.
type PackingStage struct {
	TotalBudget       int          `json:"total_budget"`
	BudgetRemaining   int          `json:"budget_remaining"`
	IncludedItems     []PackedItem `json:"included_items"`
	TruncatedManifest []string     `json:"truncated_manifest"`
}

// TransformDetail records AST skeletonization or compaction applied to an item.
type TransformDetail struct {
	Path        string `json:"path"`
	Action      string `json:"action"` // full_content | skeleton | declaration_span
	ExpandHash  string `json:"expand_hash,omitempty"`
	LinesElided int    `json:"lines_elided,omitempty"`
}

// PolicyStage records policy actions taken during assembly.
type PolicyStage struct {
	PolicyVersion   string `json:"policy_version"`
	RedactionsCount int    `json:"redactions_count"`
	DenialsCount    int    `json:"denials_count"`
}

// Trace represents an always-on 6-stage context assembly diagnostic trace.
type Trace struct {
	ID             string            `json:"id"`
	Workspace      string            `json:"workspace"`
	CreatedAt      time.Time         `json:"created_at"`
	Query          string            `json:"query"`
	Config         TraceConfig       `json:"config"`
	Discovery      []CandidateTrace  `json:"discovery"`
	Filtering      []FilterDecision  `json:"filtering"`
	Ranking        []RankedCandidate `json:"ranking"`
	Packing        PackingStage      `json:"packing"`
	Transformation []TransformDetail `json:"transformation"`
	Policy         PolicyStage       `json:"policy"`
}

// CandidateExplanation provides an epistemologically grounded reason why a candidate was omitted or included.
type CandidateExplanation struct {
	CandidatePath string `json:"candidate_path"`
	Stage         string `json:"stage"`
	Reason        string `json:"reason"`
	Rank          int    `json:"rank,omitempty"`
	Tier          string `json:"tier"` // measured | counterfactual
}

// ReplayResult holds the counterfactual packing outcome of replaying candidates under new parameters.
type ReplayResult struct {
	Budget            int          `json:"budget"`
	TokensUsed        int          `json:"tokens_used"`
	IncludedItems     []PackedItem `json:"included_items"`
	TruncatedManifest []string     `json:"truncated_manifest"`
}

// Engine manages context assembly trace recording, replay, and omission explanation.
type Engine struct {
	store  *store.Store
	policy *dlp.PolicyEngine
}

// NewEngine creates a new inspector engine.
func NewEngine(s *store.Store, policy *dlp.PolicyEngine) *Engine {
	return &Engine{
		store:  s,
		policy: policy,
	}
}

// RecordTrace serializes and persists a 6-stage context trace into the Content-Hash Store.
func (e *Engine) RecordTrace(tr *Trace) error {
	if e.store == nil {
		return nil
	}

	configBytes, err := json.Marshal(tr.Config)
	if err != nil {
		return err
	}

	type tracePayload struct {
		Discovery      []CandidateTrace  `json:"discovery"`
		Filtering      []FilterDecision  `json:"filtering"`
		Ranking        []RankedCandidate `json:"ranking"`
		Packing        PackingStage      `json:"packing"`
		Transformation []TransformDetail `json:"transformation"`
		Policy         PolicyStage       `json:"policy"`
	}

	payload := tracePayload{
		Discovery:      tr.Discovery,
		Filtering:      tr.Filtering,
		Ranking:        tr.Ranking,
		Packing:        tr.Packing,
		Transformation: tr.Transformation,
		Policy:         tr.Policy,
	}

	traceBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	dbTrace := &store.ContextTrace{
		ID:         tr.ID,
		Workspace:  tr.Workspace,
		CreatedAt:  tr.CreatedAt,
		Query:      tr.Query,
		Budget:     tr.Config.Budget,
		ConfigJSON: string(configBytes),
		TraceJSON:  string(traceBytes),
	}

	return e.store.PutContextTrace(dbTrace)
}

// GetTrace loads a context trace by ID and deserializes all 6 stages.
func (e *Engine) GetTrace(id, workspace string) (*Trace, error) {
	if e.store == nil {
		return nil, fmt.Errorf("no store available")
	}

	dbTrace, err := e.store.GetContextTrace(id, workspace)
	if err != nil {
		return nil, err
	}

	var cfg TraceConfig
	_ = json.Unmarshal([]byte(dbTrace.ConfigJSON), &cfg)

	type tracePayload struct {
		Discovery      []CandidateTrace  `json:"discovery"`
		Filtering      []FilterDecision  `json:"filtering"`
		Ranking        []RankedCandidate `json:"ranking"`
		Packing        PackingStage      `json:"packing"`
		Transformation []TransformDetail `json:"transformation"`
		Policy         PolicyStage       `json:"policy"`
	}

	var p tracePayload
	if err := json.Unmarshal([]byte(dbTrace.TraceJSON), &p); err != nil {
		return nil, fmt.Errorf("corrupt trace JSON: %w", err)
	}

	return &Trace{
		ID:             dbTrace.ID,
		Workspace:      dbTrace.Workspace,
		CreatedAt:      dbTrace.CreatedAt,
		Query:          dbTrace.Query,
		Config:         cfg,
		Discovery:      p.Discovery,
		Filtering:      p.Filtering,
		Ranking:        p.Ranking,
		Packing:        p.Packing,
		Transformation: p.Transformation,
		Policy:         p.Policy,
	}, nil
}

// ExplainCandidate determines the exact stage and reason why a candidate was omitted.
func (e *Engine) ExplainCandidate(traceID, workspace, candidatePath string) (*CandidateExplanation, error) {
	tr, err := e.GetTrace(traceID, workspace)
	if err != nil {
		return nil, err
	}

	// Check filtering stage
	for _, f := range tr.Filtering {
		if f.Path == candidatePath {
			if !f.Passed {
				return &CandidateExplanation{
					CandidatePath: candidatePath,
					Stage:         f.Stage,
					Reason:        f.Reason,
					Tier:          TierMeasured,
				}, nil
			}
			break
		}
	}

	// Check packing stage
	for _, inc := range tr.Packing.IncludedItems {
		if inc.Path == candidatePath {
			return &CandidateExplanation{
				CandidatePath: candidatePath,
				Stage:         "included",
				Reason:        fmt.Sprintf("Candidate included consuming %d tokens", inc.TokensUsed),
				Tier:          TierMeasured,
			}, nil
		}
	}

	rank := 0
	for _, r := range tr.Ranking {
		if r.Path == candidatePath {
			rank = r.Rank
			break
		}
	}

	for _, trunc := range tr.Packing.TruncatedManifest {
		if trunc == candidatePath {
			return &CandidateExplanation{
				CandidatePath: candidatePath,
				Stage:         "packing",
				Reason:        fmt.Sprintf("Candidate truncated because budget (%d) was exhausted", tr.Config.Budget),
				Rank:          rank,
				Tier:          TierMeasured,
			}, nil
		}
	}

	return &CandidateExplanation{
		CandidatePath: candidatePath,
		Stage:         "discovery",
		Reason:        "Candidate was not surfaced during discovery for query",
		Tier:          TierMeasured,
	}, nil
}

// Replay re-executes ranking and packing against saved candidates under a different budget.
// Completely offline: zero filesystem and zero LLM calls.
func (e *Engine) Replay(traceID, workspace string, newBudget int) (*ReplayResult, error) {
	tr, err := e.GetTrace(traceID, workspace)
	if err != nil {
		return nil, err
	}

	tokenMap := make(map[string]int)
	for _, d := range tr.Discovery {
		tokenMap[d.Path] = d.Tokens
	}

	// Candidates that passed filtering
	var eligible []RankedCandidate
	filteredOut := make(map[string]bool)
	for _, f := range tr.Filtering {
		if !f.Passed {
			filteredOut[f.Path] = true
		}
	}

	for _, r := range tr.Ranking {
		if !filteredOut[r.Path] {
			eligible = append(eligible, r)
		}
	}

	// Sort by rank ASC
	sort.SliceStable(eligible, func(i, j int) bool {
		return eligible[i].Rank < eligible[j].Rank
	})

	res := &ReplayResult{
		Budget: newBudget,
	}

	used := 0
	for _, cand := range eligible {
		tokens := tokenMap[cand.Path]
		if tokens == 0 {
			tokens = 50
		}

		if used+tokens <= newBudget {
			res.IncludedItems = append(res.IncludedItems, PackedItem{
				Path:       cand.Path,
				TokensUsed: tokens,
				Tier:       TierCounterfactual,
			})
			used += tokens
		} else {
			res.TruncatedManifest = append(res.TruncatedManifest, cand.Path)
		}
	}

	res.TokensUsed = used
	return res, nil
}

// GetOutcome retrieves external evaluation outcome data for a trace ID.
func (e *Engine) GetOutcome(traceID string) (map[string]any, error) {
	if e.store == nil {
		return nil, nil
	}
	jsonStr, err := e.store.GetTraceOutcome(traceID)
	if err != nil || jsonStr == "" {
		return nil, nil
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// ExportTraceMarkdown generates an agent-readable Markdown explanation while strictly enforcing privacy policy at export.
func (e *Engine) ExportTraceMarkdown(traceID, workspace string) (string, error) {
	tr, err := e.GetTrace(traceID, workspace)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Context Trace: `%s` [%s]\n\n", tr.ID, TierMeasured))
	sb.WriteString(fmt.Sprintf("- **Workspace:** `%s`\n", tr.Workspace))
	sb.WriteString(fmt.Sprintf("- **Query:** %q\n", tr.Query))
	sb.WriteString(fmt.Sprintf("- **Budget:** %d tokens\n", tr.Config.Budget))
	sb.WriteString(fmt.Sprintf("- **Created:** %s\n\n", tr.CreatedAt.Format(time.RFC3339)))

	// Check external harness outcomes
	outcome, _ := e.GetOutcome(traceID)
	if len(outcome) > 0 {
		sb.WriteString("## 🎯 External Evaluation Outcomes\n")
		for k, v := range outcome {
			sb.WriteString(fmt.Sprintf("- **%s:** `%v`\n", k, v))
		}
		sb.WriteString("\n")
	}

	// 1. Included items
	sb.WriteString(fmt.Sprintf("## Included Items (%d items, %d tokens used)\n\n", len(tr.Packing.IncludedItems), tr.Config.Budget-tr.Packing.BudgetRemaining))
	for i, item := range tr.Packing.IncludedItems {
		if e.policy != nil {
			eval := e.policy.EvaluatePath(item.Path)
			if !eval.Allowed {
				continue // Privacy silent omission at export
			}
		}
		sb.WriteString(fmt.Sprintf("%d. `%s` (%d tokens) [%s]\n", i+1, item.Path, item.TokensUsed, item.Tier))
	}
	sb.WriteString("\n")

	// 2. Truncated items
	if len(tr.Packing.TruncatedManifest) > 0 {
		sb.WriteString("## Truncated Candidates (Budget Exhausted)\n\n")
		for _, path := range tr.Packing.TruncatedManifest {
			if e.policy != nil {
				eval := e.policy.EvaluatePath(path)
				if !eval.Allowed {
					continue
				}
			}
			sb.WriteString(fmt.Sprintf("- `%s` [%s]\n", path, TierMeasured))
		}
		sb.WriteString("\n")
	}

	// 3. Filtered items
	var rejections []FilterDecision
	for _, f := range tr.Filtering {
		if !f.Passed {
			if e.policy != nil {
				eval := e.policy.EvaluatePath(f.Path)
				if !eval.Allowed {
					continue
				}
			}
			rejections = append(rejections, f)
		}
	}

	if len(rejections) > 0 {
		sb.WriteString("## Excluded Candidates\n\n")
		for _, r := range rejections {
			sb.WriteString(fmt.Sprintf("- `%s`: excluded during **%s** (%s) [%s]\n", r.Path, r.Stage, r.Reason, TierMeasured))
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}
