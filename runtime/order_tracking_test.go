package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/xiangxn/go-polymarket-sdk/orders"
	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/state"
)

func TestInitOrderTracking_AppliesDefaultsWhenZero(t *testing.T) {
	e := &Engine{}
	e.initOrderTracking()
	if e.PendingEventTTL != defaultPendingEventTTL {
		t.Fatalf("PendingEventTTL: expected %v, got %v", defaultPendingEventTTL, e.PendingEventTTL)
	}
	if e.FinalizedOrderTTL != defaultFinalizedOrderTTL {
		t.Fatalf("FinalizedOrderTTL: expected %v, got %v", defaultFinalizedOrderTTL, e.FinalizedOrderTTL)
	}
	if e.ProvisionalOrderTTL != defaultProvisionalOrderTTL {
		t.Fatalf("ProvisionalOrderTTL: expected %v, got %v", defaultProvisionalOrderTTL, e.ProvisionalOrderTTL)
	}
	if e.acceptedOrders == nil || e.finalized == nil || e.finalizedAt == nil || e.pendingByOrder == nil {
		t.Fatal("expected all maps initialised")
	}
}

func TestInitOrderTracking_PreservesNonZeroTTLs(t *testing.T) {
	e := &Engine{
		PendingEventTTL:     time.Second,
		FinalizedOrderTTL:   2 * time.Second,
		ProvisionalOrderTTL: 3 * time.Second,
	}
	e.initOrderTracking()
	if e.PendingEventTTL != time.Second ||
		e.FinalizedOrderTTL != 2*time.Second ||
		e.ProvisionalOrderTTL != 3*time.Second {
		t.Fatalf("non-zero TTLs should be preserved")
	}
}

func TestMarkAcceptedHasAccepted(t *testing.T) {
	e := &Engine{}
	e.initOrderTracking()
	if e.hasAccepted("o1") {
		t.Fatal("should not be accepted yet")
	}
	e.markAccepted("o1")
	if !e.hasAccepted("o1") {
		t.Fatal("should be accepted")
	}
	// idempotent
	e.markAccepted("o1")
	if !e.hasAccepted("o1") {
		t.Fatal("should still be accepted after duplicate mark")
	}
}

func TestBufferExecution_AppendsAndCounts(t *testing.T) {
	e := &Engine{}
	e.initOrderTracking()
	e.bufferExecution(core.ExecutionEvent{OrderID: "o1", Status: core.ExecutionStatusFilled})
	e.bufferExecution(core.ExecutionEvent{OrderID: "o1", Status: core.ExecutionStatusFilled, FilledSize: 1})
	if e.executionBuffered.Load() != 2 {
		t.Fatalf("expected buffered=2, got %d", e.executionBuffered.Load())
	}
	if e.pendingOrderCount() != 1 {
		t.Fatalf("expected one pending order, got %d", e.pendingOrderCount())
	}
}

func TestReplayPending_DrainsBuffer(t *testing.T) {
	// We need a real State to absorb the replayed FILLED event without
	// touching the bus (handleExecutionEvent will not publish risk because
	// FilledSize == 0).
	s := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, nil)
	s.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})
	if err := s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.5, 1); err != nil {
		t.Fatalf("reserve failed: %v", err)
	}
	e := &Engine{State: s}
	e.initOrderTracking()
	e.markAccepted("o1")
	e.bufferExecution(core.ExecutionEvent{
		OrderID:    "o1",
		Status:     core.ExecutionStatusFilled,
		FilledSize: 0,
	})
	if e.pendingOrderCount() != 1 {
		t.Fatalf("expected pending=1, got %d", e.pendingOrderCount())
	}
	e.replayPending("o1")
	if e.pendingOrderCount() != 0 {
		t.Fatalf("expected pending=0 after replay, got %d", e.pendingOrderCount())
	}
	if !e.isFinalized("o1") {
		t.Fatal("expected o1 finalized after filled replay")
	}
}

func TestReplayPending_NoBufferIsNoop(t *testing.T) {
	e := &Engine{}
	e.initOrderTracking()
	// no panic expected
	e.replayPending("unknown")
}

func TestFinalizeOrder_RemovesAcceptedAndPending(t *testing.T) {
	e := &Engine{}
	e.initOrderTracking()
	e.markAccepted("o1")
	e.bufferExecution(core.ExecutionEvent{OrderID: "o1", Status: core.ExecutionStatusFilled})
	e.finalizeOrder("o1")
	if !e.isFinalized("o1") {
		t.Fatal("should be finalized")
	}
	if e.hasAccepted("o1") {
		t.Fatal("accepted should be cleared")
	}
	if e.pendingOrderCount() != 0 {
		t.Fatalf("pending should be cleared, got %d", e.pendingOrderCount())
	}
}

func TestCleanupExpiredPending_RemovesStaleAndPublishesRisk(t *testing.T) {
	e := &Engine{PendingEventTTL: 10 * time.Millisecond, Bus: core.NewEventBus()}
	e.initOrderTracking()
	ch, cancel := e.Bus.SubscribeWithCancel()
	defer cancel()

	e.bufferExecution(core.ExecutionEvent{OrderID: "stale", Status: core.ExecutionStatusFilled})
	time.Sleep(30 * time.Millisecond)
	e.cleanupExpiredPending(time.Now())

	if e.pendingOrderCount() != 0 {
		t.Fatalf("expected pending cleaned, got %d", e.pendingOrderCount())
	}
	if e.executionExpired.Load() == 0 {
		t.Fatal("expected executionExpired to increment")
	}

	select {
	case ev := <-ch:
		if ev.Type != core.EventRisk {
			t.Fatalf("expected risk event, got %v", ev.Type)
		}
		if r, ok := ev.Data.(core.RiskEvent); !ok || !strings.Contains(r.Reason, "stale") {
			t.Fatalf("expected reason mentioning stale, got %#v", ev.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for risk event")
	}
}

func TestCleanupExpiredPending_TTLZeroNoop(t *testing.T) {
	e := &Engine{PendingEventTTL: -1}
	e.initOrderTracking()
	e.PendingEventTTL = 0
	e.pendingByOrder["x"] = pendingExecution{firstSeen: time.Now().Add(-time.Hour)}
	e.cleanupExpiredPending(time.Now())
	if e.pendingOrderCount() == 0 {
		t.Fatal("with TTL=0, cleanup should be a noop")
	}
}

func TestCleanupExpiredPending_FreshEntryKept(t *testing.T) {
	e := &Engine{PendingEventTTL: time.Hour, Bus: core.NewEventBus()}
	e.initOrderTracking()
	e.bufferExecution(core.ExecutionEvent{OrderID: "fresh", Status: core.ExecutionStatusFilled})
	e.cleanupExpiredPending(time.Now())
	if e.pendingOrderCount() != 1 {
		t.Fatalf("fresh entry should not be removed, got pending=%d", e.pendingOrderCount())
	}
}

func TestCleanupExpiredFinalized_RemovesStale(t *testing.T) {
	e := &Engine{FinalizedOrderTTL: 10 * time.Millisecond}
	e.initOrderTracking()
	e.markAccepted("o1")
	e.finalizeOrder("o1")
	time.Sleep(30 * time.Millisecond)
	e.cleanupExpiredFinalized(time.Now())
	if e.isFinalized("o1") {
		t.Fatal("expected finalized record cleaned")
	}
}

func TestCleanupExpiredFinalized_TTLZeroNoop(t *testing.T) {
	e := &Engine{}
	e.initOrderTracking()
	e.FinalizedOrderTTL = 0
	e.finalizedAt["o1"] = time.Now().Add(-time.Hour)
	e.finalized["o1"] = struct{}{}
	e.cleanupExpiredFinalized(time.Now())
	if !e.isFinalized("o1") {
		t.Fatal("with TTL=0, cleanup should be a noop")
	}
}

func TestCleanupExpiredFinalized_FreshKept(t *testing.T) {
	e := &Engine{FinalizedOrderTTL: time.Hour}
	e.initOrderTracking()
	e.markAccepted("o1")
	e.finalizeOrder("o1")
	e.cleanupExpiredFinalized(time.Now())
	if !e.isFinalized("o1") {
		t.Fatal("fresh finalized record should be kept")
	}
}

func TestCleanupExpiredProvisional_PublishesRiskOnExpiry(t *testing.T) {
	s := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, nil)
	s.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})
	now := time.Now()
	if err := s.TryReserveProvisional("i1", "m1", "tk1", orders.BUY, 0.5, 1, now, time.Millisecond); err != nil {
		t.Fatalf("provisional reserve failed: %v", err)
	}
	bus := core.NewEventBus()
	ch, cancel := bus.SubscribeWithCancel()
	defer cancel()
	e := &Engine{State: s, Bus: bus}
	e.initOrderTracking()

	e.cleanupExpiredProvisional(now.Add(time.Second))

	select {
	case ev := <-ch:
		if ev.Type != core.EventRisk {
			t.Fatalf("expected risk event, got %v", ev.Type)
		}
		if r, ok := ev.Data.(core.RiskEvent); !ok || !strings.Contains(r.Reason, "i1") {
			t.Fatalf("expected reason mentioning intent id, got %#v", ev.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for risk event")
	}
}

func TestCleanupExpiredProvisional_NoExpired(t *testing.T) {
	s := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, nil)
	s.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})
	e := &Engine{State: s, Bus: core.NewEventBus()}
	e.initOrderTracking()
	// no panic, no risk events expected.
	e.cleanupExpiredProvisional(time.Now())
}

func TestCleanupTracking_CallsAllThree(t *testing.T) {
	s := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, nil)
	s.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})
	bus := core.NewEventBus()
	e := &Engine{State: s, Bus: bus, PendingEventTTL: time.Millisecond, FinalizedOrderTTL: time.Millisecond}
	e.initOrderTracking()
	e.bufferExecution(core.ExecutionEvent{OrderID: "pending", Status: core.ExecutionStatusFilled})
	e.markAccepted("fin")
	e.finalizeOrder("fin")
	time.Sleep(10 * time.Millisecond)
	// Drain risk events asynchronously to avoid bus backpressure.
	ch, cancel := bus.SubscribeWithCancel()
	defer cancel()
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()

	e.cleanupTracking(time.Now())

	if e.pendingOrderCount() != 0 {
		t.Fatalf("pending should be cleaned, got %d", e.pendingOrderCount())
	}
	if e.isFinalized("fin") {
		t.Fatal("finalized should be cleaned")
	}
}

func TestNextIntentIDIncreasesMonotonically(t *testing.T) {
	e := &Engine{}
	seen := make(map[string]struct{})
	for i := 0; i < 5; i++ {
		id := e.nextIntentID()
		if id == "" {
			t.Fatal("intent id should not be empty")
		}
		if !strings.HasPrefix(id, "intent-") {
			t.Fatalf("intent id should be prefixed, got %s", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id: %s", id)
		}
		seen[id] = struct{}{}
	}
}

func TestRestoreOpenOrdersTrackingByIDs(t *testing.T) {
	e := &Engine{}
	e.initOrderTracking()
	e.restoreOpenOrdersTrackingByIDs([]string{"o1", "o2", ""})
	if !e.hasAccepted("o1") || !e.hasAccepted("o2") {
		t.Fatal("expected o1,o2 accepted")
	}
	if e.hasAccepted("") {
		t.Fatal("empty id should be skipped")
	}
}

func TestRestoreOpenOrdersTrackingByIDs_Empty(t *testing.T) {
	e := &Engine{}
	e.initOrderTracking()
	e.restoreOpenOrdersTrackingByIDs(nil)
	if len(e.acceptedOrders) != 0 {
		t.Fatalf("expected no accepted orders, got %d", len(e.acceptedOrders))
	}
}

func TestPendingOrderCount_Zero(t *testing.T) {
	e := &Engine{}
	e.initOrderTracking()
	if e.pendingOrderCount() != 0 {
		t.Fatalf("expected 0, got %d", e.pendingOrderCount())
	}
}
