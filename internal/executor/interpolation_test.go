package executor

import (
	"os"
	"strings"
	"testing"

	"tzro/internal/memory"
)

// TestInterpolateVariables_SCTNodeResolution validates the exact failure path
// from the HR onboarding benchmark: when a planner template references
// {{nodes.node_2.output.employee_email}}, does GetNodeStateTolerant find
// node_2_exec (which has the tool response) or node_2_validator (which only
// has extracted input arguments)?
//
// This test simulates the SCT-expanded node layout WITHOUT running the full
// execution engine, isolating the interpolation and tolerant-lookup logic.
func TestInterpolateVariables_SCTNodeResolution(t *testing.T) {
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_interpolation.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_interpolation.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
		_ = memory.DB.Init()
	}()
	_ = memory.DB.Init()

	taskID := "task-hr-onboarding-diag"

	// --- Scenario 1: Both validator and exec completed ---
	// This is the expected happy path after SCT expansion.
	// node_2_validator stores extracted INPUT args (name, background_pass_code).
	// node_2_exec stores the tool RESPONSE (employee_id, employee_email, etc).
	t.Run("ExecCompleted_ResolvesFromExecRawOutput", func(t *testing.T) {
		// Validator output: extracted arguments (does NOT contain employee_email)
		validatorOutput := `{"background_pass_code": "BG-PASS-2256", "name": "Mao Zedong"}`
		_ = memory.DB.SetNodeState(taskID, "node_2_validator", "completed", "[Local Tactician] "+validatorOutput)
		_ = memory.DB.SetNodeRawOutput(taskID, "node_2_validator", validatorOutput)

		// Exec output: tool response (DOES contain employee_email)
		execToolResponse := `{"status": "cleared", "bg_pass_code": "BG-PASS-2256", "employee_id": "EMP-5581", "employee_email": "maozedong@enterprise.corp", "license_status": "provisioned", "contract_id": "CONTRACT-hr_onboarding_89"}`
		_ = memory.DB.SetNodeState(taskID, "node_2_exec", "completed", "[Local Tactician] "+execToolResponse)
		_ = memory.DB.SetNodeRawOutput(taskID, "node_2_exec", execToolResponse)

		// Test: Does {{nodes.node_2.output.employee_email}} resolve?
		instruction := "Dispatch welcome email to {{nodes.node_2.output.employee_email}}"
		result := InterpolateVariables(instruction, taskID)

		t.Logf("Interpolated result: %s", result)

		if strings.Contains(result, "null") {
			t.Errorf("FAILURE: employee_email resolved to null. GetNodeStateTolerant('node_2') did not find exec node's RawOutput.")
		}
		if !strings.Contains(result, "maozedong@enterprise.corp") {
			t.Errorf("Expected 'maozedong@enterprise.corp' in result, got: %s", result)
		}

		// Also verify which node GetNodeStateTolerant actually returns
		state, ok := GetNodeStateTolerant(taskID, "node_2")
		if !ok {
			t.Fatal("GetNodeStateTolerant('node_2') returned not-found")
		}
		t.Logf("GetNodeStateTolerant('node_2') returned nodeID from state: status=%s, RawOutput length=%d", state.Status, len(state.RawOutput))
		if !strings.Contains(state.RawOutput, "employee_email") {
			t.Errorf("GetNodeStateTolerant returned a node state WITHOUT employee_email. RawOutput: %s", state.RawOutput)
		}
	})

	// --- Scenario 2: Only validator completed, exec NOT completed ---
	// This simulates a timing issue or exec failure. The validator's output
	// has extracted args but NOT the tool response fields.
	t.Run("OnlyValidatorCompleted_ResolvesToNull", func(t *testing.T) {
		taskID2 := "task-hr-onboarding-diag-2"

		validatorOutput := `{"background_pass_code": "BG-PASS-2256", "name": "Mao Zedong"}`
		_ = memory.DB.SetNodeState(taskID2, "node_2_validator", "completed", "[Local Tactician] "+validatorOutput)
		_ = memory.DB.SetNodeRawOutput(taskID2, "node_2_validator", validatorOutput)

		// node_2_exec is NOT set (still pending or doesn't exist)

		instruction := "Dispatch welcome email to {{nodes.node_2.output.employee_email}}"
		result := InterpolateVariables(instruction, taskID2)

		t.Logf("Interpolated result (validator-only): %s", result)

		// GetNodeStateTolerant should try node_2_exec first, fail, then fall back
		state, ok := GetNodeStateTolerant(taskID2, "node_2")
		if ok {
			t.Logf("GetNodeStateTolerant returned: status=%s, RawOutput=%s", state.Status, state.RawOutput)
			if strings.Contains(state.RawOutput, "employee_email") {
				t.Error("Unexpected: found employee_email in validator-only state")
			}
		} else {
			t.Log("GetNodeStateTolerant('node_2') returned not-found (no exec or direct match)")
		}

		// employee_email should NOT resolve (expected: null)
		if strings.Contains(result, "maozedong@enterprise.corp") {
			t.Error("Unexpected: employee_email resolved from validator-only state")
		}
	})

	// --- Scenario 3: Exec completed but employee_email field missing ---
	// Simulates a tool response that genuinely doesn't contain the referenced field.
	t.Run("ExecCompleted_FieldMissing_ResolvesToNull", func(t *testing.T) {
		taskID3 := "task-hr-onboarding-diag-3"

		// Tool response without employee_email
		execToolResponse := `{"status": "cleared", "bg_pass_code": "BG-PASS-2256", "employee_id": "EMP-5581"}`
		_ = memory.DB.SetNodeState(taskID3, "node_2_exec", "completed", "[Local Tactician] "+execToolResponse)
		_ = memory.DB.SetNodeRawOutput(taskID3, "node_2_exec", execToolResponse)

		instruction := "Dispatch welcome email to {{nodes.node_2.output.employee_email}}"
		result := InterpolateVariables(instruction, taskID3)

		t.Logf("Interpolated result (field-missing): %s", result)

		if !strings.Contains(result, "null") {
			t.Errorf("Expected null for missing field, got: %s", result)
		}
	})

	// --- Scenario 4: Verify employee_id resolves when email doesn't ---
	// Reproduces the benchmark observation: employee_id resolves, employee_email doesn't.
	t.Run("PartialFieldResolution", func(t *testing.T) {
		taskID4 := "task-hr-onboarding-diag-4"

		// Tool response with employee_id but NOT employee_email
		execToolResponse := `{"status": "cleared", "employee_id": "EMP-5581"}`
		_ = memory.DB.SetNodeState(taskID4, "node_2_exec", "completed", "[Local Tactician] "+execToolResponse)
		_ = memory.DB.SetNodeRawOutput(taskID4, "node_2_exec", execToolResponse)

		instruction := "Provision for employee {{nodes.node_2.output.employee_id}} and email {{nodes.node_2.output.employee_email}}"
		result := InterpolateVariables(instruction, taskID4)

		t.Logf("Interpolated result (partial): %s", result)

		if !strings.Contains(result, "EMP-5581") {
			t.Errorf("Expected employee_id to resolve to EMP-5581, got: %s", result)
		}
		if !strings.Contains(result, "null") {
			t.Errorf("Expected employee_email to be null (field absent), got: %s", result)
		}
	})
}
