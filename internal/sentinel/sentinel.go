package sentinel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"tzro/internal/agent"
	"tzro/internal/config"
	"tzro/internal/memory"
	"tzro/internal/stream"
)

// ActivityReport is the structured payload from the tzro_activity_report MCP tool.
type ActivityReport struct {
	Activity     string   `json:"activity"`
	FilesTouched []string `json:"filesTouched,omitempty"`
	ToolsUsed    []string `json:"toolsUsed,omitempty"`
	Timestamp    int64    `json:"timestamp"`
}

// SentinelAgent is a proactive Background Agent that reasons over accumulated context
// to surface emergent insights. It fires on a periodic heartbeat timer and ingested
// activity reports from the cloud agent.
//
// See ADR-0023 for the architectural decision.
type SentinelAgent struct {
	agent.BackgroundAgent

	mu              sync.RWMutex
	activityBuffer  []ActivityReport // ring buffer, max 10
	scanner         WorkspaceScanner
	confidenceGate  float64
	similarityFloor float64
}

// Verify interface compliance at compile time.
var _ agent.Agent = (*SentinelAgent)(nil)

// Name returns the agent's canonical name.
func (s *SentinelAgent) Name() string {
	return s.AgentName()
}

// NewSentinelAgent creates a new SentinelAgent.
func NewSentinelAgent() *SentinelAgent {
	return &SentinelAgent{
		BackgroundAgent: agent.NewBackgroundAgent("sentinel"),
		confidenceGate:  0.7,
		similarityFloor: 0.6,
	}
}

// SetScanner overrides the workspace scanner (for testing).
func (s *SentinelAgent) SetScanner(sc WorkspaceScanner) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scanner = sc
}

// IngestActivityReport buffers an activity report from the cloud agent.
// Maintains a ring buffer of the last 10 reports.
func (s *SentinelAgent) IngestActivityReport(report ActivityReport) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if report.Timestamp == 0 {
		report.Timestamp = time.Now().Unix()
	}

	s.activityBuffer = append(s.activityBuffer, report)
	if len(s.activityBuffer) > 10 {
		s.activityBuffer = s.activityBuffer[len(s.activityBuffer)-10:]
	}
}

// getRecentActivity returns a copy of the activity buffer and clears it.
func (s *SentinelAgent) getRecentActivity() []ActivityReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.activityBuffer) == 0 {
		return nil
	}
	result := make([]ActivityReport, len(s.activityBuffer))
	copy(result, s.activityBuffer)
	return result
}

// Start spawns the background heartbeat loop.
func (s *SentinelAgent) Start(ctx context.Context) {
	s.mu.Lock()
	if s.IsRunning() {
		s.mu.Unlock()
		return
	}

	loopCtx, cancel := context.WithCancel(ctx)
	s.SetCancel(cancel)

	// Initialize scanner if not overridden
	if s.scanner == nil {
		workspaceDir := os.Getenv("TZRO_DIR")
		if workspaceDir == "" {
			workspaceDir = "."
		}
		interval := config.GetSentinelInterval()
		s.scanner = NewWorkspaceScanner(workspaceDir, interval)
	}

	interval := config.GetSentinelInterval()
	s.mu.Unlock()

	fmt.Fprintf(os.Stderr, "[Sentinel] Started with %s heartbeat interval\n", interval)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				s.evaluateHeartbeat(loopCtx)
			}
		}
	}()
}

// Wake manually triggers a single heartbeat evaluation outside the normal timer cadence.
// An optional contextHint is injected as a synthetic activity report to bias the retrieval
// toward a specific topic (e.g., "check for security issues in auth module").
// Returns true if an alert was generated, false otherwise.
func (s *SentinelAgent) Wake(ctx context.Context, contextHint string) bool {
	if contextHint != "" {
		s.IngestActivityReport(ActivityReport{
			Activity:  contextHint,
			Timestamp: time.Now().Unix(),
		})
	}

	// Count alerts before evaluation
	beforeCount := s.alertCount()
	s.evaluateHeartbeat(ctx)
	afterCount := s.alertCount()

	return afterCount > beforeCount
}

// alertCount returns the current number of sentinel alerts in the notification store.
func (s *SentinelAgent) alertCount() int {
	notifs, err := memory.DB.GetNotifications("")
	if err != nil {
		return 0
	}
	count := 0
	for _, n := range notifs {
		if n.Source == "sentinel" {
			count++
		}
	}
	return count
}

// Stop cancels the background loop.
func (s *SentinelAgent) Stop() {
	s.Cancel()
	s.Unsubscribe()
}

// evaluateHeartbeat runs the retrieval-grounded synthesis pipeline.
func (s *SentinelAgent) evaluateHeartbeat(ctx context.Context) {
	fmt.Fprintf(os.Stderr, "[Sentinel] Heartbeat tick — gathering context...\n")

	// 1. Gather context: workspace changes + activity reports
	contextText := s.gatherContext()
	if contextText == "" {
		fmt.Fprintf(os.Stderr, "[Sentinel] No new context to evaluate, skipping.\n")
		return
	}

	// 2. Retrieve: semantic search against memory, KG, skills
	candidates := s.retrieveCandidates(contextText)
	if len(candidates) == 0 {
		fmt.Fprintf(os.Stderr, "[Sentinel] No relevant candidates found, skipping.\n")
		return
	}

	// 3. Synthesize: LLM call with grounded prompt
	s.synthesizeAndAlert(ctx, contextText, candidates)
}

// gatherContext assembles the current context snapshot from workspace changes and activity reports.
func (s *SentinelAgent) gatherContext() string {
	var parts []string

	// Workspace file changes
	s.mu.RLock()
	scanner := s.scanner
	s.mu.RUnlock()

	if scanner != nil {
		changes, err := scanner.ScanChanges()
		if err == nil && len(changes) > 0 {
			// Cap at 50 files to keep context manageable
			if len(changes) > 50 {
				changes = changes[:50]
			}
			parts = append(parts, "Recently changed files:\n"+strings.Join(changes, "\n"))
		}
	}

	// Activity reports
	activities := s.getRecentActivity()
	if len(activities) > 0 {
		var actParts []string
		for _, a := range activities {
			desc := a.Activity
			if len(a.FilesTouched) > 0 {
				desc += " (files: " + strings.Join(a.FilesTouched, ", ") + ")"
			}
			if len(a.ToolsUsed) > 0 {
				desc += " (tools: " + strings.Join(a.ToolsUsed, ", ") + ")"
			}
			actParts = append(actParts, "- "+desc)
		}
		parts = append(parts, "Recent activity reports:\n"+strings.Join(actParts, "\n"))
	}

	if len(parts) == 0 {
		return ""
	}

	return strings.Join(parts, "\n\n")
}

// candidate represents a matched context item from the retrieval phase.
type candidate struct {
	Source     string  // "memory", "skill", "kg_node"
	ID        string  // the entity ID
	Content   string  // display text for the matched item
	Score     float64 // similarity score
}

// retrieveCandidates performs semantic search against memory, skills, and KG.
func (s *SentinelAgent) retrieveCandidates(contextText string) []candidate {
	var candidates []candidate

	// 1. Search memories and KG nodes using hybrid vector/text similarity
	mems, nodes, err := memory.DB.SearchMemoriesAndNodes(contextText, 5)
	if err == nil {
		for _, m := range mems {
			candidates = append(candidates, candidate{
				Source:  "memory",
				ID:     m.ID,
				Content: fmt.Sprintf("[%s] %s", m.Type, m.Content),
				Score:  m.Confidence, // Use confidence as a proxy for relevance
			})
		}
		for _, n := range nodes {
			candidates = append(candidates, candidate{
				Source:  "kg_node",
				ID:     n.ID,
				Content: fmt.Sprintf("[%s] %s", n.NodeType, n.Name),
				Score:  n.Weight,
			})
		}
	}

	// 2. Search skills (returned pre-ranked by relevance)
	skills := memory.DB.GetRelevantSkills(contextText, 3)
	for _, sk := range skills {
		candidates = append(candidates, candidate{
			Source:  "skill",
			ID:     sk.ID,
			Content: fmt.Sprintf("Skill: %s — %s", sk.Name, sk.TriggerDescription),
			Score:  1.0, // Pre-ranked, treat as relevant
		})
	}

	// Sort by score descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	// Cap at 10 candidates
	if len(candidates) > 10 {
		candidates = candidates[:10]
	}

	return candidates
}

// synthesizeAndAlert runs the Local Model to produce a grounded alert from matched candidates.
func (s *SentinelAgent) synthesizeAndAlert(ctx context.Context, contextText string, candidates []candidate) {
	llm := s.GetLLMClient()
	if llm == nil {
		fmt.Fprintf(os.Stderr, "[Sentinel] No LLM client configured, skipping synthesis.\n")
		return
	}

	// Build grounded prompt
	var candidateDescs []string
	var candidateIDs []string
	for _, c := range candidates {
		candidateDescs = append(candidateDescs, fmt.Sprintf("- [%s] (score: %.2f) %s", c.Source, c.Score, c.Content))
		candidateIDs = append(candidateIDs, c.ID)
	}

	systemPrompt := `You are a proactive intelligence agent. Your job is to surface genuinely useful, non-obvious insights by connecting the user's current work with relevant context from their accumulated knowledge base.

Rules:
1. Only produce an alert if the matched context is genuinely relevant and actionable.
2. Be specific — reference the exact memories, skills, or entities that matched.
3. Explain WHY the match matters for the user's current work.
4. If the matches are weak or generic, set confidence to 0 and produce no alert text.
5. Never produce generic advice like "add error handling" or "consider testing."

Return valid JSON matching this schema exactly.`

	userPrompt := fmt.Sprintf(`Current user activity context:
%s

Matched knowledge base items:
%s

Based on this, produce a proactive insight or alert if genuinely warranted.`, contextText, strings.Join(candidateDescs, "\n"))

	gbnfSchema := `{
		"type": "object",
		"properties": {
			"alert": { "type": "string" },
			"confidence": { "type": "number" },
			"priority": { "type": "string", "enum": ["critical", "suggestion", "ambient"] }
		},
		"required": ["alert", "confidence", "priority"]
	}`

	result, err := llm.CallModel(ctx, systemPrompt, userPrompt, gbnfSchema)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Sentinel] Synthesis LLM call failed: %v\n", err)
		return
	}

	var synthesis struct {
		Alert      string  `json:"alert"`
		Confidence float64 `json:"confidence"`
		Priority   string  `json:"priority"`
	}
	if err := json.Unmarshal([]byte(result), &synthesis); err != nil {
		fmt.Fprintf(os.Stderr, "[Sentinel] Failed to parse synthesis output: %v\n", err)
		return
	}

	// Confidence gate
	s.mu.RLock()
	gate := s.confidenceGate
	s.mu.RUnlock()

	if synthesis.Confidence < gate {
		fmt.Fprintf(os.Stderr, "[Sentinel] Synthesis confidence %.2f below gate %.2f, suppressing.\n", synthesis.Confidence, gate)
		return
	}

	if strings.TrimSpace(synthesis.Alert) == "" {
		return
	}

	// Deduplication: fingerprint from sorted candidate IDs
	sort.Strings(candidateIDs)
	fp := agent.Fingerprint(candidateIDs...)
	if s.HasActiveAlert(fp) {
		fmt.Fprintf(os.Stderr, "[Sentinel] Active alert exists for this context, skipping.\n")
		return
	}

	// Produce the alert
	priority := synthesis.Priority
	if priority == "" {
		priority = "suggestion"
	}

	err = s.ProduceAlert(ctx, priority, "Sentinel Insight", synthesis.Alert, fp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Sentinel] Failed to produce alert: %v\n", err)
		return
	}

	tm := s.GetTelemetryManager()
	if tm != nil {
		tm.PublishStream(stream.StreamChunk{
			Source:  "sentinel",
			Type:    "sentinel_alert",
			Content: fmt.Sprintf("[%s] %s", priority, synthesis.Alert),
		})
	}

	fmt.Fprintf(os.Stderr, "[Sentinel] Alert produced: [%s] %s (confidence: %.2f)\n", priority, synthesis.Alert, synthesis.Confidence)
}

// DefaultAgent is the global singleton sentinel agent.
var DefaultAgent = NewSentinelAgent()

// Start begins the background sentinel heartbeat.
func Start() {
	DefaultAgent.Start(context.Background())
}

// Stop terminates the background sentinel heartbeat.
func Stop() {
	DefaultAgent.Stop()
}

// SetLLMClient registers an LLM client with the default agent.
func SetLLMClient(client agent.LLMClient) {
	DefaultAgent.SetLLMClient(client)
}

// Wake manually triggers a Sentinel evaluation. See SentinelAgent.Wake.
func Wake(ctx context.Context, contextHint string) bool {
	return DefaultAgent.Wake(ctx, contextHint)
}
