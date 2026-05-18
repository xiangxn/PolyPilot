package strategy

import (
	"testing"

	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/runtime"
	"github.com/xiangxn/polypilot/state"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

func TestOnPositionExpiring_SellsAvailableAndCancelsOrders(t *testing.T) {
	s := &Strategy{config: DefaultStrategyConfig()}
	ev := core.PositionExpiringEvent{
		MarketID:  "m1",
		TokenIDs:  []string{"tk1"},
		Available: map[string]float64{"tk1": 5},
	}
	snap := state.Snapshot{
		Orders: map[string]state.OrderReservation{
			"o1": {OrderID: "o1", TokenID: "tk1", Side: orders.BUY},
		},
	}
	got := s.OnPositionExpiring(ev, snap)
	if len(got) != 2 {
		t.Fatalf("expected sell + cancel, got %d", len(got))
	}
	if got[0].Side != orders.SELL || got[0].Size != 5 {
		t.Fatalf("first intent wrong: %+v", got[0])
	}
	if got[1].Action != runtime.OrderIntentActionCancel {
		t.Fatalf("second should be cancel: %+v", got[1])
	}
}

func TestOnPositionExpiring_NoAvailable_OnlyCancels(t *testing.T) {
	s := &Strategy{config: DefaultStrategyConfig()}
	ev := core.PositionExpiringEvent{
		MarketID:  "m1",
		TokenIDs:  []string{"tk1"},
		Available: map[string]float64{"tk1": 0},
	}
	snap := state.Snapshot{
		Orders: map[string]state.OrderReservation{
			"o1": {OrderID: "o1", TokenID: "tk1"},
		},
	}
	got := s.OnPositionExpiring(ev, snap)
	if len(got) != 1 || got[0].Action != runtime.OrderIntentActionCancel {
		t.Fatalf("expected only cancel, got %+v", got)
	}
}
