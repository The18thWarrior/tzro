package proactivity

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"tzro/internal/notification"
	"tzro/internal/stream"
	"tzro/internal/telemetry"
)

// Global attention scheduler singleton
var GlobalScheduler *DefaultAttentionScheduler

// DefaultAttentionScheduler coordinates background daemons, budgets, and preemption.
type DefaultAttentionScheduler struct {
	mu           sync.RWMutex
	ctx          context.Context
	cancelFunc   context.CancelFunc
	daemons      map[string]Daemon
	tracker      *BudgetTracker
	gate         *SentinelGate
	activeCancels map[string]context.CancelFunc // tracks background execution context cancels
	activeActions map[string]*ProposedAction    // maps notification/attention ID to Go ProposedAction

	telemetrySub *telemetry.TelemetrySubscription
}

// NewDefaultAttentionScheduler instantiates a scheduler with standard settings.
func NewDefaultAttentionScheduler() *DefaultAttentionScheduler {
	return &DefaultAttentionScheduler{
		daemons:       make(map[string]Daemon),
		tracker:       NewBudgetTracker(1 * time.Hour), // 1-hour interval budget reset
		gate:          NewSentinelGate(),
		activeCancels: make(map[string]context.CancelFunc),
		activeActions: make(map[string]*ProposedAction),
	}
}

// Start begins the scheduler event aggregation loop and registers preemption hooks.
func (s *DefaultAttentionScheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ctx != nil {
		return nil
	}

	s.ctx, s.cancelFunc = context.WithCancel(ctx)

	// Register preemption callback in registry
	RegisterPreemptionCallback(s.CancelActiveBackgroundActions)

	// Subscribe to telemetry stream events to feed daemons reactively
	s.telemetrySub = telemetry.Default.Subscribe(func(chunk stream.StreamChunk) bool {
		// Feed core execution events into the proactivity engine
		return chunk.Source == "executor" || chunk.Source == "workflow_orchestrator" || chunk.Source == "sentinel"
	})

	go s.eventIngestLoop()

	fmt.Fprintln(os.Stderr, "[Proactivity] AttentionScheduler started successfully.")
	return nil
}

// Stop terminates the scheduler aggregation and telemetry subscriptions.
func (s *DefaultAttentionScheduler) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cancelFunc != nil {
		s.cancelFunc()
		s.cancelFunc = nil
	}

	if s.telemetrySub != nil {
		s.telemetrySub.Unsubscribe()
		s.telemetrySub = nil
	}

	s.ctx = nil
	fmt.Fprintln(os.Stderr, "[Proactivity] AttentionScheduler stopped.")
	return nil
}

// RegisterDaemon adds a daemon to the scheduler inventory.
func (s *DefaultAttentionScheduler) RegisterDaemon(daemon Daemon) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.daemons[daemon.Name()] = daemon
	fmt.Fprintf(os.Stderr, "[Proactivity] Registered background daemon: %s\n", daemon.Name())
	return nil
}

// SubmitEvent dispatches an event to all subscribed daemons.
func (s *DefaultAttentionScheduler) SubmitEvent(ctx context.Context, event Event) error {
	s.mu.RLock()
	var matchedDaemons []Daemon
	for _, d := range s.daemons {
		for _, sub := range d.Subscriptions() {
			if sub == event.Type || sub == "*" {
				matchedDaemons = append(matchedDaemons, d)
				break
			}
		}
	}
	s.mu.RUnlock()

	for _, daemon := range matchedDaemons {
		go s.processDaemonEvent(ctx, daemon, event)
	}

	return nil
}

// processDaemonEvent runs a daemon's handler, checks policy, and executes/queues the proposed action.
func (s *DefaultAttentionScheduler) processDaemonEvent(ctx context.Context, daemon Daemon, event Event) {
	fmt.Fprintf(os.Stderr, "[Proactivity] Daemon '%s' waken by event '%s'\n", daemon.Name(), event.Type)

	// Call daemon event handler
	action, err := daemon.Handler(ctx, event)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Proactivity Error] Daemon '%s' handler failed: %v\n", daemon.Name(), err)
		return
	}
	if action == nil {
		// Daemon decided no action needed (no-op)
		return
	}

	// Tie daemon and event metadata to proposed action
	action.DaemonID = daemon.Name()
	action.TriggeringEventID = event.ID

	// Evaluate against Sentinel Gate
	decision := s.gate.Evaluate(action, s.tracker, daemon)
	if !decision.Allowed {
		fmt.Fprintf(os.Stderr, "[Proactivity Policy] Blocked action '%s' from daemon '%s': %s\n", action.ActionType, daemon.Name(), decision.Reason)
		s.emitResultEvent(action, "proactivity.action.deferred", decision.Reason)
		return
	}

	if decision.ApprovalRequired {
		// Attention required: add to the queue
		err = s.enqueueAttention(ctx, action, decision.Reason)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Proactivity Error] Failed to enqueue action: %v\n", err)
		}
		return
	}

	// Allowed L0/L1 deterministic execution
	s.executeActionAsync(action)
}

// executeActionAsync executes an allowed action in the background within a cancellable context.
func (s *DefaultAttentionScheduler) executeActionAsync(action *ProposedAction) {
	s.mu.Lock()
	if _, active := s.activeCancels[action.ID]; active {
		s.mu.Unlock()
		return
	}

	// Create cancellable context
	execCtx, cancel := context.WithCancel(s.ctx)
	s.activeCancels[action.ID] = cancel
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.activeCancels, action.ID)
			s.mu.Unlock()
		}()

		startTime := time.Now()
		fmt.Fprintf(os.Stderr, "[Proactivity Execution] Running action '%s' for daemon '%s'...\n", action.ActionType, action.DaemonID)

		var result string
		var execErr error

		if action.Execute != nil {
			result, execErr = action.Execute(execCtx)
		} else {
			// Fallback/No-op demo execution
			time.Sleep(1 * time.Second)
			result = "Default/No-op execution completed successfully"
		}

		duration := time.Since(startTime)

		if execErr != nil {
			fmt.Fprintf(os.Stderr, "[Proactivity Error] Action '%s' failed: %v\n", action.ActionType, execErr)
			s.emitResultEvent(action, "proactivity.action.failed", execErr.Error())
			return
		}

		// Record consumed resources in budget tracker
		s.tracker.RecordUsage(action.DaemonID, duration, action.PayloadTokenEstimate(), len(action.RequiredCapabilities))

		fmt.Fprintf(os.Stderr, "[Proactivity Execution] Action '%s' completed: %s\n", action.ActionType, result)
		s.emitResultEvent(action, "proactivity.action.executed", result)
	}()
}

// enqueueAttention persists a proposed action to SQLite as a Durable Notification.
func (s *DefaultAttentionScheduler) enqueueAttention(ctx context.Context, action *ProposedAction, reason string) error {
	notifType := "suggestion"
	if action.Level == L4ExternalSideEffect {
		notifType = "approval_request"
	}

	payloadBytes, _ := json.Marshal(action)

	opts := []notification.Option{
		notification.WithActionPayload(string(payloadBytes)),
		notification.WithTargetID(action.ID),
	}
	if action.TriggeringEventID != "" {
		opts = append(opts, notification.WithTargetID(action.TriggeringEventID))
	}

	notif, err := notification.Send(ctx, action.DaemonID, notifType, action.Description, reason, opts...)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.activeActions[notif.ID] = action
	s.mu.Unlock()

	s.emitResultEvent(action, "proactivity.action.requires_approval", fmt.Sprintf("Action queued with Attention ID: %s", notif.ID))
	return nil
}

// PendingAttention retrieves pending approval-required actions from SQLite.
func (s *DefaultAttentionScheduler) PendingAttention(ctx context.Context) ([]AttentionItem, error) {
	notifs, err := notification.List(ctx, "unread")
	if err != nil {
		return nil, err
	}

	var items []AttentionItem
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, n := range notifs {
		if n.Type == "suggestion" || n.Type == "approval_request" {
			action := s.activeActions[n.ID]
			if action == nil && n.ActionPayload != "" {
				// Reconstitute ProposedAction from SQLite payload
				var parsed ProposedAction
				if json.Unmarshal([]byte(n.ActionPayload), &parsed) == nil {
					action = &parsed
				}
			}
			items = append(items, AttentionItem{
				ID:            n.ID,
				ProposedAction: action,
				Reason:        n.Message,
				Severity:      n.Type,
				CreatedTime:   n.CreatedAt,
				Status:        n.Status,
			})
		}
	}

	return items, nil
}

// Approve marks a queued action as approved and schedules its execution immediately.
func (s *DefaultAttentionScheduler) Approve(ctx context.Context, attentionID string) error {
	err := notification.MarkRead(ctx, attentionID, "approved")
	if err != nil {
		return err
	}

	s.mu.RLock()
	action := s.activeActions[attentionID]
	s.mu.RUnlock()

	if action == nil {
		// Try to recover proposed action from SQLite
		notifs, err := notification.List(ctx, "")
		if err == nil {
			for _, n := range notifs {
				if n.ID == attentionID && n.ActionPayload != "" {
					var parsed ProposedAction
					if json.Unmarshal([]byte(n.ActionPayload), &parsed) == nil {
						action = &parsed
					}
					break
				}
			}
		}
	}

	if action == nil {
		return fmt.Errorf("attention item '%s' proposed action callback not found", attentionID)
	}

	// Execute action
	go s.executeActionAsync(action)

	return nil
}

// Reject marks a queued action as rejected.
func (s *DefaultAttentionScheduler) Reject(ctx context.Context, attentionID string, reason string) error {
	err := notification.MarkRead(ctx, attentionID, "dismissed")
	if err != nil {
		return err
	}

	s.mu.Lock()
	action := s.activeActions[attentionID]
	delete(s.activeActions, attentionID)
	s.mu.Unlock()

	if action != nil {
		s.emitResultEvent(action, "proactivity.action.rejected", reason)
	}

	return nil
}

// CancelActiveBackgroundActions cancels all running background contexts (preemption).
func (s *DefaultAttentionScheduler) CancelActiveBackgroundActions() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.activeCancels) == 0 {
		return
	}

	fmt.Fprintf(os.Stderr, "[Proactivity Preemption] Preempting %d running background actions due to foreground activity...\n", len(s.activeCancels))
	for id, cancel := range s.activeCancels {
		if cancel != nil {
			cancel()
		}
		delete(s.activeCancels, id)
	}
}

// eventIngestLoop translates telemetry events into scheduler events.
func (s *DefaultAttentionScheduler) eventIngestLoop() {
	if s.telemetrySub == nil {
		return
	}

	subCh := s.telemetrySub.Ch
	for {
		select {
		case <-s.ctx.Done():
			return
		case chunk, ok := <-subCh:
			if !ok {
				return
			}

			// Map stream chunks to scheduler Events
			ev := Event{
				ID:            fmt.Sprintf("ev_%d", time.Now().UnixNano()),
				Type:          chunk.Type,
				Source:        chunk.Source,
				Timestamp:     time.Now().Unix(),
				CorrelationID: chunk.TaskID,
				Payload:       chunk.Content,
			}

			// Translate executor statuses to standard scheduler event types
			switch chunk.Type {
			case "task_failed":
				ev.Type = "workflow.failed"
			case "node_failed":
				ev.Type = "tool.failed"
			case "cache_envelope_created":
				ev.Type = "memory.compaction_needed"
			}

			_ = s.SubmitEvent(s.ctx, ev)
		}
	}
}

// emitResultEvent broadcasts execution results to SSE StreamBus for GUI alerts.
func (s *DefaultAttentionScheduler) emitResultEvent(action *ProposedAction, eventType string, details string) {
	chunk := stream.StreamChunk{
		Source:  "proactivity_scheduler",
		Type:    eventType,
		TaskID:  action.TriggeringEventID,
		NodeID:  action.ID,
		Content: fmt.Sprintf("[%s] Daemon: %s | Action: %s — %s", action.Level, action.DaemonID, action.ActionType, details),
	}
	stream.GlobalBus.Publish(chunk)
}

func init() {
	// Initialize global default scheduler singleton
	GlobalScheduler = NewDefaultAttentionScheduler()
}
