package execution

import (
	"testing"
	"time"

	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/runtime"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

func TestExecute_DryRun_PublishesAcceptedThenFilled(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	// DryRun short-circuits before queue/client, so neither Init nor Client is required.
	exec := &Executor{Bus: bus, DryRun: true}

	exec.Execute([]runtime.OrderIntent{{
		MarketID: "m1", TokenID: "tk1", Price: 0.4, Size: 5, Side: orders.BUY,
	}})

	got := []core.ExecutionStatus{}
	for i := 0; i < 2; i++ {
		select {
		case ev := <-ch:
			got = append(got, ev.Data.(core.ExecutionEvent).Status)
		case <-time.After(time.Second):
			t.Fatalf("timeout: got %v", got)
		}
	}
	if got[0] != core.ExecutionStatusAccepted || got[1] != core.ExecutionStatusFilled {
		t.Fatalf("got %v", got)
	}
}

func TestExecute_DryRun_CancelIsNoOp(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, DryRun: true}

	exec.Execute([]runtime.OrderIntent{{Action: runtime.OrderIntentActionCancel, OrderID: "o1"}})

	select {
	case ev := <-ch:
		t.Fatalf("expected no event, got %+v", ev)
	case <-time.After(200 * time.Millisecond):
		// expected
	}
}
