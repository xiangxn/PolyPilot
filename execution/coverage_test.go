package execution

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/runtime"

	sdkmodel "github.com/xiangxn/go-polymarket-sdk/model"
	"github.com/xiangxn/go-polymarket-sdk/orders"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
	"github.com/xiangxn/go-polymarket-sdk/relayer"
)

// ------------------------------------------------------------
// Trivial utility tests
// ------------------------------------------------------------

func TestParseEventTime(t *testing.T) {
	// Zero timestamp falls back to time.Now()
	before := time.Now().Add(-time.Second)
	got := parseEventTime(0)
	if !got.After(before) {
		t.Fatalf("zero should fall back to now, got %v", got)
	}

	// Negative timestamp falls back to time.Now()
	got2 := parseEventTime(-1)
	if !got2.After(before) {
		t.Fatalf("negative should fall back to now, got %v", got2)
	}

	// Millisecond-scale timestamp (> 1e12)
	ms := time.Now().UnixMilli()
	if got := parseEventTime(ms); got.UnixMilli() != ms {
		t.Fatalf("expected ms-scale match: got %v want UnixMilli=%v", got, ms)
	}

	// Second-scale timestamp (small)
	sec := time.Now().Unix()
	if got := parseEventTime(sec); got.Unix() != sec {
		t.Fatalf("expected sec-scale match: got %v want Unix=%v", got, sec)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want string
	}{
		{"first non-empty", "a", "b", "a"},
		{"first empty", "", "b", "b"},
		{"first whitespace only", "   ", "b", "b"},
		{"both empty", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := firstNonEmpty(c.a, c.b); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestValidatePlacement(t *testing.T) {
	cases := []struct {
		name string
		in   runtime.OrderIntent
		ok   bool
	}{
		{"happy buy", runtime.OrderIntent{MarketID: "m", TokenID: "t", Size: 1, Price: 0.5, Side: orders.BUY}, true},
		{"happy sell", runtime.OrderIntent{MarketID: "m", TokenID: "t", Size: 2.5, Price: 0.7, Side: orders.SELL}, true},
		{"empty market", runtime.OrderIntent{TokenID: "t", Size: 1, Price: 0.5, Side: orders.BUY}, false},
		{"whitespace market", runtime.OrderIntent{MarketID: "   ", TokenID: "t", Size: 1, Price: 0.5, Side: orders.BUY}, false},
		{"empty token", runtime.OrderIntent{MarketID: "m", Size: 1, Price: 0.5, Side: orders.BUY}, false},
		{"whitespace token", runtime.OrderIntent{MarketID: "m", TokenID: "   ", Size: 1, Price: 0.5, Side: orders.BUY}, false},
		{"zero size", runtime.OrderIntent{MarketID: "m", TokenID: "t", Price: 0.5, Side: orders.BUY}, false},
		{"negative size", runtime.OrderIntent{MarketID: "m", TokenID: "t", Size: -1, Price: 0.5, Side: orders.BUY}, false},
		{"bad price low", runtime.OrderIntent{MarketID: "m", TokenID: "t", Size: 1, Price: 0, Side: orders.BUY}, false},
		{"bad price negative", runtime.OrderIntent{MarketID: "m", TokenID: "t", Size: 1, Price: -0.1, Side: orders.BUY}, false},
		{"bad price high", runtime.OrderIntent{MarketID: "m", TokenID: "t", Size: 1, Price: 1, Side: orders.BUY}, false},
		{"bad price over 1", runtime.OrderIntent{MarketID: "m", TokenID: "t", Size: 1, Price: 1.5, Side: orders.BUY}, false},
		{"bad side", runtime.OrderIntent{MarketID: "m", TokenID: "t", Size: 1, Price: 0.5, Side: "X"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validatePlacement(c.in)
			if (err == nil) != c.ok {
				t.Fatalf("got err=%v want ok=%v", err, c.ok)
			}
		})
	}
}

// ------------------------------------------------------------
// ownKey / isOwnOwner
// ------------------------------------------------------------

func TestOwnKey_NilExecutor(t *testing.T) {
	var e *Executor
	if got := e.ownKey(); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestOwnKey_NilConfig(t *testing.T) {
	e := &Executor{}
	if got := e.ownKey(); got != "" {
		t.Fatalf("expected empty (nil config), got %q", got)
	}
}

func TestOwnKey_WithKey(t *testing.T) {
	cfg := sdk.DefaultConfig()
	cfg.Polymarket.CLOBCreds = &sdkmodel.ApiKeyCreds{Key: "  myKey  "}
	e := &Executor{Config: cfg}
	if got := e.ownKey(); got != "myKey" {
		t.Fatalf("expected myKey, got %q", got)
	}
}

func TestIsOwnOwner(t *testing.T) {
	// Empty key returns true (treat all as own)
	e := &Executor{}
	if !e.isOwnOwner("anything") {
		t.Fatal("empty ownKey should treat all owners as own")
	}

	cfg := sdk.DefaultConfig()
	cfg.Polymarket.CLOBCreds = &sdkmodel.ApiKeyCreds{Key: "myKey"}
	e2 := &Executor{Config: cfg}
	if !e2.isOwnOwner("myKey") {
		t.Fatal("should match exact own key")
	}
	if !e2.isOwnOwner("   myKey   ") {
		t.Fatal("should trim whitespace when matching")
	}
	if e2.isOwnOwner("otherKey") {
		t.Fatal("should reject other owner")
	}
	if e2.isOwnOwner("") {
		t.Fatal("should reject empty owner when ownKey set")
	}
}

// ------------------------------------------------------------
// rejectBatch / publish
// ------------------------------------------------------------

func TestRejectBatch_PublishesRejected(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}

	exec.rejectBatch([]runtime.OrderIntent{
		{IntentID: "i1", MarketID: "m1", TokenID: "tk1", Price: 0.5, Size: 1, Side: orders.BUY},
		{Action: runtime.OrderIntentActionCancel, OrderID: "o1", IntentID: "i2"},
	}, "test reason")

	for i := 0; i < 2; i++ {
		select {
		case ev := <-ch:
			ee := ev.Data.(core.ExecutionEvent)
			if ee.Status != core.ExecutionStatusRejected {
				t.Fatalf("expected rejected, got %v", ee.Status)
			}
			if ee.Reason != "test reason" {
				t.Fatalf("unexpected reason: %s", ee.Reason)
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout")
		}
	}
}

func TestRejectBatch_CancelKeepsOrderID(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}

	exec.rejectBatch([]runtime.OrderIntent{
		{Action: runtime.OrderIntentActionCancel, OrderID: "order-99"},
	}, "test")

	ev := <-ch
	ee := ev.Data.(core.ExecutionEvent)
	if ee.OrderID != "order-99" {
		t.Fatalf("expected OrderID preserved on cancel rejection, got %q", ee.OrderID)
	}
}

func TestRejectBatch_Empty(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}
	exec.rejectBatch(nil, "reason")
	select {
	case ev := <-ch:
		t.Fatalf("expected no event, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestPublish_NilBus(t *testing.T) {
	exec := &Executor{}
	// Should not panic
	exec.publish(core.ExecutionEvent{Status: core.ExecutionStatusAccepted})
}

// ------------------------------------------------------------
// getOrCreateTracked / trackPostedOrder
// ------------------------------------------------------------

func TestGetOrCreateTracked_Idempotent(t *testing.T) {
	exec := &Executor{}
	a := exec.getOrCreateTracked("o1")
	b := exec.getOrCreateTracked("o1")
	if a != b {
		t.Fatal("expected same pointer for same orderID")
	}
	c := exec.getOrCreateTracked("o2")
	if a == c {
		t.Fatal("expected distinct pointers for distinct orderIDs")
	}
}

func TestGetOrCreateTracked_NilMap(t *testing.T) {
	exec := &Executor{}
	// tracked starts as nil; first call should lazy-init
	tr := exec.getOrCreateTracked("o1")
	if tr == nil {
		t.Fatal("expected non-nil tracked")
	}
	if tr.SeenTradeIDs == nil {
		t.Fatal("expected SeenTradeIDs initialized")
	}
}

func TestTrackPostedOrder(t *testing.T) {
	exec := &Executor{}
	exec.trackPostedOrder("o1", runtime.OrderIntent{
		MarketID: "m1", TokenID: "tk1", Side: orders.BUY, Price: 0.5, Size: 10,
	})
	tr := exec.tracked["o1"]
	if tr == nil {
		t.Fatal("expected tracked")
	}
	if tr.MarketID != "m1" || tr.TokenID != "tk1" || tr.Side != orders.BUY {
		t.Fatalf("unexpected tracked fields: %+v", tr)
	}
	if tr.Price != 0.5 || tr.RequestedSize != 10 || !tr.Accepted {
		t.Fatalf("unexpected price/size/accepted: %+v", tr)
	}
}

func TestTrackPostedOrder_EmptyOrderID(t *testing.T) {
	exec := &Executor{}
	exec.trackPostedOrder("   ", runtime.OrderIntent{MarketID: "m1", TokenID: "tk1"})
	if len(exec.tracked) != 0 {
		t.Fatalf("expected no tracked, got %d", len(exec.tracked))
	}
}

func TestTrackPostedOrder_PreservesExisting(t *testing.T) {
	exec := &Executor{}
	// Pre-existing tracked with some fields set
	exec.getOrCreateTracked("o1").MarketID = "preset-market"
	exec.getOrCreateTracked("o1").TokenID = "preset-token"

	// Intent with empty market/token should preserve existing
	exec.trackPostedOrder("o1", runtime.OrderIntent{Side: orders.SELL, Price: 0.6, Size: 5})
	tr := exec.tracked["o1"]
	if tr.MarketID != "preset-market" || tr.TokenID != "preset-token" {
		t.Fatalf("expected preserved market/token, got %+v", tr)
	}
	if tr.Side != orders.SELL || tr.Price != 0.6 || tr.RequestedSize != 5 {
		t.Fatalf("expected updated side/price/size, got %+v", tr)
	}
}

// ------------------------------------------------------------
// buildAcceptedEvent
// ------------------------------------------------------------

func TestBuildAcceptedEvent_Invalid(t *testing.T) {
	exec := &Executor{}
	cases := []struct {
		name string
		t    *trackedOrder
	}{
		{"nil tracked", nil},
		{"already accepted", &trackedOrder{Accepted: true, MarketID: "m", TokenID: "tk", Price: 0.5, RequestedSize: 1, Side: orders.BUY}},
		{"missing market", &trackedOrder{TokenID: "tk", Price: 0.5, RequestedSize: 1, Side: orders.BUY}},
		{"missing token", &trackedOrder{MarketID: "m", Price: 0.5, RequestedSize: 1, Side: orders.BUY}},
		{"zero price", &trackedOrder{MarketID: "m", TokenID: "tk", Price: 0, RequestedSize: 1, Side: orders.BUY}},
		{"zero size", &trackedOrder{MarketID: "m", TokenID: "tk", Price: 0.5, RequestedSize: 0, Side: orders.BUY}},
		{"invalid side", &trackedOrder{MarketID: "m", TokenID: "tk", Price: 0.5, RequestedSize: 1, Side: "X"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ok := exec.buildAcceptedEvent("o1", c.t, time.Now())
			if ok {
				t.Fatalf("expected ok=false for %s", c.name)
			}
		})
	}
}

func TestBuildAcceptedEvent_ValidBuy(t *testing.T) {
	exec := &Executor{}
	tr := &trackedOrder{MarketID: "m", TokenID: "tk", Price: 0.5, RequestedSize: 5, Side: orders.BUY}
	now := time.Now()
	ev, ok := exec.buildAcceptedEvent("o1", tr, now)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if ev.OrderID != "o1" || ev.Status != core.ExecutionStatusAccepted {
		t.Fatalf("unexpected ev: %+v", ev)
	}
	if ev.MarketID != "m" || ev.TokenID != "tk" || ev.Price != 0.5 || ev.Side != orders.BUY {
		t.Fatalf("unexpected fields: %+v", ev)
	}
	if ev.RequestedSize != 5 || ev.FilledSize != 0 {
		t.Fatalf("unexpected sizes: %+v", ev)
	}
}

func TestBuildAcceptedEvent_ValidSell(t *testing.T) {
	exec := &Executor{}
	tr := &trackedOrder{MarketID: "m", TokenID: "tk", Price: 0.55, RequestedSize: 3, Side: orders.SELL}
	ev, ok := exec.buildAcceptedEvent("o2", tr, time.Now())
	if !ok || ev.Side != orders.SELL {
		t.Fatalf("expected ok with SELL side, got ev=%+v ok=%v", ev, ok)
	}
}

// ------------------------------------------------------------
// buildFillEventsFromCumulative / buildFillEventsFromDelta
// ------------------------------------------------------------

func TestBuildFillFromCumulative_NilTracked(t *testing.T) {
	exec := &Executor{}
	if evs := exec.buildFillEventsFromCumulative("o", nil, 1, time.Now()); evs != nil {
		t.Fatalf("expected nil, got %v", evs)
	}
}

func TestBuildFillFromCumulative_ClampNegative(t *testing.T) {
	exec := &Executor{}
	tr := &trackedOrder{MarketID: "m", TokenID: "tk", Price: 0.5, RequestedSize: 10, Side: orders.BUY}
	// Negative cumulative clamps to 0 → no delta
	if evs := exec.buildFillEventsFromCumulative("o", tr, -5, time.Now()); evs != nil {
		t.Fatalf("expected nil for negative cumulative, got %v", evs)
	}
}

func TestBuildFillFromCumulative_ClampToRequestedSize(t *testing.T) {
	exec := &Executor{}
	tr := &trackedOrder{MarketID: "m", TokenID: "tk", Price: 0.5, RequestedSize: 10, Side: orders.BUY}
	// Cumulative beyond requested size clamps
	evs := exec.buildFillEventsFromCumulative("o", tr, 20, time.Now())
	if len(evs) != 1 || evs[0].Status != core.ExecutionStatusFilled || evs[0].FilledSize != 10 {
		t.Fatalf("expected full fill clamped, got %+v", evs)
	}
	if !tr.Finalized {
		t.Fatal("expected Finalized true")
	}
}

func TestBuildFillFromCumulative_NoChange(t *testing.T) {
	exec := &Executor{}
	tr := &trackedOrder{MarketID: "m", TokenID: "tk", Price: 0.5, RequestedSize: 10, Side: orders.BUY, FilledSize: 5}
	// Cumulative same as FilledSize → no events
	if evs := exec.buildFillEventsFromCumulative("o", tr, 5, time.Now()); evs != nil {
		t.Fatalf("expected nil (no delta), got %v", evs)
	}
}

func TestBuildFillFromDelta_NilTracked(t *testing.T) {
	exec := &Executor{}
	if evs := exec.buildFillEventsFromDelta("o", nil, 1, time.Now()); evs != nil {
		t.Fatalf("expected nil for nil tracked, got %v", evs)
	}
}

func TestBuildFillFromDelta_ZeroDelta(t *testing.T) {
	exec := &Executor{}
	tr := &trackedOrder{MarketID: "m", TokenID: "tk", Price: 0.5, RequestedSize: 10, Side: orders.BUY}
	if evs := exec.buildFillEventsFromDelta("o", tr, 0, time.Now()); evs != nil {
		t.Fatalf("expected nil for zero delta, got %v", evs)
	}
}

func TestBuildFillFromDelta_PartialThenFull(t *testing.T) {
	exec := &Executor{}
	tr := &trackedOrder{MarketID: "m", TokenID: "tk", Price: 0.5, RequestedSize: 10, Side: orders.BUY}
	evs := exec.buildFillEventsFromDelta("o", tr, 4, time.Now())
	if len(evs) != 1 || evs[0].Status != core.ExecutionStatusPartiallyFilled || evs[0].FilledSize != 4 {
		t.Fatalf("partial: %+v", evs)
	}
	evs2 := exec.buildFillEventsFromDelta("o", tr, 6, time.Now())
	if len(evs2) != 1 || evs2[0].Status != core.ExecutionStatusFilled || !tr.Finalized {
		t.Fatalf("final: %+v finalized=%v", evs2, tr.Finalized)
	}
}

// ------------------------------------------------------------
// publishAcceptedFromPost
// ------------------------------------------------------------

func TestPublishAcceptedFromPost(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}

	exec.publishAcceptedFromPost(runtime.OrderIntent{
		IntentID: "i1", MarketID: "m1", TokenID: "tk1", Price: 0.5, Size: 1, Side: orders.BUY,
	}, "o1", time.Now())

	select {
	case ev := <-ch:
		ee := ev.Data.(core.ExecutionEvent)
		if ee.Status != core.ExecutionStatusAccepted {
			t.Fatalf("expected accepted, got %v", ee.Status)
		}
		if ee.OrderID != "o1" || ee.ParentOrderID != "i1" {
			t.Fatalf("unexpected ids: %+v", ee)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

// ------------------------------------------------------------
// Execute branches
// ------------------------------------------------------------

func TestExecute_EmptyIntents(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}
	exec.Execute(nil)
	exec.Execute([]runtime.OrderIntent{})
	select {
	case ev := <-ch:
		t.Fatalf("expected no event, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestExecute_NoClient_NotDryRun(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	// No Client, no DryRun → Execute returns immediately
	exec := &Executor{Bus: bus}
	exec.Execute([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk", Price: 0.5, Size: 1, Side: orders.BUY,
	}})
	select {
	case ev := <-ch:
		t.Fatalf("expected no event, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestExecute_UnsupportedAction(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, Client: &sdk.PolymarketClient{}}
	exec.Execute([]runtime.OrderIntent{{
		Action: "WEIRD", MarketID: "m", TokenID: "tk", Price: 0.5, Size: 1, Side: orders.BUY,
	}})
	ev := mustRecvExecutionEvent(t, ch)
	if ev.Status != core.ExecutionStatusRejected {
		t.Fatalf("expected rejected, got %s", ev.Status)
	}
	if ev.Reason != "unsupported order action" {
		t.Fatalf("unexpected reason: %s", ev.Reason)
	}
}

func TestExecute_SplitInvalidSize(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, Client: &sdk.PolymarketClient{}}
	exec.Execute([]runtime.OrderIntent{{
		Action: runtime.OrderIntentActionSplit, MarketID: "m", Size: 0, Tokens: []string{"a", "b"},
	}})
	// Size <= 0 is logged and skipped — no event published
	select {
	case ev := <-ch:
		t.Fatalf("expected no event, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestExecute_SplitWrongTokensSkipped(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, Client: &sdk.PolymarketClient{}}
	exec.Execute([]runtime.OrderIntent{{
		Action: runtime.OrderIntentActionSplit, MarketID: "m", Size: 5, Tokens: []string{"a"},
	}})
	// tokens != 2 is logged and skipped — no event published
	select {
	case ev := <-ch:
		t.Fatalf("expected no event, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestExecute_MergeInvalidSizeSkipped(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, Client: &sdk.PolymarketClient{}}
	exec.Execute([]runtime.OrderIntent{{
		Action: runtime.OrderIntentActionMerge, MarketID: "m", Size: -1, Tokens: []string{"a", "b"},
	}})
	select {
	case ev := <-ch:
		t.Fatalf("expected no event, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestExecute_AllInvalidThenNoQueue(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	// All intents fail validation and there's no queue — should not panic
	exec := &Executor{Bus: bus, Client: &sdk.PolymarketClient{}}
	exec.Execute([]runtime.OrderIntent{{
		Action: runtime.OrderIntentActionPlace, MarketID: "m", TokenID: "", Price: 0.5, Size: 1, Side: orders.BUY,
	}})
	ev := mustRecvExecutionEvent(t, ch)
	if ev.Status != core.ExecutionStatusRejected {
		t.Fatalf("expected rejected, got %s", ev.Status)
	}
}

func TestExecute_DefaultActionIsPlace(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, Client: &sdk.PolymarketClient{}}
	// Action == "" should be treated as PLACE; invalid → rejected
	exec.Execute([]runtime.OrderIntent{{MarketID: "", TokenID: "tk", Price: 0.5, Size: 1, Side: orders.BUY}})
	ev := mustRecvExecutionEvent(t, ch)
	if ev.Status != core.ExecutionStatusRejected {
		t.Fatalf("expected rejected, got %s", ev.Status)
	}
}

func TestExecute_NilQueueDropsValidated(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	// Valid intent but queue is nil → silently drop (returns at line 217)
	exec := &Executor{Bus: bus, Client: &sdk.PolymarketClient{}}
	exec.Execute([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk", Price: 0.5, Size: 1, Side: orders.BUY,
	}})
	select {
	case ev := <-ch:
		t.Fatalf("expected no event (queue nil), got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestExecute_SplitMergeReachesQueue(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	exec := &Executor{Bus: bus, Client: &sdk.PolymarketClient{}, queue: make(chan []runtime.OrderIntent, 4)}
	exec.Execute([]runtime.OrderIntent{
		{Action: runtime.OrderIntentActionSplit, MarketID: "m", Size: 5, Tokens: []string{"a", "b"}},
		{Action: runtime.OrderIntentActionMerge, MarketID: "m", Size: 3, Tokens: []string{"a", "b"}},
	})
	// Both should be queued
	select {
	case batch := <-exec.queue:
		if len(batch) != 2 {
			t.Fatalf("expected 2 in batch, got %d", len(batch))
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected batch on queue")
	}
}

// ------------------------------------------------------------
// consumeExecuteQueue branches
// ------------------------------------------------------------

func TestConsumeExecuteQueue_EmptyBatchContinues(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	exec := &Executor{Bus: bus, queue: make(chan []runtime.OrderIntent, 4)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		exec.consumeExecuteQueue(ctx)
		close(done)
	}()

	// Push empty batch — should be silently skipped (continue), not panic
	exec.queue <- []runtime.OrderIntent{}
	exec.queue <- nil

	// Allow processing; then cancel
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("consumeExecuteQueue did not exit")
	}
}

func TestConsumeExecuteQueue_CancelEmptyQueue(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	exec := &Executor{Bus: bus, queue: make(chan []runtime.OrderIntent, 4)}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		exec.consumeExecuteQueue(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("consumeExecuteQueue did not exit after cancel")
	}
}

// ------------------------------------------------------------
// drainQueueOnShutdown edge cases
// ------------------------------------------------------------

func TestDrainQueueOnShutdown_NilQueue(t *testing.T) {
	exec := &Executor{} // queue is nil
	exec.drainQueueOnShutdown()
	// no panic = pass
}

func TestDrainQueueOnShutdown_EmptyQueue(t *testing.T) {
	exec := &Executor{queue: make(chan []runtime.OrderIntent, 4)}
	exec.drainQueueOnShutdown()
	// no panic, no hang = pass
}

// ------------------------------------------------------------
// onOrderEvent branches
// ------------------------------------------------------------

func TestOnOrderEvent_NilOrEmptyID(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}

	exec.onOrderEvent(nil)
	exec.onOrderEvent(&sdkmodel.WSOrder{Id: "   "})

	select {
	case ev := <-ch:
		t.Fatalf("expected no event, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestOnOrderEvent_ForeignOwnerSkipped(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()

	cfg := sdk.DefaultConfig()
	cfg.Polymarket.CLOBCreds = &sdkmodel.ApiKeyCreds{Key: "myKey"}
	exec := &Executor{Bus: bus, Config: cfg}

	exec.onOrderEvent(&sdkmodel.WSOrder{
		Id: "ord-foreign", Market: "m", AssetId: "tk",
		Side: "BUY", Price: 0.5, OriginalSize: 5,
		Status: "LIVE", Owner: "someoneElse",
	})

	select {
	case ev := <-ch:
		t.Fatalf("expected no event for foreign owner, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestOnOrderEvent_DefaultStatusNoEvent(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}

	exec.onOrderEvent(&sdkmodel.WSOrder{
		Id: "ord-unknown", Market: "m", AssetId: "tk",
		Side: "BUY", Price: 0.5, OriginalSize: 5,
		Status: "FOOBAR", Owner: "",
	})

	select {
	case ev := <-ch:
		t.Fatalf("expected no event for unknown status, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestOnOrderEvent_CanceledMarketResolved(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}

	exec.onOrderEvent(&sdkmodel.WSOrder{
		Id: "ord-cmr", Market: "m", AssetId: "tk",
		Side: "BUY", Price: 0.5, OriginalSize: 5,
		Status: "CANCELED_MARKET_RESOLVED", Owner: "",
	})

	ev := mustRecvExecutionEvent(t, ch)
	if ev.Status != core.ExecutionStatusCancelled {
		t.Fatalf("expected cancelled, got %s", ev.Status)
	}
}

func TestOnOrderEvent_CanceledAfterFinalized_NoDuplicate(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}

	// Pre-finalize the tracked order
	tr := exec.getOrCreateTracked("ord-f")
	tr.Finalized = true
	tr.MarketID = "m"
	tr.TokenID = "tk"
	tr.Side = orders.BUY
	tr.Price = 0.5
	tr.RequestedSize = 5

	exec.onOrderEvent(&sdkmodel.WSOrder{
		Id: "ord-f", Market: "m", AssetId: "tk",
		Side: "BUY", Price: 0.5, OriginalSize: 5,
		Status: "CANCELED", Owner: "",
	})

	select {
	case ev := <-ch:
		t.Fatalf("expected no event for already-finalized, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestOnOrderEvent_LIVEPublishesAccepted(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}

	exec.onOrderEvent(&sdkmodel.WSOrder{
		Id: "ord-live", Market: "m1", AssetId: "tk1",
		Side: "BUY", Price: 0.5, OriginalSize: 1,
		Status: "LIVE", Owner: "",
	})

	ev := mustRecvExecutionEvent(t, ch)
	if ev.Status != core.ExecutionStatusAccepted {
		t.Fatalf("expected accepted, got %s", ev.Status)
	}
	if ev.OrderID != "ord-live" {
		t.Fatalf("expected ord-live, got %s", ev.OrderID)
	}
}

// ------------------------------------------------------------
// onTradeEvent branches
// ------------------------------------------------------------

func TestOnTradeEvent_NilSafe(t *testing.T) {
	exec := &Executor{}
	exec.onTradeEvent(nil)
}

func TestOnTradeEvent_NoFills(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}

	// Empty TakerOrderId and no makers → no fills
	exec.onTradeEvent(&sdkmodel.WSTrade{
		Id: "tr", Market: "m", Status: "MINED",
	})

	select {
	case ev := <-ch:
		t.Fatalf("expected no event, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestOnTradeEvent_MakerOrderFillsPublished(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}

	// Pre-track a maker order so we can verify fields
	exec.getOrCreateTracked("maker-1")

	exec.onTradeEvent(&sdkmodel.WSTrade{
		Id: "trade-1", Market: "m1", Status: "MINED",
		TakerOrderId: "", // skip taker
		MakerOrders: []sdkmodel.WSMakerOrder{{
			OrderId:       "maker-1",
			AssetId:       "tk-maker",
			Owner:         "",
			Side:          "SELL",
			Price:         0.4,
			MatchedAmount: 2,
		}},
	})

	ev := mustRecvExecutionEvent(t, ch)
	if ev.OrderID != "maker-1" {
		t.Fatalf("expected maker-1, got %s", ev.OrderID)
	}
	if ev.Status != core.ExecutionStatusPartiallyFilled || ev.FilledSize != 2 {
		t.Fatalf("unexpected fill: %+v", ev)
	}
}

func TestOnTradeEvent_MakerEmptyOrderIDSkipped(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}

	exec.onTradeEvent(&sdkmodel.WSTrade{
		Id: "trade-empty", Market: "m1", Status: "MINED",
		MakerOrders: []sdkmodel.WSMakerOrder{{
			OrderId: "   ", Side: "SELL", MatchedAmount: 1,
		}},
	})

	select {
	case ev := <-ch:
		t.Fatalf("expected no event, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestOnTradeEvent_MakerForeignOwnerSkipped(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()

	cfg := sdk.DefaultConfig()
	cfg.Polymarket.CLOBCreds = &sdkmodel.ApiKeyCreds{Key: "myKey"}
	exec := &Executor{Bus: bus, Config: cfg}

	exec.onTradeEvent(&sdkmodel.WSTrade{
		Id: "trade-foreign", Market: "m1", Status: "MINED",
		MakerOrders: []sdkmodel.WSMakerOrder{{
			OrderId: "maker-foreign", Owner: "otherKey",
			Side: "SELL", MatchedAmount: 1,
		}},
	})

	select {
	case ev := <-ch:
		t.Fatalf("expected no event for foreign maker, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestOnTradeEvent_UnknownOrderReconcile(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	_ = bus.Subscribe()
	var fired atomic.Int32
	exec := &Executor{Bus: bus, Reconcile: func() { fired.Add(1) }}

	exec.onTradeEvent(&sdkmodel.WSTrade{
		Id: "trade-recon", Market: "m1", Status: "MINED",
		TakerOrderId: "external-1",
		AssetId:      "tk1",
		Side:         "BUY",
		Price:        0.5,
		Size:         2,
		Owner:        "",
	})

	deadline := time.After(2 * time.Second)
	for fired.Load() < 1 {
		select {
		case <-deadline:
			t.Fatalf("expected reconcile fired, got %d", fired.Load())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestOnTradeEvent_FinalizedOrderSkipped(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}

	tr := exec.getOrCreateTracked("ord-final")
	tr.Finalized = true

	exec.onTradeEvent(&sdkmodel.WSTrade{
		Id: "trade-final", Market: "m", AssetId: "tk",
		Side: "BUY", Price: 0.5, Size: 2,
		Status: "MINED", TakerOrderId: "ord-final", Owner: "",
	})

	select {
	case ev := <-ch:
		t.Fatalf("expected no event for finalized order, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

// ------------------------------------------------------------
// handleTradeEvent dispatch
// ------------------------------------------------------------

func TestHandleTradeEvent_ParseError(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}

	exec.handleTradeEvent(sdk.TradeEvent{
		ParseErr: errExample{},
	})

	select {
	case ev := <-ch:
		t.Fatalf("expected no event on parse error, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

type errExample struct{}

func (errExample) Error() string { return "parse fail" }

func TestHandleTradeEvent_OrderRoute(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}

	exec.handleTradeEvent(sdk.TradeEvent{
		EventType: sdk.TradeEventTypeOrder,
		Order: &sdkmodel.WSOrder{
			Id: "ord-route", Market: "m", AssetId: "tk",
			Side: "BUY", Price: 0.5, OriginalSize: 1,
			Status: "LIVE", Owner: "",
		},
	})

	ev := mustRecvExecutionEvent(t, ch)
	if ev.Status != core.ExecutionStatusAccepted {
		t.Fatalf("expected accepted, got %s", ev.Status)
	}
}

func TestHandleTradeEvent_OrderRouteNil(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}

	exec.handleTradeEvent(sdk.TradeEvent{
		EventType: sdk.TradeEventTypeOrder,
		Order:     nil,
	})
	select {
	case ev := <-ch:
		t.Fatalf("expected no event, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHandleTradeEvent_TradeRoute(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}

	exec.handleTradeEvent(sdk.TradeEvent{
		EventType: sdk.TradeEventTypeTrade,
		Trade: &sdkmodel.WSTrade{
			Id: "tr-route", Market: "m", AssetId: "tk",
			Side: "BUY", Price: 0.5, Size: 2,
			Status: "MINED", TakerOrderId: "ord-tr-route", Owner: "",
		},
	})

	ev := mustRecvExecutionEvent(t, ch)
	if ev.Status != core.ExecutionStatusPartiallyFilled {
		t.Fatalf("expected partial fill, got %s", ev.Status)
	}
}

func TestHandleTradeEvent_TradeRouteNil(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}

	exec.handleTradeEvent(sdk.TradeEvent{
		EventType: sdk.TradeEventTypeTrade,
		Trade:     nil,
	})
	select {
	case ev := <-ch:
		t.Fatalf("expected no event, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

// ------------------------------------------------------------
// (Init tests live in init_norace_test.go — guarded by !race because the
//  SDK's *TradeMonitor has a known race between Run (writes tm.ws) and
//  Close (reads tm.ws). That race is in production SDK code we can't modify
//  here, so the Init function is covered only under non-race builds.)
// ------------------------------------------------------------

// ------------------------------------------------------------
// submitPlacements / submitCancels error paths
//
// We can't mock *sdk.PolymarketClient without changing production code, but we
// can build a real client with cfg.Polymarket.ChainID=0 — CreateOrder/PostOrder
// fail fast with deterministic errors. Submit* calls then exercise the
// rejection-publish branches end-to-end.
// ------------------------------------------------------------

// makeBadClient returns a *sdk.PolymarketClient where CreateOrder returns
// "chainID cannot be empty" and PostOrder/Orders/CancelOrder return "creds
// cannot be empty" — deterministic error paths exercising rejection branches.
func makeBadClient() *sdk.PolymarketClient {
	cfg := sdk.DefaultConfig()
	cfg.Polymarket.ChainID = 0
	return sdk.NewClient(cfg)
}

// fakeCLOBHandler returns an http.Handler emulating Polymarket CLOB endpoints
// for tick-size, neg-risk, /order, /orders, DELETE /order, DELETE /orders.
// Each endpoint's response is controlled by the passed-in closures.
type fakeCLOBResponses struct {
	tickSize     string // e.g. "0.01"
	negRisk      bool
	postOrder    string // body returned by POST /order
	postOrders   string // body returned by POST /orders
	deleteOrder  string // body returned by DELETE /order
	deleteOrders string // body returned by DELETE /orders
	postStatus   int    // override (default 200)
}

func newFakeCLOBServer(r fakeCLOBResponses) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.HasPrefix(req.URL.Path, "/tick-size"):
			ts := r.tickSize
			if ts == "" {
				ts = "0.01"
			}
			_, _ = w.Write([]byte(`{"minimum_tick_size":"` + ts + `"}`))
		case strings.HasPrefix(req.URL.Path, "/neg-risk"):
			val := "false"
			if r.negRisk {
				val = "true"
			}
			_, _ = w.Write([]byte(`{"neg_risk":` + val + `}`))
		case req.URL.Path == "/order" && req.Method == http.MethodPost:
			if r.postStatus > 0 {
				w.WriteHeader(r.postStatus)
			}
			body := r.postOrder
			if body == "" {
				body = `{"orderID":"server-ord-1"}`
			}
			_, _ = w.Write([]byte(body))
		case req.URL.Path == "/orders" && req.Method == http.MethodPost:
			if r.postStatus > 0 {
				w.WriteHeader(r.postStatus)
			}
			body := r.postOrders
			if body == "" {
				body = `[{"orderID":"server-ord-1"}, {"orderID":"server-ord-2"}]`
			}
			_, _ = w.Write([]byte(body))
		case req.URL.Path == "/order" && req.Method == http.MethodDelete:
			body := r.deleteOrder
			if body == "" {
				body = `{"canceled":["o-1"]}`
			}
			_, _ = w.Write([]byte(body))
		case req.URL.Path == "/orders" && req.Method == http.MethodDelete:
			body := r.deleteOrders
			if body == "" {
				body = `{"canceled":["o-1","o-2"]}`
			}
			_, _ = w.Write([]byte(body))
		default:
			http.Error(w, "not found: "+req.URL.Path, http.StatusNotFound)
		}
	}))
}

// makeGoodClient returns a *sdk.PolymarketClient with a fake CLOB server URL
// and valid creds — CreateOrder, PostOrder, PostOrders, Cancel* all reach the
// HTTP layer and receive the configured canned responses.
func makeGoodClient(t *testing.T, srv *httptest.Server) *sdk.PolymarketClient {
	t.Helper()
	cfg := sdk.DefaultConfig()
	cfg.Polymarket.ClobBaseURL = srv.URL
	cfg.Polymarket.CLOBCreds = &sdkmodel.ApiKeyCreds{
		Key:        "fake-key",
		Secret:     "ZmFrZS1zZWNyZXQ=", // base64 of "fake-secret"
		Passphrase: "fake-pass",
	}
	return sdk.NewClient(cfg)
}

func TestSubmitPlacements_Empty(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, Client: makeBadClient()}
	exec.submitPlacements(nil)
	exec.submitPlacements([]runtime.OrderIntent{})
	select {
	case ev := <-ch:
		t.Fatalf("expected no event, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSubmitPlacements_CreateOrderFails(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	// With ChainID=0, every CreateOrder returns error → all intents rejected
	exec := &Executor{Bus: bus, Client: makeBadClient()}
	exec.submitPlacements([]runtime.OrderIntent{
		{IntentID: "i1", MarketID: "m", TokenID: "tk1", Price: 0.5, Size: 1, Side: orders.BUY},
		{IntentID: "i2", MarketID: "m", TokenID: "tk2", Price: 0.5, Size: 1, Side: orders.BUY},
	})
	for i := 0; i < 2; i++ {
		ev := mustRecvExecutionEvent(t, ch)
		if ev.Status != core.ExecutionStatusRejected {
			t.Fatalf("expected rejected, got %s", ev.Status)
		}
	}
}

func TestSubmitCancels_Empty(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, Client: makeBadClient()}
	exec.submitCancels(nil)
	exec.submitCancels([]runtime.OrderIntent{})
	select {
	case ev := <-ch:
		t.Fatalf("expected no event, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSubmitCancels_SingleErrorPath(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	// With ChainID=0, no CLOB creds either → CancelOrder returns "creds cannot be empty"
	exec := &Executor{Bus: bus, Client: makeBadClient()}
	exec.submitCancels([]runtime.OrderIntent{
		{Action: runtime.OrderIntentActionCancel, OrderID: "o-1"},
	})
	ev := mustRecvExecutionEvent(t, ch)
	if ev.Status != core.ExecutionStatusRejected {
		t.Fatalf("expected rejected, got %s", ev.Status)
	}
}

func TestSubmitCancels_BatchErrorPath(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, Client: makeBadClient()}
	exec.submitCancels([]runtime.OrderIntent{
		{Action: runtime.OrderIntentActionCancel, OrderID: "o-1"},
		{Action: runtime.OrderIntentActionCancel, OrderID: "o-2"},
	})
	for i := 0; i < 2; i++ {
		ev := mustRecvExecutionEvent(t, ch)
		if ev.Status != core.ExecutionStatusRejected {
			t.Fatalf("expected rejected, got %s", ev.Status)
		}
	}
}

// ------------------------------------------------------------
// submitPlacements with fake CLOB server (single + batch success/error)
// ------------------------------------------------------------

func TestSubmitPlacements_SingleSuccess(t *testing.T) {
	srv := newFakeCLOBServer(fakeCLOBResponses{
		tickSize:  "0.01",
		negRisk:   false,
		postOrder: `{"orderID":"server-ord-single"}`,
	})
	defer srv.Close()

	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, Client: makeGoodClient(t, srv), OrderType: orders.GTC}

	exec.submitPlacements([]runtime.OrderIntent{{
		IntentID: "i1", MarketID: "m", TokenID: "123456789",
		Price: 0.5, Size: 1, Side: orders.BUY,
	}})

	ev := mustRecvExecutionEvent(t, ch)
	if ev.Status != core.ExecutionStatusAccepted {
		t.Fatalf("expected accepted, got %s", ev.Status)
	}
	if ev.OrderID != "server-ord-single" {
		t.Fatalf("expected server-ord-single, got %s", ev.OrderID)
	}
}

func TestSubmitPlacements_SingleEmptyOrderID(t *testing.T) {
	srv := newFakeCLOBServer(fakeCLOBResponses{
		tickSize:  "0.01",
		postOrder: `{"orderID":""}`,
	})
	defer srv.Close()

	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, Client: makeGoodClient(t, srv), OrderType: orders.GTC}

	exec.submitPlacements([]runtime.OrderIntent{{
		IntentID: "i-empty", MarketID: "m", TokenID: "123456789",
		Price: 0.5, Size: 1, Side: orders.BUY,
	}})

	ev := mustRecvExecutionEvent(t, ch)
	if ev.Status != core.ExecutionStatusRejected {
		t.Fatalf("expected rejected, got %s", ev.Status)
	}
	if ev.Reason != "post order failed: empty order id" {
		t.Fatalf("unexpected reason: %s", ev.Reason)
	}
}

func TestSubmitPlacements_SingleErrorMsg(t *testing.T) {
	srv := newFakeCLOBServer(fakeCLOBResponses{
		tickSize:  "0.01",
		postOrder: `{"errorMsg":"insufficient allowance"}`,
	})
	defer srv.Close()

	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, Client: makeGoodClient(t, srv), OrderType: orders.GTC}

	exec.submitPlacements([]runtime.OrderIntent{{
		IntentID: "i-err", MarketID: "m", TokenID: "123456789",
		Price: 0.5, Size: 1, Side: orders.BUY,
	}})

	ev := mustRecvExecutionEvent(t, ch)
	if ev.Status != core.ExecutionStatusRejected {
		t.Fatalf("expected rejected, got %s", ev.Status)
	}
	if ev.Reason != "post order failed: insufficient allowance" {
		t.Fatalf("unexpected reason: %s", ev.Reason)
	}
}

func TestSubmitPlacements_SinglePostError(t *testing.T) {
	srv := newFakeCLOBServer(fakeCLOBResponses{
		tickSize:   "0.01",
		postStatus: 500,
		postOrder:  `boom`,
	})
	defer srv.Close()

	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, Client: makeGoodClient(t, srv), OrderType: orders.GTC}

	exec.submitPlacements([]runtime.OrderIntent{{
		IntentID: "i-post-err", MarketID: "m", TokenID: "123456789",
		Price: 0.5, Size: 1, Side: orders.BUY,
	}})

	ev := mustRecvExecutionEvent(t, ch)
	if ev.Status != core.ExecutionStatusRejected {
		t.Fatalf("expected rejected, got %s", ev.Status)
	}
	if !strings.HasPrefix(ev.Reason, "post order failed:") {
		t.Fatalf("unexpected reason: %s", ev.Reason)
	}
}

func TestSubmitPlacements_BatchSuccess(t *testing.T) {
	srv := newFakeCLOBServer(fakeCLOBResponses{
		tickSize:   "0.01",
		postOrders: `[{"orderID":"server-1"},{"orderID":"server-2"}]`,
	})
	defer srv.Close()

	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, Client: makeGoodClient(t, srv), OrderType: orders.GTC}

	exec.submitPlacements([]runtime.OrderIntent{
		{IntentID: "i1", MarketID: "m", TokenID: "111", Price: 0.5, Size: 1, Side: orders.BUY},
		{IntentID: "i2", MarketID: "m", TokenID: "222", Price: 0.6, Size: 2, Side: orders.SELL},
	})

	for i := 0; i < 2; i++ {
		ev := mustRecvExecutionEvent(t, ch)
		if ev.Status != core.ExecutionStatusAccepted {
			t.Fatalf("expected accepted, got %s", ev.Status)
		}
	}
}

func TestSubmitPlacements_BatchPostError(t *testing.T) {
	srv := newFakeCLOBServer(fakeCLOBResponses{
		tickSize:   "0.01",
		postStatus: 500,
		postOrders: `bang`,
	})
	defer srv.Close()

	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, Client: makeGoodClient(t, srv), OrderType: orders.GTC}

	exec.submitPlacements([]runtime.OrderIntent{
		{IntentID: "i1", MarketID: "m", TokenID: "111", Price: 0.5, Size: 1, Side: orders.BUY},
		{IntentID: "i2", MarketID: "m", TokenID: "222", Price: 0.6, Size: 1, Side: orders.SELL},
	})

	for i := 0; i < 2; i++ {
		ev := mustRecvExecutionEvent(t, ch)
		if ev.Status != core.ExecutionStatusRejected {
			t.Fatalf("expected rejected, got %s", ev.Status)
		}
		if !strings.HasPrefix(ev.Reason, "post orders failed:") {
			t.Fatalf("unexpected reason: %s", ev.Reason)
		}
	}
}

// ------------------------------------------------------------
// submitCancels happy paths with fake server
// ------------------------------------------------------------

func TestSubmitCancels_SingleSuccess(t *testing.T) {
	srv := newFakeCLOBServer(fakeCLOBResponses{})
	defer srv.Close()

	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, Client: makeGoodClient(t, srv)}

	exec.submitCancels([]runtime.OrderIntent{
		{Action: runtime.OrderIntentActionCancel, OrderID: "o-x"},
	})

	// On success, no event is published — verify that
	select {
	case ev := <-ch:
		t.Fatalf("expected no event on success, got %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSubmitCancels_BatchSuccess(t *testing.T) {
	srv := newFakeCLOBServer(fakeCLOBResponses{})
	defer srv.Close()

	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, Client: makeGoodClient(t, srv)}

	exec.submitCancels([]runtime.OrderIntent{
		{Action: runtime.OrderIntentActionCancel, OrderID: "o-1"},
		{Action: runtime.OrderIntentActionCancel, OrderID: "o-2"},
	})

	select {
	case ev := <-ch:
		t.Fatalf("expected no event on batch cancel success, got %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

// ------------------------------------------------------------
// submitSplits / submitMerges error paths
//
// relayClient.SplitTokens("", amount, false) returns "conditionId is empty" —
// triggers the error branch deterministically without HTTP.
// ------------------------------------------------------------

// makeRelayClient builds a real RelayClient with the SDK default config (its
// SplitTokens/MergeTokens return errors for empty conditionId without HTTP).
func makeRelayClient(t *testing.T) *relayer.RelayClient {
	t.Helper()
	cfg := sdk.DefaultConfig()
	p := cfg.Polymarket
	return relayer.NewRelayClient(p.RelayerBaseURL, p.OwnerKey, p.ChainID, p.BuilderCreds, nil, p.RelayerKey)
}

func TestSubmitSplits_Empty(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, relayClient: makeRelayClient(t)}
	exec.submitSplits(nil)
	exec.submitSplits([]runtime.OrderIntent{})
	select {
	case ev := <-ch:
		t.Fatalf("expected no event, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSubmitSplits_ErrorPath(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, relayClient: makeRelayClient(t)}

	// MarketID="" → SplitTokens returns "conditionId is empty"
	exec.submitSplits([]runtime.OrderIntent{{
		Action: runtime.OrderIntentActionSplit,
		Size:   5,
		Tokens: []string{"a", "b"},
	}})

	// Expect 2 Accepted (pre-publish) + 2 Rejected (after error)
	var accepted, rejected int
	deadline := time.After(2 * time.Second)
	for accepted+rejected < 4 {
		select {
		case ev := <-ch:
			ee := ev.Data.(core.ExecutionEvent)
			switch ee.Status {
			case core.ExecutionStatusAccepted:
				accepted++
			case core.ExecutionStatusRejected:
				rejected++
			default:
				t.Fatalf("unexpected status %s", ee.Status)
			}
		case <-deadline:
			t.Fatalf("timeout: accepted=%d rejected=%d", accepted, rejected)
		}
	}
	if accepted != 2 || rejected != 2 {
		t.Fatalf("expected 2/2, got accepted=%d rejected=%d", accepted, rejected)
	}
}

func TestSubmitMerges_Empty(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, relayClient: makeRelayClient(t)}
	exec.submitMerges(nil)
	exec.submitMerges([]runtime.OrderIntent{})
	select {
	case ev := <-ch:
		t.Fatalf("expected no event, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

// makeRelayServer returns an httptest.Server emulating the Polymarket relayer
// endpoints used by EexecuteSafeTransactions. The submitState parameter
// controls the "state" field in the /submit response.
func makeRelayServer(t *testing.T, submitState string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.HasPrefix(req.URL.Path, "/deployed"):
			_, _ = w.Write([]byte(`{"deployed":true}`))
		case strings.HasPrefix(req.URL.Path, "/nonce"):
			_, _ = w.Write([]byte(`{"nonce":"0"}`))
		case strings.HasPrefix(req.URL.Path, "/submit"):
			body := `{"transactionID":"tx-1","state":"` + submitState + `","hash":"0xabc"}`
			_, _ = w.Write([]byte(body))
		default:
			http.Error(w, "not found: "+req.URL.Path, http.StatusNotFound)
		}
	}))
}

// makeRelayClientWithURL builds a RelayClient pointing at a custom URL.
func makeRelayClientWithURL(t *testing.T, url string) *relayer.RelayClient {
	t.Helper()
	cfg := sdk.DefaultConfig()
	p := cfg.Polymarket
	return relayer.NewRelayClient(url, p.OwnerKey, p.ChainID, p.BuilderCreds, nil, p.RelayerKey)
}

func TestSubmitSplits_SuccessState(t *testing.T) {
	srv := makeRelayServer(t, "STATE_NEW")
	defer srv.Close()

	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, relayClient: makeRelayClientWithURL(t, srv.URL)}

	exec.submitSplits([]runtime.OrderIntent{{
		Action:   runtime.OrderIntentActionSplit,
		MarketID: "0x0000000000000000000000000000000000000000000000000000000000000001",
		Size:     5,
		Tokens:   []string{"a", "b"},
	}})

	// Expect 2 Accepted (pre-publish) + 2 Filled (after STATE_NEW)
	var accepted, filled int
	deadline := time.After(3 * time.Second)
	for accepted+filled < 4 {
		select {
		case ev := <-ch:
			ee := ev.Data.(core.ExecutionEvent)
			switch ee.Status {
			case core.ExecutionStatusAccepted:
				accepted++
			case core.ExecutionStatusFilled:
				filled++
			default:
				t.Fatalf("unexpected status %s", ee.Status)
			}
		case <-deadline:
			t.Fatalf("timeout: accepted=%d filled=%d", accepted, filled)
		}
	}
	if accepted != 2 || filled != 2 {
		t.Fatalf("expected 2/2, got accepted=%d filled=%d", accepted, filled)
	}
}

func TestSubmitSplits_StateNotNewPath(t *testing.T) {
	srv := makeRelayServer(t, "STATE_FAILED")
	defer srv.Close()

	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, relayClient: makeRelayClientWithURL(t, srv.URL)}

	exec.submitSplits([]runtime.OrderIntent{{
		Action:   runtime.OrderIntentActionSplit,
		MarketID: "0x0000000000000000000000000000000000000000000000000000000000000001",
		Size:     5,
		Tokens:   []string{"a", "b"},
	}})

	// Expect 2 Accepted (pre-publish) + 2 Rejected (state != STATE_NEW)
	var accepted, rejected int
	deadline := time.After(3 * time.Second)
	for accepted+rejected < 4 {
		select {
		case ev := <-ch:
			ee := ev.Data.(core.ExecutionEvent)
			switch ee.Status {
			case core.ExecutionStatusAccepted:
				accepted++
			case core.ExecutionStatusRejected:
				rejected++
			default:
				t.Fatalf("unexpected status %s", ee.Status)
			}
		case <-deadline:
			t.Fatalf("timeout: accepted=%d rejected=%d", accepted, rejected)
		}
	}
	if accepted != 2 || rejected != 2 {
		t.Fatalf("expected 2/2, got accepted=%d rejected=%d", accepted, rejected)
	}
}

func TestSubmitMerges_SuccessState(t *testing.T) {
	srv := makeRelayServer(t, "STATE_NEW")
	defer srv.Close()

	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, relayClient: makeRelayClientWithURL(t, srv.URL)}

	exec.submitMerges([]runtime.OrderIntent{{
		Action:   runtime.OrderIntentActionMerge,
		MarketID: "0x0000000000000000000000000000000000000000000000000000000000000001",
		Size:     3,
		Tokens:   []string{"a", "b"},
	}})

	var accepted, filled int
	deadline := time.After(3 * time.Second)
	for accepted+filled < 4 {
		select {
		case ev := <-ch:
			ee := ev.Data.(core.ExecutionEvent)
			switch ee.Status {
			case core.ExecutionStatusAccepted:
				accepted++
			case core.ExecutionStatusFilled:
				filled++
			default:
				t.Fatalf("unexpected status %s", ee.Status)
			}
		case <-deadline:
			t.Fatalf("timeout: accepted=%d filled=%d", accepted, filled)
		}
	}
	if accepted != 2 || filled != 2 {
		t.Fatalf("expected 2/2, got accepted=%d filled=%d", accepted, filled)
	}
}

func TestSubmitMerges_StateNotNewPath(t *testing.T) {
	srv := makeRelayServer(t, "STATE_FAILED")
	defer srv.Close()

	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, relayClient: makeRelayClientWithURL(t, srv.URL)}

	exec.submitMerges([]runtime.OrderIntent{{
		Action:   runtime.OrderIntentActionMerge,
		MarketID: "0x0000000000000000000000000000000000000000000000000000000000000001",
		Size:     3,
		Tokens:   []string{"a", "b"},
	}})

	var accepted, rejected int
	deadline := time.After(3 * time.Second)
	for accepted+rejected < 4 {
		select {
		case ev := <-ch:
			ee := ev.Data.(core.ExecutionEvent)
			switch ee.Status {
			case core.ExecutionStatusAccepted:
				accepted++
			case core.ExecutionStatusRejected:
				rejected++
			default:
				t.Fatalf("unexpected status %s", ee.Status)
			}
		case <-deadline:
			t.Fatalf("timeout: accepted=%d rejected=%d", accepted, rejected)
		}
	}
	if accepted != 2 || rejected != 2 {
		t.Fatalf("expected 2/2, got accepted=%d rejected=%d", accepted, rejected)
	}
}

func TestSubmitMerges_ErrorPath(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, relayClient: makeRelayClient(t)}

	// MarketID="" → MergeTokens returns "conditionId is empty"
	exec.submitMerges([]runtime.OrderIntent{{
		Action: runtime.OrderIntentActionMerge,
		Size:   3,
		Tokens: []string{"a", "b"},
	}})

	// Expect 2 Accepted (pre-publish) + 2 Rejected (after error)
	var accepted, rejected int
	deadline := time.After(2 * time.Second)
	for accepted+rejected < 4 {
		select {
		case ev := <-ch:
			ee := ev.Data.(core.ExecutionEvent)
			switch ee.Status {
			case core.ExecutionStatusAccepted:
				accepted++
			case core.ExecutionStatusRejected:
				rejected++
			default:
				t.Fatalf("unexpected status %s", ee.Status)
			}
		case <-deadline:
			t.Fatalf("timeout: accepted=%d rejected=%d", accepted, rejected)
		}
	}
	if accepted != 2 || rejected != 2 {
		t.Fatalf("expected 2/2, got accepted=%d rejected=%d", accepted, rejected)
	}
}

// ------------------------------------------------------------
// consumeTradeEvents
// ------------------------------------------------------------

func TestConsumeTradeEvents_NilMonitor(t *testing.T) {
	exec := &Executor{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		exec.consumeTradeEvents(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expected immediate return on nil monitor")
	}
}

func TestConsumeTradeEvents_CancelExits(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	cfg := sdk.DefaultConfig()
	tm := sdk.NewTradeMonitor(cfg.Polymarket.ClobWSBaseURL, cfg.Polymarket.CLOBCreds)
	exec := &Executor{Bus: bus, TradeMonitor: tm}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		exec.consumeTradeEvents(ctx)
		close(done)
	}()

	// Give the loop time to subscribe before cancelling
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("consumeTradeEvents did not exit on cancel")
	}
}

// TestConsumeTradeEvents_DispatchesEvents pushes raw event bytes through the
// TradeMonitor's OnMessage method and verifies consumeTradeEvents forwards
// them to handleTradeEvent (and ultimately publishes execution events).
func TestConsumeTradeEvents_DispatchesEvents(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()

	cfg := sdk.DefaultConfig()
	tm := sdk.NewTradeMonitor(cfg.Polymarket.ClobWSBaseURL, cfg.Polymarket.CLOBCreds)
	exec := &Executor{Bus: bus, TradeMonitor: tm}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		exec.consumeTradeEvents(ctx)
		close(done)
	}()

	// Inject an order event via OnMessage — TradeMonitor.handleMessage parses
	// it and emits to eventCh, which consumeTradeEvents reads.
	orderMsg := `{
        "event_type":"order",
        "id":"ord-injected",
        "market":"m",
        "asset_id":"tk",
        "side":"BUY",
        "price":"0.5",
        "original_size":"1",
        "status":"LIVE",
        "timestamp":"0",
        "owner":""
    }`
	tm.OnMessage([]byte(orderMsg))

	// Expect Accepted publish from onOrderEvent("LIVE")
	select {
	case ev := <-ch:
		ee := ev.Data.(core.ExecutionEvent)
		if ee.Status != core.ExecutionStatusAccepted {
			t.Fatalf("expected accepted, got %s", ee.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for dispatched event")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("consumeTradeEvents did not exit")
	}
}

// ------------------------------------------------------------
// consumeExecuteQueue routing (with worker)
// ------------------------------------------------------------

func TestConsumeExecuteQueue_RoutesAllActionTypes(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{
		Bus:         bus,
		Client:      makeBadClient(),
		relayClient: makeRelayClient(t),
		queue:       make(chan []runtime.OrderIntent, 4),
	}

	// Push a mixed batch covering all 4 action types — verifies routing
	// switch in consumeExecuteQueue dispatches each to the right submit method.
	exec.queue <- []runtime.OrderIntent{
		{Action: runtime.OrderIntentActionPlace, IntentID: "p1", MarketID: "m", TokenID: "tk", Price: 0.5, Size: 1, Side: orders.BUY},
		{Action: runtime.OrderIntentActionCancel, OrderID: "c1"},
		{Action: runtime.OrderIntentActionSplit, Size: 2, Tokens: []string{"a", "b"}},
		{Action: runtime.OrderIntentActionMerge, Size: 1, Tokens: []string{"a", "b"}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		exec.consumeExecuteQueue(ctx)
		close(done)
	}()

	// Wait for at least 1 placement+cancel rejection + 2 split rejections + 2 merge rejections = 6 events
	// (plus 2 accepted for split + 2 accepted for merge = 4 accepted = 10 events total)
	// Actually: placements=1 rejection, cancels=1 rejection, splits=2 accepted+2 rejected, merges=2 accepted+2 rejected = 10
	count := 0
	deadline := time.After(3 * time.Second)
	for count < 10 {
		select {
		case <-ch:
			count++
		case <-deadline:
			break
		}
		if count == 10 {
			break
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not exit")
	}

	if count < 10 {
		t.Fatalf("expected >=10 events, got %d", count)
	}
}
