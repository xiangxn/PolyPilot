package state

import (
	"math"
	"testing"

	"github.com/polymarket/go-order-utils/pkg/model"
)

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9
}

func TestState_BuyOrderLifecycle(t *testing.T) {
	s := NewState(100)
	orderID := "ord-buy-1"

	if err := s.ReserveOrder(orderID, "market-1", "token-1", model.BUY, 0.6, 10); err != nil {
		t.Fatalf("reserve failed: %v", err)
	}

	snap1 := s.Snapshot()
	if !almostEqual(snap1.Balance.Available, 94) || !almostEqual(snap1.Balance.Reserved, 6) {
		t.Fatalf("unexpected balance after reserve: %+v", snap1.Balance)
	}

	if err := s.ApplyFill(orderID, "market-1", "token-1", model.BUY, 4); err != nil {
		t.Fatalf("apply fill failed: %v", err)
	}

	snap2 := s.Snapshot()
	if !almostEqual(snap2.Balance.Reserved, 3.6) {
		t.Fatalf("unexpected reserved after fill: %.6f", snap2.Balance.Reserved)
	}
	tp := snap2.Position.Tokens["token-1"]
	if !almostEqual(tp.Available, 4) || !almostEqual(tp.Reserved, 0) {
		t.Fatalf("unexpected token position after buy fill: %+v", tp)
	}

	s.ReleaseOrder(orderID)
	snap3 := s.Snapshot()
	if !almostEqual(snap3.Balance.Reserved, 0) {
		t.Fatalf("expected reserved=0 after release, got %.6f", snap3.Balance.Reserved)
	}
	if !almostEqual(snap3.Balance.Available, 97.6) {
		t.Fatalf("unexpected available after release: %.6f", snap3.Balance.Available)
	}
	if _, ok := s.reservations[orderID]; ok {
		t.Fatalf("reservation should be deleted after release")
	}
}

func TestState_SellOrderLifecycle(t *testing.T) {
	s := NewState(10)
	s.Restore(Snapshot{
		Position: Position{Tokens: map[string]TokenPosition{
			"token-1": {Available: 5, Reserved: 0},
		}},
		Balance: Balance{Available: 10, Reserved: 0},
	}, nil)

	orderID := "ord-sell-1"
	if err := s.ReserveOrder(orderID, "market-1", "token-1", model.SELL, 0.25, 4); err != nil {
		t.Fatalf("sell reserve failed: %v", err)
	}

	snap1 := s.Snapshot()
	tp1 := snap1.Position.Tokens["token-1"]
	if !almostEqual(tp1.Available, 1) || !almostEqual(tp1.Reserved, 4) {
		t.Fatalf("unexpected token position after sell reserve: %+v", tp1)
	}
	if !almostEqual(snap1.Balance.Available, 10) || !almostEqual(snap1.Balance.Reserved, 0) {
		t.Fatalf("cash balance should not be frozen for SELL: %+v", snap1.Balance)
	}

	if err := s.ApplyFill(orderID, "market-1", "token-1", model.SELL, 1.5); err != nil {
		t.Fatalf("sell apply fill failed: %v", err)
	}

	snap2 := s.Snapshot()
	tp2 := snap2.Position.Tokens["token-1"]
	if !almostEqual(tp2.Available, 1) || !almostEqual(tp2.Reserved, 2.5) {
		t.Fatalf("unexpected token position after sell fill: %+v", tp2)
	}
	if !almostEqual(snap2.Balance.Available, 10.375) {
		t.Fatalf("unexpected cash available after sell fill: %.6f", snap2.Balance.Available)
	}

	s.ReleaseOrder(orderID)
	snap3 := s.Snapshot()
	tp3 := snap3.Position.Tokens["token-1"]
	if !almostEqual(tp3.Available, 3.5) || !almostEqual(tp3.Reserved, 0) {
		t.Fatalf("unexpected token position after sell release: %+v", tp3)
	}
}

func TestState_SellReserveInsufficientPosition(t *testing.T) {
	s := NewState(100)
	if err := s.ReserveOrder("ord-sell-insufficient", "market-1", "token-1", model.SELL, 0.5, 1); err == nil {
		t.Fatalf("expected insufficient token position error")
	}
}

func TestState_ApplyFillMismatchAndBounds(t *testing.T) {
	s := NewState(100)
	orderID := "ord-apply-1"
	if err := s.ReserveOrder(orderID, "market-1", "token-1", model.BUY, 0.5, 2); err != nil {
		t.Fatalf("reserve failed: %v", err)
	}

	if err := s.ApplyFill(orderID, "market-x", "token-1", model.BUY, 1); err == nil {
		t.Fatalf("expected market mismatch error")
	}
	if err := s.ApplyFill(orderID, "market-1", "token-x", model.BUY, 1); err == nil {
		t.Fatalf("expected token mismatch error")
	}
	if err := s.ApplyFill(orderID, "market-1", "token-1", model.SELL, 1); err == nil {
		t.Fatalf("expected side mismatch error")
	}
	if err := s.ApplyFill(orderID, "market-1", "token-1", model.BUY, 3); err == nil {
		t.Fatalf("expected exceeds remaining size error")
	}
}

func TestState_RestoreRebuildsReservedFromReservations(t *testing.T) {
	s := NewState(0)
	s.Restore(
		Snapshot{
			Position: Position{Tokens: map[string]TokenPosition{
				"token-1": {Available: 10, Reserved: 1},
			}},
			Balance: Balance{Available: 88, Reserved: 999},
		},
		[]ReservationSnapshot{
			{
				OrderID:       "ord-r-buy",
				MarketID:      "market-1",
				TokenID:       "token-1",
				Side:          model.BUY,
				Price:         0.4,
				RemainingSize: 10,
				Reserved:      4,
			},
			{
				OrderID:       "ord-r-sell",
				MarketID:      "market-1",
				TokenID:       "token-1",
				Side:          model.SELL,
				Price:         0.7,
				RemainingSize: 2,
				Reserved:      2,
			},
		},
	)

	snap := s.Snapshot()
	if !almostEqual(snap.Balance.Available, 88) {
		t.Fatalf("unexpected restored available: %.6f", snap.Balance.Available)
	}
	if !almostEqual(snap.Balance.Reserved, 4) {
		t.Fatalf("reserved cash should be rebuilt from BUY reservations, got %.6f", snap.Balance.Reserved)
	}
	tp := snap.Position.Tokens["token-1"]
	if !almostEqual(tp.Available, 10) || !almostEqual(tp.Reserved, 3) {
		t.Fatalf("unexpected restored token position: %+v", tp)
	}

	if err := s.ApplyFill("ord-r-buy", "market-1", "token-1", model.BUY, 1); err != nil {
		t.Fatalf("restored BUY reservation should be fillable: %v", err)
	}
	if err := s.ApplyFill("ord-r-sell", "market-1", "token-1", model.SELL, 1); err != nil {
		t.Fatalf("restored SELL reservation should be fillable: %v", err)
	}
}

func TestState_SnapshotReturnsDeepCopy(t *testing.T) {
	s := NewState(0)
	s.Restore(Snapshot{
		Position: Position{Tokens: map[string]TokenPosition{
			"token-1": {Available: 1, Reserved: 2},
		}},
		Balance: Balance{Available: 1, Reserved: 2},
	}, nil)

	snap := s.Snapshot()
	snap.Position.Tokens["token-1"] = TokenPosition{Available: 99, Reserved: 99}

	snap2 := s.Snapshot()
	tp := snap2.Position.Tokens["token-1"]
	if !almostEqual(tp.Available, 1) || !almostEqual(tp.Reserved, 2) {
		t.Fatalf("snapshot should be deep copy, got %+v", tp)
	}
}

func TestState_InputValidation(t *testing.T) {
	s := NewState(100)

	if err := s.ReserveOrder("", "m", "t", model.BUY, 0.5, 1); err == nil {
		t.Fatalf("expected empty order id error")
	}
	if err := s.ReserveOrder("o", "", "t", model.BUY, 0.5, 1); err == nil {
		t.Fatalf("expected empty market id error")
	}
	if err := s.ReserveOrder("o", "m", "", model.BUY, 0.5, 1); err == nil {
		t.Fatalf("expected empty token id error")
	}
	if err := s.ReserveOrder("o", "m", "t", model.BUY, 0.5, 0); err == nil {
		t.Fatalf("expected invalid requested size error")
	}
	if err := s.ReserveOrder("o", "m", "t", model.BUY, 0, 1); err == nil {
		t.Fatalf("expected invalid price error")
	}
	if err := s.ReserveOrder("o", "m", "t", model.BUY, 1, 1); err == nil {
		t.Fatalf("expected invalid price error")
	}

	if err := s.ApplyFill("", "m", "t", model.BUY, 1); err == nil {
		t.Fatalf("expected empty order id error")
	}
	if err := s.ApplyFill("o", "m", "t", model.BUY, 0); err == nil {
		t.Fatalf("expected invalid filled size error")
	}
}

func TestState_EdgeBranches(t *testing.T) {
	s := NewState(100)

	if err := s.ReserveOrder("ord-dup", "m", "t", model.BUY, 0.5, 1); err != nil {
		t.Fatalf("reserve failed: %v", err)
	}
	if err := s.ReserveOrder("ord-dup", "m", "t", model.BUY, 0.5, 1); err == nil {
		t.Fatalf("expected duplicate order reserve error")
	}
	if err := s.ReserveOrder("ord-invalid-side", "m", "t", model.Side(99), 0.5, 1); err == nil {
		t.Fatalf("expected invalid side error")
	}

	if err := s.ApplyFill("missing", "m", "t", model.BUY, 1); err == nil {
		t.Fatalf("expected reservation not found error")
	}
	if err := s.ApplyFill("ord-dup", "m", "t", model.Side(99), 1); err == nil {
		t.Fatalf("expected apply fill invalid side error")
	}

	s.ReleaseOrder("")
	s.ReleaseOrder("missing")

	s.position.Tokens = nil
	if err := s.ReserveOrder("ord-ensure-map", "m", "t2", model.BUY, 0.5, 1); err != nil {
		t.Fatalf("reserve should initialize token map: %v", err)
	}
}

func TestState_RestoreInvalidRowsAndConsumedClamp(t *testing.T) {
	s := NewState(0)
	s.Restore(
		Snapshot{Balance: Balance{Available: 50, Reserved: 999}},
		[]ReservationSnapshot{
			{OrderID: "", MarketID: "m", TokenID: "t", Side: model.BUY, Price: 0.4, RemainingSize: 1, Reserved: 1},
			{OrderID: "ord-skip", MarketID: "m", TokenID: "t", Side: model.BUY, Price: 0.4, RemainingSize: 0, Reserved: 1},
			{OrderID: "ord-neg", MarketID: "m", TokenID: "t", Side: model.BUY, Price: 0.4, RemainingSize: 1, Reserved: -5},
			{OrderID: "ord-clamp", MarketID: "m", TokenID: "t", Side: model.BUY, Price: 0.9, RemainingSize: 2, Reserved: 1},
		},
	)

	snap := s.Snapshot()
	if !almostEqual(snap.Balance.Reserved, 1) {
		t.Fatalf("reserved should be rebuilt only from valid BUY reservations, got %.6f", snap.Balance.Reserved)
	}
	if len(s.reservations) != 2 {
		t.Fatalf("expected only 2 valid reservations after restore, got %d", len(s.reservations))
	}

	if err := s.ApplyFill("ord-clamp", "m", "t", model.BUY, 2); err != nil {
		t.Fatalf("apply fill should pass with consumed clamp: %v", err)
	}
	snap2 := s.Snapshot()
	if snap2.Balance.Reserved < -1e-9 {
		t.Fatalf("reserved should not be negative, got %.6f", snap2.Balance.Reserved)
	}
}

func TestRequiredCollateral(t *testing.T) {
	if !almostEqual(requiredCollateral(model.BUY, 0.6, 10), 6) {
		t.Fatalf("buy collateral mismatch")
	}
	if !almostEqual(requiredCollateral(model.SELL, 0.6, 10), 10) {
		t.Fatalf("sell collateral mismatch")
	}
	if !almostEqual(requiredCollateral(model.Side(99), 0.6, 10), 0) {
		t.Fatalf("unknown side collateral should be 0")
	}
}
