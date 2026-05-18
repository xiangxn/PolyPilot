package state

import (
	"testing"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

func TestSnapshot_OpenOrderCount(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if got := s.Snapshot().OpenOrderCount; got != 0 {
		t.Fatalf("expected 0 got %d", got)
	}
	if err := s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.5, 10); err != nil {
		t.Fatalf("o1: %v", err)
	}
	if err := s.ReserveOrder("o2", "m1", "tk2", orders.BUY, 0.5, 5); err != nil {
		t.Fatalf("o2: %v", err)
	}
	if got := s.Snapshot().OpenOrderCount; got != 2 {
		t.Fatalf("expected 2 got %d", got)
	}
}

func TestSnapshot_OpenOrderCount_IncludesExternal(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if err := s.AttachExternalOrder("ext1", "m1", "tk1", orders.BUY, 0.5, 5); err != nil {
		t.Fatalf("ext: %v", err)
	}
	if got := s.Snapshot().OpenOrderCount; got != 1 {
		t.Fatalf("external must count, got %d", got)
	}
}
