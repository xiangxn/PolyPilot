package state

import (
	"testing"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

func TestNewStateWithBalanceSync_NegativeMinBalanceClampedToZero(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{MinBalance: -10}, nil)
	if got := s.Snapshot().Balance.MinBalance; got != 0 {
		t.Fatalf("expected min balance clamped to 0, got %v", got)
	}
}

func TestNewStateWithBalanceSync_PositiveMinBalancePreserved(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{MinBalance: 5}, nil)
	if got := s.Snapshot().Balance.MinBalance; got != 5 {
		t.Fatalf("expected min balance 5, got %v", got)
	}
}

func TestRestore_NilTokensInitializesMap(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{Balance: Balance{Available: 50}})
	snap := s.Snapshot()
	if snap.Position.Tokens == nil {
		t.Fatal("Restore should not leave Tokens nil")
	}
}

func TestRestore_SkipsEmptyOrderID(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{
		Balance: Balance{Available: 100},
		Orders: map[string]OrderReservation{
			"": {OrderID: "", MarketID: "m1", TokenID: "tk1", Side: orders.BUY, RemainingSize: 5, Reserved: 2.5},
		},
	})
	snap := s.Snapshot()
	if len(snap.Orders) != 0 {
		t.Fatalf("empty order id should be skipped, got %d orders", len(snap.Orders))
	}
}

func TestRestore_SkipsNonPositiveRemainingSize(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{
		Balance: Balance{Available: 100},
		Orders: map[string]OrderReservation{
			"o1": {OrderID: "o1", MarketID: "m1", TokenID: "tk1", Side: orders.BUY, RemainingSize: 0, Reserved: 5},
			"o2": {OrderID: "o2", MarketID: "m1", TokenID: "tk1", Side: orders.BUY, RemainingSize: -1, Reserved: 5},
		},
	})
	snap := s.Snapshot()
	if len(snap.Orders) != 0 {
		t.Fatalf("zero/negative remaining size orders should be skipped, got %d orders", len(snap.Orders))
	}
}

func TestRestore_NegativeReservedClampedToZero(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{
		Balance: Balance{Available: 100},
		Orders: map[string]OrderReservation{
			"o1": {OrderID: "o1", MarketID: "m1", TokenID: "tk1", Side: orders.BUY, RemainingSize: 5, Reserved: -1},
		},
	})
	snap := s.Snapshot()
	if got := snap.Orders["o1"].Reserved; got != 0 {
		t.Fatalf("expected Reserved clamped to 0, got %v", got)
	}
}

func TestRestore_RebuildsOrderReservationsForBoth(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{
		Balance:  Balance{Available: 100},
		Position: Position{Tokens: map[string]TokenPosition{"tk1": {Available: 10}}},
		Orders: map[string]OrderReservation{
			"buy1":  {OrderID: "buy1", MarketID: "m1", TokenID: "tkA", Side: orders.BUY, RemainingSize: 5, Reserved: 2.5},
			"sell1": {OrderID: "sell1", MarketID: "m1", TokenID: "tk1", Side: orders.SELL, RemainingSize: 3, Reserved: 3},
		},
	})
	snap := s.Snapshot()
	if snap.Balance.Reserved != 2.5 {
		t.Fatalf("expected buy reservation reflected in balance.Reserved, got %v", snap.Balance.Reserved)
	}
	if snap.Position.Tokens["tk1"].Reserved != 3 {
		t.Fatalf("expected sell reservation in tk1.Reserved, got %v", snap.Position.Tokens["tk1"].Reserved)
	}
	if snap.Position.Tokens["tk1"].Available != 7 {
		t.Fatalf("expected sell reservation deducted from tk1.Available, got %v", snap.Position.Tokens["tk1"].Available)
	}
}

func TestClearRedeemedPositions_RemovesListed(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{
		Position: Position{Tokens: map[string]TokenPosition{
			"tk1": {Available: 5},
			"tk2": {Available: 3},
			"tk3": {Available: 8},
		}},
	})
	s.ClearRedeemedPositions([]string{"tk1", "tk3"})
	snap := s.Snapshot()
	if _, ok := snap.Position.Tokens["tk1"]; ok {
		t.Fatal("tk1 should be cleared")
	}
	if _, ok := snap.Position.Tokens["tk3"]; ok {
		t.Fatal("tk3 should be cleared")
	}
	if _, ok := snap.Position.Tokens["tk2"]; !ok {
		t.Fatal("tk2 should remain")
	}
}

func TestClearRedeemedPositions_EmptyInputNoop(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{
		Position: Position{Tokens: map[string]TokenPosition{"tk1": {Available: 5}}},
	})
	s.ClearRedeemedPositions(nil)
	s.ClearRedeemedPositions([]string{})
	snap := s.Snapshot()
	if len(snap.Position.Tokens) != 1 {
		t.Fatalf("empty input should be noop, got %d tokens", len(snap.Position.Tokens))
	}
}

func TestClearRedeemedPositions_EmptyPositionsNoop(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	// no positions registered
	s.ClearRedeemedPositions([]string{"tk1"})
	if len(s.Snapshot().Position.Tokens) != 0 {
		t.Fatal("should remain empty")
	}
}

func TestClearRedeemedPositions_SkipsEmptyAndWhitespaceTokenIDs(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{
		Position: Position{Tokens: map[string]TokenPosition{"tk1": {Available: 5}}},
	})
	s.ClearRedeemedPositions([]string{"", "   ", "tk1"})
	if _, ok := s.Snapshot().Position.Tokens["tk1"]; ok {
		t.Fatal("tk1 should be cleared")
	}
}

func TestReconcileOnchainBalance_NoChangeWithinEpsilon(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{Balance: Balance{Available: 50, Reserved: 10}})
	changed, drift := s.ReconcileOnchainBalance(60, 1e-3)
	if changed || drift > 1e-6 {
		t.Fatalf("expected no change, got changed=%v drift=%v", changed, drift)
	}
}

func TestReconcileOnchainBalance_DriftAppliesUpdate(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{Balance: Balance{Available: 50, Reserved: 10}})
	changed, drift := s.ReconcileOnchainBalance(100, 1e-3)
	if !changed || drift < 39 {
		t.Fatalf("expected change with ~40 drift, got changed=%v drift=%v", changed, drift)
	}
	if got := s.Snapshot().Balance.Available; got != 90 {
		t.Fatalf("expected balance.Available=90 after reconcile, got %v", got)
	}
}

func TestReconcileOnchainBalance_NegativeOnchainClampedToZero(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{Balance: Balance{Available: 50, Reserved: 10}})
	changed, _ := s.ReconcileOnchainBalance(-100, 1e-3)
	if !changed {
		t.Fatal("expected reconcile to change available")
	}
	if got := s.Snapshot().Balance.Available; got != 0 {
		t.Fatalf("expected available clamped to 0 since onchain<0, got %v", got)
	}
}

func TestReconcileOnchainBalance_NewAvailableClampedZeroWhenReservedTooBig(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{Balance: Balance{Available: 50, Reserved: 200}})
	changed, _ := s.ReconcileOnchainBalance(100, 1e-3)
	if !changed {
		t.Fatal("expected reconcile to update available")
	}
	// 100 - 200 = -100 → clamped to 0
	if got := s.Snapshot().Balance.Available; got != 0 {
		t.Fatalf("expected available clamped to 0, got %v", got)
	}
}

func TestReconcileOnchainBalance_NegativeEpsilonTreatedAsZero(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{Balance: Balance{Available: 50, Reserved: 10}})
	// onchain=60, reserved=10 → newAvailable=50 → drift=0 → no change
	changed, drift := s.ReconcileOnchainBalance(60, -1)
	if changed || drift != 0 {
		t.Fatalf("expected no change with zero drift, got changed=%v drift=%v", changed, drift)
	}
}

func TestReleaseOrder_BUY_RestoresBalance(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if err := s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.5, 10); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	s.ReleaseOrder("o1")
	snap := s.Snapshot()
	if snap.Balance.Reserved != 0 || snap.Balance.Available != 100 {
		t.Fatalf("balance not restored: %+v", snap.Balance)
	}
	if _, ok := snap.Orders["o1"]; ok {
		t.Fatal("order should be removed")
	}
}

func TestReleaseOrder_SELL_RestoresPosition(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{
		Balance:  Balance{Available: 100},
		Position: Position{Tokens: map[string]TokenPosition{"tk1": {Available: 10}}},
	})
	if err := s.ReserveOrder("o1", "m1", "tk1", orders.SELL, 0.5, 5); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	s.ReleaseOrder("o1")
	snap := s.Snapshot()
	if snap.Position.Tokens["tk1"].Reserved != 0 {
		t.Fatalf("expected reserved=0 got %v", snap.Position.Tokens["tk1"].Reserved)
	}
	if snap.Position.Tokens["tk1"].Available != 10 {
		t.Fatalf("expected available=10 restored, got %v", snap.Position.Tokens["tk1"].Available)
	}
}

func TestReleaseOrder_EmptyOrderID_NoOp(t *testing.T) {
	s := newStateWithBalance(t, 100)
	// must not panic
	s.ReleaseOrder("")
}

func TestReleaseOrder_NonExistent_NoOp(t *testing.T) {
	s := newStateWithBalance(t, 100)
	s.ReleaseOrder("missing-order")
	snap := s.Snapshot()
	if snap.Balance.Available != 100 {
		t.Fatalf("balance should not change, got %+v", snap.Balance)
	}
}

func TestReleaseProvisional_EmptyID(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if s.ReleaseProvisional("") {
		t.Fatal("expected false for empty intent id")
	}
}

func TestReleaseProvisional_NonExistent(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if s.ReleaseProvisional("missing-intent") {
		t.Fatal("expected false for missing intent")
	}
}

func TestReleaseOrder_BUY_ClampsReservedToZero(t *testing.T) {
	// Restore state where Reserved < res.Reserved → after subtraction goes negative → clamp.
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{
		Balance: Balance{Available: 100, Reserved: 1}, // tiny reserved
		Orders: map[string]OrderReservation{
			"o1": {OrderID: "o1", MarketID: "m1", TokenID: "tk1", Side: orders.BUY, Price: 0.5, RemainingSize: 10, Reserved: 5},
		},
	})
	// Restore already set Reserved=5 due to Orders rebuild; manually drop it back.
	// Instead, use a different approach: rebuild but with negative Reserved via the restore loop's clamp.
	// Hmm — Restore re-derives Reserved from Orders. Use a direct Reserve+manual fix-up isn't safe.
	// Use ApplyFill instead to deplete the reservation first.
	// Apply a fill that completely consumes the reservation; reservation is removed,
	// then ReleaseOrder is a no-op. That doesn't hit the clamp.
	//
	// Instead, attach the order normally and then manipulate the balance via a partial fill that
	// reduces Reserved below what release expects. Actually, ApplyFill keeps res.Reserved in sync.
	//
	// The only way to test the clamp is to bypass Restore's accounting.  We can do that by
	// constructing the State directly. But that requires access to unexported fields, which
	// works because the test is in the same package.
	s2 := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s2.balance.Available = 100
	s2.balance.Reserved = 1 // smaller than res.Reserved
	s2.orderReservations["o1"] = OrderReservation{
		OrderID: "o1", MarketID: "m1", TokenID: "tk1",
		Side: orders.BUY, Price: 0.5, RemainingSize: 10, Reserved: 5,
	}
	s2.ReleaseOrder("o1")
	if got := s2.Snapshot().Balance.Reserved; got != 0 {
		t.Fatalf("expected reserved clamped to 0, got %v", got)
	}
}

func TestReleaseOrder_SELL_ClampsReservedToZero(t *testing.T) {
	// directly construct a State with mismatched token Reserved < res.Reserved
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.balance.Available = 100
	s.position.Tokens["tk1"] = TokenPosition{Available: 2, Reserved: 1} // smaller than res.Reserved
	s.orderReservations["o1"] = OrderReservation{
		OrderID: "o1", MarketID: "m1", TokenID: "tk1",
		Side: orders.SELL, Price: 0.5, RemainingSize: 10, Reserved: 5,
	}
	s.ReleaseOrder("o1")
	if got := s.Snapshot().Position.Tokens["tk1"].Reserved; got != 0 {
		t.Fatalf("expected token reserved clamped to 0, got %v", got)
	}
}

// Exercise releaseReservedLocked clamp via ReleaseProvisional with mismatched state.
func TestReleaseProvisional_BUY_ClampsBalanceReservedToZero(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.balance.Available = 100
	s.balance.Reserved = 0.001 // smaller than provisional reserved
	s.provisionalReservations["i1"] = ProvisionalReservation{
		IntentID: "i1", MarketID: "m1", TokenID: "tk1",
		Side: orders.BUY, Price: 0.5, RemainingSize: 10, Reserved: 5,
	}
	if !s.ReleaseProvisional("i1") {
		t.Fatal("expected release to succeed")
	}
	if got := s.Snapshot().Balance.Reserved; got < 0 {
		t.Fatalf("Reserved should clamp to >=0, got %v", got)
	}
}

func TestReleaseProvisional_SELL_ClampsPositionReservedToZero(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.balance.Available = 100
	s.position.Tokens["tk1"] = TokenPosition{Available: 5, Reserved: 0.001}
	s.provisionalReservations["i1"] = ProvisionalReservation{
		IntentID: "i1", MarketID: "m1", TokenID: "tk1",
		Side: orders.SELL, Price: 0.5, RemainingSize: 10, Reserved: 5,
	}
	if !s.ReleaseProvisional("i1") {
		t.Fatal("expected release to succeed")
	}
	if got := s.Snapshot().Position.Tokens["tk1"].Reserved; got < 0 {
		t.Fatalf("Reserved should clamp to >=0, got %v", got)
	}
}
