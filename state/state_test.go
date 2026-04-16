package state

import (
	"polypilot/core"
	"testing"
)

func TestState_ReserveApplyFillRelease(t *testing.T) {
	s := NewState(100)
	orderID := "ord-1"

	if err := s.ReserveOrder(orderID, "market-1", "token-1", core.SideBuy, 0.6, 10); err != nil {
		t.Fatalf("reserve failed: %v", err)
	}

	snap1 := s.Snapshot()
	if snap1.Balance.Available != 94 || snap1.Balance.Reserved != 6 {
		t.Fatalf("unexpected balance after reserve: %+v", snap1.Balance)
	}

	if err := s.ApplyFill(orderID, "market-1", "token-1", core.SideBuy, 4); err != nil {
		t.Fatalf("apply fill failed: %v", err)
	}

	snap2 := s.Snapshot()
	if snap2.Position.Buy != 4 {
		t.Fatalf("unexpected buy position after fill: %.2f", snap2.Position.Buy)
	}
	if snap2.Balance.Reserved != 3.6 {
		t.Fatalf("unexpected reserved after fill: %.2f", snap2.Balance.Reserved)
	}

	s.ReleaseOrder(orderID)
	snap3 := s.Snapshot()
	if snap3.Balance.Reserved != 0 {
		t.Fatalf("expected reserved=0 after release, got %.2f", snap3.Balance.Reserved)
	}
	if snap3.Balance.Available != 97.6 {
		t.Fatalf("unexpected available after release: %.2f", snap3.Balance.Available)
	}
}

func TestState_ApplyFillMismatch(t *testing.T) {
	s := NewState(100)
	orderID := "ord-2"
	if err := s.ReserveOrder(orderID, "market-1", "token-1", core.SideBuy, 0.5, 2); err != nil {
		t.Fatalf("reserve failed: %v", err)
	}

	if err := s.ApplyFill(orderID, "market-x", "token-1", core.SideBuy, 1); err == nil {
		t.Fatalf("expected market/token mismatch error")
	}
}

func TestState_Restore(t *testing.T) {
	s := NewState(0)
	s.Restore(Snapshot{
		Position: Position{Buy: 3, Sell: 1},
		Balance:  Balance{Available: 88, Reserved: 12},
	}, []ReservationSnapshot{{
		OrderID:       "ord-r-1",
		MarketID:      "market-1",
		TokenID:       "token-1",
		Side:          core.SideBuy,
		Price:         0.4,
		RemainingSize: 10,
		Reserved:      4,
	}})

	snap := s.Snapshot()
	if snap.Balance.Available != 88 || snap.Balance.Reserved != 12 {
		t.Fatalf("unexpected restored balance: %+v", snap.Balance)
	}
	if err := s.ApplyFill("ord-r-1", "market-1", "token-1", core.SideBuy, 1); err != nil {
		t.Fatalf("expected restored reservation to be fillable: %v", err)
	}
}
