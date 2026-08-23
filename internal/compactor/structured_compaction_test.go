package compactor

import (
	"context"
	"strings"
	"testing"
)

func TestStructuredCompaction_PreservesCodeSignatures(t *testing.T) {
	code := `package server

import "net/http"

// Config defines server configuration.
type Config struct {
	Port int
	Host string
}

// Router handles request routing.
type Router interface {
	Route(r *http.Request) (http.Handler, error)
}

// NewServer creates a new server instance.
func NewServer(cfg *Config) (*Server, error) {
	if cfg == nil {
		return nil, errors.New("nil config")
	}
	s := &Server{port: cfg.Port}
	return s, nil
}
`

	skeleton := ExtractSkeleton(code, 500)
	if !strings.Contains(skeleton, "type Config struct") {
		t.Errorf("expected struct declaration preserved, got:\n%s", skeleton)
	}
	if !strings.Contains(skeleton, "type Router interface") {
		t.Errorf("expected interface declaration preserved, got:\n%s", skeleton)
	}
	if !strings.Contains(skeleton, "func NewServer(cfg *Config) (*Server, error)") {
		t.Errorf("expected function signature preserved, got:\n%s", skeleton)
	}
}

func TestStructuredCompaction_PreservesTabularSchemaAndRows(t *testing.T) {
	csvData := "Country,Leads,WonRate\nUS,500,0.34\nUK,200,0.28\nDE,150,0.41\nFR,120,0.22\nCA,90,0.31\nJP,80,0.45"
	compacted := TruncateTabular(csvData, 200)

	if !strings.Contains(compacted, "Country,Leads,WonRate") {
		t.Errorf("expected CSV header preserved, got:\n%s", compacted)
	}
	if !strings.Contains(compacted, "US,500,0.34") {
		t.Errorf("expected top row preserved, got:\n%s", compacted)
	}
}

func TestStructuredCompaction_ToolOutputsPreservesExemptTools(t *testing.T) {
	steps := []ToolOutputStep{
		{
			StepIndex:  1,
			ToolName:   "sql_cached_data",
			ToolArgs:   `{"query": "SELECT country, count(*) FROM leads GROUP BY country"}`,
			ToolOutput: `[{"country": "US", "count": 500}, {"country": "UK", "count": 200}]`,
		},
	}

	res, err := CompactToolOutputs(context.Background(), steps, 500, nil)
	if err != nil {
		t.Fatalf("CompactToolOutputs failed: %v", err)
	}

	if !strings.Contains(res.Output, "sql_cached_data") {
		t.Errorf("expected sql_cached_data step header in output, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, `[{"country": "US", "count": 500}, {"country": "UK", "count": 200}]`) {
		t.Errorf("expected exempt SQL data preserved verbatim, got: %s", res.Output)
	}
}
