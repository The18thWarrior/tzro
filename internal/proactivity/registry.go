package proactivity

import (
	"context"
	"sync"
)

var (
	activeUserTasks = make(map[string]bool)
	registryMutex   sync.RWMutex

	preemptionCallbacks []func()
	resumeCallbacks     []func()
	callbacksMutex      sync.Mutex

	// foregroundCond is broadcast when the foreground registry empties.
	// Background callers block on this to wait for foreground to clear.
	foregroundCond = sync.NewCond(&foregroundCondMu)
	foregroundCondMu sync.Mutex
)

// RegisterActiveUserTask registers a task ID as a running user-initiated task.
// This triggers any registered preemption callbacks to cancel background work.
func RegisterActiveUserTask(taskID string) {
	if taskID == "" {
		return
	}
	registryMutex.Lock()
	activeUserTasks[taskID] = true
	registryMutex.Unlock()

	// Trigger preemption immediately
	TriggerPreemption()
}

// DeregisterActiveUserTask unregisters a user-initiated task ID upon completion or failure.
// When the foreground registry becomes empty, resume callbacks are triggered and
// blocked background callers are woken via the foreground condition variable.
func DeregisterActiveUserTask(taskID string) {
	if taskID == "" {
		return
	}
	registryMutex.Lock()
	delete(activeUserTasks, taskID)
	empty := len(activeUserTasks) == 0
	registryMutex.Unlock()

	if empty {
		// Wake all blocked background callers waiting for foreground to clear.
		foregroundCond.Broadcast()
		triggerResumeCallbacks()
	}
}

// IsForegroundActive returns true if there is at least one active user-initiated task.
func IsForegroundActive() bool {
	registryMutex.RLock()
	defer registryMutex.RUnlock()
	return len(activeUserTasks) > 0
}

// RegisterPreemptionCallback adds a callback to be executed when foreground activity starts.
func RegisterPreemptionCallback(cb func()) {
	callbacksMutex.Lock()
	defer callbacksMutex.Unlock()
	preemptionCallbacks = append(preemptionCallbacks, cb)
}

// TriggerPreemption runs all registered preemption callbacks.
func TriggerPreemption() {
	callbacksMutex.Lock()
	callbacks := make([]func(), len(preemptionCallbacks))
	copy(callbacks, preemptionCallbacks)
	callbacksMutex.Unlock()

	for _, cb := range callbacks {
		if cb != nil {
			cb()
		}
	}
}

// ActiveForegroundCount returns the number of currently active foreground tasks.
func ActiveForegroundCount() int {
	registryMutex.RLock()
	defer registryMutex.RUnlock()
	return len(activeUserTasks)
}

// WaitForForegroundClear blocks the calling goroutine until no foreground tasks
// are active, or the context is cancelled. Used by background task execution and
// inference paths to yield compute to foreground tasks.
//
// Returns nil when foreground clears, or ctx.Err() if cancelled.
func WaitForForegroundClear(ctx context.Context) error {
	foregroundCondMu.Lock()
	defer foregroundCondMu.Unlock()

	for IsForegroundActive() {
		// Wait with context cancellation support.
		// sync.Cond doesn't support context natively, so we use a goroutine
		// that cancels the wait when the context is done.
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				foregroundCond.Broadcast() // unblock the Wait()
			case <-done:
			}
		}()

		foregroundCond.Wait()
		close(done)

		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

// ClearActiveTasks clears the active user-initiated task map (mostly for testing).
func ClearActiveTasks() {
	registryMutex.Lock()
	activeUserTasks = make(map[string]bool)
	registryMutex.Unlock()
}

// RegisterResumeCallback adds a callback to be executed when foreground activity clears.
// Used to auto-resume interrupted dynamic workflows.
func RegisterResumeCallback(cb func()) {
	callbacksMutex.Lock()
	defer callbacksMutex.Unlock()
	resumeCallbacks = append(resumeCallbacks, cb)
}

// triggerResumeCallbacks runs all registered resume callbacks.
func triggerResumeCallbacks() {
	callbacksMutex.Lock()
	callbacks := make([]func(), len(resumeCallbacks))
	copy(callbacks, resumeCallbacks)
	callbacksMutex.Unlock()

	for _, cb := range callbacks {
		if cb != nil {
			cb()
		}
	}
}

// ClearCallbacks clears all preemption and resume callbacks (for testing).
func ClearCallbacks() {
	callbacksMutex.Lock()
	defer callbacksMutex.Unlock()
	preemptionCallbacks = nil
	resumeCallbacks = nil
}
