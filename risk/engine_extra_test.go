package risk

import (
	"errors"
	"testing"
	"time"

	"github.com/xiangxn/polypilot/runtime"
	"github.com/xiangxn/polypilot/state"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

func mustReject(t *testing.T, err error, want RejectionType) {
	t.Helper()
	var rej *Rejection
	if !errors.As(err, &rej) {
		t.Fatalf("expected *Rejection, got %v", err)
	}
	if rej.Type != want {
		t.Fatalf("got %s want %s", rej.Type, want)
	}
}

func TestReject_Slippage(t *testing.T) {
	r := &Engine{MaxSlippageBps: 100}
	mids := map[string]float64{"tk1": 0.5}
	err := r.Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk1", Price: 0.6, Size: 1, Side: orders.BUY,
	}}, state.Snapshot{Balance: state.Balance{Available: 100}}, mids)
	mustReject(t, err, RejectSlippage)
}

func TestReject_ExposureCap(t *testing.T) {
	r := &Engine{MaxExposurePerMarket: 10}
	snap := state.Snapshot{Balance: state.Balance{Available: 100}}
	err := r.Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk1", Price: 0.5, Size: 30, Side: orders.BUY,
	}}, snap, nil)
	mustReject(t, err, RejectExposureCap)
}

func TestReject_MaxOpenOrders(t *testing.T) {
	r := &Engine{MaxOpenOrders: 1}
	snap := state.Snapshot{
		Balance:        state.Balance{Available: 100},
		OpenOrderCount: 1,
	}
	err := r.Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk1", Price: 0.5, Size: 5, Side: orders.BUY,
	}}, snap, nil)
	mustReject(t, err, RejectMaxOpenOrders)
}

func TestReject_DailyLoss(t *testing.T) {
	r := &Engine{MaxDailyLoss: 5}
	snap := state.Snapshot{
		Balance:  state.Balance{Available: 100},
		DailyPnL: -10,
	}
	err := r.Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk1", Price: 0.5, Size: 1, Side: orders.BUY,
	}}, snap, nil)
	mustReject(t, err, RejectDailyLoss)
}

func TestReject_Cooldown(t *testing.T) {
	r := &Engine{MarketCooldown: 100 * time.Millisecond}
	snap := state.Snapshot{Balance: state.Balance{Available: 100}}
	in := []runtime.OrderIntent{{MarketID: "m", TokenID: "tk1", Price: 0.5, Size: 1, Side: orders.BUY}}
	if err := r.Check(in, snap, nil); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := r.Check(in, snap, nil); err == nil {
		t.Fatalf("second within cooldown should reject")
	} else {
		mustReject(t, err, RejectCooldown)
	}
}

func TestCancel_StillAllowedDuringDailyLoss(t *testing.T) {
	r := &Engine{MaxDailyLoss: 5}
	snap := state.Snapshot{Balance: state.Balance{Available: 100}, DailyPnL: -10}
	err := r.Check([]runtime.OrderIntent{{
		Action: runtime.OrderIntentActionCancel, OrderID: "o1",
	}}, snap, nil)
	if err != nil {
		t.Fatalf("cancel should be allowed during daily-loss lock, got %v", err)
	}
}
