package strategy

import (
	"context"
	"testing"

	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/market"
	"github.com/xiangxn/polypilot/runtime"
	"github.com/xiangxn/polypilot/state"

	"github.com/spf13/viper"
	"github.com/tidwall/gjson"
	"github.com/xiangxn/go-polymarket-sdk/orders"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

func newStrategy() *Strategy {
	s := &Strategy{}
	s.Init(core.NewEventBus(), context.Background(), viper.New())
	return s
}

func TestOnUpdate_EventMarket_BelowTimeLeft_NoOp(t *testing.T) {
	s := newStrategy()
	obj := gjson.Parse(`{"conditionId":"m1","clobTokenIds":"[\"tk1\",\"tk2\"]","endDate":"2099-01-01T00:00:00Z"}`)
	obs := runtime.Observation{
		MarketID:    "m1",
		TimeLeftSec: 30, // below default TimeLeftSec (240)
		Tokens: map[string]runtime.Token{
			"tk1": {Id: "tk1", AskPrice: 0.6, BidPrice: 0.5},
			"tk2": {Id: "tk2", AskPrice: 0.6, BidPrice: 0.5},
		},
	}
	if got := s.OnUpdate(core.Event{Type: core.EventMarket, Data: obj}, obs, state.Snapshot{}); len(got) != 0 {
		t.Fatalf("expected no intents below time-left, got %d", len(got))
	}
}

func TestOnUpdate_EventMarket_LowAskPrice_NoOp(t *testing.T) {
	s := newStrategy()
	obj := gjson.Parse(`{"conditionId":"m1","clobTokenIds":"[\"tk1\",\"tk2\"]","endDate":"2099-01-01T00:00:00Z"}`)
	obs := runtime.Observation{
		MarketID:    "m1",
		TimeLeftSec: 300,
		Tokens: map[string]runtime.Token{
			"tk1": {Id: "tk1", AskPrice: 0.1, BidPrice: 0.05}, // below MinInPrice
			"tk2": {Id: "tk2", AskPrice: 0.6, BidPrice: 0.5},
		},
	}
	if got := s.OnUpdate(core.Event{Type: core.EventMarket, Data: obj}, obs, state.Snapshot{}); len(got) != 0 {
		t.Fatalf("expected no intents below MinInPrice, got %d", len(got))
	}
}

func TestOnUpdate_EventMarket_HappyEmitsTwoBuyIntents(t *testing.T) {
	s := newStrategy()
	obj := gjson.Parse(`{"conditionId":"m1","clobTokenIds":"[\"tk1\",\"tk2\"]","endDate":"2099-01-01T00:00:00Z"}`)
	obs := runtime.Observation{
		MarketID:    "m1",
		TimeLeftSec: 300,
		Tokens: map[string]runtime.Token{
			"tk1": {Id: "tk1", AskPrice: 0.6, BidPrice: 0.5},
			"tk2": {Id: "tk2", AskPrice: 0.6, BidPrice: 0.5},
		},
	}
	got := s.OnUpdate(core.Event{Type: core.EventMarket, Data: obj}, obs, state.Snapshot{})
	if len(got) != 2 {
		t.Fatalf("expected 2 intents, got %d", len(got))
	}
	for _, in := range got {
		if in.Side != orders.BUY {
			t.Fatalf("expected BUY, got %v", in.Side)
		}
	}
}

func TestOnUpdate_EventMarket_BadObjType_NoOp(t *testing.T) {
	s := newStrategy()
	if got := s.OnUpdate(core.Event{Type: core.EventMarket, Data: 42}, runtime.Observation{}, state.Snapshot{}); len(got) != 0 {
		t.Fatal("expected no intents on wrong data type")
	}
}

func TestOnUpdate_EventOrderBook_MarketNotKnown_NoOp(t *testing.T) {
	s := newStrategy()
	obs := runtime.Observation{MarketID: "missing-market"}
	if got := s.OnUpdate(core.Event{Type: core.EventOrderBook}, obs, state.Snapshot{}); len(got) != 0 {
		t.Fatal("expected no intents when market unknown")
	}
}

func TestOnUpdate_EventOrderBook_OpenPriceZero_NoOp(t *testing.T) {
	s := newStrategy()
	s.markets.Add("m1", market.SlugMarket{MarketID: "m1"})
	obs := runtime.Observation{
		MarketID: "m1",
		Features: map[string]any{"openPrice": float64(0)},
	}
	if got := s.OnUpdate(core.Event{Type: core.EventOrderBook}, obs, state.Snapshot{}); len(got) != 0 {
		t.Fatal("expected no intents when openPrice is 0")
	}
}

func TestOnExecution_NoMarketKnown_NoOp(t *testing.T) {
	s := newStrategy()
	out := s.OnExecution(core.ExecutionEvent{
		MarketID: "missing",
		TokenID:  "tk1",
		Status:   core.ExecutionStatusRejected,
		Reason:   core.ExecutionReasonTradeFailed,
	}, runtime.Observation{}, state.Snapshot{})
	if len(out) != 0 {
		t.Fatalf("expected no intents, got %d", len(out))
	}
}

func TestOnExecution_NotTradeFailed_NoOp(t *testing.T) {
	s := newStrategy()
	s.markets.Add("m1", market.SlugMarket{MarketID: "m1", TokenIDs: []string{"tk1", "tk2"}})
	out := s.OnExecution(core.ExecutionEvent{
		MarketID: "m1",
		TokenID:  "tk1",
		Status:   core.ExecutionStatusAccepted,
		Reason:   "",
	}, runtime.Observation{}, state.Snapshot{})
	if len(out) != 0 {
		t.Fatalf("expected no intents (non-failure), got %d", len(out))
	}
}

func TestOnExecution_TooFewTokens_NoOp(t *testing.T) {
	s := newStrategy()
	s.markets.Add("m1", market.SlugMarket{MarketID: "m1", TokenIDs: []string{"tk1"}})
	out := s.OnExecution(core.ExecutionEvent{
		MarketID: "m1",
		TokenID:  "tk1",
		Status:   core.ExecutionStatusRejected,
		Reason:   core.ExecutionReasonTradeFailed,
	}, runtime.Observation{}, state.Snapshot{})
	if len(out) != 0 {
		t.Fatalf("expected no intents (only 1 token), got %d", len(out))
	}
}

func TestOnExecution_TradeFailed_CancelsAndSells(t *testing.T) {
	s := newStrategy()
	s.markets.Add("m1", market.SlugMarket{MarketID: "m1", TokenIDs: []string{"tk1", "tk2"}})
	snap := state.Snapshot{
		Orders: map[string]state.OrderReservation{
			"o-tk2": {OrderID: "o-tk2", TokenID: "tk2"},
		},
		Position: state.Position{Tokens: map[string]state.TokenPosition{
			"tk2": {Available: 3},
		}},
	}
	out := s.OnExecution(core.ExecutionEvent{
		MarketID: "m1",
		TokenID:  "tk1",
		Status:   core.ExecutionStatusRejected,
		Reason:   core.ExecutionReasonTradeFailed,
	}, runtime.Observation{}, snap)
	if len(out) < 2 {
		t.Fatalf("expected cancel + sell intents, got %d", len(out))
	}

	// Check we got at least one cancel and one sell
	hasCancel := false
	hasSell := false
	for _, in := range out {
		if in.Action == runtime.OrderIntentActionCancel {
			hasCancel = true
		}
		if in.Side == orders.SELL {
			hasSell = true
		}
	}
	if !hasCancel || !hasSell {
		t.Fatalf("expected cancel+sell, got %+v", out)
	}
}

func TestOnExecution_TradeFailed_CancelOpposite_NoPosition(t *testing.T) {
	s := newStrategy()
	s.markets.Add("m1", market.SlugMarket{MarketID: "m1", TokenIDs: []string{"tk1", "tk2"}})
	snap := state.Snapshot{
		Orders: map[string]state.OrderReservation{
			"o-tk1": {OrderID: "o-tk1", TokenID: "tk1"},
		},
	}
	// failed on tk2 → cancel orders for tk1
	out := s.OnExecution(core.ExecutionEvent{
		MarketID: "m1",
		TokenID:  "tk2",
		Status:   core.ExecutionStatusRejected,
		Reason:   core.ExecutionReasonTradeFailed,
	}, runtime.Observation{}, snap)
	if len(out) != 1 {
		t.Fatalf("expected one cancel intent, got %d", len(out))
	}
	if out[0].Action != runtime.OrderIntentActionCancel {
		t.Fatalf("expected cancel action, got %+v", out[0])
	}
}

func TestMarketQueue_Delete(t *testing.T) {
	q := NewMarketQueue(3)
	q.Add("m1", market.SlugMarket{MarketID: "m1"})
	q.Add("m2", market.SlugMarket{MarketID: "m2"})
	q.Delete("m1")
	if _, ok := q.Get("m1"); ok {
		t.Fatal("delete failed")
	}
	if _, ok := q.Get("m2"); !ok {
		t.Fatal("m2 should still exist")
	}
	q.Delete("nope") // no-op, must not panic
}

func TestDefaultStrategyConfig(t *testing.T) {
	c := DefaultStrategyConfig()
	if c.InPrice <= 0 || c.InSize <= 0 {
		t.Fatalf("expected sensible defaults, got %+v", c)
	}
	if c.TimeLeftSec <= 0 {
		t.Fatalf("expected positive TimeLeftSec, got %v", c.TimeLeftSec)
	}
	if c.MinZ <= 0 {
		t.Fatalf("expected positive MinZ, got %v", c.MinZ)
	}
	if c.ZAgo <= 0 {
		t.Fatalf("expected positive ZAgo, got %v", c.ZAgo)
	}
}

func TestOnUpdate_OrderBook_TriggersStopLossUpTrend(t *testing.T) {
	s := newStrategy()
	s.markets.Add("m1", market.SlugMarket{MarketID: "m1"})
	zw := []float64{3.0, 3.0, 3.0, 3.0, 3.0}
	obs := runtime.Observation{
		MarketID:    "m1",
		TimeLeftSec: 30, // > 5, allows stop-loss branch
		TokenIds:    []string{"tk-up", "tk-down"},
		Tokens: map[string]runtime.Token{
			"tk-up":   {Id: "tk-up", AskPrice: 0.6, BidPrice: 0.5},
			"tk-down": {Id: "tk-down", AskPrice: 0.4, BidPrice: 0.3},
		},
		Features: map[string]any{
			"openPrice":   float64(100),
			"latestPrice": float64(110), // up move
			"latestZ":     float64(3.0),
			"zWindows":    zw,
		},
		GetOrderBook: func(tID string) *sdk.OrderBook {
			return &sdk.OrderBook{
				Bids: []orders.Book{{Price: 0.3, Size: 10}},
				Asks: []orders.Book{{Price: 0.4, Size: 10}},
			}
		},
	}
	snap := state.Snapshot{
		Orders: map[string]state.OrderReservation{
			"o-up": {OrderID: "o-up", TokenID: "tk-up"},
		},
		Position: state.Position{Tokens: map[string]state.TokenPosition{
			"tk-down": {Available: 5}, // down position → trigger stop-loss on up move
		}},
	}
	out := s.OnUpdate(core.Event{Type: core.EventOrderBook}, obs, snap)
	if len(out) == 0 {
		t.Fatal("expected stop-loss intents")
	}
	hasSell := false
	hasCancel := false
	for _, in := range out {
		if in.Side == orders.SELL && in.TokenID == "tk-down" {
			hasSell = true
		}
		if in.Action == runtime.OrderIntentActionCancel {
			hasCancel = true
		}
	}
	if !hasSell {
		t.Fatalf("expected SELL on tk-down, got %+v", out)
	}
	if !hasCancel {
		t.Fatalf("expected CANCEL intent, got %+v", out)
	}
}

func TestOnUpdate_OrderBook_TriggersStopLossDownTrend(t *testing.T) {
	s := newStrategy()
	s.markets.Add("m1", market.SlugMarket{MarketID: "m1"})
	zw := []float64{-3.0, -3.0, -3.0, -3.0, -3.0}
	obs := runtime.Observation{
		MarketID:    "m1",
		TimeLeftSec: 30,
		TokenIds:    []string{"tk-up", "tk-down"},
		Tokens: map[string]runtime.Token{
			"tk-up":   {Id: "tk-up", AskPrice: 0.4, BidPrice: 0.3},
			"tk-down": {Id: "tk-down", AskPrice: 0.6, BidPrice: 0.5},
		},
		Features: map[string]any{
			"openPrice":   float64(100),
			"latestPrice": float64(90), // down move
			"latestZ":     float64(-3.0),
			"zWindows":    zw,
		},
		GetOrderBook: func(tID string) *sdk.OrderBook {
			return &sdk.OrderBook{
				Bids: []orders.Book{{Price: 0.3, Size: 10}},
				Asks: []orders.Book{{Price: 0.4, Size: 10}},
			}
		},
	}
	snap := state.Snapshot{
		Orders: map[string]state.OrderReservation{
			"o-down": {OrderID: "o-down", TokenID: "tk-down"},
		},
		Position: state.Position{Tokens: map[string]state.TokenPosition{
			"tk-up": {Available: 5}, // up position → trigger stop-loss on down move
		}},
	}
	out := s.OnUpdate(core.Event{Type: core.EventOrderBook}, obs, snap)
	if len(out) == 0 {
		t.Fatal("expected stop-loss intents on down trend")
	}
	hasSell := false
	for _, in := range out {
		if in.Side == orders.SELL && in.TokenID == "tk-up" {
			hasSell = true
		}
	}
	if !hasSell {
		t.Fatalf("expected SELL on tk-up, got %+v", out)
	}
}

func TestOnUpdate_OrderBook_NilOrderBook_NoOp(t *testing.T) {
	s := newStrategy()
	s.markets.Add("m1", market.SlugMarket{MarketID: "m1"})
	zw := []float64{3.0, 3.0, 3.0, 3.0, 3.0}
	obs := runtime.Observation{
		MarketID:    "m1",
		TimeLeftSec: 30,
		TokenIds:    []string{"tk-up", "tk-down"},
		Tokens: map[string]runtime.Token{
			"tk-up":   {Id: "tk-up", AskPrice: 0.6, BidPrice: 0.5},
			"tk-down": {Id: "tk-down", AskPrice: 0.4, BidPrice: 0.3},
		},
		Features: map[string]any{
			"openPrice":   float64(100),
			"latestPrice": float64(110),
			"latestZ":     float64(3.0),
			"zWindows":    zw,
		},
		GetOrderBook: func(tID string) *sdk.OrderBook {
			return nil // simulate stale/missing book
		},
	}
	snap := state.Snapshot{
		Position: state.Position{Tokens: map[string]state.TokenPosition{
			"tk-down": {Available: 5},
		}},
	}
	out := s.OnUpdate(core.Event{Type: core.EventOrderBook}, obs, snap)
	if len(out) != 0 {
		t.Fatalf("expected no intents when orderbook is nil, got %+v", out)
	}
}

func TestOnUpdate_OrderBook_LowZ_NoOp(t *testing.T) {
	s := newStrategy()
	s.markets.Add("m1", market.SlugMarket{MarketID: "m1"})
	zw := []float64{1.0, 1.0, 1.0, 1.0, 1.0} // below MinZ
	obs := runtime.Observation{
		MarketID:    "m1",
		TimeLeftSec: 30,
		TokenIds:    []string{"tk-up", "tk-down"},
		Tokens: map[string]runtime.Token{
			"tk-up":   {Id: "tk-up", AskPrice: 0.6, BidPrice: 0.5},
			"tk-down": {Id: "tk-down", AskPrice: 0.4, BidPrice: 0.3},
		},
		Features: map[string]any{
			"openPrice":   float64(100),
			"latestPrice": float64(110),
			"latestZ":     float64(1.0), // < MinZ (2.3)
			"zWindows":    zw,
		},
	}
	snap := state.Snapshot{
		Position: state.Position{Tokens: map[string]state.TokenPosition{
			"tk-down": {Available: 5},
		}},
	}
	out := s.OnUpdate(core.Event{Type: core.EventOrderBook}, obs, snap)
	if len(out) != 0 {
		t.Fatalf("expected no intents when z below threshold, got %+v", out)
	}
}

func TestOnUpdate_OrderBook_TooLateTimeLeft_NoOp(t *testing.T) {
	s := newStrategy()
	s.markets.Add("m1", market.SlugMarket{MarketID: "m1"})
	zw := []float64{3.0, 3.0, 3.0, 3.0, 3.0}
	obs := runtime.Observation{
		MarketID:    "m1",
		TimeLeftSec: 3, // < 5, blocks stop-loss
		TokenIds:    []string{"tk-up", "tk-down"},
		Tokens: map[string]runtime.Token{
			"tk-up":   {Id: "tk-up"},
			"tk-down": {Id: "tk-down"},
		},
		Features: map[string]any{
			"openPrice":   float64(100),
			"latestPrice": float64(110),
			"latestZ":     float64(3.0),
			"zWindows":    zw,
		},
	}
	snap := state.Snapshot{
		Position: state.Position{Tokens: map[string]state.TokenPosition{
			"tk-down": {Available: 5},
		}},
	}
	out := s.OnUpdate(core.Event{Type: core.EventOrderBook}, obs, snap)
	if len(out) != 0 {
		t.Fatalf("expected no intents when time-left too small, got %+v", out)
	}
}

func TestOnUpdate_UnknownEventType_NoOp(t *testing.T) {
	s := newStrategy()
	if got := s.OnUpdate(core.Event{Type: core.EventOrder}, runtime.Observation{}, state.Snapshot{}); len(got) != 0 {
		t.Fatalf("expected no intents for unknown type, got %d", len(got))
	}
}
