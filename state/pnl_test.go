package state

import "testing"

func TestUnrealizedPnL_SkipsUnknownCost(t *testing.T) {
	s := newStateWithBalance(t, 100)
	s.Restore(Snapshot{
		Balance: Balance{Available: 100},
		Position: Position{Tokens: map[string]TokenPosition{
			"tk1": {Available: 10, AvgCost: 0.4, AvgCostKnown: true},
			"tk2": {Available: 10, AvgCost: 0, AvgCostKnown: false},
		}},
	})
	mid := map[string]float64{"tk1": 0.5, "tk2": 0.5}
	got := s.UnrealizedPnL(mid)
	// only tk1 counts: (0.5-0.4)*10 = 1.0
	if got < 0.99 || got > 1.01 {
		t.Fatalf("got %v want ~1.0", got)
	}
}

func TestUnrealizedPnL_EmptyPosition(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if got := s.UnrealizedPnL(nil); got != 0 {
		t.Fatalf("expected 0 got %v", got)
	}
}

func TestUnrealizedPnL_MissingMidPriceSkips(t *testing.T) {
	s := newStateWithBalance(t, 100)
	s.Restore(Snapshot{
		Balance: Balance{Available: 100},
		Position: Position{Tokens: map[string]TokenPosition{
			"tk1": {Available: 10, AvgCost: 0.4, AvgCostKnown: true},
		}},
	})
	// tk1 not in mid map → skipped, pnl=0
	if got := s.UnrealizedPnL(map[string]float64{"OTHER": 0.5}); got != 0 {
		t.Fatalf("expected 0 (no mid), got %v", got)
	}
}

func TestUnrealizedPnL_ZeroAvailableSkips(t *testing.T) {
	s := newStateWithBalance(t, 100)
	s.Restore(Snapshot{
		Balance: Balance{Available: 100},
		Position: Position{Tokens: map[string]TokenPosition{
			"tk1": {Available: 0, AvgCost: 0.4, AvgCostKnown: true},
		}},
	})
	if got := s.UnrealizedPnL(map[string]float64{"tk1": 0.5}); got != 0 {
		t.Fatalf("expected 0 (zero available), got %v", got)
	}
}

func TestUnrealizedPnL_NegativeWhenMidBelowCost(t *testing.T) {
	s := newStateWithBalance(t, 100)
	s.Restore(Snapshot{
		Balance: Balance{Available: 100},
		Position: Position{Tokens: map[string]TokenPosition{
			"tk1": {Available: 10, AvgCost: 0.6, AvgCostKnown: true},
		}},
	})
	got := s.UnrealizedPnL(map[string]float64{"tk1": 0.4})
	// (0.4 - 0.6) * 10 = -2.0
	if got > -1.99 || got < -2.01 {
		t.Fatalf("got %v want ~-2.0", got)
	}
}
