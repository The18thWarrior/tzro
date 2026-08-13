package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"tzro/internal/compiler"
	"tzro/internal/memory"
)

// ResolvedBinding holds a resolved DynamicBinding value alongside the resolution
// tier that produced it. High-confidence tiers (recursive_key, fuzzy_key, kv_line)
// are eligible for proactive binding splice (ADR-0030); low-confidence tiers
// (semantic_fallback) are injected as prompt hints only.
type ResolvedBinding struct {
	Value string
	Tier  string // "recursive_key" | "fuzzy_key" | "kv_line" | "plain_text_fallback" | "whole_output" | "semantic_fallback"
}

// partitionBindings splits resolved bindings into high-confidence (safe to splice
// deterministically, bypassing inference) and low-confidence (inject as prompt hints
// only). See ADR-0030 for the design rationale.
func partitionBindings(resolved map[string]ResolvedBinding) (highConf map[string]string, lowConf map[string]string) {
	highConf = make(map[string]string)
	lowConf = make(map[string]string)
	for k, rb := range resolved {
		switch rb.Tier {
		case "recursive_key", "fuzzy_key", "kv_line", "plain_text_fallback", "whole_output", "derived_cache":
			highConf[k] = rb.Value
		default:
			lowConf[k] = rb.Value
		}
	}
	return
}

func resolveDynamicBindings(ctx context.Context, bindings map[string]interface{}, taskID string, graph *compiler.ExecutionGraph) map[string]ResolvedBinding {
	if len(bindings) == 0 || taskID == "" {
		return nil
	}

	resolved := make(map[string]ResolvedBinding)
	for paramName, rawValue := range bindings {
		// Coerce to string — handles numbers, bools, etc. that the model occasionally emits
		bindingPath := fmt.Sprintf("%v", rawValue)

		// Parse "nodeId.output.propertyName" format
		parts := strings.SplitN(bindingPath, ".", 3) // ["nodeId", "output", "propertyName"]

		// === Whole Output Binding ===
		// Accept 2-segment paths ("nodeId.output") as a request for the entire
		// raw output of the upstream node. Resolves in the binding parser before
		// the Response Resolver is invoked, keeping the 5-tier cascade clean.
		if len(parts) == 2 && parts[1] == "output" {
			state, ok := GetNodeStateTolerant(taskID, parts[0])
			if !ok {
				fmt.Fprintf(os.Stderr, "[Executor DynamicBindings] WARNING: Upstream node '%s' not found for whole_output binding '%s'\n", parts[0], paramName)
				continue
			}
			sourceOutput := state.RawOutput
			if sourceOutput == "" {
				sourceOutput = state.Output
				if idx := strings.Index(sourceOutput, "] "); idx != -1 {
					sourceOutput = sourceOutput[idx+2:]
				}
			}
			if sourceOutput != "" {
				fmt.Fprintf(os.Stderr, "[Executor DynamicBindings] Resolved '%s' via whole_output (node: %s, %d chars)\n", paramName, parts[0], len(sourceOutput))
				resolved[paramName] = ResolvedBinding{Value: sourceOutput, Tier: "whole_output"}
			} else {
				fmt.Fprintf(os.Stderr, "[Executor DynamicBindings] WARNING: Empty output from node '%s' for whole_output binding '%s'\n", parts[0], paramName)
			}
			continue
		}

		if len(parts) < 3 || parts[1] != "output" {
			// Suppress warning for literal values (file paths, URLs, etc.)
			// that the planner sometimes places in DynamicBindings instead of
			// a proper nodeId.output.propertyName reference. Use them directly.
			if strings.HasPrefix(bindingPath, "/") || strings.HasPrefix(bindingPath, "http") {
				resolved[paramName] = ResolvedBinding{Value: bindingPath, Tier: "literal"}
				continue
			}
			fmt.Fprintf(os.Stderr, "[Executor DynamicBindings] WARNING: Invalid binding format for '%s': %q (expected 'nodeId.output.propertyName')\n", paramName, bindingPath)
			continue
		}

		nodeID := parts[0]
		propertyKey := parts[2]

		// Fetch upstream node's raw output
		state, ok := GetNodeStateTolerant(taskID, nodeID)
		if !ok {
			fmt.Fprintf(os.Stderr, "[Response Resolver] WARNING: Upstream node '%s' not found for binding '%s'\n", nodeID, paramName)
			continue
		}

		sourceOutput := state.RawOutput
		if sourceOutput == "" {
			sourceOutput = state.Output
			if idx := strings.Index(sourceOutput, "] "); idx != -1 {
				sourceOutput = sourceOutput[idx+2:]
			}
		}

		if sourceOutput == "" {
			fmt.Fprintf(os.Stderr, "[Response Resolver] WARNING: Empty output from node '%s' for binding '%s'\n", nodeID, paramName)
			continue
		}

		// === Tier 1: JSON recursive key search ===
		var parsed interface{}
		jsonParsed := false
		if err := json.Unmarshal([]byte(sourceOutput), &parsed); err == nil {
			jsonParsed = true
			matches := recursiveKeySearch(parsed, propertyKey)
			if len(matches) == 1 {
				val := formatMatchValue(matches[0].Value)
				if val != "" && val != "null" && val != "<nil>" {
					fmt.Fprintf(os.Stderr, "[Response Resolver] Resolved '%s' via recursive_key (path: %s)\n", paramName, matches[0].Path)
					resolved[paramName] = ResolvedBinding{Value: val, Tier: "recursive_key"}
					continue
				}
			} else if len(matches) > 1 {
				// Key collision — fall through to semantic fallback (skip Tier 2)
				fmt.Fprintf(os.Stderr, "[Response Resolver] Key collision for '%s' (%d matches) — falling through to semantic\n", propertyKey, len(matches))
				goto semanticFallback
			}

			// === Tier 1.3: Derived cache resolution (Red-team FM-2/FM-3 fix) ===
			// When the planner uses generic binding keys like "content", "data", or
			// "filtered_data" but the upstream node output contains a derivedCacheId
			// (injected by the compactor when output > 12KB was saved to a new cache),
			// resolve the binding to the derivedCacheId. This prevents the semantic
			// fallback from hallucinating garbage for these ambiguous keys.
			if isDerivedCacheBindingKey(propertyKey) {
				cacheMatches := recursiveKeySearch(parsed, "derivedCacheId")
				if len(cacheMatches) == 1 {
					val := formatMatchValue(cacheMatches[0].Value)
					if val != "" {
						fmt.Fprintf(os.Stderr, "[Response Resolver] Resolved '%s' via derived_cache (derivedCacheId=%s)\n", paramName, val)
						resolved[paramName] = ResolvedBinding{Value: val, Tier: "derived_cache"}
						continue
					}
				}
			}

			// === Tier 1.5: Fuzzy key search (suffix/substring containment) ===
			// When Tier 1 finds 0 exact matches, try relaxed key matching. This catches
			// planner-generated binding keys like "receipt_code_path" that don't exactly match
			// the tool output key "receipt_path" but are clearly related. Resolves deterministically
			// without invoking the semantic fallback, avoiding hallucination risk.
			if fuzzyMatch := fuzzyKeySearch(parsed, propertyKey); fuzzyMatch != nil {
				val := formatMatchValue(fuzzyMatch.Value)
				if val != "" && val != "null" && val != "<nil>" {
					// Extract the actual matched key name for logging
					matchedKey := fuzzyMatch.Path
					if dotIdx := strings.LastIndex(matchedKey, "."); dotIdx != -1 {
						matchedKey = matchedKey[dotIdx+1:]
					}
					fmt.Fprintf(os.Stderr, "[Response Resolver] Resolved '%s' via fuzzy_key (target: %s → matched: %s, path: %s)\n", paramName, propertyKey, matchedKey, fuzzyMatch.Path)
					resolved[paramName] = ResolvedBinding{Value: val, Tier: "fuzzy_key"}
					continue
				}
			}
			// 0 matches from JSON (exact and fuzzy) — fall through to Tier 2 (KV-line) then Tier 3
		}

		// === Tier 1.6: Node-type-aware plain-text fallback (Grilling Decision #1) ===
		// Probe, synthesis, and recall nodes produce free-form text (markdown, prose),
		// not JSON. When the source node is one of these types, the entire output IS
		// the resolved value regardless of property key.
		if !jsonParsed && isPlainTextNodeType(graph, nodeID) {
			fmt.Fprintf(os.Stderr, "[Response Resolver] Resolved '%s' via plain_text_fallback (source node type)\n", paramName)
			resolved[paramName] = ResolvedBinding{Value: sourceOutput, Tier: "plain_text_fallback"}
			continue
		}

		// === Tier 2: KV-line key search ===
		if !jsonParsed {
			kvMap := make(map[string]string)
			lines := strings.Split(sourceOutput, "\n")
			for _, line := range lines {
				kvParts := strings.SplitN(line, ":", 2)
				if len(kvParts) == 2 {
					key := strings.TrimSpace(kvParts[0])
					val := strings.TrimSpace(kvParts[1])
					if key != "" && val != "" {
						kvMap[key] = val
					}
				}
			}
			if val, found := kvMap[propertyKey]; found {
				fmt.Fprintf(os.Stderr, "[Response Resolver] Resolved '%s' via kv_line\n", paramName)
				resolved[paramName] = ResolvedBinding{Value: val, Tier: "kv_line"}
				continue
			}
		}

	semanticFallback:
		// === Tier 3: Semantic fallback via Local Model ===
		semanticVal, err := resolveBindingSemantic(ctx, sourceOutput, propertyKey)
		if err == nil && semanticVal != "" && semanticVal != "null" {
			fmt.Fprintf(os.Stderr, "[Response Resolver] Resolved '%s' via semantic_fallback\n", paramName)
			resolved[paramName] = ResolvedBinding{Value: semanticVal, Tier: "semantic_fallback"}
			continue
		}

		// All tiers failed
		fmt.Fprintf(os.Stderr, "[Response Resolver] WARNING: Could not resolve binding '%s' from '%s' (all tiers exhausted)\n", paramName, bindingPath)
	}

	return resolved
}

func InterpolateVariables(instruction string, taskID string) string {
	reProp := regexp.MustCompile(`\{\{nodes\.([^.]+)\.output\.([^}]+)\}\}`)
	instruction = reProp.ReplaceAllStringFunc(instruction, func(match string) string {
		submatches := reProp.FindStringSubmatch(match)
		if len(submatches) < 3 {
			return match
		}
		nodeID := submatches[1]
		propertyKey := submatches[2]

		state, ok := GetNodeStateTolerant(taskID, nodeID)
		if !ok {
			return "null"
		}

		// P0 Fix: Prefer RawOutput (clean tool response) over Output (display-formatted with
		// tier prefix + compaction). This ensures JSON property lookups resolve correctly.
		sourceOutput := state.RawOutput
		if sourceOutput == "" {
			// Fallback to legacy Output with tier prefix stripping
			sourceOutput = state.Output
			if idx := strings.Index(sourceOutput, "] "); idx != -1 {
				sourceOutput = sourceOutput[idx+2:]
			}
		}

		var outputMap map[string]interface{}
		if err := json.Unmarshal([]byte(sourceOutput), &outputMap); err != nil {
			// Try parsing as KV lines (compacted object notation)
			outputMap = make(map[string]interface{})
			lines := strings.Split(sourceOutput, "\n")
			for _, line := range lines {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					key := strings.TrimSpace(parts[0])
					val := strings.TrimSpace(parts[1])
					if ls := strings.ToLower(val); ls == "true" {
						outputMap[key] = true
					} else if ls == "false" {
						outputMap[key] = false
					} else if num, err := strconv.ParseFloat(val, 64); err == nil {
						outputMap[key] = num
					} else {
						outputMap[key] = val
					}
				}
			}
		}

		val, found := outputMap[propertyKey]
		if !found {
			fmt.Fprintf(os.Stderr, "[Executor InterpolationResolver] WARNING: Property '%s' not found in node '%s' output. Available keys: %v. Returning null.\n", propertyKey, nodeID, func() []string {
				keys := make([]string, 0, len(outputMap))
				for k := range outputMap {
					keys = append(keys, k)
				}
				return keys
			}())
			return "null"
		}
		if mVal, ok := val.(map[string]interface{}); ok {
			b, _ := json.Marshal(mVal)
			return string(b)
		}
		if aVal, ok := val.([]interface{}); ok {
			b, _ := json.Marshal(aVal)
			return string(b)
		}
		return fmt.Sprintf("%v", val)
	})

	reFull := regexp.MustCompile(`\{\{nodes\.([^.]+)\.output\}\}`)
	instruction = reFull.ReplaceAllStringFunc(instruction, func(match string) string {
		submatches := reFull.FindStringSubmatch(match)
		if len(submatches) < 2 {
			return match
		}
		nodeID := submatches[1]
		state, ok := GetNodeStateTolerant(taskID, nodeID)
		if !ok {
			return "null"
		}
		// P0 Fix: Prefer RawOutput for full output interpolation too
		if state.RawOutput != "" {
			return state.RawOutput
		}
		rawOutput := state.Output
		if idx := strings.Index(rawOutput, "] "); idx != -1 {
			rawOutput = rawOutput[idx+2:]
		}
		return rawOutput
	})

	return instruction
}

func GetNodeStateTolerant(taskID, nodeID string) (memory.NodeState, bool) {
	// 1. Try finding completed suffix expansions first since they represent actual execution outcomes
	if !strings.HasSuffix(nodeID, "_exec") && !strings.HasSuffix(nodeID, "_bridge") {
		if state, ok := memory.DB.GetNodeState(taskID, nodeID+"_exec"); ok && (state.Status == "completed" || state.RawOutput != "") {
			return state, true
		}
		if state, ok := memory.DB.GetNodeState(taskID, nodeID+"_bridge"); ok && (state.Status == "completed" || state.RawOutput != "") {
			return state, true
		}
	}

	// 2. Direct match
	if state, ok := memory.DB.GetNodeState(taskID, nodeID); ok {
		return state, true
	}

	// 3. Try adding suffixes (even if not completed)
	if !strings.HasSuffix(nodeID, "_exec") && !strings.HasSuffix(nodeID, "_bridge") {
		if state, ok := memory.DB.GetNodeState(taskID, nodeID+"_exec"); ok {
			return state, true
		}
		if state, ok := memory.DB.GetNodeState(taskID, nodeID+"_bridge"); ok {
			return state, true
		}
	}

	// 4. Try removing suffixes
	if strings.HasSuffix(nodeID, "_exec") {
		baseID := strings.TrimSuffix(nodeID, "_exec")
		if state, ok := memory.DB.GetNodeState(taskID, baseID); ok {
			return state, true
		}
	}
	if strings.HasSuffix(nodeID, "_bridge") {
		baseID := strings.TrimSuffix(nodeID, "_bridge")
		if state, ok := memory.DB.GetNodeState(taskID, baseID); ok {
			return state, true
		}
	}

	return memory.NodeState{}, false
}

// getUpstreamValue fetches the exact property value from an upstream completed node state in SQLite.

func getUpstreamValue(taskID, nodeID, propertyKey string, graph *compiler.ExecutionGraph) string {
	state, ok := GetNodeStateTolerant(taskID, nodeID)
	if !ok {
		return ""
	}

	// Prefer RawOutput (clean tool response) when available, fall back to
	// Output with tier prefix stripped
	sourceOutput := state.RawOutput
	if sourceOutput == "" {
		sourceOutput = state.Output
		if idx := strings.Index(sourceOutput, "] "); idx != -1 {
			sourceOutput = sourceOutput[idx+2:]
		}
	}

	var outputMap map[string]interface{}
	if err := json.Unmarshal([]byte(sourceOutput), &outputMap); err != nil {
		// Node-type-aware fallback: probe/synthesis/recall output IS the value (Grilling Decision #10)
		if isPlainTextNodeType(graph, nodeID) {
			return sourceOutput
		}
		// Try parsing as KV lines (compacted object notation)
		outputMap = make(map[string]interface{})
		lines := strings.Split(sourceOutput, "\n")
		for _, line := range lines {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				if ls := strings.ToLower(val); ls == "true" {
					outputMap[key] = true
				} else if ls == "false" {
					outputMap[key] = false
				} else if num, err := strconv.ParseFloat(val, 64); err == nil {
					outputMap[key] = num
				} else {
					outputMap[key] = val
				}
			}
		}
	}

	val, found := outputMap[propertyKey]
	if !found {
		// Node-type-aware fallback for JSON-parseable but key-missing case
		if isPlainTextNodeType(graph, nodeID) {
			return sourceOutput
		}
		return ""
	}
	if mVal, ok := val.(map[string]interface{}); ok {
		b, _ := json.Marshal(mVal)
		return string(b)
	}
	if aVal, ok := val.([]interface{}); ok {
		b, _ := json.Marshal(aVal)
		return string(b)
	}
	return fmt.Sprintf("%v", val)
}

// isPlainTextNodeType checks whether the given node in the execution graph is a type
// that produces free-form text output (probe, synthesis, recall) rather than structured
// JSON. These node types should use the plain-text fallback tier during binding resolution.
// Returns false if graph is nil or the node is not found (safe for backward compat).
func isPlainTextNodeType(graph *compiler.ExecutionGraph, nodeID string) bool {
	if graph == nil {
		return false
	}
	// Strip SCT suffixes to find the original high-level node
	baseID := nodeID
	for _, suffix := range []string{"_exec", "_bridge", "_recall", "_validator"} {
		baseID = strings.TrimSuffix(baseID, suffix)
	}
	for _, node := range graph.Nodes {
		if node.ID == nodeID || node.ID == baseID {
			// ADR-0069: Use strategy registry ContextRole when available.
			if activeRegistry != nil {
				if s, ok := activeRegistry.Get(node.Type); ok {
					return s.ContextRole().ProducesPlainText
				}
			}
			// Fallback: legacy hardcoded check
			switch node.Type {
			case "probe", "synthesis", "recall":
				return true
			}
		}
	}
	return false
}

// resolveInterpolatedArguments resolves tool arguments that were derived from upstream node outputs.
// When the original instruction template contained {{nodes.X.output.Y}} references (now resolved
// to actual values in interpolatedInstruction), this function extracts those resolved values and
// overrides any incorrect GBNF-extracted arguments.
//
// This is the primary fix for the 0% pass rate on devops_incident and hr_onboarding categories,
// where nearly all arguments are dynamic upstream dependencies that the GBNF bridge hallucinates.
func resolveInterpolatedArguments(args map[string]interface{}, interpolatedInstruction string, originalInstruction string, taskID string, graph *compiler.ExecutionGraph) {
	if len(args) == 0 || originalInstruction == "" || taskID == "" {
		return
	}

	// Check if the original instruction contained interpolation variables
	if !strings.Contains(originalInstruction, "{{nodes.") {
		return
	}

	// Find all interpolation references in the ORIGINAL instruction template
	reProp := regexp.MustCompile(`\{\{nodes\.([^.]+)\.output\.([^}]+)\}\}`)
	matches := reProp.FindAllStringSubmatch(originalInstruction, -1)
	if len(matches) == 0 {
		return
	}

	type resolvedRef struct {
		propertyKey   string // e.g. "customer_id"
		resolvedValue string // the actual value after interpolation
		contextBefore string // text before the reference for key matching
	}

	var refs []resolvedRef

	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		nodeID := match[1]
		propKey := match[2]

		resolvedValue := getUpstreamValue(taskID, nodeID, propKey, graph)
		if resolvedValue == "" || resolvedValue == "null" {
			continue
		}

		// Find index of this match in originalInstruction for contextBefore
		fullMatchStr := match[0]
		matchIdx := strings.Index(originalInstruction, fullMatchStr)
		contextBefore := ""
		if matchIdx != -1 {
			contextStart := matchIdx - 80
			if contextStart < 0 {
				contextStart = 0
			}
			contextBefore = strings.ToLower(originalInstruction[contextStart:matchIdx])
		}

		refs = append(refs, resolvedRef{
			propertyKey:   propKey,
			resolvedValue: resolvedValue,
			contextBefore: contextBefore,
		})
	}

	if len(refs) == 0 {
		return
	}

	// Match resolved references to argument keys
	for key, val := range args {
		var strVal string
		isStr := false
		var isNum bool
		var numVal float64

		switch v := val.(type) {
		case string:
			strVal = v
			isStr = true
		case float64:
			numVal = v
			isNum = true
			strVal = fmt.Sprintf("%v", v)
		case int:
			numVal = float64(v)
			isNum = true
			strVal = fmt.Sprintf("%v", v)
		case int64:
			numVal = float64(v)
			isNum = true
			strVal = fmt.Sprintf("%v", v)
		case float32:
			numVal = float64(v)
			isNum = true
			strVal = fmt.Sprintf("%v", v)
		}

		if !isStr && !isNum {
			continue
		}

		keyLower := strings.ToLower(key)
		keyWords := strings.Split(strings.ReplaceAll(keyLower, "_", " "), " ")

		for _, ref := range refs {
			// Match 1: Direct property key to argument key match
			refKeyLower := strings.ToLower(ref.propertyKey)
			refKeyNorm := strings.ReplaceAll(refKeyLower, "_", " ")

			matched := false

			// Exact key match (e.g. "customer_id" arg matches {{...output.customer_id}})
			if keyLower == refKeyLower {
				matched = true
			}

			// Semantic key overlap (e.g. "service" arg matches context containing "service")
			if !matched {
				for _, kw := range keyWords {
					if len(kw) >= 3 && (strings.Contains(refKeyNorm, kw) || strings.Contains(ref.contextBefore, kw)) {
						matched = true
						break
					}
				}
			}

			if matched {
				if isNum {
					refNum, err := strconv.ParseFloat(ref.resolvedValue, 64)
					if err == nil {
						if numVal != refNum {
							fmt.Fprintf(os.Stderr, "[Executor InterpolationResolver] Correcting numeric argument '%s': %v -> %v (from upstream {{nodes.*.output.%s}})\n", key, numVal, refNum, ref.propertyKey)
							args[key] = refNum
							break
						}
					}
				} else if isStr {
					if strVal != ref.resolvedValue {
						// Only override if the current value is wrong (not in instruction)
						if strVal == "" || strVal == "null" || !strings.Contains(strings.ToLower(interpolatedInstruction), strings.ToLower(strVal)) {
							fmt.Fprintf(os.Stderr, "[Executor InterpolationResolver] Correcting argument '%s': %q -> %q (from upstream {{nodes.*.output.%s}})\n", key, strVal, ref.resolvedValue, ref.propertyKey)
							args[key] = ref.resolvedValue
							break
						}
					}
				}
			}
		}
	}
}

