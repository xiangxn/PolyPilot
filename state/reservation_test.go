package state

import (
	"errors"
	"testing"
	"time"

	"github.com/xiangxn/polypilot/core"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

func newStateWithBalance(t *testing.T, available float64) *State {
	t.Helper()
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{Balance: Balance{Available: available}})
	return s
}

func TestAttachOrder_ConfirmsProvisional(t *testing.T) {
	s := newStateWithBalance(t, 100)
	now := time.Now()
	if err := s.TryReserveProvisional("i1", "m1", "tk1", orders.BUY, 0.5, 10, now, 5*time.Second); err != nil {
		t.Fatalf("provisional: %v", err)
	}
	if err := s.AttachOrder("i1", "o1", "m1", "tk1", orders.BUY, 0.5, 10); err != nil {
		t.Fatalf("attach: %v", err)
	}
	snap := s.Snapshot()
	if snap.Balance.Available != 95 || snap.Balance.Reserved != 5 {
		t.Fatalf("balance: %+v", snap.Balance)
	}
	if _, ok := snap.Orders["o1"]; !ok {
		t.Fatalf("expected o1 reservation")
	}
}

func TestAttachOrder_NoProvisional_CreatesFresh(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if err := s.AttachOrder("", "o1", "m1", "tk1", orders.BUY, 0.5, 10); err != nil {
		t.Fatalf("attach: %v", err)
	}
	snap := s.Snapshot()
	if snap.Balance.Reserved != 5 {
		t.Fatalf("expected reserved=5, got %v", snap.Balance.Reserved)
	}
	if snap.Orders["o1"].ExternalOrigin {
		t.Fatalf("AttachOrder should not set ExternalOrigin")
	}
}

func TestAttachOrder_Idempotent(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if err := s.AttachOrder("", "o1", "m1", "tk1", orders.BUY, 0.5, 10); err != nil {
		t.Fatalf("first: %v", err)
	}
	err := s.AttachOrder("", "o1", "m1", "tk1", orders.BUY, 0.5, 10)
	if err != nil && !errors.Is(err, core.ErrOrderAlreadyReserved) {
		t.Fatalf("expected ErrOrderAlreadyReserved or nil, got %v", err)
	}
	snap := s.Snapshot()
	if snap.Balance.Reserved != 5 {
		t.Fatalf("idempotent attach must not double-reserve, got %v", snap.Balance.Reserved)
	}
}

func TestAttachExternalOrder_SetsExternalOrigin(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if err := s.AttachExternalOrder("ext1", "m1", "tk1", orders.BUY, 0.5, 10); err != nil {
		t.Fatalf("external: %v", err)
	}
	snap := s.Snapshot()
	r := snap.Orders["ext1"]
	if !r.ExternalOrigin {
		t.Fatalf("expected ExternalOrigin=true")
	}
	if snap.Balance.Reserved != 5 {
		t.Fatalf("expected reserved=5, got %v", snap.Balance.Reserved)
	}
}
