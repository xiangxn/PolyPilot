package store

import (
	"os"
	"polypilot/core"
	"testing"
	"time"

	"github.com/polymarket/go-order-utils/pkg/model"
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
		Side:          model.BUY,
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
	recent, err := executionStore.ListExecutionsSince(now.UnixNano())
	if err != nil {
		t.Fatalf("list executions failed: %v", err)
	}
	if len(recent) != 0 {
		t.Fatalf("expected empty executions initially, got %+v", recent)
	}

	if err := executionStore.AppendExecution(core.ExecutionEvent{OrderID: "ord-1", Status: core.ExecutionStatusAccepted, At: now.Add(-time.Second)}); err != nil {
		t.Fatalf("append execution failed: %v", err)
	}
	if err := executionStore.AppendExecution(core.ExecutionEvent{OrderID: "ord-1", Status: core.ExecutionStatusFilled, At: now}); err != nil {
		t.Fatalf("append execution failed: %v", err)
	}
	recent, err = executionStore.ListExecutionsSince(now.UnixNano())
	if err != nil {
		t.Fatalf("list executions failed: %v", err)
	}
	if len(recent) != 1 || recent[0].Status != core.ExecutionStatusFilled {
		t.Fatalf("unexpected executions: %+v", recent)
	}

	if err := stateStore.SaveSnapshot(SnapshotRecord{
		Available:  90,
		Reserved:   10,
		MinBalance: 12,
		Tokens: map[string]TokenPositionRecord{
			"token-1": {Available: 2, Reserved: 1},
		},
		At: now.UnixNano(),
	}); err != nil {
		t.Fatalf("save snapshot failed: %v", err)
	}
	rec, ok, err := stateStore.LoadLatestSnapshot()
	if err != nil {
		t.Fatalf("load snapshot failed: %v", err)
	}
	if !ok {
		t.Fatalf("expected snapshot exists")
	}
	if rec.Available != 90 || rec.Reserved != 10 || rec.MinBalance != 12 {
		t.Fatalf("unexpected snapshot basics: %+v", rec)
	}
	tp, exists := rec.Tokens["token-1"]
	if !exists || tp.Available != 2 || tp.Reserved != 1 {
		t.Fatalf("unexpected snapshot tokens: %+v", rec.Tokens)
	}
}

func TestSQLiteStores_LoadEmptySnapshotAndMissingOrder(t *testing.T) {
	dbPath := t.TempDir() + "/polymarket.db"
	orderStore, _, stateStore, err := NewSQLiteStores(dbPath)
	if err != nil {
		t.Fatalf("new sqlite stores failed: %v", err)
	}

	_, ok, err := stateStore.LoadLatestSnapshot()
	if err != nil {
		t.Fatalf("load latest snapshot failed: %v", err)
	}
	if ok {
		t.Fatalf("expected no snapshot yet")
	}

	_, ok, err = orderStore.GetOrder("missing")
	if err != nil {
		t.Fatalf("get order failed: %v", err)
	}
	if ok {
		t.Fatalf("expected missing order")
	}
}

func TestSQLiteStores_ListOpenOrders_FiltersFinalStatuses(t *testing.T) {
	dbPath := t.TempDir() + "/polymarket.db"
	orderStore, _, _, err := NewSQLiteStores(dbPath)
	if err != nil {
		t.Fatalf("new sqlite stores failed: %v", err)
	}
	statuses := []core.ExecutionStatus{
		core.ExecutionStatusAccepted,
		core.ExecutionStatusFilled,
		core.ExecutionStatusCancelled,
		core.ExecutionStatusRejected,
	}
	for i, st := range statuses {
		if err := orderStore.UpsertOrder(OrderRecord{
			OrderID:       "ord-" + time.Now().Add(time.Duration(i)*time.Nanosecond).Format("150405.000000000"),
			MarketID:      "m",
			TokenID:       "t",
			Side:          model.BUY,
			Price:         0.5,
			RequestedSize: 1,
			RemainingSize: 1,
			Reserved:      0.5,
			Status:        st,
			UpdatedAt:     time.Now().UnixNano(),
		}); err != nil {
			t.Fatalf("upsert failed: %v", err)
		}
	}
	open, err := orderStore.ListOpenOrders()
	if err != nil {
		t.Fatalf("list open orders failed: %v", err)
	}
	if len(open) != 1 || open[0].Status != core.ExecutionStatusAccepted {
		t.Fatalf("unexpected open orders: %+v", open)
	}
}

func TestNewSQLiteStores_InvalidPath(t *testing.T) {
	base := t.TempDir() + "/not-a-dir"
	if err := os.WriteFile(base, []byte("x"), 0o644); err != nil {
		t.Fatalf("write temp file failed: %v", err)
	}
	_, _, _, err := NewSQLiteStores(base + "/db.sqlite")
	if err == nil {
		t.Fatalf("expected NewSQLiteStores to fail on invalid parent path")
	}
}
