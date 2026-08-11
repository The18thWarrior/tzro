package main

import (
	"testing"

	"tzro/internal/memory"
	"tzro/internal/stream"
)

func TestComputeProgress_AllPending(t *testing.T) {
	nodes := []memory.NodeState{
		{TaskID: "t1", NodeID: "n1", Status: "pending"},
		{TaskID: "t1", NodeID: "n2", Status: "pending"},
	}
	p := computeProgress(nodes)
	if p.Status != "pending" {
		t.Errorf("status = %q, want pending", p.Status)
	}
	if p.Pending != 2 || p.Total != 2 {
		t.Errorf("pending=%d total=%d, want 2/2", p.Pending, p.Total)
	}
}

func TestComputeProgress_Running(t *testing.T) {
	nodes := []memory.NodeState{
		{TaskID: "t1", NodeID: "n1", Status: "completed"},
		{TaskID: "t1", NodeID: "n2", Status: "running"},
		{TaskID: "t1", NodeID: "n3", Status: "pending"},
	}
	p := computeProgress(nodes)
	if p.Status != "running" {
		t.Errorf("status = %q, want running", p.Status)
	}
	if p.Completed != 1 || p.Running != 1 || p.Pending != 1 || p.Total != 3 {
		t.Errorf("progress = %+v, want 1 completed, 1 running, 1 pending", p)
	}
}

func TestComputeProgress_AllCompleted(t *testing.T) {
	nodes := []memory.NodeState{
		{TaskID: "t1", NodeID: "n1", Status: "completed"},
		{TaskID: "t1", NodeID: "n2", Status: "completed"},
	}
	p := computeProgress(nodes)
	if p.Status != "completed" {
		t.Errorf("status = %q, want completed", p.Status)
	}
}

func TestComputeProgress_FailedOverridesRunning(t *testing.T) {
	nodes := []memory.NodeState{
		{TaskID: "t1", NodeID: "n1", Status: "completed"},
		{TaskID: "t1", NodeID: "n2", Status: "failed"},
		{TaskID: "t1", NodeID: "n3", Status: "running"},
	}
	p := computeProgress(nodes)
	if p.Status != "failed" {
		t.Errorf("status = %q, want failed (failed overrides running)", p.Status)
	}
	if p.Failed != 1 {
		t.Errorf("failed = %d, want 1", p.Failed)
	}
}

func TestComputeProgress_EmptyNodes(t *testing.T) {
	p := computeProgress(nil)
	if p.Status != "pending" {
		t.Errorf("status = %q, want pending for empty", p.Status)
	}
	if p.Total != 0 {
		t.Errorf("total = %d, want 0", p.Total)
	}
}

func TestRecordChunkEvent_SkipsEmptyTaskID(t *testing.T) {
	// Reset buffer
	buf := NewEventBuffer(10)
	old := taskEventBuffer
	taskEventBuffer = buf
	defer func() { taskEventBuffer = old }()

	recordChunkEvent(stream.StreamChunk{Type: "test"})
	if events := buf.Recent("", 10); events != nil {
		t.Errorf("expected nil for empty taskID, got %v", events)
	}
}

func TestRecordChunkEvent_RecordsNodeLifecycle(t *testing.T) {
	buf := NewEventBuffer(10)
	old := taskEventBuffer
	taskEventBuffer = buf
	defer func() { taskEventBuffer = old }()

	recordChunkEvent(stream.StreamChunk{
		TaskID: "task1",
		NodeID: "n1",
		Type:   "node_started",
	})
	recordChunkEvent(stream.StreamChunk{
		TaskID: "task1",
		NodeID: "n1",
		Type:   "node_completed",
	})

	events := buf.Recent("task1", 10)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != "node_started" {
		t.Errorf("events[0].Type = %q, want node_started", events[0].Type)
	}
	if events[1].Type != "node_completed" {
		t.Errorf("events[1].Type = %q, want node_completed", events[1].Type)
	}
}
