package state

import (
	"strings"
	"testing"
	"time"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

func TestTryReserveProvisionalSellPreventsOverSell(t *testing.T) {
	s := NewState(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{
		Position: Position{Tokens: map[string]TokenPosition{"tk1": {Available: 5}}},
		Balance:  Balance{Available: 100},
	})

	now := time.Now()
	if err := s.TryReserveProvisional("i1", "m1", "tk1", orders.SELL, 0.4, 5, now, 5*time.Second); err != nil {
		t.Fatalf("first provisional reserve failed: %v", err)
	}

	snap := s.Snapshot()
	if got := snap.Position.Tokens["tk1"].Available; got != 0 {
		t.Fatalf("expected tk1 available=0 after first reserve, got=%v", got)
	}
	if got := snap.Position.Tokens["tk1"].Reserved; got != 5 {
		t.Fatalf("expected tk1 reserved=5 after first reserve, got=%v", got)
	}

	err := s.TryReserveProvisional("i2", "m1", "tk1", orders.SELL, 0.4, 1, now, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "insufficient token position") {
		t.Fatalf("expected insufficient token position error, got=%v", err)
	}
}

func TestConfirmProvisionalDoesNotDoubleReserve(t *testing.T) {
	s := NewState(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{Balance: Balance{Available: 100}})

	now := time.Now()
	if err := s.TryReserveProvisional("i1", "m1", "tk1", orders.BUY, 0.5, 10, now, 5*time.Second); err != nil {
		t.Fatalf("provisional reserve failed: %v", err)
	}

	snap := s.Snapshot()
	if snap.Balance.Available != 95 || snap.Balance.Reserved != 5 {
		t.Fatalf("unexpected balance after provisional reserve: available=%v reserved=%v", snap.Balance.Available, snap.Balance.Reserved)
	}

	ok, err := s.ConfirmProvisional("i1", "o1")
	if err != nil || !ok {
		t.Fatalf("confirm provisional failed ok=%v err=%v", ok, err)
	}

	snap = s.Snapshot()
	if snap.Balance.Available != 95 || snap.Balance.Reserved != 5 {
		t.Fatalf("confirm should not double reserve: available=%v reserved=%v", snap.Balance.Available, snap.Balance.Reserved)
	}
	if _, exists := snap.Orders["o1"]; !exists {
		t.Fatalf("expected confirmed order reservation exists")
	}

	ok, err = s.ConfirmProvisional("i1", "o1")
	if err != nil || !ok {
		t.Fatalf("confirm should be idempotent for existing order, ok=%v err=%v", ok, err)
	}
}

func TestConfirmProvisionalWhenOrderAlreadyReservedReleasesProvisional(t *testing.T) {
	s := NewState(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{Balance: Balance{Available: 100}})

	now := time.Now()
	if err := s.TryReserveProvisional("i1", "m1", "tk1", orders.BUY, 0.5, 10, now, 5*time.Second); err != nil {
		t.Fatalf("provisional reserve failed: %v", err)
	}
	if err := s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.5, 10); err != nil {
		t.Fatalf("reserve order failed: %v", err)
	}

	snap := s.Snapshot()
	if snap.Balance.Available != 90 || snap.Balance.Reserved != 10 {
		t.Fatalf("unexpected balance before confirm: available=%v reserved=%v", snap.Balance.Available, snap.Balance.Reserved)
	}

	ok, err := s.ConfirmProvisional("i1", "o1")
	if err != nil || !ok {
		t.Fatalf("confirm provisional failed ok=%v err=%v", ok, err)
	}

	snap = s.Snapshot()
	if snap.Balance.Available != 95 || snap.Balance.Reserved != 5 {
		t.Fatalf("expected provisional reserve released after confirm, got available=%v reserved=%v", snap.Balance.Available, snap.Balance.Reserved)
	}
}

func TestCleanupExpiredProvisionalReleasesReserve(t *testing.T) {
	s := NewState(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{Balance: Balance{Available: 100}})

	now := time.Now()
	if err := s.TryReserveProvisional("i-expire", "m1", "tk1", orders.BUY, 0.4, 10, now, time.Second); err != nil {
		t.Fatalf("provisional reserve failed: %v", err)
	}

	snap := s.Snapshot()
	if snap.Balance.Available != 96 || snap.Balance.Reserved != 4 {
		t.Fatalf("unexpected balance after provisional reserve: available=%v reserved=%v", snap.Balance.Available, snap.Balance.Reserved)
	}

	expired := s.CleanupExpiredProvisional(now.Add(2 * time.Second))
	if len(expired) != 1 || expired[0] != "i-expire" {
		t.Fatalf("unexpected expired ids: %+v", expired)
	}

	snap = s.Snapshot()
	if snap.Balance.Available != 100 || snap.Balance.Reserved != 0 {
		t.Fatalf("expected balance restored after cleanup, got available=%v reserved=%v", snap.Balance.Available, snap.Balance.Reserved)
	}
}
