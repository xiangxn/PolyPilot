package risk

import (
	"errors"
	"testing"
	"time"

	"github.com/xiangxn/polypilot/runtime"
	"github.com/xiangxn/polypilot/state"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

// ------------------------------------------------------------
// Trivial / early-exit cases
// ------------------------------------------------------------

func TestCheck_EmptyIntents(t *testing.T) {
	if err := (&Engine{}).Check(nil, state.Snapshot{}, nil); err != nil {
		t.Fatalf("nil intents should pass, got %v", err)
	}
	if err := (&Engine{}).Check([]runtime.OrderIntent{}, state.Snapshot{}, nil); err != nil {
		t.Fatalf("empty intents should pass, got %v", err)
	}
}

func TestCheck_HappyPathBasic(t *testing.T) {
	r := &Engine{
		MaxDailyLoss:         100,
		MaxExposurePerMarket: 1000,
		MaxSlippageBps:       500,
		MaxOpenOrders:        10,
	}
	snap := state.Snapshot{Balance: state.Balance{Available: 100}}
	err := r.Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk1", Price: 0.5, Size: 1, Side: orders.BUY,
	}}, snap, map[string]float64{"tk1": 0.5})
	if err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

// ------------------------------------------------------------
// Invalid-intent rejections
// ------------------------------------------------------------

func TestCheck_EmptyCancelOrderID(t *testing.T) {
	err := (&Engine{}).Check([]runtime.OrderIntent{{
		Action: runtime.OrderIntentActionCancel, OrderID: "",
	}}, state.Snapshot{}, nil)
	mustReject(t, err, RejectInvalidIntent)
}

func TestCheck_EmptyMarketID(t *testing.T) {
	err := (&Engine{}).Check([]runtime.OrderIntent{{
		MarketID: "", TokenID: "tk", Price: 0.5, Size: 1, Side: orders.BUY,
	}}, state.Snapshot{Balance: state.Balance{Available: 100}}, nil)
	mustReject(t, err, RejectInvalidIntent)
}

func TestCheck_EmptyTokenID(t *testing.T) {
	err := (&Engine{}).Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "", Price: 0.5, Size: 1, Side: orders.BUY,
	}}, state.Snapshot{Balance: state.Balance{Available: 100}}, nil)
	mustReject(t, err, RejectInvalidIntent)
}

func TestCheck_ZeroSize(t *testing.T) {
	err := (&Engine{}).Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk", Price: 0.5, Size: 0, Side: orders.BUY,
	}}, state.Snapshot{Balance: state.Balance{Available: 100}}, nil)
	mustReject(t, err, RejectInvalidIntent)
}

func TestCheck_NegativeSize(t *testing.T) {
	err := (&Engine{}).Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk", Price: 0.5, Size: -1, Side: orders.BUY,
	}}, state.Snapshot{Balance: state.Balance{Available: 100}}, nil)
	mustReject(t, err, RejectInvalidIntent)
}

func TestCheck_InvalidPriceLow(t *testing.T) {
	err := (&Engine{}).Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk", Price: 0, Size: 1, Side: orders.BUY,
	}}, state.Snapshot{Balance: state.Balance{Available: 100}}, nil)
	mustReject(t, err, RejectInvalidIntent)
}

func TestCheck_InvalidPriceHigh(t *testing.T) {
	err := (&Engine{}).Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk", Price: 1.0, Size: 1, Side: orders.BUY,
	}}, state.Snapshot{Balance: state.Balance{Available: 100}}, nil)
	mustReject(t, err, RejectInvalidIntent)
}

func TestCheck_InvalidPriceOverOne(t *testing.T) {
	err := (&Engine{}).Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk", Price: 1.5, Size: 1, Side: orders.BUY,
	}}, state.Snapshot{Balance: state.Balance{Available: 100}}, nil)
	mustReject(t, err, RejectInvalidIntent)
}

func TestCheck_InvalidSide(t *testing.T) {
	err := (&Engine{}).Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk", Price: 0.5, Size: 1, Side: "WEIRD",
	}}, state.Snapshot{Balance: state.Balance{Available: 100}}, nil)
	mustReject(t, err, RejectInvalidIntent)
}

func TestCheck_UnsupportedAction(t *testing.T) {
	err := (&Engine{}).Check([]runtime.OrderIntent{{
		Action: "WEIRD", MarketID: "m", TokenID: "tk", Price: 0.5, Size: 1, Side: orders.BUY,
	}}, state.Snapshot{Balance: state.Balance{Available: 100}}, nil)
	mustReject(t, err, RejectInvalidIntent)
}

// ------------------------------------------------------------
// Split / Merge actions
// ------------------------------------------------------------

func TestCheck_Split_HappyPath(t *testing.T) {
	snap := state.Snapshot{Balance: state.Balance{Available: 100}}
	err := (&Engine{}).Check([]runtime.OrderIntent{{
		Action:   runtime.OrderIntentActionSplit,
		MarketID: "m",
		Tokens:   []string{"a", "b"},
		Size:     5,
	}}, snap, nil)
	if err != nil {
		t.Fatalf("split happy: %v", err)
	}
}

func TestCheck_Split_WrongTokensCount(t *testing.T) {
	cases := []struct {
		name   string
		tokens []string
	}{
		{"zero tokens", nil},
		{"one token", []string{"a"}},
		{"three tokens", []string{"a", "b", "c"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := (&Engine{}).Check([]runtime.OrderIntent{{
				Action:   runtime.OrderIntentActionSplit,
				MarketID: "m",
				Tokens:   c.tokens,
				Size:     5,
			}}, state.Snapshot{Balance: state.Balance{Available: 100}}, nil)
			mustReject(t, err, RejectInvalidIntent)
		})
	}
}

func TestCheck_Split_BalanceTooLow(t *testing.T) {
	// Split adds to buyRequired
	snap := state.Snapshot{Balance: state.Balance{Available: 5, MinBalance: 1}}
	err := (&Engine{}).Check([]runtime.OrderIntent{{
		Action:   runtime.OrderIntentActionSplit,
		MarketID: "m",
		Tokens:   []string{"a", "b"},
		Size:     100, // way more than available
	}}, snap, nil)
	mustReject(t, err, RejectInsufficientBalance)
}

func TestCheck_Merge_HappyPath(t *testing.T) {
	snap := state.Snapshot{
		Balance: state.Balance{Available: 100},
		Position: state.Position{Tokens: map[string]state.TokenPosition{
			"a": {Available: 10},
			"b": {Available: 10},
		}},
	}
	err := (&Engine{}).Check([]runtime.OrderIntent{{
		Action:   runtime.OrderIntentActionMerge,
		MarketID: "m",
		Tokens:   []string{"a", "b"},
		Size:     3,
	}}, snap, nil)
	if err != nil {
		t.Fatalf("merge happy: %v", err)
	}
}

func TestCheck_Merge_WrongTokensCount(t *testing.T) {
	err := (&Engine{}).Check([]runtime.OrderIntent{{
		Action:   runtime.OrderIntentActionMerge,
		MarketID: "m",
		Tokens:   []string{"a"},
		Size:     3,
	}}, state.Snapshot{Balance: state.Balance{Available: 100}}, nil)
	mustReject(t, err, RejectInvalidIntent)
}

func TestCheck_Merge_InsufficientToken(t *testing.T) {
	// Merge subtracts both tokens
	snap := state.Snapshot{
		Balance: state.Balance{Available: 100},
		Position: state.Position{Tokens: map[string]state.TokenPosition{
			"a": {Available: 1},
			"b": {Available: 5},
		}},
	}
	err := (&Engine{}).Check([]runtime.OrderIntent{{
		Action:   runtime.OrderIntentActionMerge,
		MarketID: "m",
		Tokens:   []string{"a", "b"},
		Size:     3,
	}}, snap, nil)
	mustReject(t, err, RejectInsufficientPosition)
}

// ------------------------------------------------------------
// Cancel rules
// ------------------------------------------------------------

func TestCheck_Cancel_HappyPath(t *testing.T) {
	err := (&Engine{}).Check([]runtime.OrderIntent{{
		Action: runtime.OrderIntentActionCancel, OrderID: "o1",
	}}, state.Snapshot{}, nil)
	if err != nil {
		t.Fatalf("cancel happy: %v", err)
	}
}

func TestCheck_Cancel_BypassesCooldown(t *testing.T) {
	r := &Engine{MarketCooldown: 1 * time.Hour}
	snap := state.Snapshot{Balance: state.Balance{Available: 100}}

	// First place to set cooldown
	if err := r.Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk", Price: 0.5, Size: 1, Side: orders.BUY,
	}}, snap, nil); err != nil {
		t.Fatalf("first place: %v", err)
	}

	// Cancel should bypass cooldown
	err := r.Check([]runtime.OrderIntent{{
		Action: runtime.OrderIntentActionCancel, OrderID: "o-x",
	}}, snap, nil)
	if err != nil {
		t.Fatalf("cancel during cooldown should pass, got %v", err)
	}
}

// ------------------------------------------------------------
// Cooldown — uncovered branches
// ------------------------------------------------------------

func TestCheck_Cooldown_NoBlockOnDifferentMarket(t *testing.T) {
	r := &Engine{MarketCooldown: 100 * time.Millisecond}
	snap := state.Snapshot{Balance: state.Balance{Available: 100}}
	// Place on market m1 to set cooldown
	if err := r.Check([]runtime.OrderIntent{{
		MarketID: "m1", TokenID: "tk", Price: 0.5, Size: 1, Side: orders.BUY,
	}}, snap, nil); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Different market should pass
	if err := r.Check([]runtime.OrderIntent{{
		MarketID: "m2", TokenID: "tk", Price: 0.5, Size: 1, Side: orders.BUY,
	}}, snap, nil); err != nil {
		t.Fatalf("different market should pass: %v", err)
	}
}

func TestCheck_Cooldown_AfterExpired(t *testing.T) {
	r := &Engine{MarketCooldown: 50 * time.Millisecond}
	snap := state.Snapshot{Balance: state.Balance{Available: 100}}
	in := []runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk", Price: 0.5, Size: 1, Side: orders.BUY,
	}}
	if err := r.Check(in, snap, nil); err != nil {
		t.Fatalf("first: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	// After cooldown expires, should pass
	if err := r.Check(in, snap, nil); err != nil {
		t.Fatalf("after cooldown: %v", err)
	}
}

// TestCheck_Cooldown_SkipsCancelInBatch verifies the cooldown loop's
// `if o.Action != "" && o.Action != PLACE { continue }` branch — when a batch
// contains a CANCEL alongside a PLACE, cooldown is checked only for PLACE.
func TestCheck_Cooldown_SkipsCancelInBatch(t *testing.T) {
	r := &Engine{MarketCooldown: 1 * time.Hour}
	snap := state.Snapshot{Balance: state.Balance{Available: 100}}

	// First place to set cooldown on market m
	if err := r.Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk", Price: 0.5, Size: 1, Side: orders.BUY,
	}}, snap, nil); err != nil {
		t.Fatalf("first place: %v", err)
	}

	// Batch with CANCEL on m + PLACE on different market m2 should fail
	// because we're checking that the cancel skips cooldown loop iteration
	// (placeCount > 0 because of m2 place; loop iterates all intents).
	err := r.Check([]runtime.OrderIntent{
		{Action: runtime.OrderIntentActionCancel, OrderID: "o1"},
		{MarketID: "m2", TokenID: "tk", Price: 0.5, Size: 1, Side: orders.BUY},
	}, snap, nil)
	// Should pass: cancel is skipped, m2 has no cooldown set
	if err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

// ------------------------------------------------------------
// Slippage edge cases — covering negative deviation branch
// ------------------------------------------------------------

func TestCheck_Slippage_NegativeDeviation(t *testing.T) {
	r := &Engine{MaxSlippageBps: 100}
	mids := map[string]float64{"tk1": 0.5}
	err := r.Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk1", Price: 0.4, Size: 1, Side: orders.BUY,
	}}, state.Snapshot{Balance: state.Balance{Available: 100}}, mids)
	// deviation = (0.4 - 0.5)/0.5 * 10000 = -2000, abs = 2000 > 100 → reject
	mustReject(t, err, RejectSlippage)
}

func TestCheck_Slippage_NoMidPriceSkipped(t *testing.T) {
	r := &Engine{MaxSlippageBps: 1}
	// midPrices missing tk1 → slippage check skipped
	err := r.Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk1", Price: 0.4, Size: 1, Side: orders.BUY,
	}}, state.Snapshot{Balance: state.Balance{Available: 100}}, map[string]float64{})
	if err != nil {
		t.Fatalf("expected pass when mid missing, got %v", err)
	}
}

func TestCheck_Slippage_ZeroMidPriceSkipped(t *testing.T) {
	r := &Engine{MaxSlippageBps: 1}
	// midPrice == 0 → slippage check skipped (avoid div by zero)
	err := r.Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk1", Price: 0.4, Size: 1, Side: orders.BUY,
	}}, state.Snapshot{Balance: state.Balance{Available: 100}}, map[string]float64{"tk1": 0})
	if err != nil {
		t.Fatalf("expected pass when mid is 0, got %v", err)
	}
}

// ------------------------------------------------------------
// Insufficient balance
// ------------------------------------------------------------

func TestCheck_InsufficientBalance(t *testing.T) {
	r := &Engine{}
	// Want 5 USDC for 10 size at price 0.5 = 5, but only 4.5 available, MinBalance is 0
	err := r.Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk", Price: 0.5, Size: 10, Side: orders.BUY,
	}}, state.Snapshot{Balance: state.Balance{Available: 4.5}}, nil)
	mustReject(t, err, RejectInsufficientBalance)
}

// ------------------------------------------------------------
// MaxExposurePerMarket — existing orders included in cap
// ------------------------------------------------------------

func TestCheck_ExposureCap_WithExistingOrders(t *testing.T) {
	r := &Engine{MaxExposurePerMarket: 10}
	snap := state.Snapshot{
		Balance: state.Balance{Available: 1000},
		Orders: map[string]state.OrderReservation{
			"existing-o": {MarketID: "m", Reserved: 6},
		},
	}
	// Existing 6 + new 5 = 11 > 10 → reject
	err := r.Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk", Price: 0.5, Size: 10, Side: orders.BUY,
	}}, snap, nil)
	mustReject(t, err, RejectExposureCap)
}

func TestCheck_ExposureCap_PassesWithRoom(t *testing.T) {
	r := &Engine{MaxExposurePerMarket: 100}
	snap := state.Snapshot{
		Balance: state.Balance{Available: 1000},
		Orders: map[string]state.OrderReservation{
			"existing-o": {MarketID: "m", Reserved: 6},
		},
	}
	// Existing 6 + new 5 = 11 < 100 → pass
	err := r.Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk", Price: 0.5, Size: 10, Side: orders.BUY,
	}}, snap, nil)
	if err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

// ------------------------------------------------------------
// Rejection.Error / reject formatting
// ------------------------------------------------------------

func TestRejection_Error(t *testing.T) {
	r := &Rejection{Type: RejectSlippage, Detail: "extra"}
	got := r.Error()
	if got != "SLIPPAGE: extra" {
		t.Fatalf("got %q", got)
	}
}

func TestReject_FormatsArgs(t *testing.T) {
	r := reject(RejectExposureCap, "market %s value %.2f cap %.2f", "m", 1.5, 1.0)
	if r.Type != RejectExposureCap {
		t.Fatalf("type=%s", r.Type)
	}
	if r.Detail != "market m value 1.50 cap 1.00" {
		t.Fatalf("detail=%q", r.Detail)
	}
}

// Sanity: errors.As works with *Rejection
func TestRejection_ErrorsAs(t *testing.T) {
	var rej *Rejection
	err := reject(RejectDailyLoss, "foo")
	if !errors.As(err, &rej) {
		t.Fatal("expected errors.As to match")
	}
	if rej.Type != RejectDailyLoss {
		t.Fatalf("type=%s", rej.Type)
	}
}
