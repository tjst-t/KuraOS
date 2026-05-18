package notify

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/kuraos-org/kura/engine/monitor"
)

// Dispatcher subscribes to the EventBus and routes matching events to the
// appropriate notification channels. It is started as a goroutine in cmd/kura.
type Dispatcher struct {
	store    *Store
	// channelBuilder builds a live Channel from a ChannelRow. Injected so
	// tests can substitute a fake (DESIGN_PRINCIPLES #9).
	channelBuilder func(ChannelRow) (Channel, error)
}

// NewDispatcher constructs a Dispatcher. When channelBuilder is nil,
// BuildChannel is used (the production default).
func NewDispatcher(store *Store, channelBuilder func(ChannelRow) (Channel, error)) *Dispatcher {
	if channelBuilder == nil {
		channelBuilder = BuildChannel
	}
	return &Dispatcher{store: store, channelBuilder: channelBuilder}
}

// Run subscribes to bus and routes events until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context, bus *monitor.EventBus) {
	sub := bus.Subscribe(ctx, 64)
	for {
		select {
		case ev, ok := <-sub:
			if !ok {
				return
			}
			d.dispatch(ctx, ev)
		case <-ctx.Done():
			return
		}
	}
}

func (d *Dispatcher) dispatch(ctx context.Context, ev monitor.Event) {
	channels, err := d.store.List(ctx)
	if err != nil {
		log.Printf("notify: dispatcher: list channels: %v", err)
		return
	}
	n := Notification{
		Title:    ev.Title,
		Body:     fmt.Sprintf("[%s] %s\n%s", ev.Category, ev.Title, ev.Detail),
		Severity: string(ev.Severity),
		Source:   ev.Source,
		SentAt:   ev.Timestamp,
	}
	if n.SentAt.IsZero() {
		n.SentAt = time.Now().UTC()
	}
	for _, row := range channels {
		if !row.Enabled {
			continue
		}
		if !matchesSeverity(row.SeverityFilter, string(ev.Severity)) {
			continue
		}
		if len(row.CategoryFilter) > 0 && !matchesCategory(row.CategoryFilter, ev.Category) {
			continue
		}
		ch, err := d.channelBuilder(row)
		if err != nil {
			log.Printf("notify: build channel %q: %v", row.ID, err)
			continue
		}
		sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if err := ch.Send(sendCtx, n); err != nil {
			log.Printf("notify: send to %q (%s): %v", row.Name, row.Kind, err)
		}
		cancel()
	}
}

func matchesSeverity(filter []string, severity string) bool {
	if len(filter) == 0 {
		return true
	}
	for _, s := range filter {
		if strings.TrimSpace(s) == severity {
			return true
		}
	}
	return false
}

func matchesCategory(filter []string, category string) bool {
	if len(filter) == 0 {
		return true
	}
	for _, c := range filter {
		if strings.TrimSpace(c) == category {
			return true
		}
	}
	return false
}
