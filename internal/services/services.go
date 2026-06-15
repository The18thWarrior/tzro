// Package services provides a declarative service registry for managing
// background agents and daemons within the tzro daemon process.
// It replaces hardcoded agent startup with a unified Start/Stop/List abstraction.
package services

import (
	"fmt"
	"sort"
	"sync"
)

// ServiceDef defines a registerable background service.
type ServiceDef struct {
	Name    string       // Unique service name (e.g., "observer", "sentinel")
	Type    string       // Service type classification (e.g., "background_agent", "scheduler")
	Enabled bool         // Whether the service should be started by StartAll
	Start   func() error // Lifecycle start callback
	Stop    func() error // Lifecycle stop callback
}

// ServiceStatus is a read-only snapshot of a service's current state.
type ServiceStatus struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
	Status  string `json:"status"` // "stopped" | "running"
}

// Registry manages service definitions and their lifecycle state.
type Registry struct {
	mu       sync.RWMutex
	services map[string]*serviceEntry
	order    []string // insertion order for deterministic listing
}

type serviceEntry struct {
	def     ServiceDef
	running bool
}

// NewRegistry creates an empty service registry.
func NewRegistry() *Registry {
	return &Registry{
		services: make(map[string]*serviceEntry),
	}
}

// Register adds a service definition to the registry. If a service with the
// same name already exists, it is silently replaced.
func (r *Registry) Register(def ServiceDef) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.services[def.Name]; !exists {
		r.order = append(r.order, def.Name)
	}
	r.services[def.Name] = &serviceEntry{def: def}
}

// Start starts a service by name. Returns an error if the service is unknown.
// Starting an already-running service is a no-op (idempotent).
func (r *Registry) Start(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.services[name]
	if !ok {
		return fmt.Errorf("service '%s' not found", name)
	}
	if entry.running {
		return nil // idempotent
	}
	if entry.def.Start != nil {
		if err := entry.def.Start(); err != nil {
			return fmt.Errorf("failed to start service '%s': %w", name, err)
		}
	}
	entry.running = true
	return nil
}

// Stop stops a service by name. Returns an error if the service is unknown.
// Stopping an already-stopped service is a no-op.
func (r *Registry) Stop(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.services[name]
	if !ok {
		return fmt.Errorf("service '%s' not found", name)
	}
	if !entry.running {
		return nil
	}
	if entry.def.Stop != nil {
		if err := entry.def.Stop(); err != nil {
			return fmt.Errorf("failed to stop service '%s': %w", name, err)
		}
	}
	entry.running = false
	return nil
}

// StartAll starts all enabled services in registration order.
func (r *Registry) StartAll() {
	r.mu.RLock()
	names := make([]string, len(r.order))
	copy(names, r.order)
	r.mu.RUnlock()

	for _, name := range names {
		r.mu.RLock()
		entry := r.services[name]
		r.mu.RUnlock()
		if entry != nil && entry.def.Enabled {
			_ = r.Start(name)
		}
	}
}

// StopAll stops all running services in reverse registration order.
func (r *Registry) StopAll() {
	r.mu.RLock()
	names := make([]string, len(r.order))
	copy(names, r.order)
	r.mu.RUnlock()

	// Reverse order for graceful shutdown
	for i := len(names) - 1; i >= 0; i-- {
		_ = r.Stop(names[i])
	}
}

// List returns a snapshot of all registered services and their current status.
// Services are returned in alphabetical order by name for deterministic output.
func (r *Registry) List() []ServiceStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make([]ServiceStatus, 0, len(r.services))
	for _, entry := range r.services {
		status := "stopped"
		if entry.running {
			status = "running"
		}
		list = append(list, ServiceStatus{
			Name:    entry.def.Name,
			Type:    entry.def.Type,
			Enabled: entry.def.Enabled,
			Status:  status,
		})
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})
	return list
}
