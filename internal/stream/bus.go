package stream

import (
	"sync"
)

type UsageInfo struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
}

type StreamChunk struct {
	StreamID string    `json:"streamId,omitempty"`
	Source   string    `json:"source"`
	TaskID   string    `json:"taskId,omitempty"`
	NodeID   string    `json:"nodeId,omitempty"`
	Type     string    `json:"type"`
	Content  string    `json:"content"`
	Usage    UsageInfo `json:"usage,omitempty"`
}

type Subscription struct {
	Ch       chan StreamChunk
	filterFn func(StreamChunk) bool
	bus      *Bus
}

func (s *Subscription) Unsubscribe() {
	s.bus.unsubscribe(s)
}

type Bus struct {
	mu          sync.RWMutex
	subscribers map[*Subscription]bool
}

func NewBus() *Bus {
	return &Bus{
		subscribers: make(map[*Subscription]bool),
	}
}

var GlobalBus = NewBus()

func (b *Bus) Subscribe(filterFn func(StreamChunk) bool) *Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Channel buffer size of 100 to avoid immediate drop under transient loads
	sub := &Subscription{
		Ch:       make(chan StreamChunk, 100),
		filterFn: filterFn,
		bus:      b,
	}
	b.subscribers[sub] = true
	return sub
}

func (b *Bus) unsubscribe(sub *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, exists := b.subscribers[sub]; exists {
		delete(b.subscribers, sub)
		close(sub.Ch)
	}
}

func (b *Bus) Publish(chunk StreamChunk) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for sub := range b.subscribers {
		if sub.filterFn != nil && !sub.filterFn(chunk) {
			continue
		}
		// Non-blocking send to prevent a slow subscriber from stalling the publisher
		select {
		case sub.Ch <- chunk:
		default:
			// Drop chunk if subscriber's buffer is full
		}
	}
}
