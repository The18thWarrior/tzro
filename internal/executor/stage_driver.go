package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"tzro/internal/compactor"
	"tzro/internal/tools"
)

// MaxPhaseToolLogChars is the hard rolling buffer ceiling for accumulated tool outputs in a phase.
const MaxPhaseToolLogChars = 24000

// compactPhaseToolOutput extracts structural skeletons from code/docs to prevent context blowup.
func compactPhaseToolOutput(toolName string, args map[string]interface{}, output string) string {
	if toolName == "read_file" {
		path, _ := args["path"].(string)
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".go", ".ts", ".js", ".tsx", ".jsx", ".py", ".rs", ".c", ".cpp", ".h", ".hpp", ".java":
			skeleton := compactor.ExtractSkeleton(output, 500)
			if skeleton != "" {
				return skeleton
			}
		case ".md", ".txt", ".markdown":
			lines := strings.Split(output, "\n")
			var docHeaders []string
			for _, line := range lines {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "#") {
					docHeaders = append(docHeaders, trimmed)
				}
			}
			if len(docHeaders) > 0 {
				return strings.Join(docHeaders, "\n")
			}
		}
	}
	return truncate(output, 500)
}

// QueueItem represents a single executable tool action in a deterministic stage queue.
type QueueItem struct {
	Tool string                 `json:"tool"`
	Args map[string]interface{} `json:"args"`
}

// StageExecutionResult contains the outcomes of a StageDriver execution.
type StageExecutionResult struct {
	StepsUsed     int
	ToolsCalled   []string
	ToolOutputLog []string
	LastOutput    string
	Error         error
}

// PhaseRunnerContext provides runtime capabilities to a StageDriver.
type PhaseRunnerContext struct {
	TaskID            string
	ProbeID           string
	GlobalStepCounter *int
	Goal              string
	ToolDispatcher    func(ctx context.Context, toolName string, args map[string]interface{}) (string, error)
	ToolFixup         func(phaseName, toolName string, args map[string]interface{}, reasoning string) (string, map[string]interface{})
	ToolPostProcess   func(phaseName, toolName string, args map[string]interface{}, output string, err error)
	PersistStep       func(phaseName, toolName string, args map[string]interface{}, output, reasoning string)
	SourceTracker     *SourceTracker
}

// StageDriver executes a phase's tool execution logic without step-level LLM inference.
type StageDriver interface {
	Execute(ctx context.Context, phase *Phase, runnerCtx *PhaseRunnerContext) (*StageExecutionResult, error)
}

// DeterministicQueueDriver drains a queue of tool actions up to StepBudget.
type DeterministicQueueDriver struct {
	QueueProvider func() []QueueItem
}

// NewDeterministicQueueDriver constructs a driver with a static queue of items.
func NewDeterministicQueueDriver(items []QueueItem) *DeterministicQueueDriver {
	return &DeterministicQueueDriver{
		QueueProvider: func() []QueueItem { return items },
	}
}

// NewDynamicQueueDriver constructs a driver with a dynamic queue provider function.
func NewDynamicQueueDriver(provider func() []QueueItem) *DeterministicQueueDriver {
	return &DeterministicQueueDriver{
		QueueProvider: provider,
	}
}

// Execute drains items from the queue up to the phase's StepBudget.
func (d *DeterministicQueueDriver) Execute(
	ctx context.Context,
	phase *Phase,
	runnerCtx *PhaseRunnerContext,
) (*StageExecutionResult, error) {
	result := &StageExecutionResult{}

	if d.QueueProvider == nil {
		return result, nil
	}

	items := d.QueueProvider()
	if len(items) == 0 {
		return result, nil
	}

	budget := phase.StepBudget
	if budget <= 0 {
		budget = len(items)
	}

	for _, item := range items {
		if result.StepsUsed >= budget {
			break
		}

		toolName := item.Tool
		args := item.Args
		if args == nil {
			args = make(map[string]interface{})
		}

		// ToolFixup hook: repair arguments before dispatch
		if runnerCtx.ToolFixup != nil {
			toolName, args = runnerCtx.ToolFixup(phase.Name, toolName, args, "")
		}

		if toolName == "noop" || toolName == "" {
			continue
		}

		result.StepsUsed++
		result.ToolsCalled = append(result.ToolsCalled, toolName)

		// Dispatch tool
		var toolOutput string
		var toolErr error

		if runnerCtx.ToolDispatcher != nil {
			toolOutput, toolErr = runnerCtx.ToolDispatcher(ctx, toolName, args)
		} else {
			toolOutput, toolErr = tools.Call(ctx, toolName, args)
		}

		// Selective retry for network tools on error
		if toolErr != nil && (toolName == "web_search" || toolName == "web_browse") {
			time.Sleep(100 * time.Millisecond)
			if runnerCtx.ToolDispatcher != nil {
				toolOutput, toolErr = runnerCtx.ToolDispatcher(ctx, toolName, args)
			} else {
				toolOutput, toolErr = tools.Call(ctx, toolName, args)
			}
		}

		if toolErr != nil {
			result.LastOutput = fmt.Sprintf("Error: %s", toolErr.Error())
		} else {
			result.LastOutput = toolOutput
		}

		// Persist ThoughtStep for downstream Recall Node pipeline
		if runnerCtx.GlobalStepCounter != nil {
			*runnerCtx.GlobalStepCounter++
		}
		if runnerCtx.PersistStep != nil {
			runnerCtx.PersistStep(phase.Name, toolName, args, result.LastOutput, "")
		}

		// Accumulate compacted tool outputs for phase synthesis
		argsStr, _ := json.Marshal(args)
		compacted := compactPhaseToolOutput(toolName, args, result.LastOutput)
		entry := fmt.Sprintf("### %s(%s)\n%s", toolName, string(argsStr), compacted)
		result.ToolOutputLog = append(result.ToolOutputLog, entry)

		// Enforce rolling buffer cap with FIFO eviction
		totalChars := 0
		for _, logEntry := range result.ToolOutputLog {
			totalChars += len(logEntry)
		}
		for totalChars > MaxPhaseToolLogChars && len(result.ToolOutputLog) > 1 {
			evicted := result.ToolOutputLog[0]
			result.ToolOutputLog = result.ToolOutputLog[1:]
			totalChars -= len(evicted)
		}

		// ToolPostProcess hook: post-dispatch state tracking
		if runnerCtx.ToolPostProcess != nil {
			runnerCtx.ToolPostProcess(phase.Name, toolName, args, result.LastOutput, toolErr)
		}
	}

	return result, nil
}

