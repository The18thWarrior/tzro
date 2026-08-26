// Package templates provides the Plan Template Registry — a set of named,
// structural graph shapes selected via GBNF classification for local-routed
// tasks (ADR-0048). Each template is a valid Abstract Graph that the local
// model mutates for a specific task rather than generating from scratch.
package templates

import (
	"encoding/json"
	"sort"

	"tzro/internal/compiler"
)

// TemplateCategory identifies a structural graph shape (Topology Archetype) in the registry (ADR-0087, ADR-0091).
type TemplateCategory string

const (
	ListSynthesis      TemplateCategory = "list-synthesis"
	ListAndWrite       TemplateCategory = "list-and-write"
	MultiListSynthesis TemplateCategory = "multi-list-synthesis"
	Codegen            TemplateCategory = "codegen"
	DataAnalysis       TemplateCategory = "data-analysis"
	ActionChain        TemplateCategory = "action-chain"

	// Legacy category aliases for backward compatibility (ADR-0048, ADR-0091)
	ProbeSynthesis      = ListSynthesis
	ProbeAndWrite       = ListAndWrite
	MultiProbeSynthesis = MultiListSynthesis
	ExploreOnly         = ListSynthesis
	Docgen              = ListAndWrite
	Research            = ListSynthesis
)

// SourceModality identifies the tool inventory and data context domain for exploration (ADR-0087).
type SourceModality string

const (
	SourceLocal  SourceModality = "local"
	SourceWeb    SourceModality = "web"
	SourceHybrid SourceModality = "hybrid"
)

// registry holds the canonical template for each Topology Archetype.
// Templates are Abstract Graphs (pre-compilation) — the Kahn Compiler
// auto-injects Recall Nodes (on budget overflow) and synthesis nodes.
var registry = map[TemplateCategory]*compiler.ExecutionGraph{
	ListSynthesis: {
		MaxCycles: 5,
		Nodes: []compiler.GraphNode{
			{
				ID:           "explore",
				Type:         "list",
				Instructions: "Extract relevant content from the target for comprehensive analysis.",
				Status:       "pending",
				ProbeConfig: &compiler.ProbeConfig{
					Goal: "Extract relevant content from the target for comprehensive analysis.",
				},
			},
		},
		Edges: []compiler.GraphEdge{},
	},

	ListAndWrite: {
		MaxCycles: 5,
		Nodes: []compiler.GraphNode{
			{
				ID:           "explore",
				Type:         "list",
				Instructions: "Extract relevant content from the target for documentation or output.",
				Status:       "pending",
				ProbeConfig: &compiler.ProbeConfig{
					Goal: "Extract relevant content from the target for documentation or output.",
				},
			},
			{
				ID:           "write_output",
				Type:         "action",
				Action:       "write_file",
				Instructions: "Write the output to the target file.",
				AllowedTools: []string{"write_file"},
				Status:       "pending",
			},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "explore", TargetID: "write_output"},
		},
	},



	DataAnalysis: {
		MaxCycles: 5,
		Nodes: []compiler.GraphNode{
			{
				ID:           "read_data",
				Type:         "action",
				Action:       "read_file",
				Instructions: "Read the data file for analysis.",
				AllowedTools: []string{"read_file"},
				Status:       "pending",
			},
			{
				ID:           "analyze",
				Type:         "analyze",
				Instructions: "Analyze the data from the upstream read operation.",
				Status:       "pending",
			},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "read_data", TargetID: "analyze"},
		},
	},

	MultiListSynthesis: {
		MaxCycles: 5,
		Nodes: []compiler.GraphNode{
			{
				ID:           "list_1",
				Type:         "list",
				Instructions: "Extract relevant content from the first source.",
				Status:       "pending",
				ProbeConfig: &compiler.ProbeConfig{
					Goal: "Extract relevant content from the first source.",
				},
			},
			{
				ID:           "list_2",
				Type:         "list",
				Instructions: "Extract relevant content from the second source.",
				Status:       "pending",
				ProbeConfig: &compiler.ProbeConfig{
					Goal: "Extract relevant content from the second source.",
				},
			},
		},
		Edges: []compiler.GraphEdge{},
	},

	Codegen: {
		MaxCycles: 5,
		Nodes: []compiler.GraphNode{
			{
				ID:           "explore_context",
				Type:         "list",
				Instructions: "Extract codebase context for code generation.",
				Status:       "pending",
				ProbeConfig: &compiler.ProbeConfig{
					Goal: "Extract codebase context for code generation.",
				},
			},
			{
				ID:           "generate_code",
				Type:         "action",
				Action:       "tzro_code",
				Instructions: "Generate the requested code using context from exploration.",
				AllowedTools: []string{"tzro_code"},
				Status:       "pending",
			},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "explore_context", TargetID: "generate_code"},
		},
	},

	ActionChain: {
		MaxCycles: 5,
		Nodes: []compiler.GraphNode{
			{
				ID:           "step_1",
				Type:         "action",
				Action:       "",
				Instructions: "Execute the first step of the workflow.",
				AllowedTools: []string{},
				Status:       "pending",
			},
			{
				ID:           "step_2",
				Type:         "action",
				Action:       "",
				Instructions: "Execute the second step of the workflow.",
				AllowedTools: []string{},
				Status:       "pending",
				DynamicBindings: map[string]interface{}{},
			},
			{
				ID:           "step_3",
				Type:         "action",
				Action:       "",
				Instructions: "Execute the third step of the workflow.",
				AllowedTools: []string{},
				Status:       "pending",
				DynamicBindings: map[string]interface{}{},
			},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "step_1", TargetID: "step_2"},
			{SourceID: "step_2", TargetID: "step_3"},
		},
	},
}


// GetWithModality returns a deep copy of the template hydrated with source hint
// appropriate for the given SourceModality (ADR-0087, ADR-0091).
func GetWithModality(category TemplateCategory, modality SourceModality) *compiler.ExecutionGraph {
	// Normalize legacy category strings
	switch string(category) {
	case "explore-only":
		category = ListSynthesis
		if modality == "" {
			modality = SourceLocal
		}
	case "docgen":
		category = ListAndWrite
		if modality == "" {
			modality = SourceLocal
		}
	case "research":
		category = ListSynthesis
		if modality == "" {
			modality = SourceWeb
		}
	}

	if modality == "" {
		modality = SourceLocal
	}

	tmpl, ok := registry[category]
	if !ok {
		return nil
	}
	g := deepCopy(tmpl)
	if g == nil {
		return nil
	}
	g.SourceModality = string(modality)

	// Hydrate list nodes with modality-appropriate source hints
	var hint string
	switch modality {
	case SourceWeb:
		hint = "web"
	case SourceHybrid:
		hint = "hybrid"
	case SourceLocal:
		hint = "filesystem"
	default:
		hint = "filesystem"
	}

	for i := range g.Nodes {
		if g.Nodes[i].Type == "list" {
			if g.Nodes[i].ProbeConfig != nil {
				g.Nodes[i].ProbeConfig.SourceHint = hint
			}
		}
	}

	return g
}

// Get returns a deep copy of the template for the given category.
// Returns nil if the category is not registered.
func Get(category TemplateCategory) *compiler.ExecutionGraph {
	if string(category) == "research" {
		return GetWithModality(category, SourceWeb)
	}
	return GetWithModality(category, SourceLocal)
}

// Categories returns all registered template category strings, sorted.
func Categories() []TemplateCategory {
	cats := make([]TemplateCategory, 0, len(registry))
	for k := range registry {
		cats = append(cats, k)
	}
	sort.Slice(cats, func(i, j int) bool { return cats[i] < cats[j] })
	return cats
}

// deepCopy serializes and deserializes the graph to produce an independent copy.
// This prevents callers from mutating the canonical registry templates.
func deepCopy(g *compiler.ExecutionGraph) *compiler.ExecutionGraph {
	data, err := json.Marshal(g)
	if err != nil {
		return nil
	}
	var copy compiler.ExecutionGraph
	if err := json.Unmarshal(data, &copy); err != nil {
		return nil
	}
	return &copy
}
