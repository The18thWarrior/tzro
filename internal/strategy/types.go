// Package strategy defines the pluggable Node Strategy interfaces for the
// composable framework abstraction (ADR-0069). Every node type — probe,
// analyze, recall, synthesis, semantic_validator, branch, action, sub_dag,
// scatter_assembly — implements NodeStrategy and is registered in the
// StrategyRegistry. The executor dispatches via registry lookup with zero
// hardcoded node type knowledge.
package strategy

import (
	"context"
	"encoding/json"

	"tzro/internal/compiler"
	"tzro/internal/inference"
	"tzro/internal/stream"
)

// ---------------------------------------------------------------------------
// Core interface: NodeStrategy
// ---------------------------------------------------------------------------

// NodeStrategy defines how a node type executes within the DAG executor.
// Each implementation provides its type identifier, execution logic
// (imperative or declarative Stage Plan), Edge Thought policy, planner
// card, compilation rules, and context role.
type NodeStrategy interface {
	// Type returns the canonical string identifier (e.g., "probe", "analyze").
	Type() string

	// StagePlan returns the declarative stage sequence for this node.
	// When non-nil, the executor runs the declared stages in order, wiring
	// data flow via summary accumulation and typed Artifact Store entries.
	// When nil, the executor calls Execute for imperative control flow
	// (e.g., the Thought Chain's reactive loop).
	StagePlan(node *compiler.GraphNode) *StagePlanDef

	// Execute runs the node's logic imperatively. Only called when
	// StagePlan returns nil. Used for reactive loops where the next step
	// depends on the previous step's output.
	Execute(ctx context.Context, nr *NodeRuntime) (*ExecutionResult, error)

	// EdgeThoughtPolicy defines how this strategy participates in Edge
	// Thought evaluation on outgoing edges. Returns nil to opt out of
	// Edge Thought generation entirely.
	EdgeThoughtPolicy() *EdgeThoughtConfig

	// PlannerCard returns a compact description of this node type for
	// injection into the strategic planner's prompt. The Strategy Registry
	// assembles all planner cards into a dynamic NodeTypeReferenceCard.
	PlannerCard() *PlannerCard

	// CompilationRules returns rules the Kahn Compiler applies when this
	// node type appears in a graph. Returns nil to pass the node through
	// the compiler unchanged.
	CompilationRules() *CompilationRules

	// ContextRole defines how this node's output participates in
	// accumulated context budgeting, compaction exemptions, and response
	// resolver splice eligibility.
	ContextRole() *ContextRole
}

// ---------------------------------------------------------------------------
// Stage — composable inner pipeline unit
// ---------------------------------------------------------------------------

// Stage is a composable execution unit within a Node Strategy's Stage Plan.
// Existing Phase Runner phases (Orient, Discover, Deep-Read, Synthesize for
// probes; Schema-Orient, Query-Dev, Compute, Synthesize for analyze) become
// Stage implementations.
type Stage interface {
	// Name returns the stage identifier (e.g., "Orient", "QueryDev").
	Name() string

	// Produces declares the artifact keys this stage may produce.
	// Used for Stage Plan contract validation at registration time.
	Produces() []ArtifactKeyMeta

	// Requires declares the artifact keys this stage needs.
	// Missing artifacts trigger degraded mode, not failure.
	Requires() []ArtifactKeyMeta

	// Run executes the stage. Receives a StageRuntime scoped to the
	// stage's tool list and model target.
	Run(ctx context.Context, sr *StageRuntime) (*StageResult, error)
}

// ArtifactKeyMeta is the untyped metadata for Stage Plan contract validation
// and WASM/external boundary schema checking.
type ArtifactKeyMeta struct {
	Name       string `json:"name"`                 // e.g., "edgeEntries"
	SchemaJSON string `json:"schemaJson,omitempty"` // JSON Schema for WASM boundary validation
}

// ---------------------------------------------------------------------------
// Execution result types
// ---------------------------------------------------------------------------

// FlowDirective tells the executor what to do after a node completes.
type FlowDirective int

const (
	// DirectiveContinue signals normal completion — proceed to downstream nodes.
	DirectiveContinue FlowDirective = iota

	// DirectiveSkipDownstream signals that downstream nodes should be skipped.
	// Used by branch nodes when the condition is not satisfied.
	DirectiveSkipDownstream

	// DirectivePause signals the executor to pause execution and await
	// external input (approval hook, client tool result).
	DirectivePause

	// DirectiveRetry signals the executor to re-execute this node
	// (e.g., after escalation to the cloud model).
	DirectiveRetry

	// DirectiveHalt signals the executor to stop the entire task.
	DirectiveHalt
)

// ExecutionResult is the structured return from a NodeStrategy.Execute call.
type ExecutionResult struct {
	// Output is the node's content output (synthesis text, tool result, etc.)
	Output string

	// Directive tells the executor what to do after this node completes.
	Directive FlowDirective

	// Artifacts contains typed outputs available to downstream strategies.
	Artifacts *ArtifactStore

	// DelegateHandled is set to true by BaseStrategy.Execute when a delegate
	// function managed the full execution lifecycle (state, events, hooks).
	// When true, the dispatch envelope skips state management and hook
	// processing to avoid double-writes. Strategy-owned Execute methods
	// leave this false (default) so the envelope handles ceremony.
	DelegateHandled bool
}


// StageDirective controls flow within a Stage Plan.
type StageDirective int

const (
	// StageContinue proceeds to the next stage.
	StageContinue StageDirective = iota

	// StageSkip skips remaining stages, using the current summary as output.
	StageSkip

	// StageBacktrack re-enters a previous stage with accumulated context.
	StageBacktrack

	// StageHalt aborts the entire Stage Plan.
	StageHalt
)

// StageResult is the return from a Stage.Run call.
type StageResult struct {
	// Summary is the compacted output injected into downstream stages'
	// system prompts via summary accumulation.
	Summary string

	// Artifacts contains typed, named outputs produced by this stage.
	Artifacts *ArtifactStore

	// Directive controls Stage Plan flow.
	Directive StageDirective
}

// ---------------------------------------------------------------------------
// Stage Plan — declarative stage sequence
// ---------------------------------------------------------------------------

// StagePlanDef is a declarative sequence of Stages within a Node Strategy.
type StagePlanDef struct {
	Stages []StageDef
}

// StageDef defines a single stage within a Stage Plan.
type StageDef struct {
	Name         string           // Stage identifier
	Stage        Stage            // The Stage implementation
	AllowedTools []string         // Tool whitelist scoped to this stage
	StepBudget   int              // Max inference steps within this stage
	ModelTarget  string           // "worker", "router", "auto"
	Recovery     RecoveryStrategy // What to do when the stage fails
}

// RecoveryStrategy determines how a stage failure is handled.
type RecoveryStrategy int

const (
	// RecoveryFail aborts the entire Stage Plan on stage failure.
	RecoveryFail RecoveryStrategy = iota

	// RecoveryRetry retries the stage (up to budget).
	RecoveryRetry

	// RecoverySkip skips the failed stage and continues to the next.
	RecoverySkip

	// RecoveryBacktrack re-enters a previous stage with error context.
	RecoveryBacktrack
)

// ---------------------------------------------------------------------------
// Edge Thought integration
// ---------------------------------------------------------------------------

// EdgeThoughtConfig defines how a strategy participates in Edge Thought
// evaluation when the executor traverses an outgoing edge.
type EdgeThoughtConfig struct {
	// EvaluateConfidence is called after the strategy completes. Returns
	// a confidence score (0.0-1.0), reasoning text, and error. The executor
	// uses this to decide spawn/continue/halt on the edge.
	EvaluateConfidence func(ctx context.Context, nr *NodeRuntime, output string) (float64, string, error)

	// SupportsMCTS indicates whether this strategy supports multi-branch
	// evaluation (K candidate actions). If false, single-shot mode only.
	SupportsMCTS bool
}

// ---------------------------------------------------------------------------
// Planner integration
// ---------------------------------------------------------------------------

// PlannerCard is a compact description of a node type for injection into
// the strategic planner's prompt. The Strategy Registry assembles these
// into a dynamic NodeTypeReferenceCard.
type PlannerCard struct {
	// Type is the node type string the planner should emit.
	Type string

	// WhenToUse is a 1-line description of when to use this node type.
	WhenToUse string

	// KeyFields lists the important schema fields for this node type.
	KeyFields []FieldDesc

	// CriticalRules are constraints the planner must follow for this type.
	CriticalRules []string

	// Example is an optional compact example node JSON.
	Example string
}

// FieldDesc describes a schema field for the planner.
type FieldDesc struct {
	Name        string // Field name in the GraphNode JSON
	Description string // What this field controls
	Required    bool   // Whether the planner must set this field
}

// ---------------------------------------------------------------------------
// Compilation rules
// ---------------------------------------------------------------------------

// CompilationRules defines how the Kahn Compiler transforms a node of this
// type during graph expansion. The single Expand function encapsulates all
// type-specific compilation logic (default field injection, sibling node
// injection, pre-compilation analysis).
type CompilationRules struct {
	// Expand transforms the node during compilation. Returns nil to pass
	// the node through unchanged. The function receives the node and the
	// full graph for context-dependent decisions (e.g., checking for
	// existing synthesis children).
	Expand func(node *compiler.GraphNode, graph *compiler.ExecutionGraph) (*ExpansionResult, error)
}

// ExpansionResult describes how the compiler should transform a node.
type ExpansionResult struct {
	// ReplacementNodes replaces the original node entirely (e.g., action →
	// semantic_validator + deterministic pair). When empty, the original
	// node is kept.
	ReplacementNodes []compiler.GraphNode

	// AdditionalNodes are injected alongside the original node (e.g.,
	// probe → recall node). The original node is kept.
	AdditionalNodes []compiler.GraphNode

	// AdditionalEdges are new edges for the injected nodes.
	AdditionalEdges []compiler.GraphEdge

	// ModifiedNode is the original node with defaults or mutations applied
	// (e.g., default ProbeConfig, step budget scaling). When nil, the
	// original node is used unchanged.
	ModifiedNode *compiler.GraphNode
}

// ---------------------------------------------------------------------------
// Context role
// ---------------------------------------------------------------------------

// ContextRole defines how a node type's output participates in accumulated
// context budgeting, compaction exemptions, and response resolver splice
// eligibility. Eliminates all node.Type switching in the context builder
// and response resolver.
type ContextRole struct {
	// IsPrimaryDataCarrier indicates this node's output should never be
	// compacted in accumulated context. True for recall nodes.
	IsPrimaryDataCarrier bool

	// HasThoughtSteps indicates this node type persists thought steps that
	// should be extracted for downstream synthesis enrichment. True for
	// probe and analyze nodes.
	HasThoughtSteps bool

	// ContextWeight is the proportional weight for accumulated context
	// budgeting. Higher weight = more budget share. Probe/recall get
	// higher weight than action/deterministic.
	ContextWeight float64

	// ProducesPlainText indicates this node type produces free-form text
	// (markdown, prose) rather than structured JSON. When true, the response
	// resolver uses the entire output as the resolved value regardless of
	// property key (plain_text_fallback tier). True for probe, recall,
	// synthesis.
	ProducesPlainText bool
}

// ---------------------------------------------------------------------------
// Node Runtime — capability object for strategies
// ---------------------------------------------------------------------------

// NodeRuntime provides capabilities to a NodeStrategy during execution.
// Each capability is a focused interface. Strategies use only what they need.
// Implementations live in internal/executor — strategies never import the
// executor package.
type NodeRuntime struct {
	taskID string
	node   *compiler.GraphNode
	graph  *compiler.ExecutionGraph

	inference InferenceProvider
	tools     ToolDispatcher
	state     StatePersister
	mutator   DAGMutator
	publisher EventPublisher
	config    ConfigProvider
	upstream  UpstreamProvider

	// Executor-supplied execution params (replaces executionParams context pattern)
	executionTier      string
	meta               inference.StreamMeta
	interpolatedPrompt string
}

// TaskID returns the current task identifier.
func (nr *NodeRuntime) TaskID() string { return nr.taskID }

// Node returns the GraphNode being executed.
func (nr *NodeRuntime) Node() *compiler.GraphNode { return nr.node }

// Graph returns the full execution graph.
func (nr *NodeRuntime) Graph() *compiler.ExecutionGraph { return nr.graph }

// Inference returns the inference provider.
func (nr *NodeRuntime) Inference() InferenceProvider { return nr.inference }

// Tools returns the tool dispatcher.
func (nr *NodeRuntime) Tools() ToolDispatcher { return nr.tools }

// State returns the state persister.
func (nr *NodeRuntime) State() StatePersister { return nr.state }

// Mutator returns the DAG mutator.
func (nr *NodeRuntime) Mutator() DAGMutator { return nr.mutator }

// Publisher returns the event publisher.
func (nr *NodeRuntime) Publisher() EventPublisher { return nr.publisher }

// Config returns the configuration provider.
func (nr *NodeRuntime) Config() ConfigProvider { return nr.config }

// Upstream returns the upstream data provider.
func (nr *NodeRuntime) Upstream() UpstreamProvider { return nr.upstream }

// ExecutionTier returns the execution tier ("Local Tactician" or "Cloud Fallback").
func (nr *NodeRuntime) ExecutionTier() string { return nr.executionTier }

// Meta returns the stream metadata for inference calls.
func (nr *NodeRuntime) Meta() inference.StreamMeta { return nr.meta }

// InterpolatedPrompt returns the variable-interpolated node instructions.
func (nr *NodeRuntime) InterpolatedPrompt() string { return nr.interpolatedPrompt }

// NewNodeRuntime constructs a NodeRuntime. Called by the executor to wire
// concrete implementations into the capability interfaces.
func NewNodeRuntime(
	taskID string,
	node *compiler.GraphNode,
	graph *compiler.ExecutionGraph,
	inf InferenceProvider,
	tools ToolDispatcher,
	state StatePersister,
	mutator DAGMutator,
	publisher EventPublisher,
	config ConfigProvider,
	upstream UpstreamProvider,
	executionTier string,
	meta inference.StreamMeta,
	interpolatedPrompt string,
) *NodeRuntime {
	return &NodeRuntime{
		taskID:             taskID,
		node:               node,
		graph:              graph,
		inference:          inf,
		tools:              tools,
		state:              state,
		mutator:            mutator,
		publisher:          publisher,
		config:             config,
		upstream:           upstream,
		executionTier:      executionTier,
		meta:               meta,
		interpolatedPrompt: interpolatedPrompt,
	}
}

// ---------------------------------------------------------------------------
// Capability interfaces
// ---------------------------------------------------------------------------

// InferenceProvider wraps LLM inference calls.
type InferenceProvider interface {
	// CallModel performs a structured inference call with optional JSON
	// schema constraint (GBNF grammar).
	CallModel(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string) (*inference.InferenceResult, error)

	// CallModelStream performs a streaming inference call.
	CallModelStream(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, meta inference.StreamMeta) (*inference.InferenceResult, error)

	// IsCloud returns true if this provider routes to the cloud model.
	IsCloud() bool
}

// ToolDispatcher executes tools with proactivity gating.
type ToolDispatcher interface {
	// Dispatch executes a tool by name with the given arguments.
	Dispatch(ctx context.Context, toolName string, args map[string]interface{}) (string, error)

	// GetSchema returns the JSON schema for a tool.
	GetSchema(toolName string) (string, error)

	// ListAvailable returns tool names available to this node.
	ListAvailable() []string
}

// StatePersister manages node execution state and thought step persistence.
type StatePersister interface {
	// SetNodeState sets the node's status and output.
	SetNodeState(status string, output string) error

	// SetRawOutput sets the node's raw output (uncompacted, for interpolation).
	SetRawOutput(output string) error

	// GetNodeState returns the state of a specific node.
	GetNodeState(nodeID string) (*compiler.NodeState, error)

	// GetAllNodeStates returns all node states for the current task.
	GetAllNodeStates() ([]compiler.NodeState, error)

	// PersistThoughtStep saves a thought step to the database.
	PersistThoughtStep(step *compiler.ThoughtStep) error

	// GetThoughtSteps retrieves all thought steps for a probe.
	GetThoughtSteps(probeID string) ([]compiler.ThoughtStep, error)

	// PersistPhaseResult saves a stage/phase result.
	PersistPhaseResult(phase string, result *StageResult) error

	// GetPhaseResults retrieves all phase results for a node.
	GetPhaseResults(nodeID string) (map[string]*StageResult, error)
}

// DAGMutator allows strategies to modify the parent DAG and spawn child tasks.
type DAGMutator interface {
	// SpawnNode adds a new node to the parent DAG with an edge from the
	// specified source node. Respects the MutationBudget.
	SpawnNode(node compiler.GraphNode, edgeFromNodeID string) error

	// PropagateSkip marks downstream nodes as skipped.
	PropagateSkip(nodeID string) error

	// GetMutationBudget returns the remaining spawn budget.
	GetMutationBudget() *compiler.MutationBudget

	// SpawnChildTask builds and executes a child graph as a new task.
	// Returns the terminal synthesis of the child task. Synchronous —
	// blocks until the child task completes. Respects MaxDepth from
	// MutationBudget to prevent infinite recursion.
	SpawnChildTask(graph *compiler.ExecutionGraph) (string, error)
}

// EventPublisher emits execution events and stream chunks.
type EventPublisher interface {
	// PublishEvent publishes a structured execution event.
	PublishEvent(eventType, taskID, nodeID, content string)

	// PublishStream publishes a stream chunk for TUI consumption.
	PublishStream(chunk stream.StreamChunk)
}

// ConfigProvider exposes execution policy and engine configuration.
type ConfigProvider interface {
	// GetExecutionPolicy returns the active execution policy.
	GetExecutionPolicy() map[string]interface{}

	// GetNodePolicy returns the node-type-specific policy.
	GetNodePolicy(nodeType, action string) map[string]interface{}
}

// UpstreamProvider accesses completed upstream node outputs.
type UpstreamProvider interface {
	// AccumulatedContext returns the formatted upstream context string
	// with compaction budgeting applied.
	AccumulatedContext() string

	// ResolveBinding resolves a dynamic binding path (e.g.,
	// "nodeId.output.propertyName") to a concrete value.
	ResolveBinding(ctx context.Context, bindingPath string) (json.RawMessage, error)

	// GetUpstreamOutput returns the raw output of a specific upstream node.
	GetUpstreamOutput(nodeID string) (string, error)
}

// ---------------------------------------------------------------------------
// Stage Runtime — scoped capability object for stages
// ---------------------------------------------------------------------------

// StageRuntime is a narrower view of NodeRuntime, scoped to a single Stage.
type StageRuntime struct {
	stageName    string
	nodeRuntime  *NodeRuntime
	inference    InferenceProvider // May be routed differently per stage
	allowedTools []string          // Scoped tool whitelist
	stepBudget   int               // Remaining steps for this stage

	// Data flow — convention over configuration
	priorSummaries string        // Concatenated summaries from prior stages
	artifacts      *ArtifactStore // Accumulated artifacts from prior stages
}

// StageName returns the current stage identifier.
func (sr *StageRuntime) StageName() string { return sr.stageName }

// NodeRuntime returns the parent node's runtime.
func (sr *StageRuntime) NodeRuntime() *NodeRuntime { return sr.nodeRuntime }

// Inference returns the stage-scoped inference provider.
func (sr *StageRuntime) Inference() InferenceProvider { return sr.inference }

// AllowedTools returns the stage-scoped tool whitelist.
func (sr *StageRuntime) AllowedTools() []string { return sr.allowedTools }

// StepBudget returns the remaining step budget for this stage.
func (sr *StageRuntime) StepBudget() int { return sr.stepBudget }

// PriorSummaries returns the concatenated summaries from prior stages.
func (sr *StageRuntime) PriorSummaries() string { return sr.priorSummaries }

// Artifacts returns the accumulated artifact store from prior stages.
func (sr *StageRuntime) Artifacts() *ArtifactStore { return sr.artifacts }

// Tools returns the parent node's tool dispatcher (scoped by AllowedTools
// at the executor level).
func (sr *StageRuntime) Tools() ToolDispatcher { return sr.nodeRuntime.tools }

// State returns the parent node's state persister.
func (sr *StageRuntime) State() StatePersister { return sr.nodeRuntime.state }

// Publisher returns the parent node's event publisher.
func (sr *StageRuntime) Publisher() EventPublisher { return sr.nodeRuntime.publisher }

// NewStageRuntime constructs a StageRuntime. Called by the executor's
// runStagePlan function to create stage-scoped context.
func NewStageRuntime(
	stageName string,
	nodeRuntime *NodeRuntime,
	inf InferenceProvider,
	allowedTools []string,
	stepBudget int,
	priorSummaries string,
	artifacts *ArtifactStore,
) *StageRuntime {
	return &StageRuntime{
		stageName:      stageName,
		nodeRuntime:    nodeRuntime,
		inference:      inf,
		allowedTools:   allowedTools,
		stepBudget:     stepBudget,
		priorSummaries: priorSummaries,
		artifacts:      artifacts,
	}
}
