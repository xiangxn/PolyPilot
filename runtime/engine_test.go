package runtime

import (
	"math"
	"polypilot/core"
	"polypilot/state"
	"polypilot/store"
	"testing"
	"time"

	"github.com/polymarket/go-order-utils/pkg/model"
)

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9
}

func TestHandleExecutionEvent_FilledBeforeAccepted(t *testing.T) {
	e := &Engine{
		Bus:   core.NewEventBus(),
		State: state.NewState(0, state.WithInitialAvailable(100)),
	}
	e.initOrderTracking()

	orderID := "ord-test-1"
	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID:       orderID,
		MarketID:      "market-1",
		TokenID:       "token-1",
		Price:         0.60,
		Side:          model.BUY,
		RequestedSize: 10,
		FilledSize:    10,
		Status:        core.ExecutionStatusFilled,
		At:            time.Now(),
	}, true)

	s1 := e.State.Snapshot()
	if !almostEqual(s1.Balance.Reserved, 0) || !almostEqual(s1.Balance.Available, 100) {
		t.Fatalf("unexpected pre-accepted state: %+v", s1)
	}
	if tp := s1.Position.Tokens["token-1"]; !almostEqual(tp.Available, 0) || !almostEqual(tp.Reserved, 0) {
		t.Fatalf("unexpected token position before accepted: %+v", tp)
	}
	if len(e.pendingByOrder) != 1 {
		t.Fatalf("expected 1 pending order, got %d", len(e.pendingByOrder))
	}

	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID:       orderID,
		MarketID:      "market-1",
		TokenID:       "token-1",
		Price:         0.60,
		Side:          model.BUY,
		RequestedSize: 10,
		FilledSize:    0,
		Status:        core.ExecutionStatusAccepted,
		At:            time.Now(),
	}, true)

	s2 := e.State.Snapshot()
	tp := s2.Position.Tokens["token-1"]
	if !almostEqual(tp.Available, 10) || !almostEqual(tp.Reserved, 0) {
		t.Fatalf("expected token-1 available=10 reserved=0, got %+v", tp)
	}
	if !almostEqual(s2.Balance.Reserved, 0) {
		t.Fatalf("expected reserved=0, got %.2f", s2.Balance.Reserved)
	}
	if !almostEqual(s2.Balance.Available, 94) {
		t.Fatalf("expected available=94, got %.2f", s2.Balance.Available)
	}
	if len(e.pendingByOrder) != 0 {
		t.Fatalf("expected pending cleared, got %d", len(e.pendingByOrder))
	}
}

func TestCleanupExpiredPending(t *testing.T) {
	e := &Engine{
		Bus:             core.NewEventBus(),
		State:           state.NewState(0, state.WithInitialAvailable(100)),
		PendingEventTTL: 1 * time.Second,
	}
	e.initOrderTracking()

	orderID := "ord-test-2"
	e.bufferExecution(core.ExecutionEvent{
		OrderID:       orderID,
		MarketID:      "market-1",
		TokenID:       "token-2",
		Price:         0.40,
		Side:          model.SELL,
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
		State:             state.NewState(0, state.WithInitialAvailable(100)),
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
		At:        time.Now().UnixNano(),
	})
	_ = orderStore.UpsertOrder(store.OrderRecord{
		OrderID:       "ord-open-buy",
		MarketID:      "market-1",
		TokenID:       "token-1",
		Side:          model.BUY,
		Price:         0.5,
		RequestedSize: 10,
		RemainingSize: 10,
		Reserved:      5,
		Status:        core.ExecutionStatusAccepted,
		UpdatedAt:     time.Now().UnixNano(),
	})
	_ = orderStore.UpsertOrder(store.OrderRecord{
		OrderID:       "ord-open-sell",
		MarketID:      "market-1",
		TokenID:       "token-2",
		Side:          model.SELL,
		Price:         0.6,
		RequestedSize: 4,
		RemainingSize: 4,
		Reserved:      0,
		Status:        core.ExecutionStatusPartiallyFilled,
		UpdatedAt:     time.Now().UnixNano(),
	})

	e := &Engine{
		Bus:        core.NewEventBus(),
		State:      state.NewState(0, state.WithInitialAvailable(100)),
		OrderStore: orderStore,
		StateStore: stateStore,
	}
	e.initOrderTracking()
	e.restoreFromStore()

	snap := e.State.Snapshot()
	if !almostEqual(snap.Balance.Available, 80) {
		t.Fatalf("unexpected restored available: %+v", snap.Balance)
	}
	if !almostEqual(snap.Balance.Reserved, 5) {
		t.Fatalf("reserved cash should be rebuilt from BUY open orders, got %+v", snap.Balance)
	}
	tp2 := snap.Position.Tokens["token-2"]
	if !almostEqual(tp2.Reserved, 4) {
		t.Fatalf("SELL open order reserved fallback should be restored, got %+v", tp2)
	}
	if !e.hasAccepted("ord-open-buy") || !e.hasAccepted("ord-open-sell") {
		t.Fatalf("expected open orders to be marked accepted")
	}
	if err := e.State.ApplyFill("ord-open-buy", "market-1", "token-1", model.BUY, 2); err != nil {
		t.Fatalf("expected restored BUY reservation usable, got err=%v", err)
	}
	if err := e.State.ApplyFill("ord-open-sell", "market-1", "token-2", model.SELL, 1); err != nil {
		t.Fatalf("expected restored SELL reservation usable, got err=%v", err)
	}
}

func TestUpsertOrderRecord_Lifecycle_Buy(t *testing.T) {
	orderStore := store.NewMemoryOrderStore()
	e := &Engine{Bus: core.NewEventBus(), OrderStore: orderStore}

	e.upsertOrderRecord(core.ExecutionEvent{
		OrderID:       "ord-1",
		MarketID:      "market-1",
		TokenID:       "token-1",
		Price:         0.4,
		Side:          model.BUY,
		RequestedSize: 10,
		Status:        core.ExecutionStatusAccepted,
		At:            time.Now(),
	})

	rec, ok, err := orderStore.GetOrder("ord-1")
	if err != nil || !ok {
		t.Fatalf("expected stored order, err=%v ok=%v", err, ok)
	}
	if !almostEqual(rec.RemainingSize, 10) || !almostEqual(rec.Reserved, 4) {
		t.Fatalf("unexpected accepted record: %+v", rec)
	}

	e.upsertOrderRecord(core.ExecutionEvent{
		OrderID:    "ord-1",
		FilledSize: 3,
		Status:     core.ExecutionStatusPartiallyFilled,
		At:         time.Now(),
	})
	rec, ok, _ = orderStore.GetOrder("ord-1")
	if !ok || !almostEqual(rec.RemainingSize, 7) || !almostEqual(rec.Reserved, 2.8) {
		t.Fatalf("unexpected partial record: %+v", rec)
	}

	e.upsertOrderRecord(core.ExecutionEvent{
		OrderID: "ord-1",
		Status:  core.ExecutionStatusFilled,
		At:      time.Now(),
	})
	rec, ok, _ = orderStore.GetOrder("ord-1")
	if !ok || !almostEqual(rec.RemainingSize, 0) || !almostEqual(rec.Reserved, 0) {
		t.Fatalf("unexpected final record: %+v", rec)
	}
}

func TestUpsertOrderRecord_Lifecycle_SellAndNegativeFill(t *testing.T) {
	orderStore := store.NewMemoryOrderStore()
	e := &Engine{Bus: core.NewEventBus(), OrderStore: orderStore}

	e.upsertOrderRecord(core.ExecutionEvent{
		OrderID:       "ord-sell-1",
		MarketID:      "market-1",
		TokenID:       "token-2",
		Price:         0.7,
		Side:          model.SELL,
		RequestedSize: 5,
		Status:        core.ExecutionStatusAccepted,
		At:            time.Now(),
	})
	rec, ok, _ := orderStore.GetOrder("ord-sell-1")
	if !ok || !almostEqual(rec.Reserved, 5) {
		t.Fatalf("sell accepted reserved should equal size, got %+v", rec)
	}

	e.upsertOrderRecord(core.ExecutionEvent{
		OrderID:    "ord-sell-1",
		Side:       model.SELL,
		FilledSize: -2,
		Status:     core.ExecutionStatusPartiallyFilled,
		At:         time.Now(),
	})
	rec, ok, _ = orderStore.GetOrder("ord-sell-1")
	if !ok || !almostEqual(rec.RemainingSize, 5) || !almostEqual(rec.Reserved, 5) {
		t.Fatalf("negative fill should be treated as 0, got %+v", rec)
	}

	e.upsertOrderRecord(core.ExecutionEvent{
		OrderID: "ord-sell-1",
		Status:  core.ExecutionStatusRejected,
		At:      time.Now(),
	})
	rec, ok, _ = orderStore.GetOrder("ord-sell-1")
	if !ok || !almostEqual(rec.RemainingSize, 0) || !almostEqual(rec.Reserved, 0) {
		t.Fatalf("unexpected rejected record: %+v", rec)
	}
}

func TestSaveStateSnapshot(t *testing.T) {
	stateStore := store.NewMemoryStateStore()
	s := state.NewState(0, state.WithInitialAvailable(100))
	if err := s.ReserveOrder("ord-save", "market-1", "token-1", model.BUY, 0.5, 10); err != nil {
		t.Fatalf("reserve failed: %v", err)
	}

	e := &Engine{Bus: core.NewEventBus(), State: s, StateStore: stateStore}
	now := time.Now()
	e.saveStateSnapshot(now)

	rec, ok, err := stateStore.LoadLatestSnapshot()
	if err != nil || !ok {
		t.Fatalf("expected saved snapshot, err=%v ok=%v", err, ok)
	}
	if !almostEqual(rec.Available, 95) || !almostEqual(rec.Reserved, 5) {
		t.Fatalf("unexpected snapshot record: %+v", rec)
	}
	if rec.At != now.UnixNano() {
		t.Fatalf("unexpected snapshot timestamp: %+v", rec)
	}
}

func TestHandleExecutionEvent_CancelledAndRejected(t *testing.T) {
	e := &Engine{Bus: core.NewEventBus(), State: state.NewState(0, state.WithInitialAvailable(100))}
	e.initOrderTracking()

	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID:       "ord-cancel",
		MarketID:      "market-1",
		TokenID:       "token-1",
		Price:         0.5,
		Side:          model.BUY,
		RequestedSize: 10,
		Status:        core.ExecutionStatusAccepted,
		At:            time.Now(),
	}, true)
	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID: "ord-cancel",
		Status:  core.ExecutionStatusCancelled,
		At:      time.Now(),
	}, true)

	snap := e.State.Snapshot()
	if !almostEqual(snap.Balance.Available, 100) || !almostEqual(snap.Balance.Reserved, 0) {
		t.Fatalf("cancel should release reserved cash, got %+v", snap.Balance)
	}
	if !e.isFinalized("ord-cancel") {
		t.Fatalf("cancelled order should be finalized")
	}

	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID:       "ord-reject",
		MarketID:      "market-1",
		TokenID:       "token-1",
		Price:         0.5,
		Side:          model.BUY,
		RequestedSize: 5,
		Status:        core.ExecutionStatusAccepted,
		At:            time.Now(),
	}, true)
	e.handleExecutionEvent(core.ExecutionEvent{
		OrderID: "ord-reject",
		Status:  core.ExecutionStatusRejected,
		Reason:  "reject-test",
		At:      time.Now(),
	}, true)
	if !e.isFinalized("ord-reject") {
		t.Fatalf("rejected order should be finalized")
	}
	if got := e.executionRejected.Load(); got == 0 {
		t.Fatalf("executionRejected should increase")
	}
}

func TestRestoreFromStore_ExecutionLogPath(t *testing.T) {
	execStore := store.NewMemoryExecutionStore()
	now := time.Now()
	_ = execStore.AppendExecution(core.ExecutionEvent{
		OrderID:       "ord-log-1",
		MarketID:      "market-1",
		TokenID:       "token-1",
		Price:         0.2,
		Side:          model.BUY,
		RequestedSize: 10,
		Status:        core.ExecutionStatusAccepted,
		At:            now,
	})
	_ = execStore.AppendExecution(core.ExecutionEvent{
		OrderID:    "ord-log-1",
		MarketID:   "market-1",
		TokenID:    "token-1",
		Price:      0.2,
		Side:       model.BUY,
		FilledSize: 3,
		Status:     core.ExecutionStatusPartiallyFilled,
		At:         now.Add(time.Millisecond),
	})

	e := &Engine{Bus: core.NewEventBus(), State: state.NewState(0, state.WithInitialAvailable(100)), ExecutionStore: execStore}
	e.initOrderTracking()
	e.restoreFromStore()

	snap := e.State.Snapshot()
	if !almostEqual(snap.Balance.Available, 98) || !almostEqual(snap.Balance.Reserved, 1.4) {
		t.Fatalf("unexpected replayed state: %+v", snap.Balance)
	}
	tp := snap.Position.Tokens["token-1"]
	if !almostEqual(tp.Available, 3) {
		t.Fatalf("unexpected token position after execution replay: %+v", tp)
	}
}

func TestPublishRiskAndMetrics(t *testing.T) {
	e := &Engine{Bus: core.NewEventBus(), State: state.NewState(0, state.WithInitialAvailable(100))}
	e.initOrderTracking()

	ch, cancel := e.Bus.SubscribeWithCancel()
	defer cancel()

	e.publishRisk("risk-test")
	e.publishMetrics()

	gotRisk := false
	gotMetrics := false
	deadline := time.After(1 * time.Second)
	for !(gotRisk && gotMetrics) {
		select {
		case ev := <-ch:
			switch ev.Type {
			case core.EventRisk:
				gotRisk = true
			case core.EventMetrics:
				gotMetrics = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting risk/metrics event, risk=%v metrics=%v", gotRisk, gotMetrics)
		}
	}
}

func TestCleanupTrackingAndPendingCount(t *testing.T) {
	e := &Engine{
		Bus:               core.NewEventBus(),
		State:             state.NewState(0, state.WithInitialAvailable(100)),
		PendingEventTTL:   1 * time.Second,
		FinalizedOrderTTL: 1 * time.Second,
	}
	e.initOrderTracking()

	e.bufferExecution(core.ExecutionEvent{OrderID: "ord-pending", Status: core.ExecutionStatusFilled, At: time.Now()})
	if e.pendingOrderCount() != 1 {
		t.Fatalf("expected pending count=1")
	}
	pending := e.pendingByOrder["ord-pending"]
	pending.firstSeen = time.Now().Add(-2 * time.Second)
	e.pendingByOrder["ord-pending"] = pending
	e.finalized["ord-final"] = struct{}{}
	e.finalizedAt["ord-final"] = time.Now().Add(-2 * time.Second)

	e.cleanupTracking(time.Now())
	if e.pendingOrderCount() != 0 {
		t.Fatalf("expected pending cleanup done")
	}
	if _, ok := e.finalized["ord-final"]; ok {
		t.Fatalf("expected finalized cleanup done")
	}
}

func TestRequiredReservedForOrder(t *testing.T) {
	if !almostEqual(requiredReservedForOrder(model.BUY, 0.4, 10), 4) {
		t.Fatalf("buy required reserve mismatch")
	}
	if !almostEqual(requiredReservedForOrder(model.SELL, 0.4, 10), 10) {
		t.Fatalf("sell required reserve mismatch")
	}
	if !almostEqual(requiredReservedForOrder(model.Side(99), 0.4, 10), 0) {
		t.Fatalf("invalid side required reserve should be 0")
	}
}
