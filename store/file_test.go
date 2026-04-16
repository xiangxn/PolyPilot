package store

import (
	"polypilot/core"
	"testing"
	"time"
)

func TestFileOrderStore_PersistAndReload(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/orders.json"

	s1, err := NewFileOrderStore(path)
	if err != nil {
		t.Fatalf("new file order store failed: %v", err)
	}

	if err := s1.UpsertOrder(OrderRecord{
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
		t.Fatalf("upsert failed: %v", err)
	}
	if err := s1.UpsertOrder(OrderRecord{
		OrderID:       "ord-2",
		Status:        core.ExecutionStatusFilled,
		UpdatedAt:     time.Now().UnixNano(),
		RequestedSize: 1,
	}); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}

	s2, err := NewFileOrderStore(path)
	if err != nil {
		t.Fatalf("reload file order store failed: %v", err)
	}
	open, err := s2.ListOpenOrders()
	if err != nil {
		t.Fatalf("list open failed: %v", err)
	}
	if len(open) != 1 || open[0].OrderID != "ord-1" {
		t.Fatalf("unexpected open orders: %+v", open)
	}
}

func TestFileExecutionStore_AppendAndListSince(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/executions.jsonl"

	s1, err := NewFileExecutionStore(path)
	if err != nil {
		t.Fatalf("new file execution store failed: %v", err)
	}
	now := time.Now()
	if err := s1.AppendExecution(core.ExecutionEvent{OrderID: "ord-1", Status: core.ExecutionStatusAccepted, At: now.Add(-time.Second)}); err != nil {
		t.Fatalf("append failed: %v", err)
	}
	if err := s1.AppendExecution(core.ExecutionEvent{OrderID: "ord-1", Status: core.ExecutionStatusFilled, At: now}); err != nil {
		t.Fatalf("append failed: %v", err)
	}

	s2, err := NewFileExecutionStore(path)
	if err != nil {
		t.Fatalf("reload file execution store failed: %v", err)
	}
	recent, err := s2.ListExecutionsSince(now.UnixNano())
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(recent) != 1 || recent[0].Status != core.ExecutionStatusFilled {
		t.Fatalf("unexpected executions: %+v", recent)
	}
}

func TestFileStateStore_SaveAndReload(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/state.json"

	s1, err := NewFileStateStore(path)
	if err != nil {
		t.Fatalf("new file state store failed: %v", err)
	}
	if err := s1.SaveSnapshot(SnapshotRecord{Available: 90, Reserved: 10, Buy: 2, Sell: 1, At: time.Now().UnixNano()}); err != nil {
		t.Fatalf("save snapshot failed: %v", err)
	}

	s2, err := NewFileStateStore(path)
	if err != nil {
		t.Fatalf("reload file state store failed: %v", err)
	}
	rec, ok, err := s2.LoadLatestSnapshot()
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
