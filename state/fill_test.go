package state

import (
	"testing"
	"time"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

func TestApplyFill_BUY_UpdatesAvgCost(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if err := s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.4, 10); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := s.ApplyFill("o1", "m1", "tk1", orders.BUY, 6, 0.4); err != nil {
		t.Fatalf("fill: %v", err)
	}
	snap := s.Snapshot()
	tp := snap.Position.Tokens["tk1"]
	if tp.Available != 6 || tp.AvgCost != 0.4 || !tp.AvgCostKnown {
		t.Fatalf("avg cost wrong: %+v", tp)
	}
}

func TestApplyFill_BUY_WeightedAvg(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if err := s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.4, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyFill("o1", "m1", "tk1", orders.BUY, 5, 0.4); err != nil {
		t.Fatal(err)
	}
	if err := s.ReserveOrder("o2", "m1", "tk1", orders.BUY, 0.6, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyFill("o2", "m1", "tk1", orders.BUY, 5, 0.6); err != nil {
		t.Fatal(err)
	}
	tp := s.Snapshot().Position.Tokens["tk1"]
	// weighted: (0.4*5 + 0.6*5)/10 = 0.5
	if tp.AvgCost != 0.5 {
		t.Fatalf("avg=%v", tp.AvgCost)
	}
}

func TestApplyFill_SELL_AccumulatesDailyPnL(t *testing.T) {
	s := newStateWithBalance(t, 100)
	// buy 10 @ 0.4 first
	if err := s.ReserveOrder("buy1", "m1", "tk1", orders.BUY, 0.4, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyFill("buy1", "m1", "tk1", orders.BUY, 10, 0.4); err != nil {
		t.Fatal(err)
	}
	// now sell 5 @ 0.6 → realized PnL = (0.6-0.4)*5 = 1.0
	if err := s.ReserveOrder("sell1", "m1", "tk1", orders.SELL, 0.6, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyFill("sell1", "m1", "tk1", orders.SELL, 5, 0.6); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if snap.DailyPnL < 0.99 || snap.DailyPnL > 1.01 {
		t.Fatalf("expected dailyPnL≈1.0, got %v", snap.DailyPnL)
	}
	if snap.DailyPnLDate != time.Now().UTC().Format("2006-01-02") {
		t.Fatalf("date mismatch: %v", snap.DailyPnLDate)
	}
}

func TestApplyFill_SELL_NoAvgCostKnown_NoRealizedPnL(t *testing.T) {
	s := newStateWithBalance(t, 100)
	// inject external position without buy history (simulate reconcile)
	s.Restore(Snapshot{
		Balance:  Balance{Available: 100},
		Position: Position{Tokens: map[string]TokenPosition{"tk1": {Available: 10, AvgCostKnown: false}}},
	})
	if err := s.ReserveOrder("sell1", "m1", "tk1", orders.SELL, 0.6, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyFill("sell1", "m1", "tk1", orders.SELL, 5, 0.6); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if snap.DailyPnL != 0 {
		t.Fatalf("expected zero PnL when AvgCostKnown=false, got %v", snap.DailyPnL)
	}
}
