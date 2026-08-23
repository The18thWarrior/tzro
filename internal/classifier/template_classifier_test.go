package classifier

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tzro/internal/inference"
	"tzro/internal/templates"
)

func TestClassifyTopologyArchetype_LLM(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {
					"content": "{\"topology\":\"probe-synthesis\"}"
				}
			}]
		}`))
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	savedStatus := inference.GlobalWorkerModel.Status
	savedPort := inference.GlobalWorkerModel.ActivePort
	defer func() {
		inference.GlobalWorkerModel.Status = savedStatus
		inference.GlobalWorkerModel.ActivePort = savedPort
	}()

	listenerAddr := srv.Listener.Addr().String()
	_, portStr, _ := netSplitHostPort(listenerAddr)
	inference.GlobalWorkerModel.ActivePort = parseInt(portStr)
	inference.GlobalWorkerModel.Status = "Active"

	ctx := context.Background()
	cat := ClassifyTopologyArchetype(ctx, "explore the codebase and explain the architecture", []string{"read_file", "list_dir"})

	if cat != templates.ProbeSynthesis {
		t.Errorf("expected %q, got %q", templates.ProbeSynthesis, cat)
	}
}

func TestClassifySourceModality_Web(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {
					"content": "{\"modality\":\"web\"}"
				}
			}]
		}`))
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	savedStatus := inference.GlobalWorkerModel.Status
	savedPort := inference.GlobalWorkerModel.ActivePort
	defer func() {
		inference.GlobalWorkerModel.Status = savedStatus
		inference.GlobalWorkerModel.ActivePort = savedPort
	}()

	listenerAddr := srv.Listener.Addr().String()
	_, portStr, _ := netSplitHostPort(listenerAddr)
	inference.GlobalWorkerModel.ActivePort = parseInt(portStr)
	inference.GlobalWorkerModel.Status = "Active"

	ctx := context.Background()
	mod := ClassifySourceModality(ctx, "search the web for AI orchestration trends", []string{"web_search"})

	if mod != templates.SourceWeb {
		t.Errorf("expected %q, got %q", templates.SourceWeb, mod)
	}
}

func TestClassifyPlanTemplate_Gating_NoWebTools(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {
					"content": "{\"topology\":\"probe-and-write\"}"
				}
			}]
		}`))
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	savedStatus := inference.GlobalWorkerModel.Status
	savedPort := inference.GlobalWorkerModel.ActivePort
	defer func() {
		inference.GlobalWorkerModel.Status = savedStatus
		inference.GlobalWorkerModel.ActivePort = savedPort
	}()

	listenerAddr := srv.Listener.Addr().String()
	_, portStr, _ := netSplitHostPort(listenerAddr)
	inference.GlobalWorkerModel.ActivePort = parseInt(portStr)
	inference.GlobalWorkerModel.Status = "Active"

	ctx := context.Background()
	top, mod := ClassifyPlanTemplate(ctx, "generate a readme and save to README.md", []string{"read_file", "write_file"})

	if top != templates.ProbeAndWrite {
		t.Errorf("expected %q, got %q", templates.ProbeAndWrite, top)
	}
	// When web tools are missing, modality must deterministically default to local without pass 2
	if mod != templates.SourceLocal {
		t.Errorf("expected %q, got %q", templates.SourceLocal, mod)
	}
}

func TestClassifyTemplateCategory_FallbackOnError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	savedStatus := inference.GlobalWorkerModel.Status
	savedPort := inference.GlobalWorkerModel.ActivePort
	defer func() {
		inference.GlobalWorkerModel.Status = savedStatus
		inference.GlobalWorkerModel.ActivePort = savedPort
	}()

	listenerAddr := srv.Listener.Addr().String()
	_, portStr, _ := netSplitHostPort(listenerAddr)
	inference.GlobalWorkerModel.ActivePort = parseInt(portStr)
	inference.GlobalWorkerModel.Status = "Active"

	ctx := context.Background()
	cat := ClassifyTopologyArchetype(ctx, "anything", []string{})

	if cat != templates.ProbeSynthesis {
		t.Errorf("expected fallback to %q, got %q", templates.ProbeSynthesis, cat)
	}
}

func TestClassifyTemplateCategory_InvalidCategory_Fallback(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {
					"content": "{\"topology\":\"garbage\"}"
				}
			}]
		}`))
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	savedStatus := inference.GlobalWorkerModel.Status
	savedPort := inference.GlobalWorkerModel.ActivePort
	defer func() {
		inference.GlobalWorkerModel.Status = savedStatus
		inference.GlobalWorkerModel.ActivePort = savedPort
	}()

	listenerAddr := srv.Listener.Addr().String()
	_, portStr, _ := netSplitHostPort(listenerAddr)
	inference.GlobalWorkerModel.ActivePort = parseInt(portStr)
	inference.GlobalWorkerModel.Status = "Active"

	ctx := context.Background()
	cat := ClassifyTopologyArchetype(ctx, "anything", []string{})

	if cat != templates.ProbeSynthesis {
		t.Errorf("expected fallback to %q, got %q", templates.ProbeSynthesis, cat)
	}
}

func TestTopologyArchetypeSystemPrompt_ContainsAllCategories(t *testing.T) {
	for _, cat := range templates.Categories() {
		if !strings.Contains(TopologyArchetypeSystemPrompt, string(cat)) {
			t.Errorf("TopologyArchetypeSystemPrompt missing category %q", cat)
		}
	}
}
