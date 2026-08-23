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

// TemplateCategory identifies a structural graph shape (Topology Archetype) in the registry (ADR-0087).
type TemplateCategory string

const (
	ProbeSynthesis      TemplateCategory = "probe-synthesis"
	ProbeAndWrite       TemplateCategory = "probe-and-write"
	MultiProbeSynthesis TemplateCategory = "multi-probe-synthesis"
	Codegen             TemplateCategory = "codegen"
	DataAnalysis        TemplateCategory = "data-analysis"
	ActionChain         TemplateCategory = "action-chain"

	// Legacy category aliases for backward compatibility with ADR-0048
	ExploreOnly TemplateCategory = "probe-synthesis"
	Docgen      TemplateCategory = "probe-and-write"
	Research    TemplateCategory = "probe-synthesis"
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
// auto-injects Recall Nodes, semantic validators, and synthesis nodes.
var registry = map[TemplateCategory]*compiler.ExecutionGraph{
	ProbeSynthesis: {
		MaxCycles: 5,
		Nodes: []compiler.GraphNode{
			{
				ID:           "explore",
				Type:         "probe",
				Instructions: "Explore the target and produce a comprehensive analysis.",
				AllowedTools: []string{"read_file", "list_dir", "search_files"},
				Status:       "pending",
				ProbeConfig: &compiler.ProbeConfig{
					Goal:         "Explore the target and produce a comprehensive analysis.",
					AllowedTools: []string{"read_file", "list_dir", "search_files"},
					StepBudget:   20,
					CompactEvery: 3,
				},
			},
		},
		Edges: []compiler.GraphEdge{},
	},

	ProbeAndWrite: {
		MaxCycles: 5,
		Nodes: []compiler.GraphNode{
			{
				ID:           "explore",
				Type:         "probe",
				Instructions: "Explore the target and produce content for documentation or output.",
				AllowedTools: []string{"read_file", "list_dir", "search_files"},
				Status:       "pending",
				ProbeConfig: &compiler.ProbeConfig{
					Goal:         "Explore the target and produce content for documentation or output.",
					AllowedTools: []string{"read_file", "list_dir", "search_files"},
					StepBudget:   20,
					CompactEvery: 3,
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

	MultiProbeSynthesis: {
		MaxCycles: 5,
		Nodes: []compiler.GraphNode{
			{
				ID:           "probe_1",
				Type:         "probe",
				Instructions: "Explore the first source.",
				AllowedTools: []string{"read_file", "list_dir", "search_files"},
				Status:       "pending",
				ProbeConfig: &compiler.ProbeConfig{
					Goal:         "Explore the first source.",
					AllowedTools: []string{"read_file", "list_dir", "search_files"},
					StepBudget:   15,
					CompactEvery: 3,
				},
			},
			{
				ID:           "probe_2",
				Type:         "probe",
				Instructions: "Explore the second source.",
				AllowedTools: []string{"read_file", "list_dir", "search_files"},
				Status:       "pending",
				ProbeConfig: &compiler.ProbeConfig{
					Goal:         "Explore the second source.",
					AllowedTools: []string{"read_file", "list_dir", "search_files"},
					StepBudget:   15,
					CompactEvery: 3,
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
				Type:         "probe",
				Instructions: "Explore the codebase to gather context for code generation.",
				AllowedTools: []string{"read_file", "list_dir", "search_files"},
				Status:       "pending",
				ProbeConfig: &compiler.ProbeConfig{
					Goal:         "Explore the codebase to gather context for code generation.",
					AllowedTools: []string{"read_file", "list_dir", "search_files"},
					StepBudget:   15,
					CompactEvery: 3,
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


// GetWithModality returns a deep copy of the template hydrated with tools and source hint
// appropriate for the given SourceModality (ADR-0087).
func GetWithModality(category TemplateCategory, modality SourceModality) *compiler.ExecutionGraph {
	// Normalize legacy category strings
	switch string(category) {
	case "explore-only":
		category = ProbeSynthesis
		if modality == "" {
			modality = SourceLocal
		}
	case "docgen":
		category = ProbeAndWrite
		if modality == "" {
			modality = SourceLocal
		}
	case "research":
		category = ProbeSynthesis
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

	// Hydrate probe nodes with modality-appropriate tools and source hints
	var probeTools []string
	var hint string
	switch modality {
	case SourceWeb:
		probeTools = []string{"web_search", "web_browse"}
		hint = "web"
	case SourceHybrid:
		probeTools = []string{"read_file", "list_dir", "search_files", "web_search", "web_browse"}
		hint = "hybrid"
	case SourceLocal:
		probeTools = []string{"read_file", "list_dir", "search_files"}
		hint = "filesystem"
	default:
		probeTools = []string{"read_file", "list_dir", "search_files"}
		hint = "filesystem"
	}

	for i := range g.Nodes {
		if g.Nodes[i].Type == "probe" {
			g.Nodes[i].AllowedTools = probeTools
			if g.Nodes[i].ProbeConfig != nil {
				g.Nodes[i].ProbeConfig.AllowedTools = probeTools
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
