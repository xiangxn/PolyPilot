package state

import (
	"errors"
	"testing"

	"github.com/xiangxn/go-polymarket-sdk/orders"
	"github.com/xiangxn/polypilot/core"
)

func TestApplyFill_EmptyOrderID(t *testing.T) {
	s := newStateWithBalance(t, 100)
	err := s.ApplyFill("", "m1", "tk1", orders.BUY, 5, 0.5)
	if !errors.Is(err, core.ErrReservationNotFound) {
		t.Fatalf("expected ErrReservationNotFound for empty order id, got %v", err)
	}
}

func TestApplyFill_NonPositiveFilledSize(t *testing.T) {
	s := newStateWithBalance(t, 100)
	_ = s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.5, 10)
	if err := s.ApplyFill("o1", "m1", "tk1", orders.BUY, 0, 0.5); !errors.Is(err, core.ErrInvalidSize) {
		t.Fatalf("expected ErrInvalidSize, got %v", err)
	}
	if err := s.ApplyFill("o1", "m1", "tk1", orders.BUY, -1, 0.5); !errors.Is(err, core.ErrInvalidSize) {
		t.Fatalf("expected ErrInvalidSize for negative, got %v", err)
	}
}

func TestApplyFill_InvalidSide(t *testing.T) {
	s := newStateWithBalance(t, 100)
	_ = s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.5, 10)
	err := s.ApplyFill("o1", "m1", "tk1", orders.Side("UNKNOWN"), 5, 0.5)
	if !errors.Is(err, core.ErrInvalidSide) {
		t.Fatalf("expected ErrInvalidSide, got %v", err)
	}
}

func TestApplyFill_NotFound(t *testing.T) {
	s := newStateWithBalance(t, 100)
	err := s.ApplyFill("missing", "m1", "tk1", orders.BUY, 5, 0.5)
	if !errors.Is(err, core.ErrReservationNotFound) {
		t.Fatalf("expected ErrReservationNotFound, got %v", err)
	}
}

func TestApplyFill_MarketMismatch(t *testing.T) {
	s := newStateWithBalance(t, 100)
	_ = s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.5, 10)
	err := s.ApplyFill("o1", "WRONG", "tk1", orders.BUY, 5, 0.5)
	if !errors.Is(err, core.ErrFillMarketTokenMismatch) {
		t.Fatalf("expected ErrFillMarketTokenMismatch, got %v", err)
	}
}

func TestApplyFill_TokenMismatch(t *testing.T) {
	s := newStateWithBalance(t, 100)
	_ = s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.5, 10)
	err := s.ApplyFill("o1", "m1", "WRONGTOKEN", orders.BUY, 5, 0.5)
	if !errors.Is(err, core.ErrFillMarketTokenMismatch) {
		t.Fatalf("expected ErrFillMarketTokenMismatch, got %v", err)
	}
}

func TestApplyFill_SideMismatch(t *testing.T) {
	s := newStateWithBalance(t, 100)
	_ = s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.5, 10)
	err := s.ApplyFill("o1", "m1", "tk1", orders.SELL, 5, 0.5)
	if !errors.Is(err, core.ErrFillSideMismatch) {
		t.Fatalf("expected ErrFillSideMismatch, got %v", err)
	}
}

// Polymarket大概率出现超过requestedSize的匹配，所以这里不再验证
// func TestApplyFill_OverFill(t *testing.T) {
// 	s := newStateWithBalance(t, 100)
// 	_ = s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.5, 10)
// 	err := s.ApplyFill("o1", "m1", "tk1", orders.BUY, 20, 0.5)
// 	if !errors.Is(err, core.ErrFillExceedsRemaining) {
// 		t.Fatalf("expected ErrFillExceedsRemaining, got %v", err)
// 	}
// }

func TestApplyFill_FillPriceFallbackToReservedPrice(t *testing.T) {
	s := newStateWithBalance(t, 100)
	_ = s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.5, 10)
	// fillPrice=0 → falls back to res.Price=0.5
	if err := s.ApplyFill("o1", "m1", "tk1", orders.BUY, 5, 0); err != nil {
		t.Fatalf("fill: %v", err)
	}
	tp := s.Snapshot().Position.Tokens["tk1"]
	if tp.AvgCost != 0.5 {
		t.Fatalf("expected AvgCost=0.5 (fallback), got %v", tp.AvgCost)
	}
}

func TestApplyFill_FillPriceNegativeFallback(t *testing.T) {
	s := newStateWithBalance(t, 100)
	_ = s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.5, 10)
	if err := s.ApplyFill("o1", "m1", "tk1", orders.BUY, 5, -0.1); err != nil {
		t.Fatalf("fill: %v", err)
	}
	tp := s.Snapshot().Position.Tokens["tk1"]
	if tp.AvgCost != 0.5 {
		t.Fatalf("expected AvgCost=0.5 (fallback), got %v", tp.AvgCost)
	}
}

func TestApplyFill_FullFillDeletesReservation(t *testing.T) {
	s := newStateWithBalance(t, 100)
	_ = s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.5, 10)
	if err := s.ApplyFill("o1", "m1", "tk1", orders.BUY, 10, 0.5); err != nil {
		t.Fatalf("fill: %v", err)
	}
	if _, ok := s.Snapshot().Orders["o1"]; ok {
		t.Fatal("expected reservation removed after full fill")
	}
}

func TestApplyFill_PartialFillKeepsReservation(t *testing.T) {
	s := newStateWithBalance(t, 100)
	_ = s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.5, 10)
	if err := s.ApplyFill("o1", "m1", "tk1", orders.BUY, 4, 0.5); err != nil {
		t.Fatalf("fill: %v", err)
	}
	res, ok := s.Snapshot().Orders["o1"]
	if !ok {
		t.Fatal("expected reservation to remain after partial fill")
	}
	if res.RemainingSize != 6 {
		t.Fatalf("expected remaining=6, got %v", res.RemainingSize)
	}
}

func TestApplyFill_ConsumedClampedToReserved(t *testing.T) {
	// craft state where consumed > res.Reserved → clamped
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.balance.Available = 50
	s.balance.Reserved = 100
	s.orderReservations["o1"] = OrderReservation{
		OrderID: "o1", MarketID: "m1", TokenID: "tk1",
		Side: orders.BUY, Price: 0.5, RemainingSize: 10,
		Reserved: 1, // tiny: less than consumed (0.5*5 = 2.5)
	}
	if err := s.ApplyFill("o1", "m1", "tk1", orders.BUY, 5, 0.5); err != nil {
		t.Fatalf("fill: %v", err)
	}
	// After fill: res.Reserved -= clamped consumed (== 1) → 0; balance.Reserved decreased by 1.
}

func TestApplyFill_BalanceReservedClampedToZero(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.balance.Available = 50
	s.balance.Reserved = 0.001 // tiny
	s.orderReservations["o1"] = OrderReservation{
		OrderID: "o1", MarketID: "m1", TokenID: "tk1",
		Side: orders.BUY, Price: 0.5, RemainingSize: 10, Reserved: 5,
	}
	if err := s.ApplyFill("o1", "m1", "tk1", orders.BUY, 5, 0.5); err != nil {
		t.Fatalf("fill: %v", err)
	}
	if got := s.Snapshot().Balance.Reserved; got < 0 {
		t.Fatalf("Reserved should clamp to 0, got %v", got)
	}
}

func TestApplyFill_SELL_PositionReservedClampedToZero(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.balance.Available = 100
	s.position.Tokens["tk1"] = TokenPosition{Available: 10, Reserved: 0.001} // tiny
	s.orderReservations["o1"] = OrderReservation{
		OrderID: "o1", MarketID: "m1", TokenID: "tk1",
		Side: orders.SELL, Price: 0.5, RemainingSize: 10, Reserved: 5,
	}
	if err := s.ApplyFill("o1", "m1", "tk1", orders.SELL, 5, 0.5); err != nil {
		t.Fatalf("fill: %v", err)
	}
	if got := s.Snapshot().Position.Tokens["tk1"].Reserved; got < 0 {
		t.Fatalf("token Reserved should clamp to 0, got %v", got)
	}
}

func TestApplyFill_SELL_WithExistingAvgCost_AccumulatesPnL(t *testing.T) {
	s := newStateWithBalance(t, 100)
	// pre-load tokens with AvgCost via direct Restore
	s.Restore(Snapshot{
		Balance:  Balance{Available: 100},
		Position: Position{Tokens: map[string]TokenPosition{"tk1": {Available: 10, AvgCost: 0.3, AvgCostKnown: true, TotalBought: 10}}},
	})
	_ = s.ReserveOrder("sell1", "m1", "tk1", orders.SELL, 0.5, 5)
	if err := s.ApplyFill("sell1", "m1", "tk1", orders.SELL, 5, 0.5); err != nil {
		t.Fatalf("fill: %v", err)
	}
	// realized PnL = (0.5 - 0.3) * 5 = 1.0
	if got := s.Snapshot().DailyPnL; got < 0.99 || got > 1.01 {
		t.Fatalf("expected DailyPnL≈1.0, got %v", got)
	}
}
