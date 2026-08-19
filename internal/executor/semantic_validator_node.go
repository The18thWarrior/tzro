package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"tzro/internal/compiler"
	"tzro/internal/inference"
	"tzro/internal/tools"
)

// runSemanticValidatorCore contains the domain-core logic for semantic_validator nodes.
// Returns (inferenceResult, error) without managing state, hooks, or events.
// Called by SemanticValidatorStrategy.Execute → dispatch envelope handles ceremony.
func (e *ExecutionEngine) runSemanticValidatorCore(
	ctx context.Context,
	graph *compiler.ExecutionGraph,
	node *compiler.GraphNode,
	executionTier string,
	meta inference.StreamMeta,
	interpolatedPrompt string,
) (string, error) {
	taskID := graph.TaskID


		schemaStr, schemaErr := tools.GetSchema(node.Action)
		if schemaErr != nil {
			fmt.Fprintf(os.Stderr, "[Executor Warning] Failed to get schema for action %s: %v. Using fallback.\n", node.Action, schemaErr)
			schemaStr = ""
		}

		accumulatedCtx := buildAccumulatedContext(taskID, graph, node.Type)
		staticBase := buildStaticBaseInstruction(true)

		var inferenceResult string
		var err error
		var usedCloud bool

		// ===== ADR-0030: Proactive Binding Splice =====
		// Resolve bindings and partition by confidence tier BEFORE inference.
		// High-confidence values (recursive_key, fuzzy_key) are stripped from the
		// schema so the model never generates them — they get spliced back after
		// Pass 2. Low-confidence values (semantic_fallback) are injected as prompt
		// hints only (existing behavior).
		var highConfBindings map[string]string
		validatorSchemaStr := schemaStr // schema the model will see (may be stripped)

		if len(node.DynamicBindings) > 0 {
			resolved := resolveDynamicBindings(ctx, node.DynamicBindings, taskID, graph)
			if len(resolved) > 0 {
				var lowConfBindings map[string]string
				highConfBindings, lowConfBindings = partitionBindings(resolved)

				// Strip high-confidence params from the inference schema
				if len(highConfBindings) > 0 {
					keys := make([]string, 0, len(highConfBindings))
					for k := range highConfBindings {
						keys = append(keys, k)
					}
					validatorSchemaStr = stripSchemaProperties(schemaStr, keys)
					highConfJSON, _ := json.MarshalIndent(highConfBindings, "", "  ")
					fmt.Fprintf(os.Stderr, "[Executor ADR-0030] Stripped %d high-confidence params from schema for %s: %s\n", len(highConfBindings), node.ID, string(highConfJSON))
				}

				// Inject low-confidence bindings as prompt hints (existing behavior)
				if len(lowConfBindings) > 0 {
					lowConfJSON, _ := json.MarshalIndent(lowConfBindings, "", "  ")
					fmt.Fprintf(os.Stderr, "[Executor] Low-confidence bindings for %s (prompt hint only): %s\n", node.ID, string(lowConfJSON))
				}
			}
		}

		// ===== PASS 1: Free-form XML extraction (no grammar constraint) =====
		// The LLM generates tool parameters as loose XML tags. This is where the
		// semantic reasoning happens — understanding context, resolving references,
		// extracting correct values. No grammar masking means maximum decoding freedom.
		var xmlResult string
		maxRetries := 3
		for attempt := 1; attempt <= maxRetries; attempt++ {
			instruction := fmt.Sprintf("Extract structured tool parameters for '%s' in XML format `<params><argName>value</argName>...</params>`. Do NOT nest the params tag inside itself.\n\n", node.Action) + node.Instructions + "\n\nResolved reference:\n" + interpolatedPrompt

			// Red-team FM-12 fix: Anchor SQLite dialect in the validator instruction
			// when the target tool is sql_cached_data. Cloud models default to PostgreSQL.
			if node.Action == "sql_cached_data" {
				instruction += "\n\nIMPORTANT: The SQL engine is SQLite. You MUST use SQLite syntax:" +
					"\n- Use GROUP_CONCAT(column) instead of STRING_AGG(column, separator)" +
					"\n- Use GROUP_CONCAT(DISTINCT column) instead of STRING_AGG(DISTINCT column, separator)" +
					"\n- Use LIKE instead of ILIKE (SQLite LIKE is case-insensitive for ASCII)" +
					"\n- Use 1/0 instead of TRUE/FALSE for boolean values"
			}

			// Inject low-confidence bindings as prompt hints (high-confidence are already stripped)
			if len(node.DynamicBindings) > 0 {
				resolved := resolveDynamicBindings(ctx, node.DynamicBindings, taskID, graph)
				if len(resolved) > 0 {
					_, lowConf := partitionBindings(resolved)
					if len(lowConf) > 0 {
						resolvedJSON, _ := json.MarshalIndent(lowConf, "", "  ")
						instruction += "\n\n## RESOLVED UPSTREAM VALUES (use these exact values for the corresponding parameters):\n" + string(resolvedJSON)
						fmt.Fprintf(os.Stderr, "[Executor] DynamicBindings (low-confidence hint) for %s: %s\n", node.ID, string(resolvedJSON))
					}
				}
			}

			msgs := buildSegmentedMessages(staticBase, accumulatedCtx, validatorSchemaStr, instruction, true)

			req := inference.StructuredInferenceRequest{
				Messages:    msgs,
				JSONSchema:  "", // No GBNF constraint — free-form XML generation
				StreamMeta:  &meta,
				TaskID:      taskID,
				IsLowStakes: true,
			}

			isBenchmark := ctx.Value("is_benchmark") != nil
			useCloud := IsForceCloud(taskID) && !isCloudEscalationBlocked()
			// Skip confidence check when cloud escalation is blocked (local_only mode)
			// — the check is only meaningful when cloud is available as a fallback.
			if !useCloud && !isBenchmark && !isCloudEscalationBlocked() && attempt == 1 {
				sufficient, _ := assessConfidenceTier(ctx, msgs, schemaStr, taskID)
				checkAndUpdateConfidence(taskID, sufficient)
				if !sufficient {
					useCloud = true
					e.getPublisher().PublishEvent("confidence_insufficient", taskID, node.ID, "Escalating to cloud")
				}
			}

			if useCloud {
				usedCloud = true
				xmlResult, err = retryWithCloud(ctx, msgs, schemaStr, taskID)
			} else {
				// Executor Pass 1 (XML extraction): standard structured extraction
				// without thinking mode. Validator nodes perform deterministic
				// translation and parameter extraction — not open-ended reasoning.
				pass1Ctx := context.WithValue(ctx, inference.MaxTokensKey, 4096)
				xmlResult, err = inference.ExecuteWorkerStructured(pass1Ctx, req)
			}

			if err != nil {
				return "", fmt.Errorf("semantic_validator pass 1 (XML extraction) failed: %w", err)
			}

			// Sanity check: does the output contain any XML-like structure?
			if strings.Contains(xmlResult, "<") && strings.Contains(xmlResult, ">") {
				break
			}

			// Also accept if the LLM returned JSON directly (some models do this)
			if strings.Contains(xmlResult, "{") && strings.Contains(xmlResult, "}") {
				break
			}

			if attempt < maxRetries {
				interpolatedPrompt += fmt.Sprintf("\n\nValidation failed on attempt %d: Invalid XML format or missing arguments. Please try again.", attempt)
				continue
			}
		}

		fmt.Fprintf(os.Stderr, "[Executor] Pass 1 XML result for %s: %s\n", node.ID, xmlResult)

		// ===== PASS 2: GBNF-constrained JSON refinement =====
		// Take the raw XML output and convert it to schema-valid JSON using grammar
		// constraints. The prompt is small (just the XML + schema) so it's fast.
		// Falls back to deterministic XML parsing if GBNF refinement fails.
		//
		// Guard: Skip GBNF refinement when Pass 1 output exceeds 4K chars.
		// GBNF refinement is designed for short parameter extraction (path, query,
		// count, etc.) — not for passing multi-kilobyte content blobs verbatim.
		// The 4B router model will summarize large content instead of preserving it,
		// destroying probe synthesis output. The deterministic XML parser handles
		// large content correctly.
		const maxGBNFRefinementInputChars = 4096
		skipGBNFRefinement := len(xmlResult) > maxGBNFRefinementInputChars
		if skipGBNFRefinement {
			fmt.Fprintf(os.Stderr, "[Executor] Skipping GBNF refinement for %s — Pass 1 output too large (%d chars > %d limit), using XML parser\n", node.ID, len(xmlResult), maxGBNFRefinementInputChars)
		}

		// F1 gate: Skip GBNF Pass 2 when Pass 1 already produced valid JSON
		// matching the tool schema. Deterministic structural check — no LLM
		// self-assessment. Prevents Pass 2 from clobbering correct parameters
		// (observed: cloud-extracted paths overwritten with "{path}/" templates).
		if !skipGBNFRefinement && schemaStr != "" {
			pass1Args := extractToolArguments(xmlResult)
			if len(pass1Args) > 0 && pass1SatisfiesSchema(pass1Args, schemaStr, node.ID) {
				skipGBNFRefinement = true
				fmt.Fprintf(os.Stderr, "[Executor] Skipping GBNF refinement for %s — Pass 1 output already satisfies tool schema\n", node.ID)
			}
		}
		if schemaStr != "" && !skipGBNFRefinement {
			refinementSystem := "You are a precise data format converter. Convert the provided XML tool arguments into a valid JSON object matching the schema. " +
				"Preserve all values exactly as they appear in the XML. Do NOT add, remove, or modify any values."

			// Red-team FM-11 fix: Anchor known cacheIds in the refinement prompt
			// so the model picks from real values instead of hallucinating fake IDs.
			if strings.Contains(schemaStr, "cacheId") {
				knownCaches := extractCacheIdsFromContext(accumulatedCtx)
				if len(knownCaches) > 0 {
					refinementSystem += fmt.Sprintf(
						"\nIMPORTANT: Valid cache IDs are: %s. For the cacheId field, use one of these EXACT values. Do NOT invent cache IDs.",
						strings.Join(knownCaches, ", "))
				}
			}

			// Use the stripped schema for Pass 2 as well — high-confidence params aren't in the XML output
			refinementUser := fmt.Sprintf("Convert the following XML tool arguments for '%s' into the JSON schema format.\n\n"+
				"XML INPUT:\n%s\n\n"+
				"TARGET JSON SCHEMA:\n%s\n\n"+
				"Return ONLY a valid JSON object matching the schema with a top-level \"tool_arguments\" key.",
				node.Action, xmlResult, validatorSchemaStr)

			refineMeta := inference.StreamMeta{
				StreamID: fmt.Sprintf("refine_%s_%s", taskID, node.ID),
				Source:   "executor",
				TaskID:   taskID,
				NodeID:   node.ID,
			}

			refineReq := inference.NewSimpleRequest(refinementSystem, refinementUser, validatorSchemaStr)
			refineReq.StreamMeta = &refineMeta
			refineReq.TaskID = taskID

			refineCtx := context.WithValue(ctx, inference.MaxTokensKey, 4096)
			// FM-18 fix: Use 4B worker for GBNF refinement, not 1B router.
			// The 1B router echoes JSON field names as values (e.g., groupCol="groupCol")
			// because it lacks the semantic capacity to resolve values from context.
			refineResult, refineErr := inference.ExecuteWorkerStructured(refineCtx, refineReq)
			if refineErr == nil {
				var check map[string]interface{}
				if json.Unmarshal([]byte(refineResult), &check) == nil {
					// Recursively unwrap nested tool_arguments (model sometimes double-wraps)
					unwrapped := extractToolArguments(refineResult)
					// Store flat args — tool_arguments wrapping is a GBNF schema concern,
					// not a state storage concern. Re-wrapping caused double nesting that
					// bloated interpolated context for downstream nodes.
					flatJSON, _ := json.MarshalIndent(unwrapped, "", "  ")
					inferenceResult = string(flatJSON)
					fmt.Fprintf(os.Stderr, "[Executor] Pass 2 GBNF refinement succeeded for %s: %s\n", node.ID, inferenceResult)
				} else {
					fmt.Fprintf(os.Stderr, "[Executor Warning] Pass 2 GBNF produced invalid JSON, falling back to XML parse: %s\n", refineResult)
					args := extractToolArguments(xmlResult)
					parsedJSON, _ := json.Marshal(map[string]interface{}{"tool_arguments": args})
					inferenceResult = string(parsedJSON)
				}
			} else {
				fmt.Fprintf(os.Stderr, "[Executor Warning] Pass 2 GBNF failed: %v. Falling back to XML parse.\n", refineErr)
				args := extractToolArguments(xmlResult)
				parsedJSON, _ := json.Marshal(map[string]interface{}{"tool_arguments": args})
				inferenceResult = string(parsedJSON)
			}
		} else {
			args := extractToolArguments(xmlResult)
			parsedJSON, _ := json.Marshal(map[string]interface{}{"tool_arguments": args})
			inferenceResult = string(parsedJSON)
		}
		fmt.Fprintf(os.Stderr, "[Executor] Final validator output for %s: %s\n", node.ID, inferenceResult)

		// ADR-0030: Splice high-confidence bindings into the final JSON.
		// These values were stripped from the schema before inference, so the model
		// never attempted to generate them. We merge them back deterministically.
		// Note: binding names may differ from schema property names (e.g.
		// "receipt_code_path" vs "receipt_path"). This is intentional — the splice
		// ensures the correct resolved value reaches the tool even when the model
		// hallucinated a placeholder. Extra synonym keys are harmless noise.
		if len(highConfBindings) > 0 {
			var parsedResult map[string]interface{}
			if json.Unmarshal([]byte(inferenceResult), &parsedResult) == nil {
				// Ensure tool_arguments map exists and is populated
				var toolArgs map[string]interface{}
				if existingArgs, ok := parsedResult["tool_arguments"].(map[string]interface{}); ok {
					toolArgs = existingArgs
				} else {
					toolArgs = make(map[string]interface{})
					parsedResult["tool_arguments"] = toolArgs
				}

				for paramName, val := range highConfBindings {
					// If the validator escalated to cloud and cloud produced a substantial
					// deliverable for this parameter (> 200 chars), preserve the cloud deliverable
					// rather than overwriting it with upstream whole_output synthesis.
					if existingVal, exists := toolArgs[paramName].(string); exists && len(strings.TrimSpace(existingVal)) > 200 && usedCloud {
						fmt.Fprintf(os.Stderr, "[Executor ADR-0030] Preserving cloud-generated '%s' (%d chars) over upstream splice (%d chars)\n", paramName, len(existingVal), len(val))
						parsedResult[paramName] = existingVal
						continue
					}

					parsedResult[paramName] = val
					toolArgs[paramName] = val
					fmt.Fprintf(os.Stderr, "[Executor ADR-0030] Spliced '%s' = %q (tier: high-confidence)\n", paramName, val)
				}
				splicedJSON, _ := json.MarshalIndent(parsedResult, "", "  ")
				inferenceResult = string(splicedJSON)
				fmt.Fprintf(os.Stderr, "[Executor] Validator output after proactive splice for %s: %s\n", node.ID, inferenceResult)
			}
		}

	return inferenceResult, nil
}
