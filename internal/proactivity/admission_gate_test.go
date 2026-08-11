package proactivity

import (
	"context"
	"sync"
	"testing"
	"time"
)

// --- TDD Cycle 5: WaitForForegroundClear blocks until foreground clears ---

func TestWaitForForegroundClear_BlocksUntilClear(t *testing.T) {
	ClearActiveTasks()
	ClearCallbacks()
	defer ClearActiveTasks()
	defer ClearCallbacks()

	// Register a foreground task
	RegisterActiveUserTask("fg_task_1")

	var woke bool
	var mu sync.Mutex

	// Background goroutine waits for foreground to clear
	go func() {
		err := WaitForForegroundClear(context.Background())
		mu.Lock()
		woke = err == nil
		mu.Unlock()
	}()

	// Should still be blocked
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	if woke {
		mu.Unlock()
		t.Fatal("Expected WaitForForegroundClear to block while foreground is active")
	}
	mu.Unlock()

	// Clear foreground — should unblock
	DeregisterActiveUserTask("fg_task_1")
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if !woke {
		t.Error("Expected WaitForForegroundClear to unblock after foreground cleared")
	}
}

// --- TDD Cycle 6: WaitForForegroundClear respects context cancellation ---

func TestWaitForForegroundClear_RespectsContextCancel(t *testing.T) {
	ClearActiveTasks()
	ClearCallbacks()
	defer ClearActiveTasks()
	defer ClearCallbacks()

	RegisterActiveUserTask("fg_task_persist")
	defer DeregisterActiveUserTask("fg_task_persist")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := WaitForForegroundClear(ctx)
	if err == nil {
		t.Fatal("Expected WaitForForegroundClear to return context error, got nil")
	}
	if err != context.DeadlineExceeded {
		t.Fatalf("Expected DeadlineExceeded, got: %v", err)
	}
}

// --- TDD Cycle 7: WaitForForegroundClear returns immediately when no foreground ---

func TestWaitForForegroundClear_ImmediateWhenNoForeground(t *testing.T) {
	ClearActiveTasks()
	ClearCallbacks()
	defer ClearActiveTasks()
	defer ClearCallbacks()

	start := time.Now()
	err := WaitForForegroundClear(context.Background())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if elapsed > 10*time.Millisecond {
		t.Errorf("WaitForForegroundClear took too long (%v) when no foreground was active", elapsed)
	}
}

// --- TDD Cycle 8: ActiveForegroundCount returns correct count ---

func TestActiveForegroundCount(t *testing.T) {
	ClearActiveTasks()
	defer ClearActiveTasks()

	if c := ActiveForegroundCount(); c != 0 {
		t.Fatalf("Expected 0, got %d", c)
	}

	RegisterActiveUserTask("a")
	RegisterActiveUserTask("b")
	if c := ActiveForegroundCount(); c != 2 {
		t.Fatalf("Expected 2, got %d", c)
	}

	DeregisterActiveUserTask("a")
	if c := ActiveForegroundCount(); c != 1 {
		t.Fatalf("Expected 1, got %d", c)
	}
}

// --- TDD Cycle 9: Multiple background waiters all wake when foreground clears ---

func TestWaitForForegroundClear_MultipleWaiters(t *testing.T) {
	ClearActiveTasks()
	ClearCallbacks()
	defer ClearActiveTasks()
	defer ClearCallbacks()

	RegisterActiveUserTask("fg_multi")

	var wg sync.WaitGroup
	var mu sync.Mutex
	wokeCount := 0

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := WaitForForegroundClear(context.Background()); err == nil {
				mu.Lock()
				wokeCount++
				mu.Unlock()
			}
		}()
	}

	time.Sleep(20 * time.Millisecond)
	DeregisterActiveUserTask("fg_multi")

	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if wokeCount != 5 {
		t.Errorf("Expected all 5 waiters to wake, got %d", wokeCount)
	}
}
