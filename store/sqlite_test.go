package store

import (
	"polypilot/core"
	"testing"
	"time"
)

func TestSQLiteStores_BasicRoundTrip(t *testing.T) {
	dbPath := t.TempDir() + "/polymarket.db"
	orderStore, executionStore, stateStore, err := NewSQLiteStores(dbPath)
	if err != nil {
		t.Fatalf("new sqlite stores failed: %v", err)
	}

	if err := orderStore.UpsertOrder(OrderRecord{
		OrderID:       "ord-1",
		MarketID:      "market-1",
		TokenID:       "token-1",
		Side:          core.SideBuy,
		Price:         0.5,
		RequestedSize: 10,
		RemainingSize: 10,
		Reserved:      5,
		Status:        core.ExecutionStatusAccepted,
		UpdatedAt:     time.Now().UnixNano(),
	}); err != nil {
		t.Fatalf("upsert order failed: %v", err)
	}
	orders, err := orderStore.ListOpenOrders()
	if err != nil {
		t.Fatalf("list open orders failed: %v", err)
	}
	if len(orders) != 1 || orders[0].OrderID != "ord-1" {
		t.Fatalf("unexpected open orders: %+v", orders)
	}

	now := time.Now()
	if err := executionStore.AppendExecution(core.ExecutionEvent{OrderID: "ord-1", Status: core.ExecutionStatusAccepted, At: now.Add(-time.Second)}); err != nil {
		t.Fatalf("append execution failed: %v", err)
	}
	if err := executionStore.AppendExecution(core.ExecutionEvent{OrderID: "ord-1", Status: core.ExecutionStatusFilled, At: now}); err != nil {
		t.Fatalf("append execution failed: %v", err)
	}
	recent, err := executionStore.ListExecutionsSince(now.UnixNano())
	if err != nil {
		t.Fatalf("list executions failed: %v", err)
	}
	if len(recent) != 1 || recent[0].Status != core.ExecutionStatusFilled {
		t.Fatalf("unexpected executions: %+v", recent)
	}

	if err := stateStore.SaveSnapshot(SnapshotRecord{Available: 90, Reserved: 10, Buy: 2, Sell: 1, At: now.UnixNano()}); err != nil {
		t.Fatalf("save snapshot failed: %v", err)
	}
	rec, ok, err := stateStore.LoadLatestSnapshot()
	if err != nil {
		t.Fatalf("load snapshot failed: %v", err)
	}
	if !ok {
		t.Fatalf("expected snapshot exists")
	}
	if rec.Available != 90 || rec.Reserved != 10 || rec.Buy != 2 || rec.Sell != 1 {
		t.Fatalf("unexpected snapshot: %+v", rec)
	}
}
