package executor

import (
	"strings"
	"testing"
)

func TestSourceTracker_WebAndFileInjection(t *testing.T) {
	st := NewSourceTracker()
	st.AddWebSource("https://docs.restate.dev/docs", "Restate Docs", "", []string{"Durable RPC"})
	st.AddWebSource("https://docs.temporal.io", "Temporal", "", []string{"Event Sourcing"})
	st.AddFileSource("internal/executor/probe.go", []string{"RunProbe"}, 120, "")

	if !st.HasSources() {
		t.Fatal("expected HasSources to be true")
	}

	synth := "Here is a comparison of Restate [1] and Temporal [2]."
	injected := st.InjectOrNormalizeReferences(synth)

	if !strings.Contains(injected, "## References & Verified Sources") {
		t.Errorf("expected references header, got %q", injected)
	}
	if !strings.Contains(injected, "[https://docs.restate.dev/docs](https://docs.restate.dev/docs)") && !strings.Contains(injected, "https://docs.restate.dev/docs") {
		t.Errorf("expected restate URL in output, got %q", injected)
	}
	if !strings.Contains(injected, "internal/executor/probe.go") {
		t.Errorf("expected probe.go in output, got %q", injected)
	}
}

func TestSourceTracker_PreservesExistingWellFormedReferences(t *testing.T) {
	st := NewSourceTracker()
	st.AddWebSource("https://example.com/docs", "Docs", "", nil)

	synth := "Report text.\n\n## References\n- [Docs](https://example.com/docs)"
	result := st.InjectOrNormalizeReferences(synth)

	if result != synth {
		t.Errorf("expected unchanged synthesis when well-formed references exist, got %q", result)
	}
}
