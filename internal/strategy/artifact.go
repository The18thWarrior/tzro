package strategy

import (
	"encoding/json"
	"sync"
)

// ---------------------------------------------------------------------------
// ArtifactKey — compile-time-safe typed key for artifact access
// ---------------------------------------------------------------------------

// ArtifactKey is a typed key for compile-time safe artifact access.
// Each key knows its payload type via Go generics. Use GetArtifact and
// SetArtifact for type-safe access.
type ArtifactKey[T any] struct {
	Name string
}

// ---------------------------------------------------------------------------
// Well-known artifact keys — the shared vocabulary
// ---------------------------------------------------------------------------
//
// Built-in strategies use these keys to pass typed data between stages.
// Custom strategies define additional keys in their own packages.
//
// Note: These are placeholder types for now. As strategies are extracted
// from the executor, the actual types (EdgeEntry, SymbolTuple, etc.) will
// be defined here or in internal/compiler.

// KeyTerminalSynthesis carries the final synthesis output text.
var KeyTerminalSynthesis = ArtifactKey[string]{Name: "terminalSynthesis"}

// KeyRefinedContext carries the refined/aligned context from recall nodes.
var KeyRefinedContext = ArtifactKey[string]{Name: "refinedContext"}

// KeyDirectoryManifest carries the directory tree manifest from probe Orient.
var KeyDirectoryManifest = ArtifactKey[string]{Name: "directoryManifest"}

// KeyAnalyticalEvidence carries structured raw data from sql_cached_data calls.
var KeyAnalyticalEvidence = ArtifactKey[string]{Name: "analyticalEvidence"}

// KeyEdgeEntries carries accumulated edge entries from probe exploration.
// Type will be refined to []EdgeEntry when the type is extracted.
var KeyEdgeEntries = ArtifactKey[string]{Name: "edgeEntries"}

// KeySymbolIndex carries AST-extracted symbols from probe exploration.
// Type will be refined to []SymbolTuple when the type is extracted.
var KeySymbolIndex = ArtifactKey[string]{Name: "symbolIndex"}

// KeyCacheSchema carries cache schema information for analyze nodes.
var KeyCacheSchema = ArtifactKey[string]{Name: "cacheSchema"}

// KeySQLQueries carries SQL query results from analyze nodes.
var KeySQLQueries = ArtifactKey[string]{Name: "sqlQueries"}

// ---------------------------------------------------------------------------
// ArtifactStore — dual-layer typed/serialized store
// ---------------------------------------------------------------------------

// ArtifactStore is a dual-layer data store for passing structured outputs
// between Stages within a Node Strategy.
//
// Layer 1 (typed): Go-native access via ArtifactKey[T] generics. Zero
// serialization cost. Used by built-in strategies on the hot path.
//
// Layer 2 (serialized): JSON wire format for WASM/external strategies.
// Lazy conversion at the boundary — serialized on first WASM read,
// deserialized on first Go read.
type ArtifactStore struct {
	mu         sync.RWMutex
	typed      map[string]interface{}
	serialized map[string]json.RawMessage
}

// NewArtifactStore creates an empty ArtifactStore.
func NewArtifactStore() *ArtifactStore {
	return &ArtifactStore{
		typed:      make(map[string]interface{}),
		serialized: make(map[string]json.RawMessage),
	}
}

// GetTyped retrieves a typed artifact by name. Returns the value and a
// boolean indicating whether the artifact was found and the type assertion
// succeeded. This is the low-level accessor — prefer the generic
// GetArtifact function for compile-time safety.
func (s *ArtifactStore) GetTyped(name string) (interface{}, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.typed[name]
	return v, ok
}

// SetTyped stores a typed artifact by name.
func (s *ArtifactStore) SetTyped(name string, value interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.typed[name] = value
	// Invalidate any cached serialized form
	delete(s.serialized, name)
}

// GetSerialized retrieves an artifact in JSON wire format. If the artifact
// exists only in the typed layer, it is lazily serialized on first access.
func (s *ArtifactStore) GetSerialized(name string) (json.RawMessage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check serialized layer first
	if v, ok := s.serialized[name]; ok {
		return v, true
	}

	// Lazy serialize from typed layer
	if v, ok := s.typed[name]; ok {
		data, err := json.Marshal(v)
		if err != nil {
			return nil, false
		}
		s.serialized[name] = json.RawMessage(data)
		return s.serialized[name], true
	}

	return nil, false
}

// SetSerialized stores an artifact in JSON wire format.
func (s *ArtifactStore) SetSerialized(name string, data json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serialized[name] = data
	// Invalidate any cached typed form
	delete(s.typed, name)
}

// Keys returns all artifact key names across both layers.
func (s *ArtifactStore) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := make(map[string]bool)
	var keys []string
	for k := range s.typed {
		if !seen[k] {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	for k := range s.serialized {
		if !seen[k] {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	return keys
}

// Merge copies all artifacts from other into this store. Existing keys
// in this store are NOT overwritten.
func (s *ArtifactStore) Merge(other *ArtifactStore) {
	if other == nil {
		return
	}

	other.mu.RLock()
	defer other.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	for k, v := range other.typed {
		if _, exists := s.typed[k]; !exists {
			s.typed[k] = v
		}
	}
	for k, v := range other.serialized {
		if _, exists := s.serialized[k]; !exists {
			s.serialized[k] = v
		}
	}
}

// ---------------------------------------------------------------------------
// Generic artifact access functions
// ---------------------------------------------------------------------------

// GetArtifact retrieves a typed artifact with compile-time type safety.
// Returns the value and a boolean indicating whether the artifact was
// found and the type assertion succeeded. Missing artifacts return the
// zero value and false — graceful degradation, not panics.
func GetArtifact[T any](store *ArtifactStore, key ArtifactKey[T]) (T, bool) {
	if store == nil {
		var zero T
		return zero, false
	}

	store.mu.RLock()

	// Try typed layer first (hot path, zero serialization)
	if raw, ok := store.typed[key.Name]; ok {
		store.mu.RUnlock()
		if val, ok := raw.(T); ok {
			return val, true
		}
		var zero T
		return zero, false
	}

	// Try serialized layer — copy the data under RLock, then release
	var dataCopy json.RawMessage
	if data, ok := store.serialized[key.Name]; ok {
		dataCopy = make(json.RawMessage, len(data))
		copy(dataCopy, data)
	}
	store.mu.RUnlock()

	if dataCopy == nil {
		var zero T
		return zero, false
	}

	// Deserialize outside any lock (may be expensive)
	var val T
	if err := json.Unmarshal(dataCopy, &val); err != nil {
		var zero T
		return zero, false
	}

	// Cache in typed layer under write lock
	store.mu.Lock()
	store.typed[key.Name] = val
	store.mu.Unlock()

	return val, true
}

// SetArtifact stores a typed artifact with compile-time type safety.
func SetArtifact[T any](store *ArtifactStore, key ArtifactKey[T], value T) {
	if store == nil {
		return
	}
	store.SetTyped(key.Name, value)
}
