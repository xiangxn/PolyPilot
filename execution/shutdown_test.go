package execution

import (
	"context"
	"testing"
	"time"

	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/runtime"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

// TestShutdown_DrainsQueueAndRejects verifies that batches still pending in
// the execution queue are rejected with reason "shutting down" rather than
// being silently dropped or processed mid-shutdown.
//
// We exercise the drain helper directly rather than racing the worker
// goroutine against ctx cancellation — Go's select picks pseudo-randomly when
// multiple cases are ready, so testing the consumer end-to-end against a
// pre-filled queue would be flaky under -race.
func TestShutdown_DrainsQueueAndRejects(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()

	exec := &Executor{Bus: bus, ExecutionQueueSize: 8}
	exec.queue = make(chan []runtime.OrderIntent, exec.ExecutionQueueSize)

	for i := 0; i < 4; i++ {
		exec.queue <- []runtime.OrderIntent{{
			Action:   runtime.OrderIntentActionPlace,
			MarketID: "m", TokenID: "tk1", Price: 0.5, Size: 1, Side: orders.BUY,
		}}
	}

	exec.drainQueueOnShutdown()

	deadline := time.After(3 * time.Second)
	rejected := 0
	for rejected < 4 {
		select {
		case ev := <-ch:
			ee, ok := ev.Data.(core.ExecutionEvent)
			if !ok {
				continue
			}
			if ee.Status != core.ExecutionStatusRejected {
				t.Fatalf("unexpected status: %s", ee.Status)
			}
			if ee.Reason != "shutting down" {
				t.Fatalf("unexpected reason: %s", ee.Reason)
			}
			rejected++
		case <-deadline:
			t.Fatalf("expected 4 rejects, got %d", rejected)
		}
	}
}

// TestShutdown_ConsumeQueueExitsOnCancel verifies the worker goroutine
// returns when its context is cancelled and an empty queue.
func TestShutdown_ConsumeQueueExitsOnCancel(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	exec := &Executor{Bus: bus, ExecutionQueueSize: 8}
	exec.queue = make(chan []runtime.OrderIntent, exec.ExecutionQueueSize)

	done := make(chan struct{})
	go func() {
		exec.consumeExecuteQueue(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// expected
	case <-time.After(2 * time.Second):
		t.Fatal("consumeExecuteQueue did not return after ctx cancel")
	}
}
