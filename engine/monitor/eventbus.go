package monitor

import (
	"context"
	"sync"
	"time"
)

// EventBus is a Go-channel-based publish/subscribe bus for KuraOS events.
// Publishers call Publish; subscribers register via Subscribe. Each subscriber
// receives a copy of every Event published after their subscription.
//
// DESIGN_PRINCIPLES architecture: "イベント Bus は Go の channel ベース。各エンジンが
// Event を publish、NotificationEngine が subscribe してチャネルへルーティング"
type EventBus struct {
	mu          sync.RWMutex
	subscribers []chan Event
	// persist, when non-nil, is called for every event so it can be written
	// to the event_log SQLite table (Story 2 / Story 3 integration).
	persist func(Event)
}

// NewEventBus returns a ready-to-use EventBus. When persist is non-nil,
// every published event is also handed to it (e.g. for SQLite persistence).
func NewEventBus(persist func(Event)) *EventBus {
	return &EventBus{persist: persist}
}

// Severity mirrors the UI vocabulary from the prototype.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
	SeverityOK       Severity = "ok"
)

// Event is a single occurrence that engines and the alert evaluator publish.
// Category is a dot-separated path (e.g. "storage", "apps", "backup") that
// maps to config.json alert rule `category` patterns.
type Event struct {
	ID        int64     // set by the persistence layer; 0 until persisted
	Timestamp time.Time
	Severity  Severity
	Category  string
	Source    string // engine name or subsystem (e.g. "storage", "monitor")
	Title     string // short human-readable summary (i18n key resolved by caller)
	Detail    string // optional extended detail
	// MetricName and MetricValue are populated when the event originates
	// from an alert rule evaluation (so the notification message can say
	// "pool.tank.used_percent = 82.3%").
	MetricName  string
	MetricValue float64
}

// Subscribe returns a channel on which the caller will receive all future
// events. The channel is buffered by size. When ctx is cancelled, the
// subscription is removed and the channel is closed.
func (b *EventBus) Subscribe(ctx context.Context, size int) <-chan Event {
	ch := make(chan Event, size)
	b.mu.Lock()
	b.subscribers = append(b.subscribers, ch)
	b.mu.Unlock()

	go func() {
		<-ctx.Done()
		b.mu.Lock()
		defer b.mu.Unlock()
		for i, s := range b.subscribers {
			if s == ch {
				b.subscribers = append(b.subscribers[:i], b.subscribers[i+1:]...)
				close(ch)
				return
			}
		}
	}()
	return ch
}

// Publish sends ev to all subscribers. Slow subscribers are skipped (their
// channel is full) rather than blocking the publisher — metric events arrive
// every 30 seconds so a subscriber that can't keep up is likely stuck.
func (b *EventBus) Publish(ev Event) {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	if b.persist != nil {
		b.persist(ev)
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subscribers {
		select {
		case ch <- ev:
		default:
			// subscriber buffer full — skip rather than block
		}
	}
}
