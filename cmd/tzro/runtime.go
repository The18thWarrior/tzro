package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"tzro/pkg/decision"
	"tzro/pkg/executor"
	"tzro/pkg/extractor"
	"tzro/pkg/store"
)

// configuredEngine is shared by the public CLI and MCP entry points.
// Optional workers start lazily, so Standard requires no model installation.
func configuredEngine(workspace string, s *store.Store, concurrency int) (*executor.Engine, func(), error) {
	options := []executor.EngineOption{
		executor.WithMaxConcurrency(concurrency),
		executor.WithToolDispatcher(executor.NewBuiltinDispatcher(workspace, s)),
	}
	closeWorkers := func() {}
	if os.Getenv("TZRO_EXPERIMENTAL_RUNTIMES") != "1" {
		return executor.NewEngine(options...), closeWorkers, nil
	}
	extractorBin := os.Getenv("TZRO_EXTRACTOR_BIN")
	if extractorBin == "" {
		return nil, closeWorkers, errors.New("experimental runtimes require TZRO_EXTRACTOR_BIN")
	}
	var args []string
	if value := os.Getenv("TZRO_EXTRACTOR_ARGS"); value != "" {
		if err := json.Unmarshal([]byte(value), &args); err != nil {
			return nil, closeWorkers, fmt.Errorf("TZRO_EXTRACTOR_ARGS must be a JSON string array: %w", err)
		}
	}
	provider, err := decision.NewProviderFromConfig(nil)
	if err != nil {
		return nil, closeWorkers, err
	}
	worker := extractor.NewWorkerClient(extractorBin, args...)
	closeWorkers = func() { _ = provider.Close(); _ = worker.Close() }
	options = append(options,
		executor.WithDecider(decision.NewDeciderAdapter(&observedDecision{provider}, nil)),
		executor.WithExtractor(&observedExtractor{extractor.NewGLiNERAdapter(worker)}),
	)
	return executor.NewEngine(options...), closeWorkers, nil
}

type observedDecision struct{ decision.DecisionProvider }

func (p *observedDecision) Evaluate(ctx context.Context, request *decision.DecisionRequest) (*decision.DecisionResponse, error) {
	start := time.Now()
	response, err := p.DecisionProvider.Evaluate(ctx, request)
	if err == nil && (response == nil || response.Answer == "" || response.Error != "") {
		err = errors.New("decision worker returned no valid answer")
	}
	recordRuntime("decision", start, err)
	return response, err
}

type observedExtractor struct{ executor.Extractor }

func (p *observedExtractor) Extract(ctx context.Context, text string, labels []string) ([]executor.ExtractedSpan, error) {
	start := time.Now()
	spans, err := p.Extractor.Extract(ctx, text, labels)
	recordRuntime("extractor", start, err)
	return spans, err
}

// Opt-in diagnostics contain outcomes only, never model inputs or credentials.
func recordRuntime(kind string, start time.Time, err error) {
	status := "completed"
	if err != nil {
		status = "failed"
	}
	recordActivity(kind, status, time.Since(start).Milliseconds())
}

func recordActivity(kind, status string, durationMS int64) {
	path := os.Getenv("TZRO_RUNTIME_TRACE")
	if path == "" {
		return
	}
	data, _ := json.Marshal(map[string]any{"runtime": kind, "status": status, "duration_ms": durationMS})
	f, openErr := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if openErr == nil {
		_, _ = f.Write(append(data, '\n'))
		_ = f.Close()
	}
}
