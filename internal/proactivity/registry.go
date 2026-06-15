package proactivity

import (
	"sync"
)

var (
	activeUserTasks = make(map[string]bool)
	registryMutex   sync.RWMutex

	preemptionCallbacks []func()
	resumeCallbacks     []func()
	callbacksMutex      sync.Mutex
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
// When the foreground registry becomes empty, resume callbacks are triggered.
func DeregisterActiveUserTask(taskID string) {
	if taskID == "" {
		return
	}
	registryMutex.Lock()
	delete(activeUserTasks, taskID)
	empty := len(activeUserTasks) == 0
	registryMutex.Unlock()

	if empty {
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
