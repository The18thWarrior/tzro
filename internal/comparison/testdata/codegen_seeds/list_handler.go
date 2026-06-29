package handlers

import (
	"encoding/json"
	"net/http"
	"sync"
)

// Item represents a resource in the system.
type Item struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
}

// ItemStore is a thread-safe in-memory store of items.
type ItemStore struct {
	mu    sync.RWMutex
	items []Item
}

// NewItemStore creates an empty ItemStore.
func NewItemStore() *ItemStore {
	return &ItemStore{}
}

// Add appends an item to the store.
func (s *ItemStore) Add(item Item) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append(s.items, item)
}

// All returns all items in insertion order.
func (s *ItemStore) All() []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Item, len(s.items))
	copy(result, s.items)
	return result
}

// ListHandler returns an HTTP handler that responds with all items as JSON.
func ListHandler(store *ItemStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items := store.All()

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(items); err != nil {
			http.Error(w, "failed to encode response", http.StatusInternalServerError)
		}
	}
}
