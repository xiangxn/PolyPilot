package state

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tidwall/gjson"
	"github.com/xiangxn/go-polymarket-sdk/orders"
)

// triggerExchangeClient implements ExchangeStateClient with call counts and error toggles
// for testing the reconcile retry/loop paths.
type triggerExchangeClient struct {
	mu          sync.Mutex
	openOrders  []orders.OpenOrder
	positions   *gjson.Result
	openErr     error
	positionErr error
	calls       atomic.Int32
	failTimes   int // fail this many initial calls, then succeed
}

func (c *triggerExchangeClient) GetOpenOrders() ([]orders.OpenOrder, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls.Add(1)
	if c.failTimes > 0 {
		c.failTimes--
		return nil, errors.New("transient")
	}
	if c.openErr != nil {
		return nil, c.openErr
	}
	return c.openOrders, nil
}

func (c *triggerExchangeClient) GetPositions() (*gjson.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.positionErr != nil {
		return nil, c.positionErr
	}
	return c.positions, nil
}

func (c *triggerExchangeClient) Redeem(_ context.Context, _ func([]string)) {}

func TestReconcile_MissingClient_ReturnsErr(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	rep := s.ReconcileWithExchange(context.Background())
	if rep.Err == nil {
		t.Fatal("expected error with no client")
	}
}

// positionsOnlyErrClient returns ok for openOrders but errors on GetPositions
type positionsOnlyErrClient struct{}

func (positionsOnlyErrClient) GetOpenOrders() ([]orders.OpenOrder, error) {
	return nil, nil
}

func (positionsOnlyErrClient) GetPositions() (*gjson.Result, error) {
	return nil, errors.New("positions failed")
}

func (positionsOnlyErrClient) Redeem(_ context.Context, _ func([]string)) {}

func TestReconcile_GetPositionsErrReturnsReportErr(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, positionsOnlyErrClient{})
	rep := s.ReconcileWithExchange(context.Background())
	if rep.Err == nil {
		t.Fatal("expected positions err to surface in report")
	}
}

func TestReconcile_StartLoopDisabledNoOp(t *testing.T) {
	fake := &fakeExchangeClient{}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// disabled → should return immediately
	s.StartReconcileLoop(ctx, ReconcileConfig{Enabled: false, Interval: 10 * time.Millisecond})
}

func TestReconcile_StartLoopNilClientNoOp(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// no restoreClient → should return immediately even if Enabled
	s.StartReconcileLoop(ctx, ReconcileConfig{Enabled: true, Interval: 10 * time.Millisecond})
}

func TestReconcile_StartLoopRunsPeriodically(t *testing.T) {
	fake := &fakeExchangeClient{}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	ctx, cancel := context.WithCancel(context.Background())
	var ran atomic.Int32
	s.StartReconcileLoop(ctx, ReconcileConfig{
		Enabled:  true,
		Interval: 20 * time.Millisecond,
		OnReport: func(rep ReconcileReport) { ran.Add(1) },
	})
	time.Sleep(100 * time.Millisecond)
	cancel()
	if ran.Load() < 2 {
		t.Fatalf("expected multiple runs, got %d", ran.Load())
	}
}

func TestReconcile_StartLoopFiresFromTrigger(t *testing.T) {
	fake := &fakeExchangeClient{}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	ctx, cancel := context.WithCancel(context.Background())
	var ran atomic.Int32
	s.StartReconcileLoop(ctx, ReconcileConfig{
		Enabled:  true,
		Interval: 10 * time.Second, // long interval; rely on trigger
		OnReport: func(rep ReconcileReport) { ran.Add(1) },
	})
	s.TriggerReconcile()
	time.Sleep(80 * time.Millisecond)
	cancel()
	if ran.Load() < 1 {
		t.Fatalf("expected trigger to fire at least once, got %d", ran.Load())
	}
}

func TestReconcile_StartLoopDefaultIntervalWhenZero(t *testing.T) {
	fake := &fakeExchangeClient{}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Interval=0 → default 30s. We don't wait for it; just confirm we can trigger.
	s.StartReconcileLoop(ctx, ReconcileConfig{Enabled: true, Interval: 0})
	// trigger should still work
	s.TriggerReconcile()
	time.Sleep(50 * time.Millisecond)
}

func TestReconcile_RunReconcileWithRetry_RetryBackoffSucceeds(t *testing.T) {
	client := &triggerExchangeClient{failTimes: 2}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, client)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var lastReport atomic.Pointer[ReconcileReport]
	var reportedCount atomic.Int32

	s.StartReconcileLoop(ctx, ReconcileConfig{
		Enabled:      true,
		Interval:     10 * time.Second,
		RetryBackoff: []time.Duration{5 * time.Millisecond, 5 * time.Millisecond, 10 * time.Millisecond},
		OnReport: func(rep ReconcileReport) {
			r := rep
			lastReport.Store(&r)
			reportedCount.Add(1)
		},
	})
	s.TriggerReconcile()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if reportedCount.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	rep := lastReport.Load()
	if rep == nil {
		t.Fatal("expected report to be emitted")
	}
	if rep.Err != nil {
		t.Fatalf("expected eventual success, got err: %v", rep.Err)
	}
	if client.calls.Load() < 3 {
		t.Fatalf("expected at least 3 calls (2 fail + 1 succeed), got %d", client.calls.Load())
	}
}

func TestReconcile_RunReconcileWithRetry_AllRetriesFail(t *testing.T) {
	// failTimes very large; all attempts fail
	client := &triggerExchangeClient{failTimes: 1000}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, client)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var lastReport atomic.Pointer[ReconcileReport]
	var reportedCount atomic.Int32

	s.StartReconcileLoop(ctx, ReconcileConfig{
		Enabled:      true,
		Interval:     10 * time.Second,
		RetryBackoff: []time.Duration{5 * time.Millisecond, 5 * time.Millisecond},
		OnReport: func(rep ReconcileReport) {
			r := rep
			lastReport.Store(&r)
			reportedCount.Add(1)
		},
	})
	s.TriggerReconcile()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if reportedCount.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	rep := lastReport.Load()
	if rep == nil || rep.Err == nil {
		t.Fatal("expected report with error after all retries")
	}
}

func TestReconcile_RunReconcileWithRetry_ContextCanceledMidRetry(t *testing.T) {
	client := &triggerExchangeClient{failTimes: 1000}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, client)
	ctx, cancel := context.WithCancel(context.Background())
	var lastReport atomic.Pointer[ReconcileReport]

	s.StartReconcileLoop(ctx, ReconcileConfig{
		Enabled:      true,
		Interval:     10 * time.Second,
		RetryBackoff: []time.Duration{200 * time.Millisecond, 200 * time.Millisecond},
		OnReport: func(rep ReconcileReport) {
			r := rep
			lastReport.Store(&r)
		},
	})
	s.TriggerReconcile()
	// cancel quickly before the retry sleeps finish
	time.Sleep(50 * time.Millisecond)
	cancel()
	// give the goroutine time to exit
	time.Sleep(100 * time.Millisecond)
}

func TestReconcile_AttachExternalLocked_SELL(t *testing.T) {
	// Provide matching remote position to prevent reconcilePositions from removing it.
	// Note: reconcileOrders runs FIRST; the SELL reserves 4 of tkA. Then reconcilePositions
	// runs with remote size=10 vs local Available=6+Reserved=4 = 10 → no change.
	pos := gjson.Parse(`[{"asset":"tkA","size":10}]`)
	fake := &fakeExchangeClient{
		openOrders: []orders.OpenOrder{
			{Id: "ext-sell", Market: "m1", AssetId: "tkA", Side: "SELL", Price: 0.6, OriginalSize: 4, SizeMatched: 0},
		},
		positions: &pos,
	}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	s.Restore(Snapshot{
		Balance:  Balance{Available: 100},
		Position: Position{Tokens: map[string]TokenPosition{"tkA": {Available: 10}}},
	})
	rep := s.ReconcileWithExchange(context.Background())
	if rep.OrdersAdded != 1 {
		t.Fatalf("expected 1 added, got %+v", rep)
	}
	tp := s.Snapshot().Position.Tokens["tkA"]
	if tp.Reserved != 4 {
		t.Fatalf("expected reserved=4, got %v (full=%+v)", tp.Reserved, tp)
	}
	if tp.Available != 6 {
		t.Fatalf("expected available=6 after SELL reserve, got %v", tp.Available)
	}
}

func TestReconcile_AttachExternalLocked_InvalidArgs(t *testing.T) {
	// invalid args: price >= 1 → returns ErrReconcileFailed, not added
	fake := &fakeExchangeClient{
		openOrders: []orders.OpenOrder{
			{Id: "ext-bad", Market: "m1", AssetId: "tkA", Side: "BUY", Price: 1.5, OriginalSize: 4, SizeMatched: 0},
		},
	}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	s.Restore(Snapshot{Balance: Balance{Available: 100}})
	rep := s.ReconcileWithExchange(context.Background())
	if rep.OrdersAdded != 0 {
		t.Fatalf("expected 0 added for invalid price, got %+v", rep)
	}
}

func TestReconcile_AttachExternalLocked_InvalidSide(t *testing.T) {
	fake := &fakeExchangeClient{
		openOrders: []orders.OpenOrder{
			{Id: "ext-bad", Market: "m1", AssetId: "tkA", Side: "UNKNOWN", Price: 0.5, OriginalSize: 4, SizeMatched: 0},
		},
	}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	s.Restore(Snapshot{Balance: Balance{Available: 100}})
	rep := s.ReconcileWithExchange(context.Background())
	if rep.OrdersAdded != 0 {
		t.Fatalf("expected 0 added for invalid side, got %+v", rep)
	}
}

func TestReconcile_AttachExternalLocked_IdempotentAlreadyAttached(t *testing.T) {
	fake := &fakeExchangeClient{
		openOrders: []orders.OpenOrder{
			{Id: "o1", Market: "m1", AssetId: "tkA", Side: "BUY", Price: 0.5, OriginalSize: 4, SizeMatched: 0},
		},
	}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	s.Restore(Snapshot{
		Balance: Balance{Available: 100},
		Orders: map[string]OrderReservation{
			"o1": {OrderID: "o1", MarketID: "m1", TokenID: "tkA", Side: orders.BUY, Price: 0.5, RemainingSize: 4, Reserved: 2},
		},
	})
	rep := s.ReconcileWithExchange(context.Background())
	if rep.OrdersAdded != 0 {
		t.Fatalf("expected 0 added (already present), got %+v", rep)
	}
}

func TestReconcile_PositionUpdated_RemoteLarger(t *testing.T) {
	pos := gjson.Parse(`[{"asset":"tkA","size":12}]`)
	fake := &fakeExchangeClient{positions: &pos}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	s.Restore(Snapshot{
		Balance:  Balance{Available: 100},
		Position: Position{Tokens: map[string]TokenPosition{"tkA": {Available: 8}}},
	})
	rep := s.ReconcileWithExchange(context.Background())
	if rep.PositionsUpdated != 1 {
		t.Fatalf("expected 1 updated, got %+v", rep)
	}
	if got := s.Snapshot().Position.Tokens["tkA"].Available; got != 12 {
		t.Fatalf("expected 12, got %v", got)
	}
}

func TestReconcile_PositionUpdated_RemoteSmallerClampsToZero(t *testing.T) {
	// remote says 2, local has 8+4 reserved=12 → diff=-10, available 8-10 = -2 → clamp to 0
	pos := gjson.Parse(`[{"asset":"tkA","size":2}]`)
	fake := &fakeExchangeClient{positions: &pos}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	s.Restore(Snapshot{
		Balance:  Balance{Available: 100},
		Position: Position{Tokens: map[string]TokenPosition{"tkA": {Available: 8, Reserved: 4}}},
	})
	rep := s.ReconcileWithExchange(context.Background())
	if rep.PositionsUpdated != 1 {
		t.Fatalf("expected 1 updated, got %+v", rep)
	}
	if got := s.Snapshot().Position.Tokens["tkA"].Available; got != 0 {
		t.Fatalf("expected clamp to 0, got %v", got)
	}
}

func TestReconcile_OrderRemoteRemainingZeroSkipped(t *testing.T) {
	// When remote returns o1 with OriginalSize-SizeMatched <= 0, the order is in remoteByID
	// (so local-only check doesn't fire), but in add-or-update pass remaining<=0 → skipped.
	// Result: no add, no remove, no update.
	fake := &fakeExchangeClient{
		openOrders: []orders.OpenOrder{
			{Id: "o1", Market: "m1", AssetId: "tk1", Side: "BUY", Price: 0.5, OriginalSize: 10, SizeMatched: 10},
		},
	}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	s.Restore(Snapshot{Balance: Balance{Available: 100}})
	if err := s.AttachOrder("", "o1", "m1", "tk1", orders.BUY, 0.5, 10); err != nil {
		t.Fatal(err)
	}
	rep := s.ReconcileWithExchange(context.Background())
	if rep.OrdersRemoved != 0 || rep.OrdersAdded != 0 || rep.OrdersUpdated != 0 {
		t.Fatalf("expected all zero since remote remaining=0 is treated as no-op, got %+v", rep)
	}
}

func TestReconcile_RemoteSkipsEmptyOrderID(t *testing.T) {
	fake := &fakeExchangeClient{
		openOrders: []orders.OpenOrder{
			{Id: "", Market: "m1", AssetId: "tk1", Side: "BUY", Price: 0.5, OriginalSize: 5, SizeMatched: 0},
			{Id: "o1", Market: "m1", AssetId: "tk1", Side: "BUY", Price: 0.5, OriginalSize: 5, SizeMatched: 0},
		},
	}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	s.Restore(Snapshot{Balance: Balance{Available: 100}})
	rep := s.ReconcileWithExchange(context.Background())
	if rep.OrdersAdded != 1 {
		t.Fatalf("expected 1 added (empty id skipped), got %+v", rep)
	}
}

func TestReconcile_PositionsSkipsEmptyTokenIDsAndZeroSize(t *testing.T) {
	pos := gjson.Parse(`[
		{"asset":"","size":5},
		{"asset":"tk-z","size":0},
		{"asset":"tk-real","size":2}
	]`)
	fake := &fakeExchangeClient{positions: &pos}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	s.Restore(Snapshot{Balance: Balance{Available: 100}})
	rep := s.ReconcileWithExchange(context.Background())
	if rep.PositionsAdded != 1 {
		t.Fatalf("expected 1 added, got %+v", rep)
	}
	if got := s.Snapshot().Position.Tokens["tk-real"].Available; got != 2 {
		t.Fatalf("expected tk-real=2, got %v", got)
	}
}

func TestFirstNonEmptyJSON_AllEmpty(t *testing.T) {
	r := gjson.Parse(`{}`)
	got := firstNonEmptyJSON(r, "a", "b", "c")
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestFirstNonEmptyJSON_PicksFirstNonEmpty(t *testing.T) {
	r := gjson.Parse(`{"a":"","b":"   ","c":"value"}`)
	got := firstNonEmptyJSON(r, "a", "b", "c")
	if got != "value" {
		t.Fatalf("got %q", got)
	}
}

func TestReleaseOrderLocked_BUY_NegativeReservedClamped(t *testing.T) {
	// Directly construct state with mismatched accounting to trigger the clamp branch.
	fake := &fakeExchangeClient{} // no remote orders → all local will be released
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	s.balance.Available = 100
	s.balance.Reserved = 0.001 // tiny, smaller than the order's res.Reserved
	s.orderReservations["o1"] = OrderReservation{
		OrderID: "o1", MarketID: "m1", TokenID: "tk1", Side: orders.BUY,
		Price: 0.5, RemainingSize: 10, Reserved: 5,
	}
	rep := s.ReconcileWithExchange(context.Background())
	if rep.OrdersRemoved != 1 {
		t.Fatalf("expected 1 removed, got %+v", rep)
	}
	if got := s.Snapshot().Balance.Reserved; got < 0 {
		t.Fatalf("Reserved should be clamped to >=0, got %v", got)
	}
}

func TestReleaseOrderLocked_SELL_NegativeReservedClamped(t *testing.T) {
	fake := &fakeExchangeClient{}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	s.balance.Available = 100
	s.position.Tokens["tk1"] = TokenPosition{Available: 2, Reserved: 0.001}
	s.orderReservations["o1"] = OrderReservation{
		OrderID: "o1", MarketID: "m1", TokenID: "tk1", Side: orders.SELL,
		Price: 0.5, RemainingSize: 5, Reserved: 5,
	}
	rep := s.ReconcileWithExchange(context.Background())
	if rep.OrdersRemoved != 1 {
		t.Fatalf("expected 1 removed, got %+v", rep)
	}
	if got := s.Snapshot().Position.Tokens["tk1"].Reserved; got < 0 {
		t.Fatalf("token reserved must be clamped to >=0, got %v", got)
	}
}
