package compiler

import (
	"encoding/json"
	"testing"
)

func TestGraphNode_UnmarshalStaticArgs(t *testing.T) {
	tests := []struct {
		name     string
		jsonStr  string
		expected string
	}{
		{
			name:     "string staticArgs",
			jsonStr:  `{"id":"node_1","type":"action","staticArgs":"README.md"}`,
			expected: "README.md",
		},
		{
			name:     "object staticArgs",
			jsonStr:  `{"id":"node_1","type":"action","staticArgs":{"path":"README.md","overwrite":true}}`,
			expected: `{"path":"README.md","overwrite":true}`,
		},
		{
			name:     "empty/omitted staticArgs",
			jsonStr:  `{"id":"node_1","type":"action"}`,
			expected: "",
		},
		{
			name:     "null staticArgs",
			jsonStr:  `{"id":"node_1","type":"action","staticArgs":null}`,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var node GraphNode
			err := json.Unmarshal([]byte(tt.jsonStr), &node)
			if err != nil {
				t.Fatalf("unexpected unmarshal error: %v", err)
			}
			if node.StaticArgs != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, node.StaticArgs)
			}
		})
	}
}
