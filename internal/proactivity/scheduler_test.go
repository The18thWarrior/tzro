package proactivity

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"tzro/internal/memory"
)

// SetupTestDB initializes a clean temporary SQLite database for test runs.
func SetupTestDB(t *testing.T) func() {
	oldDBPath := memory.DB.GetDBPathForTesting()
	dbName := "test_proactivity_scheduler.db"
	memory.DB.SetDBPathForTesting(dbName)

	_ = os.Remove(dbName)
	err := memory.DB.Init()
	if err != nil {
		t.Fatalf("Failed to initialize memory DB: %v", err)
	}

	return func() {
		_ = memory.DB.Close()
		_ = os.Remove(dbName)
		memory.DB.SetDBPathForTesting(oldDBPath)
	}
}

// MockDaemon helps test handler scenarios and resource limits.
type MockDaemon struct {
	name          string
	subs          []string
	maxLevel      ProactivityLevel
	reqBudget     Budget
	requiresLLM   bool
	handlerResult *ProposedAction
	handlerErr    error
	handlerCalls  int32
}

func (d *MockDaemon) Name() string                 { return d.name }
func (d *MockDaemon) Subscriptions() []string      { return d.subs }
func (d *MockDaemon) MaxLevel() ProactivityLevel   { return d.maxLevel }
func (d *MockDaemon) ResourceRequirements() Budget { return d.reqBudget }
func (d *MockDaemon) RequiresLLM() bool            { return d.requiresLLM }
func (d *MockDaemon) Handler(ctx context.Context, event Event) (*ProposedAction, error) {
	atomic.AddInt32(&d.handlerCalls, 1)
	return d.handlerResult, d.handlerErr
}

// Test1_L1DeterministicActionExecutes verifies that safe L1 actions execute automatically when budget permits.
func Test1_L1DeterministicActionExecutes(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	scheduler := NewDefaultAttentionScheduler()
	ctx := context.Background()
	_ = scheduler.Start(ctx)
	defer scheduler.Stop()

	var executed int32
	mockAction := &ProposedAction{
		ID:           "action_l1_test",
		Level:        L1Prepare,
		ActionType:   "test_action",
		RequiresLLM:  false,
		IsReversible: true,
		Execute: func(ctx context.Context) (string, error) {
			atomic.StoreInt32(&executed, 1)
			return "success", nil
		},
	}

	daemon := &MockDaemon{
		name:          "test_daemon",
		subs:          []string{"test.event"},
		maxLevel:      L1Prepare,
		handlerResult: mockAction,
	}

	_ = scheduler.RegisterDaemon(daemon)
	_ = scheduler.SubmitEvent(ctx, Event{ID: "ev_1", Type: "test.event"})

	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&executed) != 1 {
		t.Error("Expected L1 deterministic action to execute automatically without user approval")
	}
}

// Test2_L2SuggestionEnqueues verifies that L2 recommendations are enqueued in the attention queue.
func Test2_L2SuggestionEnqueues(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	scheduler := NewDefaultAttentionScheduler()
	ctx := context.Background()
	_ = scheduler.Start(ctx)
	defer scheduler.Stop()

	mockAction := &ProposedAction{
		ID:          "action_l2_test",
		Level:       L2Suggest,
		ActionType:  "test_suggestion",
		Description: "Verify config properties",
	}

	daemon := &MockDaemon{
		name:          "test_daemon",
		subs:          []string{"test.event"},
		maxLevel:      L2Suggest,
		handlerResult: mockAction,
	}

	_ = scheduler.RegisterDaemon(daemon)
	_ = scheduler.SubmitEvent(ctx, Event{ID: "ev_1", Type: "test.event"})

	time.Sleep(100 * time.Millisecond)

	items, err := scheduler.PendingAttention(ctx)
	if err != nil {
		t.Fatalf("Failed to fetch pending attention: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("Expected 1 pending suggestion in the attention queue, got %d", len(items))
	}
	if items[0].Severity != "suggestion" {
		t.Errorf("Expected item severity to be 'suggestion', got %s", items[0].Severity)
	}
}

// Test3_L4RequiresApproval verifies L4 external actions always require explicit user approval.
func Test3_L4RequiresApproval(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	scheduler := NewDefaultAttentionScheduler()
	ctx := context.Background()
	_ = scheduler.Start(ctx)
	defer scheduler.Stop()

	var executed int32
	mockAction := &ProposedAction{
		ID:          "action_l4_test",
		Level:       L4ExternalSideEffect,
		ActionType:  "external_side_effect",
		Description: "Commit codebase updates",
		Execute: func(ctx context.Context) (string, error) {
			atomic.StoreInt32(&executed, 1)
			return "committed", nil
		},
	}

	daemon := &MockDaemon{
		name:          "test_daemon",
		subs:          []string{"test.event"},
		maxLevel:      L4ExternalSideEffect,
		handlerResult: mockAction,
	}

	_ = scheduler.RegisterDaemon(daemon)
	_ = scheduler.SubmitEvent(ctx, Event{ID: "ev_1", Type: "test.event"})

	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&executed) == 1 {
		t.Fatal("L4 action executed immediately without user approval")
	}

	items, err := scheduler.PendingAttention(ctx)
	if err != nil {
		t.Fatalf("Failed to fetch pending attention: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("Expected 1 pending approval request, got %d", len(items))
	}
	if items[0].Severity != "approval_request" {
		t.Errorf("Expected item type to be 'approval_request', got %s", items[0].Severity)
	}
}

// Test4_ForegroundBlocksBackground verifies that active foreground tasks defer background executions.
func Test4_ForegroundBlocksBackground(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	scheduler := NewDefaultAttentionScheduler()
	ctx := context.Background()
	_ = scheduler.Start(ctx)
	defer scheduler.Stop()

	// Simulate active foreground task
	RegisterActiveUserTask("task_user_active")
	defer ClearActiveTasks()

	var executed int32
	mockAction := &ProposedAction{
		ID:           "action_l1_test",
		Level:        L1Prepare,
		ActionType:   "test_action",
		IsReversible: true,
		Execute: func(ctx context.Context) (string, error) {
			atomic.StoreInt32(&executed, 1)
			return "success", nil
		},
	}

	daemon := &MockDaemon{
		name:          "test_daemon",
		subs:          []string{"test.event"},
		maxLevel:      L1Prepare,
		handlerResult: mockAction,
	}

	_ = scheduler.RegisterDaemon(daemon)
	_ = scheduler.SubmitEvent(ctx, Event{ID: "ev_1", Type: "test.event"})

	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&executed) == 1 {
		t.Error("Background execution was not blocked by active foreground task")
	}
}

// Test5_BudgetExhaustionDefersAction verifies actions are blocked if they exceed the remaining budget.
func Test5_BudgetExhaustionDefersAction(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	scheduler := NewDefaultAttentionScheduler()
	ctx := context.Background()
	_ = scheduler.Start(ctx)
	defer scheduler.Stop()

	// Consumptive action that requests 2 LLM tokens, but daemon only has 1 max allowed token budget
	mockAction := &ProposedAction{
		ID:          "action_l1_test",
		Level:       L1Prepare,
		ActionType:  "test_action",
		RequiresLLM: true,
	}

	daemon := &MockDaemon{
		name: "test_daemon",
		subs: []string{"test.event"},
		reqBudget: Budget{
			MaxTokens: 5, // Daemon interval limit is 5 tokens
		},
		maxLevel:      L1Prepare,
		requiresLLM:   true,
		handlerResult: mockAction,
	}

	_ = scheduler.RegisterDaemon(daemon)

	// Consume budget manually
	scheduler.tracker.RecordUsage(daemon.Name(), 0, 5, 0) // exact 5 tokens consumed

	_ = scheduler.SubmitEvent(ctx, Event{ID: "ev_1", Type: "test.event"})

	time.Sleep(100 * time.Millisecond)

	// Action should be blocked/deferred due to budget limit
	items, err := scheduler.PendingAttention(ctx)
	if err != nil {
		t.Fatalf("Failed to fetch pending attention: %v", err)
	}

	if len(items) > 0 {
		t.Error("Action was enqueued or executed instead of being silently deferred/dropped under budget exhaustion")
	}
}

// Test6_DaemonSubscriptionMatching verifies daemons only wake for subscribed event types.
func Test6_DaemonSubscriptionMatching(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	scheduler := NewDefaultAttentionScheduler()
	ctx := context.Background()
	_ = scheduler.Start(ctx)
	defer scheduler.Stop()

	daemon := &MockDaemon{
		name:     "test_daemon",
		subs:     []string{"file.changed"},
		maxLevel: L1Prepare,
	}

	_ = scheduler.RegisterDaemon(daemon)

	// Submit an event not subscribed to
	_ = scheduler.SubmitEvent(ctx, Event{ID: "ev_1", Type: "user.idle"})
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&daemon.handlerCalls) != 0 {
		t.Error("Daemon was triggered by unsubscribed event type")
	}

	// Submit a subscribed event
	_ = scheduler.SubmitEvent(ctx, Event{ID: "ev_2", Type: "file.changed"})
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&daemon.handlerCalls) != 1 {
		t.Error("Daemon was not triggered by subscribed event type")
	}
}

// Test7_ObserverRepeatedWorkflowFailures verifies that ObserverDaemon suggests a smaller context retry after 2 failures.
func Test7_ObserverRepeatedWorkflowFailures(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	scheduler := NewDefaultAttentionScheduler()
	ctx := context.Background()
	_ = scheduler.Start(ctx)
	defer scheduler.Stop()

	obsDaemon := NewObserverDaemon()
	_ = scheduler.RegisterDaemon(obsDaemon)

	// Fail once
	_ = scheduler.SubmitEvent(ctx, Event{ID: "ev_fail_1", Type: "workflow.failed", CorrelationID: "task_123"})
	time.Sleep(50 * time.Millisecond)
	items, _ := scheduler.PendingAttention(ctx)
	if len(items) != 0 {
		t.Fatalf("Expected no L2 suggestions on single workflow failure, got %d", len(items))
	}

	// Fail twice
	_ = scheduler.SubmitEvent(ctx, Event{ID: "ev_fail_2", Type: "workflow.failed", CorrelationID: "task_123"})
	time.Sleep(100 * time.Millisecond)
	items, _ = scheduler.PendingAttention(ctx)
	if len(items) != 1 {
		t.Fatalf("Expected 1 L2 retry suggestion after repeated failures, got %d", len(items))
	}
	if items[0].ProposedAction.ActionType != "workflow_retry_suggestion" {
		t.Errorf("Expected ActionType workflow_retry_suggestion, got %s", items[0].ProposedAction.ActionType)
	}
}

// Test8_PrefetcherDoesNotRunDuringForeground verifies prefetcher doesn't execute when foreground is active.
func Test8_PrefetcherDoesNotRunDuringForeground(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	scheduler := NewDefaultAttentionScheduler()
	ctx := context.Background()
	_ = scheduler.Start(ctx)
	defer scheduler.Stop()

	// Simulate active foreground task
	RegisterActiveUserTask("task_active")
	defer ClearActiveTasks()

	prefetchDaemon := NewPrefetcherDaemon()
	_ = scheduler.RegisterDaemon(prefetchDaemon)

	_ = scheduler.SubmitEvent(ctx, Event{ID: "ev_1", Type: "user.idle"})
	time.Sleep(100 * time.Millisecond)

	// Check that no prefetch action was executed
	items, _ := scheduler.PendingAttention(ctx)
	if len(items) > 0 {
		t.Error("Prefetcher enqueued attention requests during active foreground task")
	}
}

// Test9_ApprovalFlowExecutes verifies that Approving an attention item runs the action.
func Test9_ApprovalFlowExecutes(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	scheduler := NewDefaultAttentionScheduler()
	ctx := context.Background()
	_ = scheduler.Start(ctx)
	defer scheduler.Stop()

	var executed int32
	mockAction := &ProposedAction{
		ID:          "action_l4_test",
		Level:       L4ExternalSideEffect,
		ActionType:  "test_action",
		Description: "Costly external operation",
		Execute: func(ctx context.Context) (string, error) {
			atomic.StoreInt32(&executed, 1)
			return "executed successfully", nil
		},
	}

	daemon := &MockDaemon{
		name:          "test_daemon",
		subs:          []string{"test.event"},
		maxLevel:      L4ExternalSideEffect,
		handlerResult: mockAction,
	}

	_ = scheduler.RegisterDaemon(daemon)
	_ = scheduler.SubmitEvent(ctx, Event{ID: "ev_1", Type: "test.event"})

	time.Sleep(100 * time.Millisecond)

	items, _ := scheduler.PendingAttention(ctx)
	if len(items) != 1 {
		t.Fatalf("Expected 1 pending attention item, got %d", len(items))
	}

	// Approve the item
	err := scheduler.Approve(ctx, items[0].ID)
	if err != nil {
		t.Fatalf("Failed to approve action: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&executed) != 1 {
		t.Error("Action was not executed after approval was granted")
	}
}

// Test10_RejectionFlowMarksRejected verifies that Rejecting an attention item cancels execution.
func Test10_RejectionFlowMarksRejected(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	scheduler := NewDefaultAttentionScheduler()
	ctx := context.Background()
	_ = scheduler.Start(ctx)
	defer scheduler.Stop()

	var executed int32
	mockAction := &ProposedAction{
		ID:          "action_l4_test",
		Level:       L4ExternalSideEffect,
		ActionType:  "test_action",
		Description: "Costly external operation",
		Execute: func(ctx context.Context) (string, error) {
			atomic.StoreInt32(&executed, 1)
			return "executed", nil
		},
	}

	daemon := &MockDaemon{
		name:          "test_daemon",
		subs:          []string{"test.event"},
		maxLevel:      L4ExternalSideEffect,
		handlerResult: mockAction,
	}

	_ = scheduler.RegisterDaemon(daemon)
	_ = scheduler.SubmitEvent(ctx, Event{ID: "ev_1", Type: "test.event"})

	time.Sleep(100 * time.Millisecond)

	items, _ := scheduler.PendingAttention(ctx)
	if len(items) != 1 {
		t.Fatalf("Expected 1 pending attention item, got %d", len(items))
	}

	// Reject the item
	err := scheduler.Reject(ctx, items[0].ID, "unnecessary action")
	if err != nil {
		t.Fatalf("Failed to reject action: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&executed) == 1 {
		t.Error("Action executed despite being rejected by user")
	}

	// Verify notification has dismissed status
	notifs, _ := memory.DB.GetNotifications("dismissed")
	if len(notifs) != 1 {
		t.Errorf("Expected 1 dismissed notification, got %d", len(notifs))
	}
}

// Test11_PreemptionAbortsBackground verifies that background execution is cancelled upon foreground startup.
func Test11_PreemptionAbortsBackground(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	scheduler := NewDefaultAttentionScheduler()
	ctx := context.Background()
	_ = scheduler.Start(ctx)
	defer scheduler.Stop()

	var executed int32
	var cancelled int32

	mockAction := &ProposedAction{
		ID:           "action_l1_long",
		Level:        L1Prepare,
		ActionType:   "test_action",
		IsReversible: true,
		Execute: func(ctx context.Context) (string, error) {
			select {
			case <-ctx.Done():
				atomic.StoreInt32(&cancelled, 1)
				return "", ctx.Err()
			case <-time.After(1 * time.Second):
				atomic.StoreInt32(&executed, 1)
				return "done", nil
			}
		},
	}

	daemon := &MockDaemon{
		name:          "test_daemon",
		subs:          []string{"test.event"},
		maxLevel:      L1Prepare,
		handlerResult: mockAction,
	}

	_ = scheduler.RegisterDaemon(daemon)
	_ = scheduler.SubmitEvent(ctx, Event{ID: "ev_1", Type: "test.event"})

	time.Sleep(50 * time.Millisecond)

	// Background action is currently sleeping. Trigger foreground preemption.
	RegisterActiveUserTask("foreground_task_999")
	defer ClearActiveTasks()

	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&executed) == 1 {
		t.Error("Background action completed execution instead of being preempted")
	}
	if atomic.LoadInt32(&cancelled) != 1 {
		t.Error("Background action did not receive cancel signal on foreground activation")
	}
}
