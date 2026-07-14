package tools

import (
	"sync"
)

// ProactivityLevel constants mirror proactivity.ProactivityLevel but are defined
// here to avoid a circular dependency between tools and proactivity packages.
const (
	PLevelObserve            = 0 // L0: read-only observation
	PLevelPrepare            = 1 // L1: local deterministic preparation
	PLevelSuggest            = 2 // L2: surfaces recommendations
	PLevelReversibleAction   = 3 // L3: local reversible actions
	PLevelExternalSideEffect = 4 // L4: costly or external-facing actions
)

var (
	proactivityLevels      = make(map[string]int)
	proactivityLevelsMutex sync.RWMutex

	// builtInToolLevels maps built-in tool names to their hardcoded proactivity levels.
	// These are set once at init and represent the source-based defaults described in the PRD.
	builtInToolLevels = map[string]int{
		// L0 — read-only observation
		"read_file":        PLevelObserve,
		"list_dir":         PLevelObserve,
		"search_files":     PLevelObserve,
		"introspect_cache": PLevelObserve,
		"sql_cached_data":   PLevelObserve,
		"list_tools":       PLevelObserve,

		// L1 — local deterministic preparation
		"save_memory":    PLevelPrepare,
		"recall_memory":  PLevelPrepare,
		"forget_memory":  PLevelPrepare,
		"search_kb":      PLevelPrepare,
		"query_kg":       PLevelPrepare,
		"explore_entity": PLevelPrepare,
		"ingest_kg":      PLevelPrepare,

		// L2 — suggestions (none currently built-in as tools)

		// L3 — reversible local actions
		"create_database": PLevelReversibleAction,
		"create_table":    PLevelReversibleAction,
		"insert":          PLevelReversibleAction,
		"update":          PLevelReversibleAction,
		"delete":          PLevelReversibleAction,
		"query":           PLevelReversibleAction,
		"create_task":     PLevelReversibleAction,

		// L4 — external side effects
		"web_search": PLevelExternalSideEffect,
	}
)

// GetProactivityLevel returns the proactivity level for a tool by name.
// Resolution order:
//  1. Explicit override (set via SetProactivityLevelOverride)
//  2. Built-in default (hardcoded map for known tools)
//  3. Type-based default: MCPToolAdapter → L3, ClientToolAdapter → L1, others → L1
func GetProactivityLevel(name string) int {
	proactivityLevelsMutex.RLock()
	if level, ok := proactivityLevels[name]; ok {
		proactivityLevelsMutex.RUnlock()
		return level
	}
	proactivityLevelsMutex.RUnlock()

	// Check built-in defaults
	if level, ok := builtInToolLevels[name]; ok {
		return level
	}

	// Type-based defaults from registry
	mutex.RLock()
	t, exists := registry[name]
	mutex.RUnlock()

	if exists {
		switch t.(type) {
		case *MCPToolAdapter:
			return PLevelReversibleAction // L3 default for MCP Host tools
		case *ClientToolAdapter:
			return PLevelPrepare // L1 default for harness-forwarded tools
		}
	}

	// Default: L1 for unknown/function tools
	return PLevelPrepare
}

// SetProactivityLevelOverride sets an explicit proactivity level override for a tool.
// This is used for config-driven level customization.
func SetProactivityLevelOverride(name string, level int) {
	proactivityLevelsMutex.Lock()
	defer proactivityLevelsMutex.Unlock()
	proactivityLevels[name] = level
}

// ClearProactivityLevelOverride removes an explicit override, reverting to defaults.
func ClearProactivityLevelOverride(name string) {
	proactivityLevelsMutex.Lock()
	defer proactivityLevelsMutex.Unlock()
	delete(proactivityLevels, name)
}

// ClearAllProactivityLevelOverrides removes all overrides. Primarily for testing.
func ClearAllProactivityLevelOverrides() {
	proactivityLevelsMutex.Lock()
	defer proactivityLevelsMutex.Unlock()
	proactivityLevels = make(map[string]int)
}
