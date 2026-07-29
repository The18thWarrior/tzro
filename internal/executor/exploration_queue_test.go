package executor

import "testing"

func TestExplorationQueue_NextUnvisited(t *testing.T) {
	eq := NewExplorationQueue([]string{"a.go", "b.go", "c.go"})

	// First unvisited should be a.go
	next, ok := eq.NextUnvisited()
	if !ok || next != "a.go" {
		t.Errorf("expected a.go, got %q (ok=%v)", next, ok)
	}

	// Mark a.go visited, next should be b.go
	eq.MarkVisited("a.go")
	next, ok = eq.NextUnvisited()
	if !ok || next != "b.go" {
		t.Errorf("expected b.go, got %q (ok=%v)", next, ok)
	}

	// Mark b.go and c.go visited — should return empty
	eq.MarkVisited("b.go")
	eq.MarkVisited("c.go")
	next, ok = eq.NextUnvisited()
	if ok || next != "" {
		t.Errorf("expected empty, got %q (ok=%v)", next, ok)
	}
}

func TestExplorationQueue_Stats(t *testing.T) {
	eq := NewExplorationQueue([]string{"a.go", "b.go", "c.go"})

	visited, total := eq.Stats()
	if visited != 0 || total != 3 {
		t.Errorf("expected 0/3, got %d/%d", visited, total)
	}

	eq.MarkVisited("a.go")
	visited, total = eq.Stats()
	if visited != 1 || total != 3 {
		t.Errorf("expected 1/3, got %d/%d", visited, total)
	}
}

func TestExplorationQueue_Empty(t *testing.T) {
	eq := NewExplorationQueue(nil)
	if !eq.IsEmpty() {
		t.Error("expected empty queue")
	}
	next, ok := eq.NextUnvisited()
	if ok || next != "" {
		t.Errorf("expected empty from nil queue, got %q (ok=%v)", next, ok)
	}
}

func TestExplorationQueue_RedirectsToUnvisited(t *testing.T) {
	// Simulates the loop-breaking pattern: model keeps requesting a.go,
	// queue redirects to b.go, then c.go
	eq := NewExplorationQueue([]string{"a.go", "b.go", "c.go"})
	eq.MarkVisited("a.go")

	// "Model requests a.go again" — redirect to b.go
	next, ok := eq.NextUnvisited()
	if !ok || next != "b.go" {
		t.Errorf("expected redirect to b.go, got %q", next)
	}

	eq.MarkVisited("b.go")

	// "Model requests a.go again" — redirect to c.go
	next, ok = eq.NextUnvisited()
	if !ok || next != "c.go" {
		t.Errorf("expected redirect to c.go, got %q", next)
	}
}
