package executor

import (
	"fmt"
	"strings"
)

// ResolvePointers walks the args map and replaces any {"$ref": "/nodes/<id>/output/<field>"}
// values with the actual output from previously completed nodes.
func ResolvePointers(args map[string]interface{}, outputs map[string]NodeOutput) (map[string]interface{}, error) {
	if args == nil {
		return nil, nil
	}
	resolved := make(map[string]interface{}, len(args))
	for k, v := range args {
		r, err := resolveValue(v, outputs)
		if err != nil {
			return nil, fmt.Errorf("resolving key %q: %w", k, err)
		}
		resolved[k] = r
	}
	return resolved, nil
}

func resolveValue(v interface{}, outputs map[string]NodeOutput) (interface{}, error) {
	switch val := v.(type) {
	case map[string]interface{}:
		// Check if this is a $ref pointer
		if ref, ok := val["$ref"]; ok {
			refStr, isStr := ref.(string)
			if !isStr {
				return nil, fmt.Errorf("$ref value must be a string, got %T", ref)
			}
			return resolvePointer(refStr, outputs)
		}
		// Recursively resolve nested maps
		resolved := make(map[string]interface{}, len(val))
		for k, inner := range val {
			r, err := resolveValue(inner, outputs)
			if err != nil {
				return nil, err
			}
			resolved[k] = r
		}
		return resolved, nil

	case []interface{}:
		resolved := make([]interface{}, len(val))
		for i, inner := range val {
			r, err := resolveValue(inner, outputs)
			if err != nil {
				return nil, err
			}
			resolved[i] = r
		}
		return resolved, nil

	default:
		return v, nil
	}
}

// resolvePointer resolves a JSON pointer path like "/nodes/<id>/output/<field>"
// against the completed node outputs.
func resolvePointer(ref string, outputs map[string]NodeOutput) (interface{}, error) {
	// Expected format: /nodes/<node_id>/output/<field>
	parts := strings.Split(strings.TrimPrefix(ref, "/"), "/")
	if len(parts) < 4 || parts[0] != "nodes" || parts[2] != "output" {
		return nil, fmt.Errorf("invalid $ref pointer format: %q (expected /nodes/<id>/output/<field>)", ref)
	}

	nodeID := parts[1]
	field := parts[3]

	output, ok := outputs[nodeID]
	if !ok {
		return nil, fmt.Errorf("$ref references unknown node %q", nodeID)
	}

	switch field {
	case "stdout":
		return strings.TrimSpace(output.Stdout), nil
	case "stderr":
		return strings.TrimSpace(output.Stderr), nil
	case "exit_code":
		return output.ExitCode, nil
	case "status":
		return output.Status, nil
	default:
		// Check the Data map for custom fields
		if output.Data != nil {
			if val, ok := output.Data[field]; ok {
				return val, nil
			}
		}
		return nil, fmt.Errorf("$ref field %q not found in node %q output", field, nodeID)
	}
}
