package store

import (
	"encoding/json"
	"os"
	"polypilot/core"
	"testing"
	"time"

	"github.com/polymarket/go-order-utils/pkg/model"
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
		Side:          model.BUY,
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

func TestFileOrderStore_LoadSkipsEmptyOrderID(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/orders.json"
	data, err := json.Marshal([]OrderRecord{
		{OrderID: "", Status: core.ExecutionStatusAccepted},
		{OrderID: "ord-ok", Status: core.ExecutionStatusAccepted},
	})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write fixture failed: %v", err)
	}

	s, err := NewFileOrderStore(path)
	if err != nil {
		t.Fatalf("new file order store failed: %v", err)
	}
	if _, ok, _ := s.GetOrder("ord-ok"); !ok {
		t.Fatalf("expected ord-ok to be loaded")
	}
	open, err := s.ListOpenOrders()
	if err != nil {
		t.Fatalf("list open failed: %v", err)
	}
	if len(open) != 1 || open[0].OrderID != "ord-ok" {
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

func TestFileExecutionStore_ListSinceMissingFile(t *testing.T) {
	path := t.TempDir() + "/missing-executions.jsonl"
	s := &FileExecutionStore{path: path}
	out, err := s.ListExecutionsSince(time.Now().UnixNano())
	if err != nil {
		t.Fatalf("list should tolerate missing file: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty executions for missing file, got %+v", out)
	}
}

func TestFileStateStore_SaveAndReload(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/state.json"

	s1, err := NewFileStateStore(path)
	if err != nil {
		t.Fatalf("new file state store failed: %v", err)
	}
	now := time.Now().UnixNano()
	if err := s1.SaveSnapshot(SnapshotRecord{
		Available: 90,
		Reserved:  10,
		Tokens: map[string]TokenPositionRecord{
			"token-1": {Available: 2, Reserved: 1},
		},
		At: now,
	}); err != nil {
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
	if rec.Available != 90 || rec.Reserved != 10 || rec.At != now {
		t.Fatalf("unexpected snapshot basics: %+v", rec)
	}
	tp, exists := rec.Tokens["token-1"]
	if !exists || tp.Available != 2 || tp.Reserved != 1 {
		t.Fatalf("unexpected snapshot tokens: %+v", rec.Tokens)
	}
}

func TestFileStateStore_LoadNoData(t *testing.T) {
	s, err := NewFileStateStore(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatalf("new file state store failed: %v", err)
	}
	_, ok, err := s.LoadLatestSnapshot()
	if err != nil {
		t.Fatalf("load snapshot failed: %v", err)
	}
	if ok {
		t.Fatalf("expected no snapshot yet")
	}
}

func TestMemoryStores_Basic(t *testing.T) {
	orders := NewMemoryOrderStore()
	if err := orders.UpsertOrder(OrderRecord{OrderID: "ord-open", Side: model.BUY, Status: core.ExecutionStatusAccepted}); err != nil {
		t.Fatalf("upsert open failed: %v", err)
	}
	if err := orders.UpsertOrder(OrderRecord{OrderID: "ord-final", Status: core.ExecutionStatusFilled}); err != nil {
		t.Fatalf("upsert final failed: %v", err)
	}
	if _, ok, err := orders.GetOrder("ord-open"); err != nil || !ok {
		t.Fatalf("get order failed, ok=%v err=%v", ok, err)
	}
	open, err := orders.ListOpenOrders()
	if err != nil {
		t.Fatalf("list open failed: %v", err)
	}
	if len(open) != 1 || open[0].OrderID != "ord-open" {
		t.Fatalf("unexpected open orders: %+v", open)
	}

	execs := NewMemoryExecutionStore()
	now := time.Now()
	if err := execs.AppendExecution(core.ExecutionEvent{OrderID: "ord-open", Status: core.ExecutionStatusAccepted, At: now.Add(-time.Second)}); err != nil {
		t.Fatalf("append old execution failed: %v", err)
	}
	if err := execs.AppendExecution(core.ExecutionEvent{OrderID: "ord-open", Status: core.ExecutionStatusFilled, At: now}); err != nil {
		t.Fatalf("append new execution failed: %v", err)
	}
	recent, err := execs.ListExecutionsSince(now.UnixNano())
	if err != nil {
		t.Fatalf("list executions failed: %v", err)
	}
	if len(recent) != 1 || recent[0].Status != core.ExecutionStatusFilled {
		t.Fatalf("unexpected recent executions: %+v", recent)
	}

	states := NewMemoryStateStore()
	if err := states.SaveSnapshot(SnapshotRecord{Available: 10, Reserved: 2, Tokens: map[string]TokenPositionRecord{"t": {Available: 1, Reserved: 1}}}); err != nil {
		t.Fatalf("save snapshot failed: %v", err)
	}
	rec, ok, err := states.LoadLatestSnapshot()
	if err != nil || !ok {
		t.Fatalf("load snapshot failed, ok=%v err=%v", ok, err)
	}
	if rec.Available != 10 || rec.Reserved != 2 || rec.Tokens["t"].Available != 1 {
		t.Fatalf("unexpected state snapshot: %+v", rec)
	}
}
