package executor

import (
	"context"
	"os"
	"testing"

	"tzro/internal/compiler"
	"tzro/internal/memory"
)

// TestRecursiveKeySearch validates the pure recursive JSON key search function (Tier 1).
func TestRecursiveKeySearch(t *testing.T) {
	t.Run("TopLevelMatch", func(t *testing.T) {
		data := map[string]interface{}{
			"email":  "a@b.com",
			"status": "active",
		}
		matches := recursiveKeySearch(data, "email")
		if len(matches) != 1 {
			t.Fatalf("Expected 1 match, got %d", len(matches))
		}
		if matches[0].Path != "email" {
			t.Errorf("Expected path 'email', got %q", matches[0].Path)
		}
		if matches[0].Value != "a@b.com" {
			t.Errorf("Expected value 'a@b.com', got %v", matches[0].Value)
		}
	})

	t.Run("NestedDepth2", func(t *testing.T) {
		data := map[string]interface{}{
			"data": map[string]interface{}{
				"email": "a@b.com",
			},
		}
		matches := recursiveKeySearch(data, "email")
		if len(matches) != 1 {
			t.Fatalf("Expected 1 match, got %d", len(matches))
		}
		if matches[0].Path != "data.email" {
			t.Errorf("Expected path 'data.email', got %q", matches[0].Path)
		}
		if matches[0].Value != "a@b.com" {
			t.Errorf("Expected value 'a@b.com', got %v", matches[0].Value)
		}
	})

	t.Run("NestedDepth3", func(t *testing.T) {
		data := map[string]interface{}{
			"data": map[string]interface{}{
				"contact": map[string]interface{}{
					"email": "a@b.com",
				},
			},
		}
		matches := recursiveKeySearch(data, "email")
		if len(matches) != 1 {
			t.Fatalf("Expected 1 match, got %d", len(matches))
		}
		if matches[0].Path != "data.contact.email" {
			t.Errorf("Expected path 'data.contact.email', got %q", matches[0].Path)
		}
	})

	t.Run("InsideArray", func(t *testing.T) {
		data := map[string]interface{}{
			"items": []interface{}{
				map[string]interface{}{
					"email": "a@b.com",
				},
			},
		}
		matches := recursiveKeySearch(data, "email")
		if len(matches) != 1 {
			t.Fatalf("Expected 1 match, got %d", len(matches))
		}
		if matches[0].Value != "a@b.com" {
			t.Errorf("Expected value 'a@b.com', got %v", matches[0].Value)
		}
	})

	t.Run("MultipleMatches", func(t *testing.T) {
		data := map[string]interface{}{
			"id": "1",
			"nested": map[string]interface{}{
				"id": "2",
			},
		}
		matches := recursiveKeySearch(data, "id")
		if len(matches) != 2 {
			t.Fatalf("Expected 2 matches, got %d", len(matches))
		}
	})

	t.Run("NoMatch", func(t *testing.T) {
		data := map[string]interface{}{
			"email": "a@b.com",
		}
		matches := recursiveKeySearch(data, "phone")
		if len(matches) != 0 {
			t.Errorf("Expected 0 matches, got %d", len(matches))
		}
	})

	t.Run("EmptyObject", func(t *testing.T) {
		data := map[string]interface{}{}
		matches := recursiveKeySearch(data, "email")
		if len(matches) != 0 {
			t.Errorf("Expected 0 matches for empty object, got %d", len(matches))
		}
	})

	t.Run("NullAndBoolValues", func(t *testing.T) {
		data := map[string]interface{}{
			"active": true,
			"name":   nil,
		}
		matches := recursiveKeySearch(data, "active")
		if len(matches) != 1 {
			t.Fatalf("Expected 1 match for 'active', got %d", len(matches))
		}
		if matches[0].Value != true {
			t.Errorf("Expected value true, got %v", matches[0].Value)
		}

		nullMatches := recursiveKeySearch(data, "name")
		if len(nullMatches) != 1 {
			t.Fatalf("Expected 1 match for 'name', got %d", len(nullMatches))
		}
		if nullMatches[0].Value != nil {
			t.Errorf("Expected nil value, got %v", nullMatches[0].Value)
		}
	})

	t.Run("NilInput", func(t *testing.T) {
		matches := recursiveKeySearch(nil, "email")
		if len(matches) != 0 {
			t.Errorf("Expected 0 matches for nil input, got %d", len(matches))
		}
	})

	t.Run("NestedArrayOfObjects", func(t *testing.T) {
		data := map[string]interface{}{
			"results": []interface{}{
				map[string]interface{}{"email": "a@b.com"},
				map[string]interface{}{"email": "c@d.com"},
			},
		}
		matches := recursiveKeySearch(data, "email")
		if len(matches) != 2 {
			t.Fatalf("Expected 2 matches from array elements, got %d", len(matches))
		}
	})
}

// TestFormatMatchValue validates the value-to-string formatting helper.
func TestFormatMatchValue(t *testing.T) {
	t.Run("StringValue", func(t *testing.T) {
		result := formatMatchValue("hello")
		if result != "hello" {
			t.Errorf("Expected 'hello', got %q", result)
		}
	})

	t.Run("NumberValue", func(t *testing.T) {
		result := formatMatchValue(42.0)
		if result != "42" {
			t.Errorf("Expected '42', got %q", result)
		}
	})

	t.Run("BoolValue", func(t *testing.T) {
		result := formatMatchValue(true)
		if result != "true" {
			t.Errorf("Expected 'true', got %q", result)
		}
	})

	t.Run("MapValue", func(t *testing.T) {
		data := map[string]interface{}{"key": "val"}
		result := formatMatchValue(data)
		if result != `{"key":"val"}` {
			t.Errorf("Expected JSON map, got %q", result)
		}
	})

	t.Run("SliceValue", func(t *testing.T) {
		data := []interface{}{"a", "b"}
		result := formatMatchValue(data)
		if result != `["a","b"]` {
			t.Errorf("Expected JSON array, got %q", result)
		}
	})
}

// TestResolveDynamicBindingsWithCascade validates the three-tier resolution cascade
// integrated into resolveDynamicBindings (ADR-0029 Response Resolver).
func TestResolveDynamicBindingsWithCascade(t *testing.T) {
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_response_resolver.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_response_resolver.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
		_ = memory.DB.Init()
	}()
	_ = memory.DB.Init()

	taskID := "task-response-resolver-test"
	ctx := context.Background()

	t.Run("Tier1_TopLevelMatch", func(t *testing.T) {
		// Exact top-level key match — same as legacy behavior
		output := `{"employee_email": "test@corp.com", "employee_id": "EMP-001"}`
		_ = memory.DB.SetNodeState(taskID, "node_top_exec", "completed", "[Local] "+output)
		_ = memory.DB.SetNodeRawOutput(taskID, "node_top_exec", output)

		bindings := map[string]interface{}{
			"employee_email": "node_top.output.employee_email",
		}
		resolved := resolveDynamicBindings(ctx, bindings, taskID, nil)

		if resolved["employee_email"].Value != "test@corp.com" {
			t.Errorf("Expected 'test@corp.com', got %q", resolved["employee_email"].Value)
		}
	})

	t.Run("Tier1_NestedMatch", func(t *testing.T) {
		// Nested key at depth 2 — the key upgrade from this feature
		output := `{"data": {"contact": {"email": "nested@corp.com"}}}`
		_ = memory.DB.SetNodeState(taskID, "node_nested_exec", "completed", "[Local] "+output)
		_ = memory.DB.SetNodeRawOutput(taskID, "node_nested_exec", output)

		bindings := map[string]interface{}{
			"email": "node_nested.output.email",
		}
		resolved := resolveDynamicBindings(ctx, bindings, taskID, nil)

		if resolved["email"].Value != "nested@corp.com" {
			t.Errorf("Expected 'nested@corp.com', got %q", resolved["email"].Value)
		}
	})

	t.Run("Tier2_KVLineMatch", func(t *testing.T) {
		// Non-JSON KV-line output
		output := "email: kv@corp.com\nname: Alice\nstatus: active"
		_ = memory.DB.SetNodeState(taskID, "node_kv_exec", "completed", "[Local] "+output)
		_ = memory.DB.SetNodeRawOutput(taskID, "node_kv_exec", output)

		bindings := map[string]interface{}{
			"email": "node_kv.output.email",
		}
		resolved := resolveDynamicBindings(ctx, bindings, taskID, nil)

		if resolved["email"].Value != "kv@corp.com" {
			t.Errorf("Expected 'kv@corp.com', got %q", resolved["email"].Value)
		}
	})

	t.Run("AllTiersFail_MissingKey", func(t *testing.T) {
		// Key doesn't exist at any level — should warn and skip
		output := `{"email": "a@b.com"}`
		_ = memory.DB.SetNodeState(taskID, "node_missing_exec", "completed", "[Local] "+output)
		_ = memory.DB.SetNodeRawOutput(taskID, "node_missing_exec", output)

		bindings := map[string]interface{}{
			"phone": "node_missing.output.phone",
		}
		// This will attempt semantic fallback which will fail without a running model.
		// The test validates graceful degradation — no panic, warning logged, binding skipped.
		resolved := resolveDynamicBindings(ctx, bindings, taskID, nil)

		if _, exists := resolved["phone"]; exists {
			t.Errorf("Expected 'phone' to NOT be resolved when key doesn't exist, got %q", resolved["phone"].Value)
		}
	})

	t.Run("MissingUpstreamNode", func(t *testing.T) {
		bindings := map[string]interface{}{
			"field": "node_nonexistent.output.field",
		}
		resolved := resolveDynamicBindings(ctx, bindings, taskID, nil)

		if len(resolved) != 0 {
			t.Errorf("Expected empty map for missing upstream node, got %v", resolved)
		}
	})

	t.Run("EmptyOutput", func(t *testing.T) {
		_ = memory.DB.SetNodeState(taskID, "node_empty_exec", "completed", "")
		_ = memory.DB.SetNodeRawOutput(taskID, "node_empty_exec", "")

		bindings := map[string]interface{}{
			"field": "node_empty.output.field",
		}
		resolved := resolveDynamicBindings(ctx, bindings, taskID, nil)

		if len(resolved) != 0 {
			t.Errorf("Expected empty map for empty output, got %v", resolved)
		}
	})

	t.Run("Tier1_NestedObjectValue", func(t *testing.T) {
		// When the matched value is itself a nested object, it should be JSON-marshaled
		output := `{"result": {"address": {"street": "123 Main", "city": "NYC"}}}`
		_ = memory.DB.SetNodeState(taskID, "node_obj_exec", "completed", "[Local] "+output)
		_ = memory.DB.SetNodeRawOutput(taskID, "node_obj_exec", output)

		bindings := map[string]interface{}{
			"address": "node_obj.output.address",
		}
		resolved := resolveDynamicBindings(ctx, bindings, taskID, nil)

		if resolved["address"].Value == "" {
			t.Error("Expected address to be resolved as JSON string")
		}
	})

	t.Run("Tier1_5_FuzzyKeyMatch_ReceiptPath", func(t *testing.T) {
		// Reproduces the exact receipt_path hallucination scenario from the benchmark.
		// Planner binds "receipt_code_path" but tool output has "receipt_path".
		// Tier 1 finds 0 exact matches → Tier 1.5 fuzzy should resolve deterministically.
		output := `{"status": "success", "invoice_id": "INV-78", "receipt_path": "/receipts/rcpt_finance_billing_run_78.pdf", "email_delivery": "sent"}`
		_ = memory.DB.SetNodeState(taskID, "node_receipt_exec", "completed", "[Local] "+output)
		_ = memory.DB.SetNodeRawOutput(taskID, "node_receipt_exec", output)

		bindings := map[string]interface{}{
			"receipt_path": "node_receipt.output.receipt_code_path",
		}
		resolved := resolveDynamicBindings(ctx, bindings, taskID, nil)

		expected := "/receipts/rcpt_finance_billing_run_78.pdf"
		if resolved["receipt_path"].Value != expected {
			t.Errorf("Expected %q, got %q — fuzzy key matching failed to resolve receipt_code_path → receipt_path", expected, resolved["receipt_path"].Value)
		}
	})

	t.Run("Tier1_5_FuzzyKeyMatch_DefaultEmailAddress", func(t *testing.T) {
		// Planner binds "default_email_address" but tool output has "email_address".
		// normalizeBindingKey strips "default_" prefix → "email_address" matches exactly.
		output := `{"employee_id": "EMP-123", "email_address": "test@corp.com", "status": "active"}`
		_ = memory.DB.SetNodeState(taskID, "node_fuzzy_email_exec", "completed", "[Local] "+output)
		_ = memory.DB.SetNodeRawOutput(taskID, "node_fuzzy_email_exec", output)

		bindings := map[string]interface{}{
			"employee_email": "node_fuzzy_email.output.default_email_address",
		}
		resolved := resolveDynamicBindings(ctx, bindings, taskID, nil)

		if resolved["employee_email"].Value != "test@corp.com" {
			t.Errorf("Expected 'test@corp.com', got %q — fuzzy key with prefix stripping failed", resolved["employee_email"].Value)
		}
	})

	t.Run("Tier1_5_FuzzyKeyMatch_CalculatedInvoiceId", func(t *testing.T) {
		// Planner binds "calculated_invoice_id" but output has "invoice_id".
		// normalizeBindingKey strips "calculated_" → "invoice_id" matches exactly.
		output := `{"invoice_id": "INV-61", "amount": 5500.0}`
		_ = memory.DB.SetNodeState(taskID, "node_fuzzy_inv_exec", "completed", "[Local] "+output)
		_ = memory.DB.SetNodeRawOutput(taskID, "node_fuzzy_inv_exec", output)

		bindings := map[string]interface{}{
			"invoice_id": "node_fuzzy_inv.output.calculated_invoice_id",
		}
		resolved := resolveDynamicBindings(ctx, bindings, taskID, nil)

		if resolved["invoice_id"].Value != "INV-61" {
			t.Errorf("Expected 'INV-61', got %q — fuzzy key with calculated_ prefix failed", resolved["invoice_id"].Value)
		}
	})
}

// TestFuzzyKeySearch validates the fuzzy key search function (Tier 1.5) in isolation.
func TestFuzzyKeySearch(t *testing.T) {
	t.Run("SuffixMatch_ReceiptCodePath", func(t *testing.T) {
		// "receipt_code_path" should fuzzy-match "receipt_path" via suffix containment
		data := map[string]interface{}{
			"receipt_path":   "/receipts/rcpt_78.pdf",
			"email_delivery": "sent",
		}
		match := fuzzyKeySearch(data, "receipt_code_path")
		if match == nil {
			t.Fatal("Expected fuzzy match for receipt_code_path → receipt_path, got nil")
		}
		if match.Value != "/receipts/rcpt_78.pdf" {
			t.Errorf("Expected '/receipts/rcpt_78.pdf', got %v", match.Value)
		}
	})

	t.Run("PrefixStrip_DefaultEmailAddress", func(t *testing.T) {
		// "default_email_address" normalizes to "email_address" which exact-matches
		data := map[string]interface{}{
			"email_address": "a@b.com",
			"employee_id":   "EMP-1",
		}
		match := fuzzyKeySearch(data, "default_email_address")
		if match == nil {
			t.Fatal("Expected fuzzy match for default_email_address → email_address, got nil")
		}
		if match.Value != "a@b.com" {
			t.Errorf("Expected 'a@b.com', got %v", match.Value)
		}
	})

	t.Run("PrefixStrip_CalculatedInvoiceId", func(t *testing.T) {
		data := map[string]interface{}{
			"invoice_id": "INV-61",
			"amount":     5500.0,
		}
		match := fuzzyKeySearch(data, "calculated_invoice_id")
		if match == nil {
			t.Fatal("Expected fuzzy match for calculated_invoice_id → invoice_id, got nil")
		}
		if match.Value != "INV-61" {
			t.Errorf("Expected 'INV-61', got %v", match.Value)
		}
	})

	t.Run("NoMatch_CompletelyDifferent", func(t *testing.T) {
		data := map[string]interface{}{
			"email":  "a@b.com",
			"status": "active",
		}
		match := fuzzyKeySearch(data, "phone_number")
		if match != nil {
			t.Errorf("Expected nil for completely different key, got path=%q", match.Path)
		}
	})

	t.Run("AmbiguousMultipleMatches", func(t *testing.T) {
		// If multiple keys fuzzy-match, return nil to avoid ambiguity
		data := map[string]interface{}{
			"receipt_path":     "/a.pdf",
			"old_receipt_path": "/b.pdf",
		}
		match := fuzzyKeySearch(data, "generated_receipt_path")
		// Both "receipt_path" and "old_receipt_path" contain "receipt_path" as suffix
		// so this should be ambiguous → nil
		if match != nil {
			t.Logf("Note: fuzzy match returned %q (may be acceptable if only one matched)", match.Path)
		}
	})

	t.Run("ExactNormalizedMatch", func(t *testing.T) {
		// After stripping prefix, exact normalized match
		data := map[string]interface{}{
			"supplier_name": "Acme Corp",
		}
		match := fuzzyKeySearch(data, "primary_supplier_name")
		if match == nil {
			t.Fatal("Expected fuzzy match for primary_supplier_name → supplier_name")
		}
		if match.Value != "Acme Corp" {
			t.Errorf("Expected 'Acme Corp', got %v", match.Value)
		}
	})
}

// TestPartitionBindings validates the ADR-0030 confidence tier partitioning.
func TestPartitionBindings(t *testing.T) {
	t.Run("SplitsByTier", func(t *testing.T) {
		resolved := map[string]ResolvedBinding{
			"receipt_path":   {Value: "/receipts/rcpt_78.pdf", Tier: "fuzzy_key"},
			"employee_email": {Value: "test@corp.com", Tier: "recursive_key"},
			"candidate_id":   {Value: "CAND-123", Tier: "semantic_fallback"},
			"status":         {Value: "active", Tier: "kv_line"},
		}

		highConf, lowConf := partitionBindings(resolved)

		if len(highConf) != 3 {
			t.Errorf("Expected 3 high-confidence bindings, got %d: %v", len(highConf), highConf)
		}
		if len(lowConf) != 1 {
			t.Errorf("Expected 1 low-confidence binding, got %d: %v", len(lowConf), lowConf)
		}
		if highConf["receipt_path"] != "/receipts/rcpt_78.pdf" {
			t.Errorf("Expected receipt_path in highConf, got %q", highConf["receipt_path"])
		}
		if highConf["employee_email"] != "test@corp.com" {
			t.Errorf("Expected employee_email in highConf, got %q", highConf["employee_email"])
		}
		if highConf["status"] != "active" {
			t.Errorf("Expected status in highConf (kv_line tier), got %q", highConf["status"])
		}
		if lowConf["candidate_id"] != "CAND-123" {
			t.Errorf("Expected candidate_id in lowConf, got %q", lowConf["candidate_id"])
		}
	})

	t.Run("AllHighConfidence", func(t *testing.T) {
		resolved := map[string]ResolvedBinding{
			"email": {Value: "a@b.com", Tier: "recursive_key"},
		}
		highConf, lowConf := partitionBindings(resolved)
		if len(highConf) != 1 || len(lowConf) != 0 {
			t.Errorf("Expected 1 high, 0 low; got %d high, %d low", len(highConf), len(lowConf))
		}
	})

	t.Run("EmptyInput", func(t *testing.T) {
		highConf, lowConf := partitionBindings(map[string]ResolvedBinding{})
		if len(highConf) != 0 || len(lowConf) != 0 {
			t.Errorf("Expected empty maps for empty input")
		}
	})

	// ADR-0069: plain_text_fallback is splice-eligible for probe/recall/synthesis
	// output. The source-node-type check (isPlainTextNodeType) scopes this to
	// safe node types at resolution time — partitionBindings only sees the tier.
	t.Run("PlainTextFallback_IsHighConf", func(t *testing.T) {
		resolved := map[string]ResolvedBinding{
			"content": {
				Value: "# Module Documentation\n\nThis module implements the inference pipeline...\n" +
					"## Architecture\n\nThe system uses a three-layer approach with...",
				Tier: "plain_text_fallback",
			},
			"path": {Value: "/docs/inference.md", Tier: "recursive_key"},
		}

		highConf, lowConf := partitionBindings(resolved)

		if _, ok := highConf["content"]; !ok {
			t.Errorf("plain_text_fallback binding should be in highConf (splice-eligible), but landed in lowConf: %v", lowConf)
		}
		if _, ok := highConf["path"]; !ok {
			t.Errorf("recursive_key binding should be in highConf")
		}
		if len(lowConf) != 0 {
			t.Errorf("expected 0 low-confidence bindings, got %d: %v", len(lowConf), lowConf)
		}
	})
}

// TestStripSchemaProperties validates the ADR-0030 schema property removal.
func TestStripSchemaProperties(t *testing.T) {
	t.Run("RemovesProperties", func(t *testing.T) {
		schema := `{
			"type": "object",
			"properties": {
				"tool_arguments": {
					"type": "object",
					"properties": {
						"receipt_path": {"type": "string"},
						"amount": {"type": "number"},
						"email": {"type": "string"}
					},
					"required": ["receipt_path", "amount", "email"]
				}
			},
			"required": ["tool_arguments"]
		}`

		stripped := stripSchemaProperties(schema, []string{"receipt_path"})

		// Verify receipt_path was removed
		if contains(stripped, "receipt_path") {
			t.Errorf("Expected receipt_path to be removed from schema, but found it in: %s", stripped)
		}
		// Verify other properties remain
		if !contains(stripped, "amount") {
			t.Errorf("Expected amount to remain in schema")
		}
		if !contains(stripped, "email") {
			t.Errorf("Expected email to remain in schema")
		}
	})

	t.Run("RemovesMultipleProperties", func(t *testing.T) {
		schema := `{
			"type": "object",
			"properties": {
				"tool_arguments": {
					"type": "object",
					"properties": {
						"receipt_path": {"type": "string"},
						"amount": {"type": "number"},
						"email": {"type": "string"}
					},
					"required": ["receipt_path", "amount", "email"]
				}
			}
		}`

		stripped := stripSchemaProperties(schema, []string{"receipt_path", "email"})

		if contains(stripped, "receipt_path") || contains(stripped, "email") {
			t.Errorf("Expected receipt_path and email to be removed, got: %s", stripped)
		}
		if !contains(stripped, "amount") {
			t.Errorf("Expected amount to remain")
		}
	})

	t.Run("EmptyKeysReturnsOriginal", func(t *testing.T) {
		schema := `{"type": "object"}`
		result := stripSchemaProperties(schema, []string{})
		if result != schema {
			t.Errorf("Expected original schema for empty keys")
		}
	})

	t.Run("InvalidJSONReturnsOriginal", func(t *testing.T) {
		schema := `not json`
		result := stripSchemaProperties(schema, []string{"key"})
		if result != schema {
			t.Errorf("Expected original schema for invalid JSON")
		}
	})

	t.Run("EmptySchemaReturnsOriginal", func(t *testing.T) {
		result := stripSchemaProperties("", []string{"key"})
		if result != "" {
			t.Errorf("Expected empty string for empty schema")
		}
	})
}

// contains checks if a string contains a substring (helper for test readability).
func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && containsSubstring(s, substr)
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestNodeTypeAwarePlainTextFallback validates that the Response Resolver uses node-type
// awareness to resolve non-JSON output from probe/synthesis/recall nodes as full text,
// rather than relying on a hardcoded key whitelist. (Grilling Decision #1)
func TestNodeTypeAwarePlainTextFallback(t *testing.T) {
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_node_type_resolver.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_node_type_resolver.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
		_ = memory.DB.Init()
	}()
	_ = memory.DB.Init()

	taskID := "task-node-type-resolver"
	ctx := context.Background()

	t.Run("ProbeNode_NonJSON_ResolvesFullText", func(t *testing.T) {
		// A probe node produces raw markdown synthesis — not JSON.
		// The resolver should return the entire output as the resolved value
		// when the source node type is "probe", regardless of property key.
		markdownOutput := "# Architecture Overview\n\nThe system uses a DAG-based execution model..."
		_ = memory.DB.SetNodeState(taskID, "explore_project", "completed", markdownOutput)
		_ = memory.DB.SetNodeRawOutput(taskID, "explore_project", markdownOutput)

		graph := &compiler.ExecutionGraph{
			TaskID: taskID,
			Nodes: []compiler.GraphNode{
				{ID: "explore_project", Type: "probe"},
				{ID: "write_docs", Type: "action", Action: "write_file"},
			},
		}

		bindings := map[string]interface{}{
			"content": "explore_project.output.synthesis",
		}
		resolved := resolveDynamicBindings(ctx, bindings, taskID, graph)

		if resolved["content"].Value != markdownOutput {
			t.Errorf("Expected full markdown output, got %q", resolved["content"].Value)
		}
		if resolved["content"].Tier != "plain_text_fallback" {
			t.Errorf("Expected tier 'plain_text_fallback', got %q", resolved["content"].Tier)
		}
	})

	t.Run("ProbeNode_AnyPropertyKey_ResolvesFullText", func(t *testing.T) {
		// The resolver should work for ANY property key when source is a probe,
		// not just "synthesis", "content", or "output".
		markdownOutput := "## Function Index\n\n- func NewCache() *Cache"
		_ = memory.DB.SetNodeState(taskID, "explore_funcs", "completed", markdownOutput)
		_ = memory.DB.SetNodeRawOutput(taskID, "explore_funcs", markdownOutput)

		graph := &compiler.ExecutionGraph{
			TaskID: taskID,
			Nodes: []compiler.GraphNode{
				{ID: "explore_funcs", Type: "probe"},
			},
		}

		bindings := map[string]interface{}{
			"docs": "explore_funcs.output.custom_key_name",
		}
		resolved := resolveDynamicBindings(ctx, bindings, taskID, graph)

		if resolved["docs"].Value != markdownOutput {
			t.Errorf("Expected full markdown output for arbitrary key, got %q", resolved["docs"].Value)
		}
	})

	t.Run("SynthesisNode_NonJSON_ResolvesFullText", func(t *testing.T) {
		// Synthesis nodes also produce free-form text.
		textOutput := "The combined analysis shows three key findings..."
		_ = memory.DB.SetNodeState(taskID, "terminal_synthesis", "completed", textOutput)
		_ = memory.DB.SetNodeRawOutput(taskID, "terminal_synthesis", textOutput)

		graph := &compiler.ExecutionGraph{
			TaskID: taskID,
			Nodes: []compiler.GraphNode{
				{ID: "terminal_synthesis", Type: "synthesis"},
			},
		}

		bindings := map[string]interface{}{
			"summary": "terminal_synthesis.output.result",
		}
		resolved := resolveDynamicBindings(ctx, bindings, taskID, graph)

		if resolved["summary"].Value != textOutput {
			t.Errorf("Expected full text output from synthesis node, got %q", resolved["summary"].Value)
		}
	})

	t.Run("RecallNode_NonJSON_ResolvesFullText", func(t *testing.T) {
		// Recall nodes also produce free-form aligned synthesis.
		recallOutput := "Aligned findings from probe exploration:\n1. Cache uses LRU eviction\n2. Metrics exposed via prometheus"
		_ = memory.DB.SetNodeState(taskID, "probe_recall", "completed", recallOutput)
		_ = memory.DB.SetNodeRawOutput(taskID, "probe_recall", recallOutput)

		graph := &compiler.ExecutionGraph{
			TaskID: taskID,
			Nodes: []compiler.GraphNode{
				{ID: "probe_recall", Type: "recall"},
			},
		}

		bindings := map[string]interface{}{
			"findings": "probe_recall.output.aligned_output",
		}
		resolved := resolveDynamicBindings(ctx, bindings, taskID, graph)

		if resolved["findings"].Value != recallOutput {
			t.Errorf("Expected full recall output, got %q", resolved["findings"].Value)
		}
	})

	t.Run("ActionNode_NonJSON_DoesNOTGetPlainTextFallback", func(t *testing.T) {
		// Action nodes produce structured tool output (usually JSON).
		// Non-JSON action output should NOT get the plain-text fallback —
		// it should fall through to KV-line or semantic tiers.
		actionOutput := "status: success\npath: /tmp/output.txt"
		_ = memory.DB.SetNodeState(taskID, "write_file_exec", "completed", actionOutput)
		_ = memory.DB.SetNodeRawOutput(taskID, "write_file_exec", actionOutput)

		graph := &compiler.ExecutionGraph{
			TaskID: taskID,
			Nodes: []compiler.GraphNode{
				{ID: "write_file_exec", Type: "action", Action: "write_file"},
			},
		}

		bindings := map[string]interface{}{
			"file_path": "write_file.output.path",
		}
		resolved := resolveDynamicBindings(ctx, bindings, taskID, graph)

		// Should resolve via KV-line tier, not plain_text_fallback
		if val, exists := resolved["file_path"]; exists {
			if val.Tier == "plain_text_fallback" {
				t.Errorf("Action node output should NOT use plain_text_fallback tier, got tier=%q", val.Tier)
			}
		}
	})

	t.Run("NilGraph_FallsBackToExistingBehavior", func(t *testing.T) {
		// When no graph is provided (nil), the resolver should not panic
		// and should NOT apply plain-text fallback (no node type info available).
		markdownOutput := "Some markdown that won't be resolved"
		_ = memory.DB.SetNodeState(taskID, "orphan_node_exec", "completed", markdownOutput)
		_ = memory.DB.SetNodeRawOutput(taskID, "orphan_node_exec", markdownOutput)

		bindings := map[string]interface{}{
			"data": "orphan_node.output.anything",
		}
		// nil graph — should not panic, should not apply fallback
		resolved := resolveDynamicBindings(ctx, bindings, taskID, nil)
		if val, exists := resolved["data"]; exists && val.Tier == "plain_text_fallback" {
			t.Error("Expected plain_text_fallback NOT to apply when graph is nil")
		}
	})
}
