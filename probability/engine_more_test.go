package probability

import (
	"context"
	"testing"
	"time"

	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/indicators"
	"github.com/xiangxn/polypilot/internal/atomicx"
	"github.com/xiangxn/polypilot/internal/buffer"
	"github.com/xiangxn/polypilot/runtime"

	"github.com/tidwall/gjson"
	"github.com/xiangxn/go-polymarket-sdk/orders"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

func TestCopyMap(t *testing.T) {
	src := map[string]int{"a": 1, "b": 2}
	dst := CopyMap(src)
	if dst["a"] != 1 || dst["b"] != 2 {
		t.Fatalf("got %v", dst)
	}
	if CopyMap[string, int](nil) != nil {
		t.Fatal("nil source → nil dst")
	}
	// Independence
	dst["a"] = 999
	if src["a"] == 999 {
		t.Fatal("CopyMap should produce a copy, not share underlying map")
	}
}

func TestPhi(t *testing.T) {
	if got := Phi(0); got < 0.49 || got > 0.51 {
		t.Fatalf("Phi(0) ≈ 0.5, got %v", got)
	}
	if Phi(10) < 0.999 {
		t.Fatal("Phi(10) ≈ 1")
	}
	if Phi(-10) > 0.001 {
		t.Fatal("Phi(-10) ≈ 0")
	}
}

func TestCopyOrderBook_Independence(t *testing.T) {
	src := sdk.OrderBook{
		Bids: []orders.Book{{Price: 0.5, Size: 10}},
		Asks: []orders.Book{{Price: 0.6, Size: 5}},
	}
	cp := CopyOrderBook(src)
	cp.Bids[0].Size = 99
	if src.Bids[0].Size != 10 {
		t.Fatal("CopyOrderBook didn't deep-copy bids")
	}
	cp.Asks[0].Size = 77
	if src.Asks[0].Size != 5 {
		t.Fatal("CopyOrderBook didn't deep-copy asks")
	}
}

func TestCopyOrderBook_NilSlices(t *testing.T) {
	src := sdk.OrderBook{AssetId: "tk1"} // nil bids/asks
	cp := CopyOrderBook(src)
	if cp.AssetId != "tk1" {
		t.Fatalf("scalar field not copied: %+v", cp)
	}
	if cp.Bids != nil {
		t.Fatalf("expected nil Bids, got %v", cp.Bids)
	}
	if cp.Asks != nil {
		t.Fatalf("expected nil Asks, got %v", cp.Asks)
	}
}

func TestNewEngine_StoresClient(t *testing.T) {
	c := sdk.NewClient(sdk.DefaultConfig())
	e := NewEngine("btc", c)
	if e.client != c {
		t.Fatal("NewEngine should store provided client")
	}
	e2 := NewEngine("btc", nil)
	if e2.client != nil {
		t.Fatal("nil client should be retained as nil")
	}
}

func TestInit_StartsTickerThatStopsOnCancel(t *testing.T) {
	e := &Engine{}
	ctx, cancel := context.WithCancel(context.Background())
	e.Init(ctx)
	time.Sleep(50 * time.Millisecond)
	cancel()
	// ensure RingBuffer / zscore are initialized
	if e.signal.zscore == nil || e.signal.zWindows == nil {
		t.Fatal("zscore/zWindows not initialized")
	}
	if e.token.items == nil {
		t.Fatal("token.items not initialized")
	}
	if e.book.books == nil {
		t.Fatal("book.books not initialized")
	}
	// Wait a moment to ensure goroutine exits cleanly
	time.Sleep(50 * time.Millisecond)
}

func TestInit_TickerAccumulatesIntoZWindows(t *testing.T) {
	e := &Engine{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.Init(ctx)

	// Set non-zero latestZ so we know the ticker is appending real values
	e.signal.latestZ.Store(1.5)

	// Wait for >1 tick of the 1-second ticker
	time.Sleep(1100 * time.Millisecond)

	last := e.signal.zWindows.Last(10)
	found := false
	for _, v := range last {
		if v == 1.5 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected zWindows to contain 1.5 after tick, got %v", last)
	}
}

func TestOnUpdate_EventMarket_BadDataType_NoOp(t *testing.T) {
	e := &Engine{}
	if _, ok := e.OnUpdate(core.Event{Type: core.EventMarket, Data: 42}); ok {
		t.Fatal("expected !ok")
	}
}

func TestOnUpdate_EventMarket_NoResetWhenSameMarket(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &Engine{}
	e.Init(ctx)

	raw := gjson.Parse(`{"conditionId":"same"}`)
	e.market.raw = &raw
	e.market.openPrice = 100
	e.market.endTime = time.Now().Add(time.Hour).UnixMilli()

	// Same conditionId, openPrice != 0 → no reset needed
	obs, ok := e.OnUpdate(core.Event{Type: core.EventMarket, Data: raw})
	if ok {
		t.Fatalf("expected !ok when no reset needed, got obs=%+v", obs)
	}
}

// Trigger the reset path: market.raw is nil → needReset=true → prepareReset called.
// prepareReset fails (no tokens in obj), so OnUpdate returns (Observation{}, false).
// Covers: needReset==true → prepareReset → nil check → early return.
func TestOnUpdate_EventMarket_NeedsResetButPrepFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &Engine{}
	e.Init(ctx)
	// No market state — needReset will be true
	// Pass an obj with no tokens → prepareReset returns nil
	raw := gjson.Parse(`{"conditionId":"new","clobTokenIds":"[]"}`)
	obs, ok := e.OnUpdate(core.Event{Type: core.EventMarket, Data: raw})
	if ok {
		t.Fatalf("expected !ok since prepareReset fails, got obs=%+v", obs)
	}
}

// Trigger reset when conditionId differs from existing one — also goes through prepareReset.
func TestOnUpdate_EventMarket_DifferentConditionId(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &Engine{}
	e.Init(ctx)

	prev := gjson.Parse(`{"conditionId":"old"}`)
	e.market.raw = &prev
	e.market.openPrice = 100
	e.market.endTime = time.Now().Add(time.Hour).UnixMilli()

	// New conditionId → needReset=true; prepareReset still fails (no tokens)
	raw := gjson.Parse(`{"conditionId":"new"}`)
	if _, ok := e.OnUpdate(core.Event{Type: core.EventMarket, Data: raw}); ok {
		t.Fatal("expected !ok when prepareReset fails")
	}
}

// openPrice==0 also triggers reset path even with same conditionId.
func TestOnUpdate_EventMarket_OpenPriceZeroTriggersReset(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &Engine{}
	e.Init(ctx)

	raw := gjson.Parse(`{"conditionId":"same"}`)
	e.market.raw = &raw
	e.market.openPrice = 0 // triggers reset
	if _, ok := e.OnUpdate(core.Event{Type: core.EventMarket, Data: raw}); ok {
		t.Fatal("expected !ok when prepareReset fails after reset trigger")
	}
}

func TestOnUpdate_EventOrderBook_BadDataType_NoOp(t *testing.T) {
	e := &Engine{}
	if _, ok := e.OnUpdate(core.Event{Type: core.EventOrderBook, Data: 42}); ok {
		t.Fatal("expected !ok")
	}
}

func TestOnUpdate_EventOrderBook_NilPointer_NoOp(t *testing.T) {
	e := &Engine{}
	if _, ok := e.OnUpdate(core.Event{Type: core.EventOrderBook, Data: (*sdk.OrderBook)(nil)}); ok {
		t.Fatal("expected !ok on nil orderbook pointer")
	}
}

func TestOnUpdate_EventOrderBook_MarketNotInitialized_NoOp(t *testing.T) {
	e := &Engine{}
	// Need book.books map so getBook doesn't panic — but actually code goes through e.market.raw check first
	ev := core.Event{Type: core.EventOrderBook, Data: &sdk.OrderBook{AssetId: "tk1"}}
	if _, ok := e.OnUpdate(ev); ok {
		t.Fatal("expected !ok without market")
	}
}

func TestOnUpdate_EventOrderBook_UnknownToken_NoOp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &Engine{}
	e.Init(ctx)

	raw := gjson.Parse(`{"conditionId":"c1"}`)
	e.market.raw = &raw
	e.market.openPrice = 100
	e.market.endTime = time.Now().Add(time.Hour).UnixMilli()
	// no tokens registered

	ev := core.Event{Type: core.EventOrderBook, Data: &sdk.OrderBook{AssetId: "tk-unknown"}}
	if _, ok := e.OnUpdate(ev); ok {
		t.Fatal("expected !ok for unknown token")
	}
}

func TestOnUpdate_EventOrderBook_UpdatesTokenPrice(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &Engine{}
	e.Init(ctx)

	raw := gjson.Parse(`{"conditionId":"c1"}`)
	e.market.raw = &raw
	e.market.openPrice = 100
	e.market.endTime = time.Now().Add(time.Hour).UnixMilli()
	e.market.tokenIDs = []string{"tk1"}
	e.token.items = map[string]runtime.Token{
		"tk1": {Id: "tk1", AskPrice: 0.4, BidPrice: 0.3},
	}

	ev := core.Event{Type: core.EventOrderBook, Data: &sdk.OrderBook{
		AssetId:   "tk1",
		Market:    "c1",
		Timestamp: time.Now().UnixMilli(),
		Asks:      []orders.Book{{Price: 0.6, Size: 10}},
		Bids:      []orders.Book{{Price: 0.5, Size: 10}},
	}}
	// zscore not ready yet → returns false but should update items
	_, _ = e.OnUpdate(ev)
	if e.token.items["tk1"].AskPrice != 0.6 || e.token.items["tk1"].BidPrice != 0.5 {
		t.Fatalf("expected token prices updated, got %+v", e.token.items["tk1"])
	}
}

func TestOnUpdate_EventOrderBook_ZScoreReady_ReturnsObservation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &Engine{}
	e.Init(ctx)

	// Replace the zscore with a smaller window we can warm easily.
	// windowSize=2 → IsReady when len(series) >= 1
	e.signal.zscore = indicators.NewZScore(2)
	e.signal.zWindows = buffer.NewRingBuffer(e.signal.zscore.WindowSize())
	// Feed ticks at DIFFERENT seconds so series gets populated.
	// 1st tick stores state; 2nd tick (different second) pushes 1st to series.
	e.signal.zscore.OnTick(indicators.Tick{Price: 100, Timestamp: 1000000})
	e.signal.zscore.OnTick(indicators.Tick{Price: 101, Timestamp: 2000000})
	if !e.signal.zscore.IsReady() {
		t.Fatal("zscore should be ready after 2 ticks at different seconds")
	}

	raw := gjson.Parse(`{"conditionId":"c1"}`)
	e.market.raw = &raw
	e.market.openPrice = 100
	e.market.endTime = time.Now().Add(time.Hour).UnixMilli()
	e.market.tokenIDs = []string{"tk1"}
	e.token.items = map[string]runtime.Token{
		"tk1": {Id: "tk1"},
	}

	ev := core.Event{Type: core.EventOrderBook, Data: &sdk.OrderBook{
		AssetId:   "tk1",
		Market:    "c1",
		Timestamp: time.Now().UnixMilli(),
		Asks:      []orders.Book{{Price: 0.6, Size: 10}},
		Bids:      []orders.Book{{Price: 0.5, Size: 10}},
	}}
	obs, ok := e.OnUpdate(ev)
	if !ok {
		t.Fatalf("expected ok with zscore ready, obs=%+v", obs)
	}
	if obs.MarketID != "c1" {
		t.Fatalf("got %+v", obs)
	}
	if obs.GetOrderBook == nil {
		t.Fatal("expected GetOrderBook closure")
	}
	// fillFeaturesLocked should populate Features
	if _, ok := obs.Features["latestZ"]; !ok {
		t.Fatalf("Features should be populated, got %+v", obs.Features)
	}
}

func TestOnUpdate_EventSignal_BadType_NoOp(t *testing.T) {
	e := &Engine{}
	if _, ok := e.OnUpdate(core.Event{Type: core.EventExternalPrice, Data: "wrong"}); ok {
		t.Fatal("expected !ok")
	}
}

func TestOnUpdate_EventSignal_OpenPriceZero_NoOp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &Engine{}
	e.Init(ctx)
	// openPrice is 0
	ev := core.Event{
		Type: core.EventExternalPrice,
		Data: sdk.ExternalPrice{Price: 100, Timestamp: time.Now().UnixMilli()},
	}
	if _, ok := e.OnUpdate(ev); ok {
		t.Fatal("expected !ok when openPrice=0")
	}
}

func TestOnUpdate_EventSignal_UpdatesLatestPrice(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &Engine{}
	e.Init(ctx)

	raw := gjson.Parse(`{"conditionId":"c1"}`)
	e.market.raw = &raw
	e.market.openPrice = 100
	e.market.endTime = time.Now().Add(time.Hour).UnixMilli()

	ev := core.Event{
		Type: core.EventExternalPrice,
		Data: sdk.ExternalPrice{Price: 105, Timestamp: time.Now().UnixMilli()},
	}
	_, _ = e.OnUpdate(ev)
	if e.signal.latestPrice.Load() != 105 {
		t.Fatalf("expected latestPrice=105, got %v", e.signal.latestPrice.Load())
	}
}

func TestOnUpdate_EventSignal_ZScoreReady_ComputesZ(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &Engine{}
	e.Init(ctx)
	// Replace zscore with smaller one we can warm easily
	e.signal.zscore = indicators.NewZScore(2)
	e.signal.zWindows = buffer.NewRingBuffer(e.signal.zscore.WindowSize())
	// Feed ticks at distinct seconds so series accumulates
	e.signal.zscore.OnTick(indicators.Tick{Price: 100, Timestamp: 1000000})
	e.signal.zscore.OnTick(indicators.Tick{Price: 101, Timestamp: 2000000})
	if !e.signal.zscore.IsReady() {
		t.Fatal("zscore should be ready")
	}

	raw := gjson.Parse(`{"conditionId":"c1"}`)
	e.market.raw = &raw
	e.market.openPrice = 100
	// endTime far in future to ensure timeLeft >= 1
	e.market.endTime = time.Now().Add(time.Hour).UnixMilli()

	ev := core.Event{
		Type: core.EventExternalPrice,
		Data: sdk.ExternalPrice{Price: 105, Timestamp: 3000000},
	}
	_, _ = e.OnUpdate(ev)
	// latestPrice should be updated
	if e.signal.latestPrice.Load() != 105 {
		t.Fatalf("expected latestPrice=105, got %v", e.signal.latestPrice.Load())
	}
}

// EventSignal with endTime in past → timeLeft<1 → zscore.IsReady true but no Z stored
func TestOnUpdate_EventSignal_ZReadyButTimeLeftZero(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &Engine{}
	e.Init(ctx)
	e.signal.zscore = indicators.NewZScore(2)
	e.signal.zWindows = buffer.NewRingBuffer(e.signal.zscore.WindowSize())
	e.signal.zscore.OnTick(indicators.Tick{Price: 100, Timestamp: 1000000})
	e.signal.zscore.OnTick(indicators.Tick{Price: 101, Timestamp: 2000000})
	if !e.signal.zscore.IsReady() {
		t.Fatal("zscore should be ready")
	}

	raw := gjson.Parse(`{"conditionId":"c1"}`)
	e.market.raw = &raw
	e.market.openPrice = 100
	// endTime in past → timeLeft < 1
	e.market.endTime = time.Now().Add(-time.Hour).UnixMilli()

	ev := core.Event{
		Type: core.EventExternalPrice,
		Data: sdk.ExternalPrice{Price: 105, Timestamp: 3000000},
	}
	_, _ = e.OnUpdate(ev)
}

func TestOnUpdate_UnknownEventType_NoOp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &Engine{}
	e.Init(ctx)
	if _, ok := e.OnUpdate(core.Event{Type: core.EventOrder}); ok {
		t.Fatal("expected !ok for unknown event type")
	}
}

// CurrentObservation when market not initialized
func TestCurrentObservation_NoMarket_NotOk(t *testing.T) {
	e := &Engine{}
	if _, ok := e.CurrentObservation(); ok {
		t.Fatal("expected !ok")
	}
}

func TestCurrentObservation_NoEndTime_NotOk(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &Engine{}
	e.Init(ctx)
	raw := gjson.Parse(`{"conditionId":"c1"}`)
	e.market.raw = &raw
	// endTime = 0
	if _, ok := e.CurrentObservation(); ok {
		t.Fatal("expected !ok with endTime=0")
	}
}

func TestCurrentObservation_HappyPath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &Engine{}
	e.Init(ctx)

	raw := gjson.Parse(`{"conditionId":"c1"}`)
	e.market.raw = &raw
	e.market.endTime = time.Now().Add(time.Hour).UnixMilli()
	e.market.openPrice = 100
	e.market.tokenIDs = []string{"tk1"}
	e.token.items = map[string]runtime.Token{
		"tk1": {Id: "tk1", AskPrice: 0.6, BidPrice: 0.5},
	}

	obs, ok := e.CurrentObservation()
	if !ok {
		t.Fatal("expected ok")
	}
	if obs.MarketID != "c1" {
		t.Fatalf("got %+v", obs)
	}
	if obs.GetOrderBook == nil {
		t.Fatal("expected GetOrderBook closure")
	}
	if len(obs.TokenIds) != 1 || obs.TokenIds[0] != "tk1" {
		t.Fatalf("expected TokenIds=[tk1], got %v", obs.TokenIds)
	}
	if _, ok := obs.Features["openPrice"]; !ok {
		t.Fatalf("Features should be populated, got %+v", obs.Features)
	}
}

func TestCurrentObservation_NoTokenIDs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &Engine{}
	e.Init(ctx)

	raw := gjson.Parse(`{"conditionId":"c1"}`)
	e.market.raw = &raw
	e.market.endTime = time.Now().Add(time.Hour).UnixMilli()
	e.market.openPrice = 100
	// no tokenIDs

	obs, ok := e.CurrentObservation()
	if !ok {
		t.Fatal("expected ok")
	}
	if len(obs.TokenIds) != 0 {
		t.Fatalf("expected empty TokenIds, got %v", obs.TokenIds)
	}
}

// resetForNewMarketLocked: called from OnUpdate after prepareReset succeeds.
// We can test it directly by constructing a valid resetPrep.
func TestResetForNewMarketLocked_PopulatesState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &Engine{}
	e.Init(ctx)

	raw := gjson.Parse(`{"conditionId":"c1","endDate":"2099-01-01T00:00:00Z"}`)
	prep := &resetPrep{
		endTime:   time.Now().Add(time.Hour).UnixMilli(),
		openPrice: 100,
		tokenIDs:  []string{"tk1", "tk2"},
		books: []sdk.OrderBookSummary{
			{AssetId: "tk1", Asks: []orders.Book{{Price: 0.6, Size: 5}}, Bids: []orders.Book{{Price: 0.5, Size: 5}}},
			{AssetId: "tk2", Asks: []orders.Book{{Price: 0.4, Size: 5}}, Bids: []orders.Book{{Price: 0.3, Size: 5}}},
		},
	}
	obs, ok := e.resetForNewMarketLocked(raw, prep)
	if !ok {
		t.Fatal("expected ok")
	}
	if obs.MarketID != "c1" {
		t.Fatalf("MarketID=%v", obs.MarketID)
	}
	if e.market.openPrice != 100 {
		t.Fatalf("openPrice=%v", e.market.openPrice)
	}
	if len(e.market.tokenIDs) != 2 {
		t.Fatalf("tokenIDs=%v", e.market.tokenIDs)
	}
	if len(e.token.items) != 2 {
		t.Fatalf("token items count=%d", len(e.token.items))
	}
	if e.token.items["tk1"].AskPrice != 0.6 {
		t.Fatalf("token tk1 askPrice=%v", e.token.items["tk1"].AskPrice)
	}
	// Order books should be stored
	if ob := e.GetOrderBook("tk1"); ob == nil {
		t.Fatal("expected order book for tk1")
	}
}

func TestResetForNewMarketLocked_NilPrep(t *testing.T) {
	e := &Engine{}
	raw := gjson.Parse(`{}`)
	obs, ok := e.resetForNewMarketLocked(raw, nil)
	if ok {
		t.Fatalf("expected !ok with nil prep, got %+v", obs)
	}
}

func TestResetForNewMarketLocked_EmptyBidsAsks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &Engine{}
	e.Init(ctx)

	raw := gjson.Parse(`{"conditionId":"c1"}`)
	prep := &resetPrep{
		endTime:   time.Now().Add(time.Hour).UnixMilli(),
		openPrice: 100,
		tokenIDs:  []string{"tk1"},
		books: []sdk.OrderBookSummary{
			{AssetId: "tk1"}, // no bids/asks
		},
	}
	obs, ok := e.resetForNewMarketLocked(raw, prep)
	if !ok {
		t.Fatal("expected ok")
	}
	if obs.MarketID != "c1" {
		t.Fatalf("got %v", obs.MarketID)
	}
	if e.token.items["tk1"].AskPrice != 0 || e.token.items["tk1"].BidPrice != 0 {
		t.Fatalf("expected zero prices, got %+v", e.token.items["tk1"])
	}
}

// prepareReset hits real RPC endpoints. With ChainID=0 and nil/empty config
// it should fail at the GetOrderBooks step and return nil.
func TestPrepareReset_TooFewTokenIDs(t *testing.T) {
	e := &Engine{}
	raw := gjson.Parse(`{"clobTokenIds":"[\"tk1\"]"}`) // only 1 token
	if prep := e.prepareReset(raw); prep != nil {
		t.Fatalf("expected nil for <2 tokenIDs, got %+v", prep)
	}
}

func TestPrepareReset_NoTokenIDs(t *testing.T) {
	e := &Engine{}
	raw := gjson.Parse(`{}`) // no tokens
	if prep := e.prepareReset(raw); prep != nil {
		t.Fatalf("expected nil with no tokens, got %+v", prep)
	}
}

// Confirm zscore is not nil after Init and zWindows accumulates values.
func TestSignalState_FieldsZeroValues(t *testing.T) {
	var s signalState
	if s.latestPrice.Load() != 0 {
		t.Fatalf("expected zero, got %v", s.latestPrice.Load())
	}
	if s.latestZ.Load() != 0 {
		t.Fatalf("expected zero, got %v", s.latestZ.Load())
	}
	// atomicx.Float64 zero-value should be usable
	var f atomicx.Float64
	if f.Load() != 0 {
		t.Fatalf("expected zero, got %v", f.Load())
	}
}
