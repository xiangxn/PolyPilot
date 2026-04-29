package runtime

import (
	"testing"
	"time"

	"polypilot/core"
	"polypilot/state"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

func TestHandleExecutionEventRejectedWithoutOrderIDReleasesProvisional(t *testing.T) {
	s := state.NewState(state.BalanceSyncConfig{}, nil)
	s.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})

	now := time.Now()
	if err := s.TryReserveProvisional("i1", "m1", "tk1", orders.BUY, 0.5, 10, now, 5*time.Second); err != nil {
		t.Fatalf("provisional reserve failed: %v", err)
	}

	e := &Engine{State: s}
	e.initOrderTracking()
	e.handleExecutionEvent(core.ExecutionEvent{
		ParentOrderID: "i1",
		Status:        core.ExecutionStatusRejected,
		Reason:        "post order failed",
	}, true)

	snap := s.Snapshot()
	if snap.Balance.Available != 100 || snap.Balance.Reserved != 0 {
		t.Fatalf("expected provisional released on rejected without order id, got available=%v reserved=%v", snap.Balance.Available, snap.Balance.Reserved)
	}
}

func TestHandleExecutionEventAcceptedWSFirstThenPostAckDoesNotLeakReserved(t *testing.T) {
	s := state.NewState(state.BalanceSyncConfig{}, nil)
	s.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})

	now := time.Now()
	if err := s.TryReserveProvisional("i1", "m1", "tk1", orders.BUY, 0.5, 10, now, 5*time.Second); err != nil {
		t.Fatalf("provisional reserve failed: %v", err)
	}

	e := &Engine{State: s}
	e.initOrderTracking()

	// WS LIVE accepted arrives first (without ParentOrderID)
	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID:       "o1",
		MarketID:      "m1",
		TokenID:       "tk1",
		Price:         0.5,
		Side:          orders.BUY,
		RequestedSize: 10,
		Status:        core.ExecutionStatusAccepted,
		At:            now,
	}, true)

	// post-order accepted arrives later (with ParentOrderID)
	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID:       "o1",
		ParentOrderID: "i1",
		MarketID:      "m1",
		TokenID:       "tk1",
		Price:         0.5,
		Side:          orders.BUY,
		RequestedSize: 10,
		Status:        core.ExecutionStatusAccepted,
		At:            now,
	}, true)

	snap := s.Snapshot()
	if snap.Balance.Available != 95 || snap.Balance.Reserved != 5 {
		t.Fatalf("accepted ws-first sequence should not leak reserve, got available=%v reserved=%v", snap.Balance.Available, snap.Balance.Reserved)
	}
}

func TestHandleExecutionEventFilledReleasesRoundingResidualReserve(t *testing.T) {
	s := state.NewState(state.BalanceSyncConfig{}, nil)
	s.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})
	if err := s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.35, 5); err != nil {
		t.Fatalf("reserve order failed: %v", err)
	}

	e := &Engine{State: s}
	e.initOrderTracking()
	now := time.Now()
	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID:       "o1",
		MarketID:      "m1",
		TokenID:       "tk1",
		Price:         0.35,
		Side:          orders.BUY,
		RequestedSize: 5,
		Status:        core.ExecutionStatusAccepted,
		At:            now,
	}, true)
	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID:       "o1",
		MarketID:      "m1",
		TokenID:       "tk1",
		Price:         0.35,
		Side:          orders.BUY,
		RequestedSize: 5,
		FilledSize:    4.993074,
		Status:        core.ExecutionStatusFilled,
		At:            now,
	}, true)

	snap := s.Snapshot()
	if snap.Balance.Reserved != 0 {
		t.Fatalf("expected filled to release residual reserve, got reserved=%v", snap.Balance.Reserved)
	}
	if _, ok := snap.Orders["o1"]; ok {
		t.Fatalf("expected order reservation removed after filled")
	}
}

func TestHandleExecutionEventAcceptedConfirmsProvisionalWithoutDoubleReserve(t *testing.T) {
	s := state.NewState(state.BalanceSyncConfig{}, nil)
	s.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})

	now := time.Now()
	if err := s.TryReserveProvisional("i1", "m1", "tk1", orders.BUY, 0.5, 10, now, 5*time.Second); err != nil {
		t.Fatalf("provisional reserve failed: %v", err)
	}

	e := &Engine{State: s}
	e.initOrderTracking()
	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID:       "o1",
		ParentOrderID: "i1",
		MarketID:      "m1",
		TokenID:       "tk1",
		Price:         0.5,
		Side:          orders.BUY,
		RequestedSize: 10,
		Status:        core.ExecutionStatusAccepted,
		At:            now,
	}, true)

	snap := s.Snapshot()
	if snap.Balance.Available != 95 || snap.Balance.Reserved != 5 {
		t.Fatalf("accepted confirm should not double reserve, got available=%v reserved=%v", snap.Balance.Available, snap.Balance.Reserved)
	}
	if _, ok := snap.Orders["o1"]; !ok {
		t.Fatalf("expected confirmed order reservation for o1")
	}
	if released := s.ReleaseProvisional("i1"); released {
		t.Fatalf("provisional should already be removed after confirm")
	}
}
