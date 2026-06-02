package notification

import (
	"context"
	"os"
	"testing"
	"time"

	"tzro/internal/memory"
	"tzro/internal/stream"
)

func setupTestDB(t *testing.T) func() {
	oldDB := memory.DB.GetDBPathForTesting()
	testDB := "tzro_notification_test.db"
	memory.DB.SetDBPathForTesting(testDB)

	err := memory.DB.Init()
	if err != nil {
		t.Fatalf("failed to initialize test db: %v", err)
	}

	return func() {
		memory.DB.Close()
		os.Remove(testDB)
		memory.DB.SetDBPathForTesting(oldDB)
	}
}

func TestNotificationSendAndList(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Register subscription on StreamBus to verify real-time SSE broadcasts
	sub := stream.GlobalBus.Subscribe(func(chunk stream.StreamChunk) bool {
		return chunk.Source == "notification"
	})
	defer sub.Unsubscribe()

	// 1. Send normal info notification
	n, err := Send(ctx, "executor", "info", "Task Complete", "Task finished successfully", WithTaskID("t_123"), WithTargetID("node_456"))
	if err != nil {
		t.Fatalf("failed to send notification: %v", err)
	}

	if n.Source != "executor" || n.Type != "info" || n.Title != "Task Complete" || n.Message != "Task finished successfully" || n.TaskID != "t_123" || n.TargetID != "node_456" || n.Status != "unread" {
		t.Errorf("unexpected notification fields: %+v", n)
	}

	// Verify SSE broadcast was received
	select {
	case chunk := <-sub.Ch:
		if chunk.Type != "notification_created" || chunk.Source != "notification" {
			t.Errorf("unexpected stream chunk: %+v", chunk)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out waiting for StreamBus notification chunk")
	}

	// 2. Fetch list of unread
	list, err := List(ctx, "unread")
	if err != nil {
		t.Fatalf("failed to list notifications: %v", err)
	}
	if len(list) != 1 || list[0].ID != n.ID {
		t.Errorf("expected list to contain 1 unread notification, got %d", len(list))
	}

	// 3. Mark read
	err = MarkRead(ctx, n.ID, "read")
	if err != nil {
		t.Fatalf("failed to mark read: %v", err)
	}

	list, err = List(ctx, "unread")
	if err != nil {
		t.Fatalf("failed to list unread: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 unread notifications, got %d", len(list))
	}

	list, err = List(ctx, "read")
	if err != nil {
		t.Fatalf("failed to list read: %v", err)
	}
	if len(list) != 1 || list[0].ID != n.ID {
		t.Errorf("expected 1 read notification, got %d", len(list))
	}
}

func TestNotificationDeduplication(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Send duplicate unread warnings within deduplication window (10 minutes)
	n1, err := Send(ctx, "sidecar", "warning", "High CPU", "CPU usage is 95%")
	if err != nil {
		t.Fatalf("failed to send first warning: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	n2, err := Send(ctx, "sidecar", "warning", "High CPU", "CPU usage has reached 98%")
	if err != nil {
		t.Fatalf("failed to send second warning: %v", err)
	}

	// Because it was identical (source, type, title) and within 10 minutes,
	// it should have UPDATED the existing record instead of inserting a new one!
	if n1.ID != n2.ID {
		t.Errorf("expected same ID due to deduplication, got n1=%s and n2=%s", n1.ID, n2.ID)
	}

	list, err := List(ctx, "unread")
	if err != nil {
		t.Fatalf("failed to list: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected exactly 1 notification in db, got %d", len(list))
	}

	if list[0].Message != "CPU usage has reached 98%" {
		t.Errorf("expected message to be updated to latest, got: %s", list[0].Message)
	}
}
