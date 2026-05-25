package state

import (
	"strings"
	"testing"
	"time"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

func TestTryReserveProvisional_EmptyIntentID(t *testing.T) {
	s := newStateWithBalance(t, 100)
	err := s.TryReserveProvisional("", "m1", "tk1", orders.BUY, 0.5, 10, time.Now(), time.Second)
	if err == nil || !strings.Contains(err.Error(), "empty intent id") {
		t.Fatalf("expected empty intent id error, got %v", err)
	}
}

func TestTryReserveProvisional_EmptyMarketID(t *testing.T) {
	s := newStateWithBalance(t, 100)
	err := s.TryReserveProvisional("i1", "", "tk1", orders.BUY, 0.5, 10, time.Now(), time.Second)
	if err == nil || !strings.Contains(err.Error(), "empty market id") {
		t.Fatalf("expected empty market id error, got %v", err)
	}
}

func TestTryReserveProvisional_EmptyTokenID(t *testing.T) {
	s := newStateWithBalance(t, 100)
	err := s.TryReserveProvisional("i1", "m1", "", orders.BUY, 0.5, 10, time.Now(), time.Second)
	if err == nil || !strings.Contains(err.Error(), "empty token id") {
		t.Fatalf("expected empty token id error, got %v", err)
	}
}

func TestTryReserveProvisional_InvalidSize(t *testing.T) {
	s := newStateWithBalance(t, 100)
	err := s.TryReserveProvisional("i1", "m1", "tk1", orders.BUY, 0.5, 0, time.Now(), time.Second)
	if err == nil || !strings.Contains(err.Error(), "invalid requested size") {
		t.Fatalf("expected invalid requested size error, got %v", err)
	}
}

func TestTryReserveProvisional_InvalidPrice(t *testing.T) {
	s := newStateWithBalance(t, 100)
	for _, p := range []float64{0, -1, 1, 1.5} {
		err := s.TryReserveProvisional("i1", "m1", "tk1", orders.BUY, p, 10, time.Now(), time.Second)
		if err == nil || !strings.Contains(err.Error(), "invalid price") {
			t.Fatalf("expected invalid price for %v, got %v", p, err)
		}
	}
}

func TestTryReserveProvisional_InvalidSide(t *testing.T) {
	s := newStateWithBalance(t, 100)
	err := s.TryReserveProvisional("i1", "m1", "tk1", orders.Side("OOPS"), 0.5, 10, time.Now(), time.Second)
	if err == nil || !strings.Contains(err.Error(), "invalid side") {
		t.Fatalf("expected invalid side, got %v", err)
	}
}

func TestTryReserveProvisional_ZeroNowDefaults(t *testing.T) {
	s := newStateWithBalance(t, 100)
	// passing zero time → falls back to time.Now()
	if err := s.TryReserveProvisional("i1", "m1", "tk1", orders.BUY, 0.5, 10, time.Time{}, time.Second); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestTryReserveProvisional_ZeroTTLDefaults(t *testing.T) {
	s := newStateWithBalance(t, 100)
	// passing zero ttl → defaults to 5 seconds
	if err := s.TryReserveProvisional("i1", "m1", "tk1", orders.BUY, 0.5, 10, time.Now(), 0); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestTryReserveProvisional_AlreadyReserved(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if err := s.TryReserveProvisional("i1", "m1", "tk1", orders.BUY, 0.5, 10, time.Now(), time.Second); err != nil {
		t.Fatal(err)
	}
	err := s.TryReserveProvisional("i1", "m1", "tk1", orders.BUY, 0.5, 10, time.Now(), time.Second)
	if err == nil || !strings.Contains(err.Error(), "already reserved") {
		t.Fatalf("expected already reserved, got %v", err)
	}
}

func TestTryReserveProvisional_InsufficientBuyBalance(t *testing.T) {
	s := newStateWithBalance(t, 1)
	err := s.TryReserveProvisional("i1", "m1", "tk1", orders.BUY, 0.5, 10, time.Now(), time.Second)
	if err == nil || !strings.Contains(err.Error(), "insufficient available balance") {
		t.Fatalf("expected insufficient balance, got %v", err)
	}
}

func TestConfirmProvisional_EmptyIntentID(t *testing.T) {
	s := newStateWithBalance(t, 100)
	_, err := s.ConfirmProvisional("", "o1")
	if err == nil {
		t.Fatal("expected error for empty intent id")
	}
}

func TestConfirmProvisional_EmptyOrderID(t *testing.T) {
	s := newStateWithBalance(t, 100)
	_, err := s.ConfirmProvisional("i1", "")
	if err == nil {
		t.Fatal("expected error for empty order id")
	}
}

func TestConfirmProvisional_NoProvisionalAndNoOrder(t *testing.T) {
	s := newStateWithBalance(t, 100)
	ok, err := s.ConfirmProvisional("missing", "missing-o")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok {
		t.Fatal("expected false when neither provisional nor order exists")
	}
}

func TestConfirmProvisional_NoProvisionalButOrderExistsTrue(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if err := s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.5, 10); err != nil {
		t.Fatal(err)
	}
	ok, err := s.ConfirmProvisional("missing-intent", "o1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !ok {
		t.Fatal("expected true when order exists (idempotent re-ack)")
	}
}

func TestReleaseProvisional_SELL_RestoresPosition(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{
		Balance:  Balance{Available: 100},
		Position: Position{Tokens: map[string]TokenPosition{"tk1": {Available: 10}}},
	})
	if err := s.TryReserveProvisional("i1", "m1", "tk1", orders.SELL, 0.4, 6, time.Now(), time.Second); err != nil {
		t.Fatal(err)
	}
	if !s.ReleaseProvisional("i1") {
		t.Fatal("expected ReleaseProvisional to return true")
	}
	tp := s.Snapshot().Position.Tokens["tk1"]
	if tp.Reserved != 0 {
		t.Fatalf("expected reserved=0 after release, got %v", tp.Reserved)
	}
	if tp.Available != 10 {
		t.Fatalf("expected available=10 restored, got %v", tp.Available)
	}
}

func TestCleanupExpiredProvisional_SELL_RestoresPosition(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{
		Balance:  Balance{Available: 100},
		Position: Position{Tokens: map[string]TokenPosition{"tk1": {Available: 10}}},
	})
	now := time.Now()
	if err := s.TryReserveProvisional("i-expire", "m1", "tk1", orders.SELL, 0.4, 5, now, time.Second); err != nil {
		t.Fatal(err)
	}
	expired := s.CleanupExpiredProvisional(now.Add(2 * time.Second))
	if len(expired) != 1 {
		t.Fatalf("expected 1 expired, got %+v", expired)
	}
	tp := s.Snapshot().Position.Tokens["tk1"]
	if tp.Available != 10 || tp.Reserved != 0 {
		t.Fatalf("expected position restored after cleanup, got %+v", tp)
	}
}

func TestCleanupExpiredProvisional_ZeroTime(t *testing.T) {
	// zero-time → uses time.Now() internally
	s := newStateWithBalance(t, 100)
	if err := s.TryReserveProvisional("i1", "m1", "tk1", orders.BUY, 0.5, 10, time.Now().Add(-time.Hour), -time.Hour); err != nil {
		t.Fatal(err)
	}
	expired := s.CleanupExpiredProvisional(time.Time{})
	if len(expired) != 1 {
		t.Fatalf("expected 1 expired, got %+v", expired)
	}
}

func TestCleanupExpiredProvisional_SkipsUnexpired(t *testing.T) {
	s := newStateWithBalance(t, 100)
	now := time.Now()
	if err := s.TryReserveProvisional("i-still-fresh", "m1", "tk1", orders.BUY, 0.5, 10, now, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	expired := s.CleanupExpiredProvisional(now)
	if len(expired) != 0 {
		t.Fatalf("expected 0 expired, got %+v", expired)
	}
}

func TestConfirmProvisional_AlreadyExistingOrderReleasesProvisional(t *testing.T) {
	// provisional exists + same order exists → confirm releases provisional via releaseReservedLocked
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{
		Balance:  Balance{Available: 100},
		Position: Position{Tokens: map[string]TokenPosition{"tk1": {Available: 10}}},
	})
	if err := s.TryReserveProvisional("i1", "m1", "tk1", orders.SELL, 0.4, 5, time.Now(), time.Second); err != nil {
		t.Fatal(err)
	}
	// independently create the order via reserve
	if err := s.AttachExternalOrder("o-sell", "m1", "tk1", orders.SELL, 0.4, 4); err != nil {
		t.Fatal(err)
	}
	// confirm: should release the provisional via releaseReservedLocked SELL path
	ok, err := s.ConfirmProvisional("i1", "o-sell")
	if err != nil || !ok {
		t.Fatalf("confirm ok=%v err=%v", ok, err)
	}
	tp := s.Snapshot().Position.Tokens["tk1"]
	// after release of provisional 5 + still reserved 4 from external → reserved=4
	if tp.Reserved != 4 {
		t.Fatalf("expected reserved=4 after release of provisional, got %v", tp.Reserved)
	}
}
