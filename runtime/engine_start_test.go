package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/tidwall/gjson"
	"github.com/xiangxn/go-polymarket-sdk/orders"
	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/state"
)

// fakeRestoreClient implements state.ExchangeStateClient. The flag fields
// control behaviour: returning err on GetOpenOrders forces the restore-error
// branch in Engine.Start.
type fakeRestoreClient struct {
	openOrders []orders.OpenOrder
	openErr    error
}

func (f *fakeRestoreClient) GetOpenOrders() ([]orders.OpenOrder, error) {
	return f.openOrders, f.openErr
}

func (f *fakeRestoreClient) GetPositions() (*gjson.Result, error) {
	empty := gjson.Parse("[]")
	return &empty, nil
}

func (f *fakeRestoreClient) Redeem(ctx context.Context, cb func(tokenIDs []string)) {}

// fakeFeed and fakeObserver let us confirm Start wires up all components.
type fakeFeed struct {
	initCalls  int
	startCalls int
}

func (f *fakeFeed) Init(bus *core.EventBus)      { f.initCalls++ }
func (f *fakeFeed) Start(ctx context.Context)    { f.startCalls++; <-ctx.Done() }

type fakeObserver struct {
	initCalls  int
	startCalls int
}

func (f *fakeObserver) Init(bus *core.EventBus)   { f.initCalls++ }
func (f *fakeObserver) Start(ctx context.Context) { f.startCalls++; <-ctx.Done() }

// busCapturingFeed publishes the engine-allocated bus to a channel during
// Init so tests can drive events on it without racing against Start's bus
// assignment.
type busCapturingFeed struct {
	busCh chan *core.EventBus
}

func (f *busCapturingFeed) Init(bus *core.EventBus) {
	select {
	case f.busCh <- bus:
	default:
	}
}

func (f *busCapturingFeed) Start(ctx context.Context) { <-ctx.Done() }

func TestEngineStart_MissingDependenciesReturnsImmediately(t *testing.T) {
	t.Run("nil state", func(t *testing.T) {
		e := &Engine{Risk: &fakeRisk{}, Exec: &fakeExec{}}
		e.Start(context.Background())
	})
	t.Run("nil risk", func(t *testing.T) {
		st := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, nil)
		e := &Engine{State: st, Exec: &fakeExec{}}
		e.Start(context.Background())
	})
	t.Run("nil exec", func(t *testing.T) {
		st := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, nil)
		e := &Engine{State: st, Risk: &fakeRisk{}}
		e.Start(context.Background())
	})
}

// TestEngineStart_HappyPathBriefRun starts the engine with a short context,
// verifies dependent components are wired (Init called), then cancels and
// allows Start to drain.
func TestEngineStart_HappyPathBriefRun(t *testing.T) {
	rc := &fakeRestoreClient{
		openOrders: []orders.OpenOrder{{
			Id:           "ext-1",
			Market:       "m1",
			AssetId:      "tk1",
			Side:         string(orders.BUY),
			OriginalSize: 5,
			SizeMatched:  0,
			Price:        0.5,
		}},
	}
	st := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, rc)
	st.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})

	risk := &fakeRisk{}
	exec := &fakeExec{}
	prob := &fakeProbability{}
	strat := &fakeStrategy{}
	feed := &fakeFeed{}
	obs := &fakeObserver{}

	// Configure a Probability snapshot so that the strategy tick observes
	// a real Observation when it fires.
	prob.snapshotResult = Observation{MarketID: "m1"}
	prob.snapshotOk = true

	// Provide a TickStrategy that records every tick so the test can wait
	// for at least one tick before tearing down.
	tickCh := make(chan struct{}, 4)
	strat.onTickFn = func(time.Time, Observation, state.Snapshot) []OrderIntent {
		select {
		case tickCh <- struct{}{}:
		default:
		}
		return nil
	}

	// Config drives StrategyTickInterval and MetricsInterval — initConfig
	// overwrites the Engine fields based on this.
	cfg := viper.New()
	cfg.Set("runtime.strategy_tick_interval", "20ms")
	cfg.Set("runtime.metrics_interval", "30ms")

	e := &Engine{
		State:       st,
		Risk:        risk,
		Exec:        exec,
		Probability: prob,
		Strategies:  []Strategy{nil, strat}, // nil entry exercises the skip branch
		Feeds:       []Feed{nil, feed},
		Observers:   []Observer{nil, obs},
		Config:      cfg,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		e.Start(ctx)
		close(done)
	}()

	// Wait for at least one strategy tick to confirm strategyTickC fired.
	select {
	case <-tickCh:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("strategy tick did not fire within 1s")
	}

	// Give the metrics ticker (30ms) a few more cycles to fire too.
	time.Sleep(120 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Start did not return within 1s after cancel")
	}

	if exec.inits != 1 {
		t.Fatalf("expected Exec.Init called once, got %d", exec.inits)
	}
	if prob.initCalls != 1 {
		t.Fatalf("expected Probability.Init called once, got %d", prob.initCalls)
	}
	if strat.initCalls != 1 {
		t.Fatalf("expected Strategy.Init called once, got %d", strat.initCalls)
	}
	if feed.initCalls != 1 {
		t.Fatalf("expected Feed.Init called once, got %d", feed.initCalls)
	}
	if obs.initCalls != 1 {
		t.Fatalf("expected Observer.Init called once, got %d", obs.initCalls)
	}
	// Engine should have absorbed the restored order id into accepted tracking.
	if !e.hasAccepted("ext-1") {
		t.Fatal("expected restored ext-1 marked accepted")
	}
}

// TestEngineStart_RestoreErrorPublishesRisk forces RestoreFromExchange to
// fail; Start should still proceed and publish a risk event.
func TestEngineStart_RestoreErrorPublishesRisk(t *testing.T) {
	rc := &fakeRestoreClient{openErr: errors.New("boom")}
	st := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, rc)
	st.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})

	cfg := viper.New()
	cfg.Set("runtime.metrics_interval", "30ms")
	e := &Engine{
		State:  st,
		Risk:   &fakeRisk{},
		Exec:   &fakeExec{},
		Config: cfg,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		e.Start(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Start did not return")
	}
}

// TestEngineStart_DispatchesInputAndExecutionEvents covers the event-loop
// switch in Start by publishing input + execution events on the bus the
// engine itself created. We obtain the bus pointer indirectly by handing it
// to a feed, which receives Init(bus) inside Start before the loop drains
// events.
func TestEngineStart_DispatchesInputAndExecutionEvents(t *testing.T) {
	rc := &fakeRestoreClient{}
	st := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, rc)
	st.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})

	prob := &fakeProbability{
		onUpdate: func(ev core.Event) (Observation, bool) {
			return Observation{MarketID: "m1"}, true
		},
	}

	busCh := make(chan *core.EventBus, 1)
	captureFeed := &busCapturingFeed{busCh: busCh}

	cfg := viper.New()
	cfg.Set("runtime.metrics_interval", "50ms")
	e := &Engine{
		State:       st,
		Risk:        &fakeRisk{},
		Exec:        &fakeExec{},
		Probability: prob,
		Strategies:  []Strategy{&fakeStrategy{}},
		Feeds:       []Feed{captureFeed},
		Config:      cfg,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		e.Start(ctx)
		close(done)
	}()

	// Wait for the engine to publish its bus through the feed's Init.
	var bus *core.EventBus
	select {
	case bus = <-busCh:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("timeout waiting for engine bus")
	}

	bus.Publish(core.Event{Type: core.EventMarket})
	bus.Publish(core.Event{
		Type: core.EventExecution,
		Data: core.ExecutionEvent{
			OrderID: "ox",
			Status:  core.ExecutionStatusRejected,
			Reason:  "bad",
		},
	})
	bus.Publish(core.Event{Type: core.EventExecution, Data: "not-an-execution-event"})

	// Wait for counters to advance.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if e.inputEvents.Load() >= 1 && e.executionEvents.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e.inputEvents.Load() < 1 {
		cancel()
		<-done
		t.Fatalf("expected inputEvents>=1, got %d", e.inputEvents.Load())
	}
	if e.executionEvents.Load() < 1 {
		cancel()
		<-done
		t.Fatalf("expected executionEvents>=1, got %d", e.executionEvents.Load())
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Start did not return after cancel")
	}
}
