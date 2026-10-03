package executor

import (
	"context"
	"testing"
	"time"
)

func TestGraphCancellationStopsShellChildren(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	g := &Graph{Version: "3.0", TaskID: "cancel", Nodes: []Node{{ID: "wait", Type: NodeTypeTool, Tool: "bash", Args: map[string]any{"command": "sleep 3; printf should-not-run"}}}}
	start := time.Now()
	_, _ = NewEngine().Execute(ctx, g)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("cancellation waited for an orphan shell child: %s", elapsed)
	}
}
