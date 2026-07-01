package codegen

import (
	"strings"
	"testing"
)

func TestBuildPseudocodeExpansionPrompt_Create(t *testing.T) {
	pseudocode := `package cache; imports: container/list, sync, time

struct Cache[K,V] { mu sync.RWMutex, items map[K]*list.Element }

func New[K,V](maxSize, defaultTTL) *Cache[K,V]:
  return &Cache{items: make(map), evictList: list.New()}

func (c *Cache) Get(key K) (V, bool):
  lock mu; lookup in map; if expired: remove, return zero,false
  MoveToFront; return value, true`

	prompt := BuildPseudocodeExpansionPrompt(
		pseudocode,
		"Create a generic LRU cache with TTL support",
		"/path/to/cache/lru.go",
		"go",
		"create",
		"", // no existing content
		nil,
		500,
		"",
	)

	// Must contain the pseudo-code
	if !strings.Contains(prompt, "struct Cache[K,V]") {
		t.Error("prompt must contain the pseudo-code")
	}

	// Must contain the spec for additional context
	if !strings.Contains(prompt, "Create a generic LRU cache") {
		t.Error("prompt must contain the spec")
	}

	// Must have expansion-specific system instruction
	if !strings.Contains(prompt, "code expander") {
		t.Error("prompt must identify as a code expander, not a code generator")
	}

	// Must reference the target language in expansion rules
	if !strings.Contains(prompt, "go") {
		t.Error("prompt must reference the target language")
	}

	// Must have expansion-specific rules (different from standard codegen prompt)
	if !strings.Contains(prompt, "Expand ALL pseudo-code") {
		t.Error("prompt must contain expansion-specific rules")
	}

	// Must NOT contain existing content section for create action
	if strings.Contains(prompt, "Existing Content") {
		t.Error("prompt must NOT contain Existing Content for create action")
	}

	// Must contain line cap
	if !strings.Contains(prompt, "500") {
		t.Error("prompt must contain the line cap")
	}
}

func TestBuildPseudocodeExpansionPrompt_Update(t *testing.T) {
	pseudocode := `func (c *Cache) Delete(key): lock; removeElement if exists
func (c *Cache) Len() int: RLock; return len(items)`

	prompt := BuildPseudocodeExpansionPrompt(
		pseudocode,
		"Add Delete and Len methods",
		"/path/to/cache/lru.go",
		"go",
		"update",
		"package cache\n\ntype Cache struct {\n\tmu sync.RWMutex\n}\n",
		map[string]string{
			"types.go": "package cache\n\ntype Entry struct{}\n",
		},
		300,
		"",
	)

	// Must contain existing content section for update
	if !strings.Contains(prompt, "Existing Content") {
		t.Error("prompt must contain Existing Content for update action")
	}
	if !strings.Contains(prompt, "type Cache struct") {
		t.Error("prompt must include the existing file content")
	}

	// Must contain sibling files
	if !strings.Contains(prompt, "### types.go") {
		t.Error("prompt must include sibling file heading")
	}
	if !strings.Contains(prompt, "type Entry struct") {
		t.Error("prompt must include sibling content")
	}

	// Must still have pseudo-code and spec
	if !strings.Contains(prompt, "func (c *Cache) Delete") {
		t.Error("prompt must contain the pseudo-code")
	}
	if !strings.Contains(prompt, "Add Delete and Len") {
		t.Error("prompt must contain the spec")
	}
}

func TestBuildPseudocodeExpansionPrompt_NoSpec(t *testing.T) {
	prompt := BuildPseudocodeExpansionPrompt(
		"func hello(): print 'world'",
		"", // empty spec
		"/path/to/hello.py",
		"python",
		"create",
		"",
		nil,
		100,
		"",
	)

	// Spec section should be omitted entirely
	if strings.Contains(prompt, "## Spec") {
		t.Error("prompt must NOT contain Spec section when spec is empty")
	}

	// Pseudo-code must still be present
	if !strings.Contains(prompt, "func hello()") {
		t.Error("prompt must still contain the pseudo-code")
	}
}

func TestBuildPseudocodeExpansionDAG_Structure(t *testing.T) {
	ctx := &CodeContext{
		Exists:          true,
		ExistingContent: "package cache\n\nfunc Old() {}\n",
		Language:        "go",
		Siblings: map[string]string{
			"types.go": "package cache\n\ntype Config struct{}\n",
		},
	}

	graph := BuildPseudocodeExpansionDAG(
		"task_expand_1",
		"struct Cache[K,V] { items map[K]*Element }",
		"Add LRU cache implementation",
		"/tmp/cache.go",
		"go",
		500,
		ctx,
	)

	// Two-node graph: reason_code → validate_code
	if len(graph.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(graph.Nodes))
	}

	node := graph.Nodes[0]

	// Node ID and type
	if node.ID != "reason_code" {
		t.Errorf("expected node ID 'reason_code', got %q", node.ID)
	}
	if node.Type != "synthesis" {
		t.Errorf("expected node type 'synthesis', got %q", node.Type)
	}

	// No tools needed for pure expansion
	if len(node.AllowedTools) != 0 {
		t.Errorf("expected no allowed tools, got %v", node.AllowedTools)
	}

	// Edge: reason_code → validate_code
	if len(graph.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(graph.Edges))
	}
	if graph.Edges[0].SourceID != "reason_code" || graph.Edges[0].TargetID != "validate_code" {
		t.Errorf("expected edge reason_code→validate_code")
	}

	// MutationBudget
	if graph.MutationBudget == nil {
		t.Fatal("expected MutationBudget")
	}
	if graph.MutationBudget.MaxSpawns != 1 {
		t.Errorf("expected MaxSpawns=1, got %d", graph.MutationBudget.MaxSpawns)
	}

	// Goal prompt should mention expansion
	if !strings.Contains(graph.GoalPrompt, "Expand pseudo-code") {
		t.Error("goal prompt should mention pseudo-code expansion")
	}

	// Instructions should contain the pseudo-code
	if !strings.Contains(node.Instructions, "struct Cache[K,V]") {
		t.Error("node instructions must contain the pseudo-code")
	}

	// Instructions should contain existing content (update action)
	if !strings.Contains(node.Instructions, "func Old()") {
		t.Error("node instructions should include existing file content for update")
	}

	// Instructions should contain siblings
	if !strings.Contains(node.Instructions, "types.go") {
		t.Error("node instructions should include sibling files")
	}

	// TaskID
	if graph.TaskID != "task_expand_1" {
		t.Errorf("expected taskID 'task_expand_1', got %q", graph.TaskID)
	}
}

func TestBuildPseudocodeExpansionDAG_NewFile(t *testing.T) {
	// nil-ish context: file doesn't exist
	ctx := &CodeContext{
		Exists:   false,
		Language: "typescript",
		Siblings: map[string]string{},
	}

	graph := BuildPseudocodeExpansionDAG(
		"task_expand_2",
		"class EventEmitter { on(event, handler): add to listeners }",
		"Create an event emitter",
		"/tmp/emitter.ts",
		"typescript",
		300,
		ctx,
	)

	node := graph.Nodes[0]

	// Should be create action (no existing content section)
	if strings.Contains(node.Instructions, "Existing Content") {
		t.Error("should not include Existing Content for new file")
	}

	// Language should come from context
	if !strings.Contains(node.Instructions, "typescript") {
		t.Error("instructions should reference typescript")
	}
}
