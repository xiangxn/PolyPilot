package state

import (
	"errors"
	"testing"
	"time"

	"github.com/xiangxn/go-polymarket-sdk/orders"
	"github.com/xiangxn/polypilot/core"
)

func TestValidateOrderArgs_EmptyOrderID(t *testing.T) {
	err := validateOrderArgs("", "m1", "tk1", orders.BUY, 0.5, 10)
	if err == nil || err.Error() != "empty order id" {
		t.Fatalf("expected empty order id error, got %v", err)
	}
}

func TestValidateOrderArgs_EmptyMarket(t *testing.T) {
	err := validateOrderArgs("o1", "", "tk1", orders.BUY, 0.5, 10)
	if !errors.Is(err, core.ErrInvalidMarket) {
		t.Fatalf("expected ErrInvalidMarket, got %v", err)
	}
}

func TestValidateOrderArgs_EmptyToken(t *testing.T) {
	err := validateOrderArgs("o1", "m1", "", orders.BUY, 0.5, 10)
	if !errors.Is(err, core.ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

func TestValidateOrderArgs_InvalidSize(t *testing.T) {
	err := validateOrderArgs("o1", "m1", "tk1", orders.BUY, 0.5, 0)
	if !errors.Is(err, core.ErrInvalidSize) {
		t.Fatalf("expected ErrInvalidSize for zero, got %v", err)
	}
	err = validateOrderArgs("o1", "m1", "tk1", orders.BUY, 0.5, -1)
	if !errors.Is(err, core.ErrInvalidSize) {
		t.Fatalf("expected ErrInvalidSize for negative, got %v", err)
	}
}

func TestValidateOrderArgs_InvalidPrice(t *testing.T) {
	tests := []float64{0, -0.1, 1, 1.5}
	for _, p := range tests {
		err := validateOrderArgs("o1", "m1", "tk1", orders.BUY, p, 10)
		if !errors.Is(err, core.ErrInvalidPrice) {
			t.Fatalf("expected ErrInvalidPrice for price=%v, got %v", p, err)
		}
	}
}

func TestValidateOrderArgs_InvalidSide(t *testing.T) {
	err := validateOrderArgs("o1", "m1", "tk1", orders.Side("OOPS"), 0.5, 10)
	if !errors.Is(err, core.ErrInvalidSide) {
		t.Fatalf("expected ErrInvalidSide, got %v", err)
	}
}

func TestValidateOrderArgs_Valid(t *testing.T) {
	if err := validateOrderArgs("o1", "m1", "tk1", orders.BUY, 0.5, 10); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if err := validateOrderArgs("o1", "m1", "tk1", orders.SELL, 0.5, 10); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestAttachOrder_InvalidArgsReturnErr(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if err := s.AttachOrder("", "", "m1", "tk1", orders.BUY, 0.5, 10); err == nil {
		t.Fatal("expected error for empty order id")
	}
}

func TestAttachOrder_InsufficientBalanceBUY(t *testing.T) {
	s := newStateWithBalance(t, 1) // only $1 available
	err := s.AttachOrder("", "o1", "m1", "tk1", orders.BUY, 0.5, 10) // needs $5
	if !errors.Is(err, core.ErrInsufficientBalance) {
		t.Fatalf("expected ErrInsufficientBalance, got %v", err)
	}
}

func TestAttachOrder_InsufficientPositionSELL(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{
		Balance:  Balance{Available: 100},
		Position: Position{Tokens: map[string]TokenPosition{"tk1": {Available: 2}}},
	})
	err := s.AttachOrder("", "o1", "m1", "tk1", orders.SELL, 0.5, 10)
	if !errors.Is(err, core.ErrInsufficientPosition) {
		t.Fatalf("expected ErrInsufficientPosition, got %v", err)
	}
}

func TestAttachOrder_AlreadyReservedReleasesProvisional(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if err := s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.5, 10); err != nil {
		t.Fatal(err)
	}
	// add a provisional with intentID = "i1"
	if err := s.TryReserveProvisional("i1", "m1", "tk1", orders.BUY, 0.5, 5, time.Now(), 0); err != nil {
		t.Fatal(err)
	}
	// AttachOrder with intentID "i1" and same orderID o1 → ErrOrderAlreadyReserved
	// AND provisional released
	err := s.AttachOrder("i1", "o1", "m1", "tk1", orders.BUY, 0.5, 10)
	if !errors.Is(err, core.ErrOrderAlreadyReserved) {
		t.Fatalf("expected ErrOrderAlreadyReserved, got %v", err)
	}
	snap := s.Snapshot()
	// only the original reservation of 5 ($) should remain; provisional released.
	if snap.Balance.Reserved != 5 {
		t.Fatalf("expected reserved=5, got %v", snap.Balance.Reserved)
	}
}

func TestAttachExternalOrder_InvalidArgs(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if err := s.AttachExternalOrder("", "m1", "tk1", orders.BUY, 0.5, 10); err == nil {
		t.Fatal("expected error for empty order id")
	}
}

func TestAttachExternalOrder_InsufficientBalanceBUY(t *testing.T) {
	s := newStateWithBalance(t, 1)
	err := s.AttachExternalOrder("ext1", "m1", "tk1", orders.BUY, 0.5, 10)
	if !errors.Is(err, core.ErrInsufficientBalance) {
		t.Fatalf("expected ErrInsufficientBalance, got %v", err)
	}
}

func TestAttachExternalOrder_InsufficientPositionSELL(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{
		Balance:  Balance{Available: 100},
		Position: Position{Tokens: map[string]TokenPosition{"tk1": {Available: 2}}},
	})
	err := s.AttachExternalOrder("ext1", "m1", "tk1", orders.SELL, 0.5, 10)
	if !errors.Is(err, core.ErrInsufficientPosition) {
		t.Fatalf("expected ErrInsufficientPosition, got %v", err)
	}
}

func TestAttachExternalOrder_Idempotent(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if err := s.AttachExternalOrder("ext1", "m1", "tk1", orders.BUY, 0.5, 10); err != nil {
		t.Fatal(err)
	}
	// second attach: idempotent → returns nil
	if err := s.AttachExternalOrder("ext1", "m1", "tk1", orders.BUY, 0.5, 10); err != nil {
		t.Fatalf("expected nil on idempotent re-attach, got %v", err)
	}
	if got := s.Snapshot().Balance.Reserved; got != 5 {
		t.Fatalf("expected no double reserve, got %v", got)
	}
}

func TestAttachExternalOrder_SELL(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{
		Balance:  Balance{Available: 100},
		Position: Position{Tokens: map[string]TokenPosition{"tk1": {Available: 10}}},
	})
	if err := s.AttachExternalOrder("ext-sell", "m1", "tk1", orders.SELL, 0.5, 4); err != nil {
		t.Fatal(err)
	}
	tp := s.Snapshot().Position.Tokens["tk1"]
	if tp.Reserved != 4 || tp.Available != 6 {
		t.Fatalf("got %+v", tp)
	}
	r := s.Snapshot().Orders["ext-sell"]
	if !r.ExternalOrigin {
		t.Fatalf("expected ExternalOrigin=true")
	}
}
