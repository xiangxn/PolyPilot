package execution

import (
	"polypilot/core"
	"polypilot/runtime"
	"testing"
	"time"
)

func TestExecutor_ExecutePublishesAcceptedAndFilled(t *testing.T) {
	bus := core.NewEventBus()
	exec := &Executor{}
	exec.Init(bus)

	ch, cancel := bus.SubscribeWithCancel()
	defer cancel()

	exec.Execute([]runtime.OrderIntent{{
		MarketID: "market-1",
		TokenID:  "token-1",
		Price:    0.42,
		Side:     core.SideBuy,
		Size:     3,
	}})

	statuses := make([]core.ExecutionStatus, 0, 2)
	timeout := time.After(1 * time.Second)
	for len(statuses) < 2 {
		select {
		case ev := <-ch:
			if ev.Type != core.EventExecution {
				continue
			}
			data, ok := ev.Data.(core.ExecutionEvent)
			if !ok {
				t.Fatalf("invalid payload type")
			}
			statuses = append(statuses, data.Status)
		case <-timeout:
			t.Fatalf("timeout waiting execution events")
		}
	}

	if statuses[0] != core.ExecutionStatusAccepted || statuses[1] != core.ExecutionStatusFilled {
		t.Fatalf("unexpected status sequence: %+v", statuses)
	}
}

func TestExecutor_ExecuteWithoutBusDoesNotPanic(t *testing.T) {
	exec := &Executor{}
	exec.Execute([]runtime.OrderIntent{{
		MarketID: "market-1",
		TokenID:  "token-1",
		Price:    0.42,
		Side:     core.SideBuy,
		Size:     1,
	}})
}
