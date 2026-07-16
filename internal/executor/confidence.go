package executor

// confidence.go — Confidence Tier pre-flight assessment (ADR-0020).
//
// Before dispatching a GBNF bridge/exec inference to the local model, the
// Confidence Tier asks the local model a lightweight self-assessment question:
// "Can you extract all required parameters from the accumulated context for
// the given schema?" If the local model answers "insufficient", the node
// escalates to the cloud model. Consecutive insufficient results trigger
// a sticky cloud fallback for the remainder of the task.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"tzro/internal/config"
	"tzro/internal/inference"
)

// ConfidenceSchema is the GBNF-constrained JSON schema for the self-assessment.
const ConfidenceSchema = `{
	"type": "object",
	"properties": {
		"confidence": {
			"type": "string",
			"enum": ["sufficient", "insufficient"]
		},
		"reason": {
			"type": "string"
		}
	},
	"required": ["confidence", "reason"]
}`

// confidenceState tracks per-task consecutive insufficient confidence assessments.
type confidenceState struct {
	mu               sync.Mutex
	consecutiveFails map[string]int  // taskID → consecutive insufficient count
	forceCloudByTask map[string]bool // taskID → sticky cloud fallback active
}

var globalConfidenceState = &confidenceState{
	consecutiveFails: make(map[string]int),
	forceCloudByTask: make(map[string]bool),
}

// IsForceCloud returns true if the given task has triggered sticky cloud fallback
// due to exceeding the confidence threshold.
func IsForceCloud(taskID string) bool {
	if config.Get().PrivacyLevel == "strict-local" {
		return false
	}
	globalConfidenceState.mu.Lock()
	defer globalConfidenceState.mu.Unlock()
	return globalConfidenceState.forceCloudByTask[taskID]
}

// assessConfidenceTier runs a lightweight pre-flight self-assessment against the
// local model. It appends a confidence assessment question to the existing segmented
// messages and dispatches via ExecuteStructured with ConfidenceSchema.
//
// Returns (true, "") if the local model is confident, (false, reason) otherwise.
func assessConfidenceTier(ctx context.Context, messages []inference.InferenceMessage, schema string, taskID string) (bool, string) {
	// Build assessment prompt by appending the self-assessment question
	assessMsgs := make([]inference.InferenceMessage, len(messages))
	copy(assessMsgs, messages)

	assessMsgs = append(assessMsgs, inference.InferenceMessage{
		Role: "user",
		Content: "Before extracting parameters, assess your capability: " +
			"Can you extract ALL required parameters from the accumulated context " +
			"for the given schema? Consider whether you have sufficient information " +
			"to fill every required field accurately. " +
			"Return your assessment as JSON matching the confidence schema.",
	})

	req := inference.StructuredInferenceRequest{
		Messages:   assessMsgs,
		JSONSchema: ConfidenceSchema,
		TaskID:     taskID,
	}

	result, err := inference.ExecuteRouterStructured(ctx, req)
	if err != nil {
		// On assessment failure, assume sufficient to avoid blocking execution
		fmt.Fprintf(os.Stderr, "[ConfidenceTier] Assessment call failed for task %s: %v — defaulting to sufficient\n", taskID, err)
		return true, ""
	}

	var assessment struct {
		Confidence string `json:"confidence"`
		Reason     string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(result), &assessment); err != nil {
		fmt.Fprintf(os.Stderr, "[ConfidenceTier] Failed to parse assessment for task %s: %v — defaulting to sufficient\n", taskID, err)
		return true, ""
	}

	sufficient := assessment.Confidence == "sufficient"
	fmt.Fprintf(os.Stderr, "[ConfidenceTier] Task %s assessment: %s (reason: %s)\n", taskID, assessment.Confidence, assessment.Reason)
	return sufficient, assessment.Reason
}

// checkAndUpdateConfidence updates the consecutive confidence failure counter for
// a task. If the counter exceeds the configured threshold, sticky cloud fallback
// is activated. A successful assessment resets the counter.
func checkAndUpdateConfidence(taskID string, sufficient bool) {
	globalConfidenceState.mu.Lock()
	defer globalConfidenceState.mu.Unlock()

	if sufficient {
		// Reset on success
		globalConfidenceState.consecutiveFails[taskID] = 0
		return
	}

	// Increment consecutive failures
	globalConfidenceState.consecutiveFails[taskID]++
	count := globalConfidenceState.consecutiveFails[taskID]
	threshold := config.GetConfidenceThreshold()

	if count >= threshold {
		globalConfidenceState.forceCloudByTask[taskID] = true
		fmt.Fprintf(os.Stderr, "[ConfidenceTier] Task %s reached %d consecutive insufficient assessments (threshold: %d) — activating sticky cloud fallback\n",
			taskID, count, threshold)
	}
}
