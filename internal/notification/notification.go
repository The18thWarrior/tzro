package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tzro/internal/memory"
	"tzro/internal/stream"
)

type Notification = memory.DurableNotification

type Option func(*Notification)

func WithTaskID(id string) Option {
	return func(n *Notification) {
		n.TaskID = id
	}
}

func WithWorkflowID(id string) Option {
	return func(n *Notification) {
		n.WorkflowID = id
	}
}

func WithTargetID(id string) Option {
	return func(n *Notification) {
		n.TargetID = id
	}
}

func WithActionPayload(payload string) Option {
	return func(n *Notification) {
		n.ActionPayload = payload
	}
}

// Send durably saves a notification to SQLite and publishes it to stream.GlobalBus.
// It automatically updates identical unread warnings or errors from the same source within 10 minutes to prevent spam.
func Send(ctx context.Context, source, nType, title, message string, opts ...Option) (*Notification, error) {
	now := time.Now().Unix()

	// 1. Check for duplicates in the last 10 minutes (600 seconds)
	if nType == "warning" || nType == "error" {
		existing, err := memory.DB.GetNotifications("unread")
		if err == nil {
			for _, entry := range existing {
				if entry.Source == source && entry.Type == nType && entry.Title == title {
					// Check if within 10 minutes
					if now-entry.CreatedAt <= 600 {
						// Found duplicate! Update it instead of inserting a new one
						entry.Message = message // Update message with latest info
						entry.CreatedAt = now   // Bump timestamp

						// Apply optional fields
						for _, opt := range opts {
							opt(&entry)
						}

						err = memory.DB.AddNotification(entry)
						if err != nil {
							return nil, err
						}

						// Broadcast update chunk
						broadcastNotification(entry)
						return &entry, nil
					}
				}
			}
		}
	}

	// 2. Create new notification
	n := Notification{
		ID:        fmt.Sprintf("notif_%d", time.Now().UnixNano()),
		Source:    source,
		Type:      nType,
		Title:     title,
		Message:   message,
		Status:    "unread",
		CreatedAt: now,
	}

	for _, opt := range opts {
		opt(&n)
	}

	err := memory.DB.AddNotification(n)
	if err != nil {
		return nil, err
	}

	// 3. Broadcast real-time SSE chunk
	broadcastNotification(n)

	return &n, nil
}

func List(ctx context.Context, statusFilter string) ([]Notification, error) {
	return memory.DB.GetNotifications(statusFilter)
}

func MarkRead(ctx context.Context, id string, status string) error {
	if status != "read" && status != "dismissed" && status != "unread" && status != "approved" {
		return fmt.Errorf("invalid status: %s", status)
	}
	return memory.DB.UpdateNotificationStatus(id, status)
}

func broadcastNotification(n Notification) {
	data, err := json.Marshal(n)
	if err != nil {
		fmt.Printf("[Notification Error] Failed to marshal notification for broadcast: %v\n", err)
		return
	}

	stream.GlobalBus.Publish(stream.StreamChunk{
		Source:  "notification",
		Type:    "notification_created",
		Content: string(data),
	})
}
