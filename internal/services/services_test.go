package services

import (
	"testing"
)

func TestRegistry_ListsZeroServicesInitially(t *testing.T) {
	r := NewRegistry()
	list := r.List()
	if len(list) != 0 {
		t.Errorf("expected 0 services, got %d", len(list))
	}
}

func TestRegistry_RegisterMakesServiceAppearInList(t *testing.T) {
	r := NewRegistry()
	r.Register(ServiceDef{
		Name:    "test-svc",
		Type:    "background",
		Enabled: true,
		Start:   func() error { return nil },
		Stop:    func() error { return nil },
	})

	list := r.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 service, got %d", len(list))
	}
	if list[0].Name != "test-svc" {
		t.Errorf("expected name 'test-svc', got '%s'", list[0].Name)
	}
	if list[0].Status != "stopped" {
		t.Errorf("expected status 'stopped', got '%s'", list[0].Status)
	}
}

func TestRegistry_StartChangesStatusToRunning(t *testing.T) {
	r := NewRegistry()
	r.Register(ServiceDef{
		Name:    "test-svc",
		Type:    "background",
		Enabled: true,
		Start:   func() error { return nil },
		Stop:    func() error { return nil },
	})

	if err := r.Start("test-svc"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	list := r.List()
	if list[0].Status != "running" {
		t.Errorf("expected status 'running', got '%s'", list[0].Status)
	}
}

func TestRegistry_StopChangesStatusToStopped(t *testing.T) {
	r := NewRegistry()
	r.Register(ServiceDef{
		Name:    "test-svc",
		Type:    "background",
		Enabled: true,
		Start:   func() error { return nil },
		Stop:    func() error { return nil },
	})

	_ = r.Start("test-svc")
	if err := r.Stop("test-svc"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	list := r.List()
	if list[0].Status != "stopped" {
		t.Errorf("expected status 'stopped', got '%s'", list[0].Status)
	}
}

func TestRegistry_StartIsIdempotent(t *testing.T) {
	callCount := 0
	r := NewRegistry()
	r.Register(ServiceDef{
		Name:    "test-svc",
		Type:    "background",
		Enabled: true,
		Start:   func() error { callCount++; return nil },
		Stop:    func() error { return nil },
	})

	_ = r.Start("test-svc")
	_ = r.Start("test-svc")

	if callCount != 1 {
		t.Errorf("expected Start called once (idempotent), but was called %d times", callCount)
	}
}

func TestRegistry_StartAllStartsEnabledServices(t *testing.T) {
	r := NewRegistry()
	r.Register(ServiceDef{
		Name:    "enabled-svc",
		Type:    "background",
		Enabled: true,
		Start:   func() error { return nil },
		Stop:    func() error { return nil },
	})
	r.Register(ServiceDef{
		Name:    "disabled-svc",
		Type:    "background",
		Enabled: false,
		Start:   func() error { return nil },
		Stop:    func() error { return nil },
	})

	r.StartAll()

	list := r.List()
	for _, s := range list {
		if s.Name == "enabled-svc" && s.Status != "running" {
			t.Errorf("expected enabled-svc to be running, got '%s'", s.Status)
		}
		if s.Name == "disabled-svc" && s.Status != "stopped" {
			t.Errorf("expected disabled-svc to remain stopped, got '%s'", s.Status)
		}
	}
}

func TestRegistry_StopAllStopsRunningServices(t *testing.T) {
	r := NewRegistry()
	r.Register(ServiceDef{
		Name:    "svc-a",
		Type:    "background",
		Enabled: true,
		Start:   func() error { return nil },
		Stop:    func() error { return nil },
	})
	r.Register(ServiceDef{
		Name:    "svc-b",
		Type:    "background",
		Enabled: true,
		Start:   func() error { return nil },
		Stop:    func() error { return nil },
	})

	r.StartAll()
	r.StopAll()

	for _, s := range r.List() {
		if s.Status != "stopped" {
			t.Errorf("expected %s to be stopped, got '%s'", s.Name, s.Status)
		}
	}
}

func TestRegistry_StartUnknownServiceReturnsError(t *testing.T) {
	r := NewRegistry()
	err := r.Start("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown service")
	}
}

func TestRegistry_StopUnknownServiceReturnsError(t *testing.T) {
	r := NewRegistry()
	err := r.Stop("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown service")
	}
}
