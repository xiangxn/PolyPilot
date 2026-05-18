package observer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tidwall/gjson"
	"github.com/xiangxn/go-polymarket-sdk/orders"
	"github.com/xiangxn/polypilot/core"
)

// waitForSubscriber polls until at least one subscriber is registered, or fails.
func waitForSubscriber(t *testing.T, bus *core.EventBus, want int) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if bus.Stats().Subscribers >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %d subscribers, got %d", want, bus.Stats().Subscribers)
}

func TestLogger_AllEventTypes(t *testing.T) {
	bus := core.NewEventBus()
	t.Cleanup(bus.Close)
	l := &Logger{}
	l.Init(bus)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	// Note: Start is synchronous in subscribing; the worker goroutine is internal.
	l.Start(ctx)
	waitForSubscriber(t, bus, 1)

	// Each Publish should be handled without panic / no error.
	bus.Publish(core.Event{Type: core.EventMarket, Data: gjson.Parse(`{"question":"Q","endDate":"2099"}`)})
	bus.Publish(core.Event{Type: core.EventExecution, Data: core.ExecutionEvent{
		OrderID: "o1", MarketID: "m1", TokenID: "tk1", Status: core.ExecutionStatusFilled,
		Side: orders.BUY, Price: 0.5, RequestedSize: 5, FilledSize: 5,
		Reason: "n/a", At: time.Now(),
	}})
	bus.Publish(core.Event{Type: core.EventRisk, Data: core.RiskEvent{Reason: "rj", At: time.Now()}})
	bus.Publish(core.Event{Type: core.EventMetrics, Data: core.MetricsEvent{
		Ticks: 1, InputEvents: 2, At: time.Now(),
	}})
	bus.Publish(core.Event{Type: core.EventPositionExpiring, Data: core.PositionExpiringEvent{
		MarketID: "m1", EndTime: time.Now().UnixMilli(), TokenIDs: []string{"tk1"},
	}})
	bus.Publish(core.Event{Type: core.EventReconcile, Data: core.ReconcileEvent{
		Type: "BOTH", Added: 1, At: time.Now(),
	}})
	bus.Publish(core.Event{Type: core.EventReconcile, Data: core.ReconcileEvent{
		Type: "BOTH", Err: errors.New("reconcile boom"), At: time.Now(),
	}})

	time.Sleep(200 * time.Millisecond)
}

func TestLogger_UnknownEventType_NoOp(t *testing.T) {
	bus := core.NewEventBus()
	t.Cleanup(bus.Close)
	l := &Logger{}
	l.Init(bus)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	l.Start(ctx)
	waitForSubscriber(t, bus, 1)

	bus.Publish(core.Event{Type: "UNKNOWN", Data: nil})
	time.Sleep(100 * time.Millisecond)
}

func TestLogger_Start_ExitsOnContextCancel(t *testing.T) {
	bus := core.NewEventBus()
	t.Cleanup(bus.Close)
	l := &Logger{}
	l.Init(bus)
	ctx, cancel := context.WithCancel(context.Background())
	l.Start(ctx)
	waitForSubscriber(t, bus, 1)

	// Cancel context — the worker goroutine should exit and the subscriber
	// should be removed via the deferred cancel().
	cancel()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if bus.Stats().Subscribers == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("subscriber not removed after context cancel, still %d", bus.Stats().Subscribers)
}

func TestLogger_Start_ExitsOnBusClose(t *testing.T) {
	bus := core.NewEventBus()
	l := &Logger{}
	l.Init(bus)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	l.Start(ctx)
	waitForSubscriber(t, bus, 1)

	// Closing the bus closes the subscriber channel; the worker should exit
	// via the `!ok` branch in the receive.
	bus.Close()
	time.Sleep(50 * time.Millisecond) // worker exit is async, but no specific
	// observable except no panic. Reaching here means no deadlock.
}
