package inference

import "testing"

func TestDualSidecar_GlobalsAreSeparateInstances(t *testing.T) {
	if GlobalRouterModel == nil {
		t.Fatal("GlobalRouterModel should not be nil")
	}
	if GlobalWorkerModel == nil {
		t.Fatal("GlobalWorkerModel should not be nil")
	}
	if GlobalRouterModel == GlobalWorkerModel {
		t.Error("GlobalRouterModel and GlobalWorkerModel should be distinct instances")
	}
}

func TestDualSidecar_GlobalLocalModelAliasPointsToWorker(t *testing.T) {
	// Backward compat: GlobalLocalModel should be the same as GlobalWorkerModel
	if GlobalLocalModel != GlobalWorkerModel {
		t.Error("GlobalLocalModel should alias GlobalWorkerModel for backward compatibility")
	}
}

func TestDualSidecar_RoleIdentity(t *testing.T) {
	if GlobalRouterModel.Role != "router" {
		t.Errorf("GlobalRouterModel.Role = %q, want \"router\"", GlobalRouterModel.Role)
	}
	if GlobalWorkerModel.Role != "worker" {
		t.Errorf("GlobalWorkerModel.Role = %q, want \"worker\"", GlobalWorkerModel.Role)
	}
}
