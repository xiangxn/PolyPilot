package runtime

import (
	"polypilot/core"
	"polypilot/state"
	"polypilot/store"
	"testing"
	"time"
)

func TestHandleExecutionEvent_FilledBeforeAccepted(t *testing.T) {
	e := &Engine{
		Bus:   core.NewEventBus(),
		State: state.NewState(100),
	}
	e.initOrderTracking()

	orderID := "ord-test-1"
	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID:       orderID,
		MarketID:      "market-1",
		TokenID:       "token-1",
		Price:         0.60,
		Side:          core.SideBuy,
		RequestedSize: 10,
		FilledSize:    10,
		Status:        core.ExecutionStatusFilled,
		At:            time.Now(),
	}, true)

	s1 := e.State.Snapshot()
	if s1.Position.Buy != 0 || s1.Balance.Reserved != 0 || s1.Balance.Available != 100 {
		t.Fatalf("unexpected pre-accepted state: %+v", s1)
	}
	if len(e.pendingByOrder) != 1 {
		t.Fatalf("expected 1 pending order, got %d", len(e.pendingByOrder))
	}

	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID:       orderID,
		MarketID:      "market-1",
		TokenID:       "token-1",
		Price:         0.60,
		Side:          core.SideBuy,
		RequestedSize: 10,
		FilledSize:    0,
		Status:        core.ExecutionStatusAccepted,
		At:            time.Now(),
	}, true)

	s2 := e.State.Snapshot()
	if s2.Position.Buy != 10 {
		t.Fatalf("expected buy position=10, got %.2f", s2.Position.Buy)
	}
	if s2.Balance.Reserved != 0 {
		t.Fatalf("expected reserved=0, got %.2f", s2.Balance.Reserved)
	}
	if s2.Balance.Available != 94 {
		t.Fatalf("expected available=94, got %.2f", s2.Balance.Available)
	}
	if len(e.pendingByOrder) != 0 {
		t.Fatalf("expected pending cleared, got %d", len(e.pendingByOrder))
	}
}

func TestCleanupExpiredPending(t *testing.T) {
	e := &Engine{
		Bus:             core.NewEventBus(),
		State:           state.NewState(100),
		PendingEventTTL: 1 * time.Second,
	}
	e.initOrderTracking()

	orderID := "ord-test-2"
	e.bufferExecution(core.ExecutionEvent{
		OrderID:       orderID,
		MarketID:      "market-1",
		TokenID:       "token-2",
		Price:         0.40,
		Side:          core.SideSell,
		RequestedSize: 5,
		FilledSize:    5,
		Status:        core.ExecutionStatusFilled,
		At:            time.Now(),
	})

	pending := e.pendingByOrder[orderID]
	pending.firstSeen = time.Now().Add(-3 * time.Second)
	e.pendingByOrder[orderID] = pending

	e.cleanupExpiredPending(time.Now())

	if len(e.pendingByOrder) != 0 {
		t.Fatalf("expected expired pending removed, got %d", len(e.pendingByOrder))
	}
	if got := e.executionExpired.Load(); got != 1 {
		t.Fatalf("expected executionExpired=1, got %d", got)
	}
}

func TestCleanupExpiredFinalized(t *testing.T) {
	e := &Engine{
		Bus:               core.NewEventBus(),
		State:             state.NewState(100),
		FinalizedOrderTTL: 1 * time.Second,
	}
	e.initOrderTracking()

	e.finalized["ord-old"] = struct{}{}
	e.finalizedAt["ord-old"] = time.Now().Add(-3 * time.Second)
	e.finalized["ord-new"] = struct{}{}
	e.finalizedAt["ord-new"] = time.Now()

	e.cleanupExpiredFinalized(time.Now())

	if _, ok := e.finalized["ord-old"]; ok {
		t.Fatalf("expected ord-old removed from finalized map")
	}
	if _, ok := e.finalizedAt["ord-old"]; ok {
		t.Fatalf("expected ord-old removed from finalizedAt map")
	}
	if _, ok := e.finalized["ord-new"]; !ok {
		t.Fatalf("expected ord-new to stay in finalized map")
	}
}

func TestRestoreFromStore_SnapshotAndOpenOrders(t *testing.T) {
	orderStore := store.NewMemoryOrderStore()
	stateStore := store.NewMemoryStateStore()

	_ = stateStore.SaveSnapshot(store.SnapshotRecord{
		Available: 80,
		Reserved:  20,
		Buy:       2,
		Sell:      1,
		At:        time.Now().UnixNano(),
	})
	_ = orderStore.UpsertOrder(store.OrderRecord{
		OrderID:       "ord-open-1",
		MarketID:      "market-1",
		TokenID:       "token-1",
		Side:          core.SideBuy,
		Price:         0.5,
		RequestedSize: 10,
		RemainingSize: 10,
		Reserved:      5,
		Status:        core.ExecutionStatusAccepted,
		UpdatedAt:     time.Now().UnixNano(),
	})

	e := &Engine{
		Bus:        core.NewEventBus(),
		State:      state.NewState(100),
		OrderStore: orderStore,
		StateStore: stateStore,
	}
	e.initOrderTracking()
	e.restoreFromStore()

	snap := e.State.Snapshot()
	if snap.Balance.Available != 80 || snap.Balance.Reserved != 20 {
		t.Fatalf("unexpected restored balance: %+v", snap.Balance)
	}
	if snap.Position.Buy != 2 || snap.Position.Sell != 1 {
		t.Fatalf("unexpected restored position: %+v", snap.Position)
	}
	if !e.hasAccepted("ord-open-1") {
		t.Fatalf("expected open order to be marked accepted")
	}
	if err := e.State.ApplyFill("ord-open-1", "market-1", "token-1", core.SideBuy, 2); err != nil {
		t.Fatalf("expected restored reservation usable, got err=%v", err)
	}
}

func TestUpsertOrderRecord_Lifecycle(t *testing.T) {
	orderStore := store.NewMemoryOrderStore()
	e := &Engine{Bus: core.NewEventBus(), OrderStore: orderStore}

	e.upsertOrderRecord(core.ExecutionEvent{
		OrderID:       "ord-1",
		MarketID:      "market-1",
		TokenID:       "token-1",
		Price:         0.4,
		Side:          core.SideBuy,
		RequestedSize: 10,
		Status:        core.ExecutionStatusAccepted,
		At:            time.Now(),
	})

	rec, ok, err := orderStore.GetOrder("ord-1")
	if err != nil || !ok {
		t.Fatalf("expected stored order, err=%v ok=%v", err, ok)
	}
	if rec.RemainingSize != 10 || rec.Reserved != 4 {
		t.Fatalf("unexpected accepted record: %+v", rec)
	}

	e.upsertOrderRecord(core.ExecutionEvent{
		OrderID:    "ord-1",
		FilledSize: 3,
		Status:     core.ExecutionStatusPartiallyFilled,
		At:         time.Now(),
	})
	rec, ok, _ = orderStore.GetOrder("ord-1")
	if !ok || rec.RemainingSize != 7 {
		t.Fatalf("unexpected partial record: %+v", rec)
	}

	e.upsertOrderRecord(core.ExecutionEvent{
		OrderID: "ord-1",
		Status:  core.ExecutionStatusFilled,
		At:      time.Now(),
	})
	rec, ok, _ = orderStore.GetOrder("ord-1")
	if !ok || rec.RemainingSize != 0 || rec.Reserved != 0 {
		t.Fatalf("unexpected final record: %+v", rec)
	}
}
