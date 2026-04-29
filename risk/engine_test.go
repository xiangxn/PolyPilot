package risk

import (
	"strings"
	"testing"

	"polypilot/runtime"
	"polypilot/state"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

func TestCheck_BuyRejectedWhenAtMinReserve(t *testing.T) {
	r := &Engine{}
	err := r.Check([]runtime.OrderIntent{{
		MarketID: "m1",
		TokenID:  "t1",
		Price:    0.5,
		Size:     1,
		Side:     orders.BUY,
	}}, state.Snapshot{Balance: state.Balance{Available: 10, MinBalance: 10}})
	if err == nil || !strings.Contains(err.Error(), "reached minimum reserve") {
		t.Fatalf("expected min reserve rejection, got err=%v", err)
	}
}

func TestCheck_BuyRejectedWhenPostOrderBelowMinReserve(t *testing.T) {
	r := &Engine{}
	err := r.Check([]runtime.OrderIntent{{
		MarketID: "m1",
		TokenID:  "t1",
		Price:    0.5,
		Size:     10,
		Side:     orders.BUY,
	}}, state.Snapshot{Balance: state.Balance{Available: 14, MinBalance: 10}})
	if err == nil || !strings.Contains(err.Error(), "below minimum reserve") {
		t.Fatalf("expected post-order min reserve rejection, got err=%v", err)
	}
}

func TestCheck_BuyPassesWhenAboveMinReserve(t *testing.T) {
	r := &Engine{}
	err := r.Check([]runtime.OrderIntent{{
		MarketID: "m1",
		TokenID:  "t1",
		Price:    0.5,
		Size:     6,
		Side:     orders.BUY,
	}}, state.Snapshot{Balance: state.Balance{Available: 20, MinBalance: 10}})
	if err != nil {
		t.Fatalf("expected buy check pass, got err=%v", err)
	}
}

func TestCheck_SellRejectedOnInsufficientToken(t *testing.T) {
	r := &Engine{}
	err := r.Check([]runtime.OrderIntent{{
		MarketID: "m1",
		TokenID:  "t1",
		Price:    0.3,
		Size:     2,
		Side:     orders.SELL,
	}}, state.Snapshot{Position: state.Position{Tokens: map[string]state.TokenPosition{"t1": {Available: 1}}}})
	if err == nil || !strings.Contains(err.Error(), "insufficient token position") {
		t.Fatalf("expected insufficient token rejection, got err=%v", err)
	}
}
