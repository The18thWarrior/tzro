package macronodes

import (
	"context"
	"fmt"
	"tzro/internal/compiler"
)

// SubDAGTemplate defines the interface for a macro-node template.
type SubDAGTemplate interface {
	Name() string
	Description() string
	RequiredInputs() []string
	BuildGraph(taskID string, inputs map[string]interface{}) (*compiler.ExecutionGraph, error)
}

var registry = make(map[string]SubDAGTemplate)

func init() {
	Register(&CodebaseExplorer{})
	Register(&WebResearcher{})
	Register(&DataAnalyzer{})
	Register(&MemoryIngestionPipeline{})
}

func Register(t SubDAGTemplate) {
	registry[t.Name()] = t
}

func GetTemplate(name string) (SubDAGTemplate, bool) {
	t, ok := registry[name]
	return t, ok
}

// BuildSubDAG tries to build a DAG from the native macro-nodes registry.
// Returns nil, nil if the template is not found natively.
func BuildSubDAG(ctx context.Context, action, taskID string, inputs map[string]interface{}) (*compiler.ExecutionGraph, error) {
	t, ok := GetTemplate(action)
	if !ok {
		return nil, nil // Let the caller check user-defined skills directory
	}

	for _, req := range t.RequiredInputs() {
		if _, ok := inputs[req]; !ok {
			return nil, fmt.Errorf("missing required input '%s' for macro-node '%s'", req, action)
		}
	}

	return t.BuildGraph(taskID, inputs)
}
