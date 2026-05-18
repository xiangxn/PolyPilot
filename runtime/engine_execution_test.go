package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/xiangxn/go-polymarket-sdk/orders"
	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/state"
)

// TestHandleExecutionEvent_FinalizedShortCircuit covers the early return when
// the order has already been finalized.
func TestHandleExecutionEvent_FinalizedShortCircuit(t *testing.T) {
	s := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, nil)
	s.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})
	e := &Engine{State: s}
	e.initOrderTracking()
	e.finalizeOrder("o1")

	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID: "o1",
		Status:  core.ExecutionStatusAccepted,
	}, true)

	if e.executionAccepted.Load() != 0 {
		t.Fatalf("accepted counter should not increment after finalize, got %d", e.executionAccepted.Load())
	}
	if e.hasAccepted("o1") {
		t.Fatal("should not mark accepted after finalize")
	}
}

// TestHandleExecutionEvent_AcceptedAttachFailsPublishesRisk triggers
// validateOrderArgs failure in AttachOrder (empty MarketID) so we hit the
// non-ErrOrderAlreadyReserved error branch.
func TestHandleExecutionEvent_AcceptedAttachFailsPublishesRisk(t *testing.T) {
	s := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, nil)
	s.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})
	bus := core.NewEventBus()
	ch, cancel := bus.SubscribeWithCancel()
	defer cancel()
	e := &Engine{State: s, Bus: bus}
	e.initOrderTracking()

	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID:       "o1",
		MarketID:      "", // invalid → AttachOrder returns ErrInvalidMarket
		TokenID:       "tk1",
		Price:         0.5,
		Side:          orders.BUY,
		RequestedSize: 1,
		Status:        core.ExecutionStatusAccepted,
	}, true)

	select {
	case ev := <-ch:
		if ev.Type != core.EventRisk {
			t.Fatalf("expected risk event, got %v", ev.Type)
		}
		r, ok := ev.Data.(core.RiskEvent)
		if !ok || !strings.Contains(r.Reason, "attach failed") {
			t.Fatalf("expected attach-failed risk, got %#v", ev.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for risk event")
	}
}

// TestHandleExecutionEvent_FillBeforeAcceptBuffers ensures that a FILLED event
// arriving before ACCEPTED is buffered (not applied).
func TestHandleExecutionEvent_FillBeforeAcceptBuffers(t *testing.T) {
	s := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, nil)
	s.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})
	e := &Engine{State: s}
	e.initOrderTracking()

	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID:    "o1",
		MarketID:   "m1",
		TokenID:    "tk1",
		Price:      0.5,
		Side:       orders.BUY,
		FilledSize: 1,
		Status:     core.ExecutionStatusFilled,
	}, true)

	if e.executionFilled.Load() != 0 {
		t.Fatal("filled counter should not increment when buffered")
	}
	if e.pendingOrderCount() != 1 {
		t.Fatalf("expected one pending order, got %d", e.pendingOrderCount())
	}
}

// TestHandleExecutionEvent_PartiallyFilledBuffersWhenNotAccepted covers the
// PARTIALLY_FILLED arm of the same buffer branch.
func TestHandleExecutionEvent_PartiallyFilledBuffersWhenNotAccepted(t *testing.T) {
	s := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, nil)
	s.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})
	e := &Engine{State: s}
	e.initOrderTracking()

	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID:    "o1",
		MarketID:   "m1",
		TokenID:    "tk1",
		Price:      0.5,
		Side:       orders.BUY,
		FilledSize: 0.5,
		Status:     core.ExecutionStatusPartiallyFilled,
	}, true)

	if e.pendingOrderCount() != 1 {
		t.Fatalf("expected pending=1, got %d", e.pendingOrderCount())
	}
	if e.executionFilled.Load() != 0 {
		t.Fatal("filled counter should not increment when buffered")
	}
}

// TestHandleExecutionEvent_PartiallyFilledApplyAfterAccept covers the
// PARTIALLY_FILLED non-finalize tail path (ApplyFill succeeds but order stays
// open).
func TestHandleExecutionEvent_PartiallyFilledApplyAfterAccept(t *testing.T) {
	s := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, nil)
	s.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})
	if err := s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.5, 5); err != nil {
		t.Fatalf("reserve failed: %v", err)
	}
	e := &Engine{State: s}
	e.initOrderTracking()
	e.markAccepted("o1")

	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID:    "o1",
		MarketID:   "m1",
		TokenID:    "tk1",
		Price:      0.5,
		Side:       orders.BUY,
		FilledSize: 2,
		Status:     core.ExecutionStatusPartiallyFilled,
	}, true)

	if e.isFinalized("o1") {
		t.Fatal("partial fill should not finalize order")
	}
	if e.executionFilled.Load() != 1 {
		t.Fatalf("expected executionFilled=1, got %d", e.executionFilled.Load())
	}
}

// TestHandleExecutionEvent_FillApplyErrorPublishesRisk exercises the
// ApplyFill→error branch (Side mismatch).
func TestHandleExecutionEvent_FillApplyErrorPublishesRisk(t *testing.T) {
	s := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, nil)
	s.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})
	if err := s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.5, 1); err != nil {
		t.Fatalf("reserve failed: %v", err)
	}
	bus := core.NewEventBus()
	ch, cancel := bus.SubscribeWithCancel()
	defer cancel()
	e := &Engine{State: s, Bus: bus}
	e.initOrderTracking()
	e.markAccepted("o1")

	// Side mismatch (SELL vs BUY reservation) → ApplyFill returns
	// ErrFillSideMismatch.
	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID:    "o1",
		MarketID:   "m1",
		TokenID:    "tk1",
		Price:      0.5,
		Side:       orders.SELL,
		FilledSize: 1,
		Status:     core.ExecutionStatusFilled,
	}, true)

	select {
	case ev := <-ch:
		if ev.Type != core.EventRisk {
			t.Fatalf("expected risk event, got %v", ev.Type)
		}
		r, ok := ev.Data.(core.RiskEvent)
		if !ok || !strings.Contains(r.Reason, "fill apply failed") {
			t.Fatalf("expected fill-apply-failed risk, got %#v", ev.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
	if e.isFinalized("o1") {
		t.Fatal("order should NOT finalize after apply-fill error")
	}
}

// TestHandleExecutionEvent_FilledZeroSizeStillFinalizes covers the
// filledSize==0 branch (skips ApplyFill but still releases+finalizes).
func TestHandleExecutionEvent_FilledZeroSizeStillFinalizes(t *testing.T) {
	s := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, nil)
	s.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})
	if err := s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.5, 1); err != nil {
		t.Fatalf("reserve failed: %v", err)
	}
	e := &Engine{State: s}
	e.initOrderTracking()
	e.markAccepted("o1")

	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID:    "o1",
		MarketID:   "m1",
		TokenID:    "tk1",
		Price:      0.5,
		Side:       orders.BUY,
		FilledSize: 0,
		Status:     core.ExecutionStatusFilled,
	}, true)

	if !e.isFinalized("o1") {
		t.Fatal("expected finalize even with zero filled size")
	}
}

// TestHandleExecutionEvent_CancelledBeforeAcceptedBuffers covers the cancelled
// buffer branch.
func TestHandleExecutionEvent_CancelledBeforeAcceptedBuffers(t *testing.T) {
	s := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, nil)
	e := &Engine{State: s}
	e.initOrderTracking()

	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID: "o1",
		Status:  core.ExecutionStatusCancelled,
	}, true)

	if e.pendingOrderCount() != 1 {
		t.Fatalf("expected cancelled to be buffered, got pending=%d", e.pendingOrderCount())
	}
	if e.isFinalized("o1") {
		t.Fatal("buffered cancel should not finalize")
	}
}

// TestHandleExecutionEvent_CancelledAfterAcceptedFinalizes covers the
// cancelled→release→finalize tail.
func TestHandleExecutionEvent_CancelledAfterAcceptedFinalizes(t *testing.T) {
	s := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, nil)
	s.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})
	if err := s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.5, 5); err != nil {
		t.Fatalf("reserve failed: %v", err)
	}
	e := &Engine{State: s}
	e.initOrderTracking()
	e.markAccepted("o1")

	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID: "o1",
		Status:  core.ExecutionStatusCancelled,
	}, true)

	if !e.isFinalized("o1") {
		t.Fatal("cancelled should finalize after accepted")
	}
	snap := s.Snapshot()
	if _, ok := snap.Orders["o1"]; ok {
		t.Fatal("reservation should be released")
	}
}

// TestHandleExecutionEvent_RejectedWithOrderIDAndAcceptedReleases covers the
// rejected branch path where OrderID is present, ParentOrderID is set, and
// the order is also already accepted (exercises both release calls).
func TestHandleExecutionEvent_RejectedWithOrderIDAndAcceptedReleases(t *testing.T) {
	s := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, nil)
	s.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})
	now := time.Now()
	if err := s.TryReserveProvisional("i1", "m1", "tk1", orders.BUY, 0.5, 1, now, time.Second); err != nil {
		t.Fatalf("provisional reserve failed: %v", err)
	}
	if err := s.AttachOrder("i1", "o1", "m1", "tk1", orders.BUY, 0.5, 1); err != nil {
		t.Fatalf("attach failed: %v", err)
	}
	bus := core.NewEventBus()
	ch, cancel := bus.SubscribeWithCancel()
	defer cancel()
	e := &Engine{State: s, Bus: bus}
	e.initOrderTracking()
	e.markAccepted("o1")

	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID:       "o1",
		ParentOrderID: "i1",
		Status:        core.ExecutionStatusRejected,
		Reason:        "bad",
	}, true)

	if !e.isFinalized("o1") {
		t.Fatal("expected finalize after rejected")
	}
	if e.executionRejected.Load() != 1 {
		t.Fatalf("expected executionRejected=1, got %d", e.executionRejected.Load())
	}
	snap := s.Snapshot()
	if _, ok := snap.Orders["o1"]; ok {
		t.Fatal("expected order reservation released on rejected")
	}

	select {
	case ev := <-ch:
		if ev.Type != core.EventRisk {
			t.Fatalf("expected risk event, got %v", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for risk event")
	}
}

// TestHandleExecutionEvent_RejectedWithOrderIDNotAccepted covers the rejected
// branch path where OrderID is present but the order was never accepted
// (skips ReleaseOrder).
func TestHandleExecutionEvent_RejectedWithOrderIDNotAccepted(t *testing.T) {
	s := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, nil)
	s.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})
	bus := core.NewEventBus()
	ch, cancel := bus.SubscribeWithCancel()
	defer cancel()
	e := &Engine{State: s, Bus: bus}
	e.initOrderTracking()

	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID: "o1",
		Status:  core.ExecutionStatusRejected,
		Reason:  "bad",
	}, true)

	if !e.isFinalized("o1") {
		t.Fatal("expected finalize")
	}
	if e.executionRejected.Load() != 1 {
		t.Fatalf("expected executionRejected=1, got %d", e.executionRejected.Load())
	}

	select {
	case ev := <-ch:
		if ev.Type != core.EventRisk {
			t.Fatalf("expected risk event, got %v", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for risk event")
	}
}

// TestHandleExecutionEvent_CountFalseSkipsIncrement guards the "count=false"
// override used by replayPending.
func TestHandleExecutionEvent_CountFalseSkipsIncrement(t *testing.T) {
	s := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, nil)
	s.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})
	e := &Engine{State: s}
	e.initOrderTracking()
	e.markAccepted("o1")
	if err := s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.5, 1); err != nil {
		t.Fatalf("reserve failed: %v", err)
	}

	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID:  "o1",
		MarketID: "m1",
		TokenID:  "tk1",
		Side:     orders.BUY,
		Price:    0.5,
		Status:   core.ExecutionStatusFilled,
	}, false)

	if e.executionEvents.Load() != 0 {
		t.Fatalf("executionEvents should NOT increment when count=false, got %d", e.executionEvents.Load())
	}
}
