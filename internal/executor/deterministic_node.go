package executor

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"tzro/internal/cache"
	"tzro/internal/compiler"
	"tzro/internal/inference"
	"tzro/internal/tools"
)

// deterministicResult holds the domain outputs from runDeterministicCore.
type deterministicResult struct {
	rawOutput       string // The raw tool output
	compactedOutput string // Output after cache compaction
	derivedCacheID  string // CacheID from compactor, if any
}

// runDeterministicCore contains the domain-core logic for deterministic nodes.
// Returns a deterministicResult without managing state, hooks, or events.
// Called by both the legacy executeDeterministicNode and DeterministicStrategy.
func (e *ExecutionEngine) runDeterministicCore(
	ctx context.Context,
	graph *compiler.ExecutionGraph,
	node *compiler.GraphNode,
	executionTier string,
	meta inference.StreamMeta,
	interpolatedPrompt string,
) (*deterministicResult, error) {
	taskID := graph.TaskID

	// P0 Fix: Add inference step for deterministic nodes using accumulated context.
	schemaStr, schemaErr := tools.GetSchema(node.Action)
	if schemaErr != nil {
		fmt.Fprintf(os.Stderr, "[Executor Warning] Failed to get GBNF schema for deterministic action %s: %v\n", node.Action, schemaErr)
		schemaStr = ""
	}

	accumulatedCtx := buildAccumulatedContext(taskID, graph, node.Type)
	var toolArguments map[string]interface{}

	if accumulatedCtx != "" && schemaStr != "" {
		// Use segmented 4-message structure for KV cache prefix sharing (ADR-0021)
		staticBase := buildStaticBaseInstruction(false)
		detInstruction := fmt.Sprintf("Extract structured tool parameters for '%s'.\n\n", node.Action) + node.Instructions + "\n\nResolved reference:\n" + interpolatedPrompt
		detMsgs := buildSegmentedMessages(staticBase, accumulatedCtx, schemaStr, detInstruction, false)

		detReq := inference.StructuredInferenceRequest{
			Messages:    detMsgs,
			JSONSchema:  schemaStr,
			StreamMeta:  &meta,
			TaskID:      taskID,
			IsLowStakes: true,
		}

		// FM-18 fix: Use 4B worker for deterministic args inference.
		detResult, detErr := inference.ExecuteWorkerStructured(ctx, detReq)
		if detErr == nil {
			toolArguments = extractToolArguments(detResult)
			fmt.Fprintf(os.Stderr, "[Executor] Deterministic args via context-aware inference: %v\n", toolArguments)
		} else {
			fmt.Fprintf(os.Stderr, "[Executor Warning] Deterministic inference failed, falling back to interpolation: %v\n", detErr)
			toolArguments = extractToolArguments(interpolatedPrompt)
		}
	} else {
		toolArguments = extractToolArguments(interpolatedPrompt)
	}

	// Safety net: apply coercion pipeline
	coerceNumericArguments(toolArguments, interpolatedPrompt)
	coerceStringArguments(toolArguments, interpolatedPrompt, node.Action)
	resolveInterpolatedArguments(toolArguments, interpolatedPrompt, node.Instructions, taskID, graph)

	// Red-team FM-9 fix: Guard against deterministic inference overriding the
	// validator's cacheId with a stale/wrong one from accumulated context.
	if inferredCacheId, ok := toolArguments["cacheId"].(string); ok && inferredCacheId != "" {
		validatorCacheId := extractCacheIdFromText(interpolatedPrompt)
		if validatorCacheId != "" && validatorCacheId != inferredCacheId {
			fmt.Fprintf(os.Stderr, "[Executor] FM-9 Guard: deterministic inference changed cacheId %q → %q, restoring validator value\n", validatorCacheId, inferredCacheId)
			toolArguments["cacheId"] = validatorCacheId
		}
	}

	// Hard-override any dynamically bound params with resolved upstream values.
	// ADR-0030: High-confidence tiers override, UNLESS the validator already extracted
	// a substantial deliverable (> 200 chars) that is more specific than whole_output.
	if len(node.DynamicBindings) > 0 {
		resolved := resolveDynamicBindings(ctx, node.DynamicBindings, taskID, graph)
		for paramName, rb := range resolved {
			if rb.Value != "" && rb.Value != "null" {
				existingVal, exists := toolArguments[paramName]
				existingStr := fmt.Sprintf("%v", existingVal)

				// If validator already extracted a substantial structured document (> 200 chars),
				// do NOT overwrite it with raw whole_output or plain_text_fallback.
				if exists && len(strings.TrimSpace(existingStr)) > 200 && (rb.Tier == "whole_output" || rb.Tier == "plain_text_fallback") {
					fmt.Fprintf(os.Stderr, "[Executor DynamicBindings] Preserving validator deliverable '%s' (%d chars) over %s (%d chars)\n", paramName, len(existingStr), rb.Tier, len(rb.Value))
					continue
				}

				if rb.Tier == "recursive_key" || rb.Tier == "fuzzy_key" || rb.Tier == "kv_line" || rb.Tier == "whole_output" || rb.Tier == "plain_text_fallback" || rb.Tier == "derived_cache" {
					fmt.Fprintf(os.Stderr, "[Executor DynamicBindings] Overriding exec arg '%s': %q -> %q (tier: %s)\n", paramName, existingStr, rb.Value, rb.Tier)
					toolArguments[paramName] = rb.Value
				} else if exists && (existingStr == "null" || existingStr == "" || existingStr == "<nil>") {
					fmt.Fprintf(os.Stderr, "[Executor DynamicBindings] Overriding exec arg '%s': %q -> %q (tier: %s, null-only)\n", paramName, existingStr, rb.Value, rb.Tier)
					toolArguments[paramName] = rb.Value
				}
			}
		}
	}

	fmt.Fprintf(os.Stderr, "[Executor] Deterministic tool arguments extracted: %v\n", toolArguments)

	// Red-team FM-18 fix: Detect validator field-name hallucination.
	schemaFieldNames := map[string]bool{
		"cacheId": true, "groupCol": true, "aggCol": true, "aggFunc": true,
		"column": true, "orderCol": true, "direction": true, "operator": true,
		"value": true, "n": true, "sql": true,
	}
	for paramName, paramVal := range toolArguments {
		if strVal, ok := paramVal.(string); ok && schemaFieldNames[strVal] {
			fmt.Fprintf(os.Stderr, "[Executor] FM-18 Guard: param %q has value %q (echoed field name), clearing\n", paramName, strVal)
			delete(toolArguments, paramName)
		}
	}

	// Tool-existence validation with classification fallback.
	if tools.GetTool(node.Action) == nil {
		resolved := classifyToolName(ctx, node.Action, node.Instructions)
		if resolved != "" {
			fmt.Fprintf(os.Stderr, "[Executor] Tool validation: hallucinated '%s' → classified as '%s'\n", node.Action, resolved)
			node.Action = resolved
		} else {
			return nil, fmt.Errorf("tool '%s' is not registered and could not be classified to a known tool", node.Action)
		}
	}

	// Red-team FM-11 fix: Final cacheId validation before tool dispatch.
	// FM-14 fix: Also accept derived cache IDs (cache_derived_[hex]{16}).
	if cacheIdArg, ok := toolArguments["cacheId"].(string); ok && cacheIdArg != "" {
		cacheIdPattern := regexp.MustCompile(`^cache_(?:\d{10,}|derived_[a-f0-9]{16})$`)
		if !cacheIdPattern.MatchString(cacheIdArg) {
			knownCaches := extractCacheIdsFromContext(accumulatedCtx)
			if len(knownCaches) > 0 {
				fmt.Fprintf(os.Stderr, "[Executor] FM-11 Guard: cacheId %q fails format check, replacing with %q\n", cacheIdArg, knownCaches[0])
				toolArguments["cacheId"] = knownCaches[0]
			} else {
				fmt.Fprintf(os.Stderr, "[Executor] FM-11 Guard: cacheId %q fails format check and no known caches available\n", cacheIdArg)
			}
		} else {
			knownCaches := extractCacheIdsFromContext(accumulatedCtx)
			if len(knownCaches) > 0 {
				found := false
				for _, kc := range knownCaches {
					if kc == cacheIdArg {
						found = true
						break
					}
				}
				if !found {
					fmt.Fprintf(os.Stderr, "[Executor] FM-11 Guard: cacheId %q not in known set %v, replacing with %q\n", cacheIdArg, knownCaches, knownCaches[0])
					toolArguments["cacheId"] = knownCaches[0]
					// Also fix any SQL that references the old cacheId
					if sqlArg, ok := toolArguments["sql"].(string); ok && sqlArg != "" {
						toolArguments["sql"] = strings.ReplaceAll(sqlArg, cacheIdArg, knownCaches[0])
					}
				}
			}
		}
	}

	output, err := tools.Call(ctx, node.Action, toolArguments)
	if err != nil {
		return nil, fmt.Errorf("tool '%s' execution failed: %w", node.Action, err)
	}

	// Cache compaction
	var compactedOutput string
	var derivedCacheID string
	if isCompactionDisabled(ctx) {
		compactedOutput = output
	} else {
		var cacheID string
		compactedOutput, cacheID, err = cache.Process(ctx, output, interpolatedPrompt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Executor Compactor Warning] Failed to process payload in cache: %v\n", err)
			compactedOutput = output
		} else if cacheID != "" {
			derivedCacheID = cacheID
			fmt.Fprintf(os.Stderr, "[Executor Compactor] Payload > 12KB. Saved to SQLite and disk cache -> CacheID: %s\n", cacheID)
			e.getPublisher().PublishEvent("cache_envelope_created", taskID, node.ID, fmt.Sprintf("Cached %s output to SQLite and disk (%dKB) -> CacheID: %s", node.Action, len(output)/1024, cacheID))
		}
	}

	return &deterministicResult{
		rawOutput:       output,
		compactedOutput: compactedOutput,
		derivedCacheID:  derivedCacheID,
	}, nil
}

