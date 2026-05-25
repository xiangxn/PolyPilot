package observer

import (
	"context"
	"testing"
	"time"

	"github.com/xiangxn/polypilot/core"
)

func TestLogger_NoCrashOnWrongEventPayload(t *testing.T) {
	bus := core.NewEventBus()
	t.Cleanup(bus.Close)
	l := &Logger{}
	l.Init(bus)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go l.Start(ctx)

	bus.Publish(core.Event{Type: core.EventMarket, Data: 42}) // wrong type
	bus.Publish(core.Event{Type: core.EventExecution, Data: "not an event"})
	bus.Publish(core.Event{Type: core.EventRisk, Data: nil})
	bus.Publish(core.Event{Type: core.EventMetrics, Data: 12.5})
	bus.Publish(core.Event{Type: core.EventPositionExpiring, Data: nil})
	bus.Publish(core.Event{Type: core.EventReconcile, Data: nil})

	// Allow async dispatch to attempt to log
	time.Sleep(100 * time.Millisecond)
	// Reaching here means no panic
}

func TestLogger_HandlesValidEvents(t *testing.T) {
	bus := core.NewEventBus()
	t.Cleanup(bus.Close)
	l := &Logger{}
	l.Init(bus)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go l.Start(ctx)

	bus.Publish(core.Event{
		Type: core.EventPositionExpiring,
		Data: core.PositionExpiringEvent{
			MarketID: "m1", EndTime: time.Now().UnixMilli(), TokenIDs: []string{"tk1"},
		},
	})
	bus.Publish(core.Event{
		Type: core.EventReconcile,
		Data: core.ReconcileEvent{Type: "BOTH", Added: 1, At: time.Now()},
	})
	time.Sleep(100 * time.Millisecond)
}
