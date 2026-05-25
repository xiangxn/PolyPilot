package state

import (
	"context"
	"testing"
	"time"

	"github.com/xiangxn/polypilot/core"
)

func TestPositionExpiring_FiresOnce(t *testing.T) {
	s := newStateWithBalance(t, 100)
	bus := core.NewEventBus()
	t.Cleanup(bus.Close)

	// market ends 20s from now; warn-before is 30s → should fire
	endsAt := time.Now().Add(20 * time.Second).UnixMilli()
	s.RegisterMarketExpiry("m1", endsAt, []string{"tk1"})

	ch, cancel := bus.SubscribeWithCancel()
	t.Cleanup(cancel)

	ctx, ctxCancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(ctxCancel)

	go s.StartPositionExpiringLoop(ctx, bus, 100*time.Millisecond, 30*time.Second)

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type == core.EventPositionExpiring {
				got, ok := ev.Data.(core.PositionExpiringEvent)
				if !ok {
					t.Fatalf("unexpected payload type")
				}
				if got.MarketID != "m1" {
					t.Fatalf("expected m1, got %+v", got)
				}
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for EventPositionExpiring")
		}
	}
}

func TestPositionExpiring_DoesNotFireForFarFuture(t *testing.T) {
	s := newStateWithBalance(t, 100)
	bus := core.NewEventBus()
	t.Cleanup(bus.Close)

	// market ends 5 minutes from now; warn-before is 30s → should NOT fire
	endsAt := time.Now().Add(5 * time.Minute).UnixMilli()
	s.RegisterMarketExpiry("m1", endsAt, []string{"tk1"})

	ch, cancel := bus.SubscribeWithCancel()
	t.Cleanup(cancel)

	ctx, ctxCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	t.Cleanup(ctxCancel)

	go s.StartPositionExpiringLoop(ctx, bus, 50*time.Millisecond, 30*time.Second)

	select {
	case ev := <-ch:
		if ev.Type == core.EventPositionExpiring {
			t.Fatal("should not have fired for far-future market")
		}
	case <-time.After(400 * time.Millisecond):
		// expected: no event
	}
}
