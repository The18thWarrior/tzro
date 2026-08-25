package strategy

import (
	"context"

	"tzro/internal/compiler"
)

// ---------------------------------------------------------------------------
// BaseStrategy — shared skeleton for built-in strategies
// ---------------------------------------------------------------------------

// BaseStrategy provides default implementations for NodeStrategy methods
// that most built-in strategies share. Concrete strategies embed this and
// override Execute (and optionally Type for polymorphic strategies).
type BaseStrategy struct {
	NodeType   string
	Card       *PlannerCard
	Rules      *CompilationRules
	Role       *ContextRole
	ThoughtCfg *EdgeThoughtConfig
}

func (s *BaseStrategy) Type() string { return s.NodeType }

func (s *BaseStrategy) StagePlan(node *compiler.GraphNode) *StagePlanDef { return nil }

// Execute is the default implementation for strategies that haven't been
// extracted into their own concrete types. Returns DirectiveHalt to signal
// that dispatch should not proceed.
func (s *BaseStrategy) Execute(ctx context.Context, nr *NodeRuntime) (*ExecutionResult, error) {
	return &ExecutionResult{
		Output:    "strategy not yet implemented",
		Directive: DirectiveHalt,
	}, nil
}

func (s *BaseStrategy) EdgeThoughtPolicy() *EdgeThoughtConfig { return s.ThoughtCfg }

func (s *BaseStrategy) PlannerCard() *PlannerCard { return s.Card }

func (s *BaseStrategy) CompilationRules() *CompilationRules { return s.Rules }

func (s *BaseStrategy) ContextRole() *ContextRole {
	if s.Role != nil {
		return s.Role
	}
	return &ContextRole{}
}

// ---------------------------------------------------------------------------
// Built-in strategy stubs — PlannerCard + ContextRole metadata only
// ---------------------------------------------------------------------------

// NewProbeStrategy creates the probe strategy stub.
func NewProbeStrategy() *BaseStrategy {
	return &BaseStrategy{
		NodeType: "probe",
		Card: &PlannerCard{
			Type:      "probe",
			WhenToUse: "Open-ended exploration of codebases, logs, or docs. Autonomous Thought Chain.",
			KeyFields: []FieldDesc{
				{Name: "probeConfig.goal", Description: "exploration objective", Required: true},
				{Name: "probeConfig.allowedTools", Description: "tool whitelist", Required: true},
				{Name: "probeConfig.stepBudget", Description: "max steps (default 20)", Required: false},
				{Name: "probeConfig.sourceHint", Description: "web|filesystem|cache", Required: false},
			},
			CriticalRules: []string{
				"For open-ended exploration, ALWAYS use probe. Never chain multiple action nodes for what a probe can explore autonomously.",
				"Set probeConfig.sourceHint='web' for internet research. Default is 'filesystem'.",
				"Add git_log, git_diff, git_show to allowedTools when the goal involves commit history, code changes, regressions, or evolution.",
			},
		},
		Role: &ContextRole{
			IsPrimaryDataCarrier: false,
			HasThoughtSteps:      true,
			ContextWeight:        0.5, // int(0.5*4)=2, matches hardcoded typeWeights["probe"]=2
			ProducesPlainText:    true,
		},
	}
}

// NewAnalyzeStrategy creates the analyze strategy stub.
func NewAnalyzeStrategy() *BaseStrategy {
	return &BaseStrategy{
		NodeType: "analyze",
		Card: &PlannerCard{
			Type:      "analyze",
			WhenToUse: "Data analysis via cached tabular data (SQL queries, aggregation).",
			KeyFields: []FieldDesc{
				{Name: "probeConfig.goal", Description: "analysis objective", Required: true},
				{Name: "probeConfig.sourceHint", Description: "must be 'cache'", Required: true},
				{Name: "probeConfig.allowedTools", Description: "cache + SQL tools", Required: true},
			},
		},
		Role: &ContextRole{
			IsPrimaryDataCarrier: false,
			HasThoughtSteps:      true,
			ContextWeight:        1.0, // int(1.0*4)=4, matches legacy defaultWeight (analyze not in typeWeights map)
			ProducesPlainText:    true,
		},
	}
}

// NewRecallStrategy creates the recall strategy stub.
func NewRecallStrategy() *BaseStrategy {
	return &BaseStrategy{
		NodeType: "recall",
		Card: &PlannerCard{
			Type:      "recall",
			WhenToUse: "Align upstream probe findings with the original task requirements.",
			KeyFields: []FieldDesc{
				{Name: "instructions", Description: "alignment objective", Required: true},
			},
		},
		Role: &ContextRole{
			IsPrimaryDataCarrier: true,
			HasThoughtSteps:      false,
			ContextWeight:        2.0,
			ProducesPlainText:    true,
		},
	}
}

// NewSynthesisStrategy creates the synthesis strategy stub.
func NewSynthesisStrategy() *BaseStrategy {
	return &BaseStrategy{
		NodeType: "synthesis",
		Card: &PlannerCard{
			Type:      "synthesis",
			WhenToUse: "Final consolidation of all upstream outputs into a single response.",
			KeyFields: []FieldDesc{
				{Name: "instructions", Description: "synthesis goal", Required: true},
			},
		},
		Role: &ContextRole{
			IsPrimaryDataCarrier: false,
			HasThoughtSteps:      false,
			ContextWeight:        1.0,
			ProducesPlainText:    true,
		},
	}
}

// NewSemanticValidatorStrategy creates the semantic_validator strategy stub.
func NewSemanticValidatorStrategy() *BaseStrategy {
	return &BaseStrategy{
		NodeType: "semantic_validator",
		Card: &PlannerCard{
			Type:      "semantic_validator",
			WhenToUse: "Parameter extraction bridge for action tool calls.",
			KeyFields: []FieldDesc{
				{Name: "action", Description: "target tool name", Required: true},
				{Name: "instructions", Description: "parameter extraction context", Required: true},
			},
		},
		Role: &ContextRole{
			IsPrimaryDataCarrier: false,
			HasThoughtSteps:      false,
			ContextWeight:        1.0, // int(1.0*4)=4, default weight (validators are action-typed in SCT)
			ProducesPlainText:    false,
		},
	}
}

// NewActionStrategy creates the action strategy stub.
func NewActionStrategy() *BaseStrategy {
	return &BaseStrategy{
		NodeType: "action",
		Card: &PlannerCard{
			Type:      "action",
			WhenToUse: "Execute a single known tool with extracted parameters.",
			KeyFields: []FieldDesc{
				{Name: "action", Description: "tool name to execute", Required: true},
				{Name: "staticArgs", Description: "pre-known arguments JSON", Required: false},
			},
		},
		Role: &ContextRole{
			IsPrimaryDataCarrier: false,
			HasThoughtSteps:      false,
			ContextWeight:        1.5, // int(1.5*4)=6, matches hardcoded typeWeights["action"]=6
			ProducesPlainText:    false,
		},
	}
}

// NewBranchStrategy creates the branch strategy stub.
func NewBranchStrategy() *BaseStrategy {
	return &BaseStrategy{
		NodeType: "branch",
		Card: &PlannerCard{
			Type:      "branch",
			WhenToUse: "Conditional execution — skip downstream if condition not met.",
			KeyFields: []FieldDesc{
				{Name: "condition", Description: "evaluation expression", Required: true},
				{Name: "defaultTarget", Description: "fallback node ID", Required: false},
			},
		},
		Role: &ContextRole{
			IsPrimaryDataCarrier: false,
			HasThoughtSteps:      false,
			ContextWeight:        1.0, // int(1.0*4)=4, matches legacy defaultWeight (branches rarely produce output)
			ProducesPlainText:    false,
		},
	}
}

// NewSubDAGStrategy creates the sub_dag strategy stub.
func NewSubDAGStrategy() *BaseStrategy {
	return &BaseStrategy{
		NodeType: "sub_dag",
		Card: &PlannerCard{
			Type:      "sub_dag",
			WhenToUse: "Invoke a pre-built macro node template (codebase_explorer, web_researcher, etc.).",
			KeyFields: []FieldDesc{
				{Name: "action", Description: "template name", Required: true},
				{Name: "inputs", Description: "template parameters", Required: false},
			},
		},
		Role: &ContextRole{
			IsPrimaryDataCarrier: false,
			HasThoughtSteps:      false,
			ContextWeight:        1.0,
			ProducesPlainText:    true,
		},
	}
}

// NewScatterAssemblyStrategy creates the scatter_assembly strategy stub.
func NewScatterAssemblyStrategy() *BaseStrategy {
	return &BaseStrategy{
		NodeType: "scatter_assembly",
		Card:     nil, // Not directly emitted by planner — internal type
		Role: &ContextRole{
			IsPrimaryDataCarrier: false,
			HasThoughtSteps:      false,
			ContextWeight:        1.0, // int(1.0*4)=4, default weight
			ProducesPlainText:    false,
		},
	}
}

// NewDeterministicStrategy creates the deterministic strategy stub.
// This is the legacy "deterministic" node type for direct tool dispatch.
func NewDeterministicStrategy() *BaseStrategy {
	return &BaseStrategy{
		NodeType: "deterministic",
		Card:     nil, // Not directly emitted by planner — internal/legacy type
		Role: &ContextRole{
			IsPrimaryDataCarrier: false,
			HasThoughtSteps:      false,
			ContextWeight:        0.25, // int(0.25*4)=1, matches hardcoded typeWeights["deterministic"]=1
			ProducesPlainText:    false,
		},
	}
}

// NewListStrategy creates the list strategy stub (ADR-0090).
func NewListStrategy() *BaseStrategy {
	return &BaseStrategy{
		NodeType: "list",
		Card: &PlannerCard{
			Type:      "list",
			WhenToUse: "Extraction and enumeration tasks: list symbols, catalog endpoints, index declarations. Produces verbatim source snippets without synthesis.",
			KeyFields: []FieldDesc{
				{Name: "probeConfig.goal", Description: "extraction objective — what to find", Required: true},
				{Name: "probeConfig.preloadPaths", Description: "target directories to scan", Required: false},
			},
			CriticalRules: []string{
				"Use 'list' for extraction tasks where source fidelity matters. Use 'probe' when understanding/synthesis is needed.",
				"The model identifies relevant line ranges; the harness copies content verbatim.",
			},
		},
		Role: &ContextRole{
			IsPrimaryDataCarrier: true,
			HasThoughtSteps:      false,
			ContextWeight:        0.3,
			ProducesPlainText:    true,
		},
	}
}

// ---------------------------------------------------------------------------
// Registration — bulk register all built-in strategies
// ---------------------------------------------------------------------------

// RegisterBuiltins registers all built-in strategies into the registry.
// Called by the executor at startup. Each strategy starts as a metadata-only
// BaseStrategy, then gets replaced with a concrete strategy implementation
// by the executor's wireStrategies method.
func RegisterBuiltins(r *StrategyRegistry) error {
	builtins := []NodeStrategy{
		NewProbeStrategy(),
		NewAnalyzeStrategy(),
		NewRecallStrategy(),
		NewSynthesisStrategy(),
		NewSemanticValidatorStrategy(),
		NewActionStrategy(),
		NewBranchStrategy(),
		NewSubDAGStrategy(),
		NewScatterAssemblyStrategy(),
		NewDeterministicStrategy(),
		NewListStrategy(),
	}

	for _, s := range builtins {
		if err := r.Register(s); err != nil {
			return err
		}
	}

	return nil
}
