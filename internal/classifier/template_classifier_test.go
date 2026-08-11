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

func TestClassifyTemplateCategory_LLM(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {
					"content": "{\"category\":\"explore-only\"}"
				}
			}]
		}`))
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	savedStatus := inference.GlobalRouterModel.Status
	savedPort := inference.GlobalRouterModel.ActivePort
	defer func() {
		inference.GlobalRouterModel.Status = savedStatus
		inference.GlobalRouterModel.ActivePort = savedPort
	}()

	listenerAddr := srv.Listener.Addr().String()
	_, portStr, _ := netSplitHostPort(listenerAddr)
	inference.GlobalRouterModel.ActivePort = parseInt(portStr)
	inference.GlobalRouterModel.Status = "Active"

	ctx := context.Background()
	cat := ClassifyTemplateCategory(ctx, "explore the codebase and explain the architecture", []string{"read_file", "list_dir"})

	if cat != templates.ExploreOnly {
		t.Errorf("expected %q, got %q", templates.ExploreOnly, cat)
	}
}

func TestClassifyTemplateCategory_Research(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {
					"content": "{\"category\":\"research\"}"
				}
			}]
		}`))
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	savedStatus := inference.GlobalRouterModel.Status
	savedPort := inference.GlobalRouterModel.ActivePort
	defer func() {
		inference.GlobalRouterModel.Status = savedStatus
		inference.GlobalRouterModel.ActivePort = savedPort
	}()

	listenerAddr := srv.Listener.Addr().String()
	_, portStr, _ := netSplitHostPort(listenerAddr)
	inference.GlobalRouterModel.ActivePort = parseInt(portStr)
	inference.GlobalRouterModel.Status = "Active"

	ctx := context.Background()
	cat := ClassifyTemplateCategory(ctx, "search the web for AI orchestration trends", []string{"web_search"})

	if cat != templates.Research {
		t.Errorf("expected %q, got %q", templates.Research, cat)
	}
}

func TestClassifyTemplateCategory_FallbackOnError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	savedStatus := inference.GlobalRouterModel.Status
	savedPort := inference.GlobalRouterModel.ActivePort
	defer func() {
		inference.GlobalRouterModel.Status = savedStatus
		inference.GlobalRouterModel.ActivePort = savedPort
	}()

	listenerAddr := srv.Listener.Addr().String()
	_, portStr, _ := netSplitHostPort(listenerAddr)
	inference.GlobalRouterModel.ActivePort = parseInt(portStr)
	inference.GlobalRouterModel.Status = "Active"

	ctx := context.Background()
	cat := ClassifyTemplateCategory(ctx, "anything", []string{})

	if cat != templates.ExploreOnly {
		t.Errorf("expected fallback to %q, got %q", templates.ExploreOnly, cat)
	}
}

func TestClassifyTemplateCategory_InvalidCategory_Fallback(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {
					"content": "{\"category\":\"garbage\"}"
				}
			}]
		}`))
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	savedStatus := inference.GlobalRouterModel.Status
	savedPort := inference.GlobalRouterModel.ActivePort
	defer func() {
		inference.GlobalRouterModel.Status = savedStatus
		inference.GlobalRouterModel.ActivePort = savedPort
	}()

	listenerAddr := srv.Listener.Addr().String()
	_, portStr, _ := netSplitHostPort(listenerAddr)
	inference.GlobalRouterModel.ActivePort = parseInt(portStr)
	inference.GlobalRouterModel.Status = "Active"

	ctx := context.Background()
	cat := ClassifyTemplateCategory(ctx, "anything", []string{})

	if cat != templates.ExploreOnly {
		t.Errorf("expected fallback to %q, got %q", templates.ExploreOnly, cat)
	}
}

func TestTemplateCategorySystemPrompt_ContainsAllCategories(t *testing.T) {
	for _, cat := range templates.Categories() {
		if !strings.Contains(TemplateCategorySystemPrompt, string(cat)) {
			t.Errorf("TemplateCategorySystemPrompt missing category %q", cat)
		}
	}
}
