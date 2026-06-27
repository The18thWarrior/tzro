package executor

// retry.go — Dual retry policy for GBNF bridge/exec nodes (ADR-0020).
//
// Two retry triggers exist:
// 1. Schema validation: after local model extraction, validate required fields
//    are present and types match before calling the tool.
// 2. Tool execution failure: if the tool call itself fails, retry with cloud.
//
// On successful cloud retry, a corrective micro-skill is extracted to teach the
// local model how to avoid the same class of error in the future.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"tzro/internal/config"
	"tzro/internal/inference"
	"tzro/internal/tools"
)

// isCloudEscalationBlocked returns true when cloud retry/escalation must not
// be attempted. This covers both explicit privacy quarantine (strict-local)
// and ModelMode=local where no cloud tokens should be consumed at all.
func isCloudEscalationBlocked() bool {
	cfg := config.Get()
	return cfg.PrivacyLevel == "strict-local" || cfg.ModelMode == "local"
}

// validateAgainstSchema performs basic pre-flight validation of extracted tool arguments
// against the tool's JSON schema. Returns nil if valid, or a descriptive error.
func validateAgainstSchema(toolName string, args map[string]interface{}) error {
	schemaStr, err := tools.GetSchema(toolName)
	if err != nil {
		// If we can't get the schema, skip validation
		return nil
	}

	// Parse the schema to extract required fields
	var schema struct {
		Properties map[string]interface{} `json:"properties"`
		Required   []string               `json:"required"`
	}

	// The schema may be wrapped in a tool_arguments envelope
	var envelope struct {
		Properties struct {
			ToolArguments struct {
				Properties map[string]interface{} `json:"properties"`
				Required   []string               `json:"required"`
			} `json:"tool_arguments"`
		} `json:"properties"`
	}

	if err := json.Unmarshal([]byte(schemaStr), &envelope); err == nil && len(envelope.Properties.ToolArguments.Properties) > 0 {
		schema.Properties = envelope.Properties.ToolArguments.Properties
		schema.Required = envelope.Properties.ToolArguments.Required
	} else if err := json.Unmarshal([]byte(schemaStr), &schema); err != nil {
		// Can't parse schema — skip validation
		return nil
	}

	if len(schema.Required) == 0 {
		return nil
	}

	// Check required fields are present and non-empty
	var missing []string
	for _, field := range schema.Required {
		val, exists := args[field]
		if !exists {
			missing = append(missing, field)
			continue
		}
		// Check for zero-value strings
		if strVal, ok := val.(string); ok && strings.TrimSpace(strVal) == "" {
			missing = append(missing, field+" (empty)")
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("schema validation failed for %s: missing or empty required fields: %s",
			toolName, strings.Join(missing, ", "))
	}

	return nil
}

// retryWithCloud sends the same segmented messages to the cloud model for retry.
// Returns the cloud model's raw response string, or an error.
func retryWithCloud(ctx context.Context, messages []inference.InferenceMessage, schema string, taskID string) (string, error) {
	fmt.Fprintf(os.Stderr, "[RetryPolicy] Escalating to cloud for task %s\n", taskID)

	result, err := inference.CallCloudModel(ctx, messages, schema)
	if err != nil {
		return "", fmt.Errorf("cloud retry failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[RetryPolicy] Cloud retry succeeded for task %s (%d chars)\n", taskID, len(result))
	return result, nil
}
