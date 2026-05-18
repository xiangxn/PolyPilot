package runtime

import (
	"errors"
	"testing"
	"time"

	"github.com/xiangxn/go-polymarket-sdk/orders"
	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/state"
)

// newEngineForTest builds an Engine wired with fakes; State has 1000 USDC
// available so provisional reservations succeed in the place tests.
func newEngineForTest() (*Engine, *fakeProbability, *fakeStrategy, *fakeRisk, *fakeExec, *state.State) {
	bus := core.NewEventBus()
	st := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, nil)
	st.Restore(state.Snapshot{Balance: state.Balance{Available: 1000}})
	prob := &fakeProbability{}
	strat := &fakeStrategy{}
	risk := &fakeRisk{}
	exec := &fakeExec{}
	e := &Engine{
		Bus:         bus,
		State:       st,
		Risk:        risk,
		Exec:        exec,
		Probability: prob,
		Strategies:  []Strategy{strat},
	}
	e.initOrderTracking()
	return e, prob, strat, risk, exec, st
}

// waitForEventType pulls events until one of the requested types arrives or
// the timeout fires. It tolerates other intermediate events.
func waitForEventType(t *testing.T, ch chan core.Event, want core.EventType, timeout time.Duration) core.Event {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("event channel closed before receiving %v", want)
			}
			if ev.Type == want {
				return ev
			}
		case <-deadline.C:
			t.Fatalf("timeout waiting for event %v", want)
		}
	}
}

func TestHandleInputUpdate_NoStrategiesPublishesRisk(t *testing.T) {
	e, _, _, _, _, _ := newEngineForTest()
	e.Strategies = nil
	ch, cancel := e.Bus.SubscribeWithCancel()
	defer cancel()

	e.handleInputUpdate(core.Event{Type: core.EventMarket})

	ev := waitForEventType(t, ch, core.EventRisk, time.Second)
	if r, ok := ev.Data.(core.RiskEvent); !ok || r.Reason == "" {
		t.Fatalf("expected RiskEvent with reason, got %#v", ev.Data)
	}
}

func TestHandleInputUpdate_ProbabilityNilPublishesRisk(t *testing.T) {
	e, _, _, _, _, _ := newEngineForTest()
	e.Probability = nil
	ch, cancel := e.Bus.SubscribeWithCancel()
	defer cancel()

	e.handleInputUpdate(core.Event{Type: core.EventMarket})

	waitForEventType(t, ch, core.EventRisk, time.Second)
}

func TestHandleInputUpdate_ProbabilitySkipReturnsSilently(t *testing.T) {
	e, prob, _, _, exec, _ := newEngineForTest()
	prob.onUpdate = func(ev core.Event) (Observation, bool) {
		return Observation{}, false
	}

	e.handleInputUpdate(core.Event{Type: core.EventMarket})

	if e.ticks.Load() != 0 {
		t.Fatalf("ticks should remain 0, got %d", e.ticks.Load())
	}
	if len(exec.executed) != 0 {
		t.Fatalf("exec should not be called, got %d batches", len(exec.executed))
	}
}

func TestHandleInputUpdate_NoIntentsLeavesExecutorIdle(t *testing.T) {
	e, prob, strat, _, exec, _ := newEngineForTest()
	prob.onUpdate = func(ev core.Event) (Observation, bool) {
		return Observation{MarketID: "m1"}, true
	}
	strat.onUpdateFn = func(core.Event, Observation, state.Snapshot) []OrderIntent {
		return nil
	}

	e.handleInputUpdate(core.Event{Type: core.EventMarket})

	if e.ticks.Load() != 1 {
		t.Fatalf("ticks should be 1, got %d", e.ticks.Load())
	}
	if len(exec.executed) != 0 {
		t.Fatalf("exec should not be called when no intents, got %d", len(exec.executed))
	}
}

func TestHandleInputUpdate_SkipsNilStrategyEntry(t *testing.T) {
	e, prob, strat, _, exec, _ := newEngineForTest()
	e.Strategies = []Strategy{nil, strat}
	prob.onUpdate = func(ev core.Event) (Observation, bool) {
		return Observation{MarketID: "m1"}, true
	}
	strat.onUpdateFn = func(core.Event, Observation, state.Snapshot) []OrderIntent {
		return []OrderIntent{{
			Action:   OrderIntentActionCancel,
			OrderID:  "o-existing",
		}}
	}

	e.handleInputUpdate(core.Event{Type: core.EventMarket})

	if len(exec.executed) != 1 {
		t.Fatalf("expected exec to fire once after skipping nil, got %d", len(exec.executed))
	}
}

func TestHandleInputUpdate_StrategyEmitsIntentExecuteCalled(t *testing.T) {
	e, prob, strat, risk, exec, _ := newEngineForTest()
	prob.onUpdate = func(ev core.Event) (Observation, bool) {
		return Observation{
			MarketID: "m1",
			Tokens: map[string]Token{
				"tk1": {Id: "tk1", AskPrice: 0.6, BidPrice: 0.5},
			},
		}, true
	}
	strat.onUpdateFn = func(e core.Event, o Observation, s state.Snapshot) []OrderIntent {
		return []OrderIntent{{
			MarketID: "m1",
			TokenID:  "tk1",
			Price:    0.5,
			Size:     1,
			Side:     orders.BUY,
		}}
	}

	e.handleInputUpdate(core.Event{Type: core.EventMarket})

	if e.ticks.Load() != 1 {
		t.Fatalf("expected ticks=1, got %d", e.ticks.Load())
	}
	if risk.checks != 1 {
		t.Fatalf("expected risk.Check called once, got %d", risk.checks)
	}
	if len(exec.executed) != 1 || len(exec.executed[0]) != 1 {
		t.Fatalf("expected one batch with one intent, got %+v", exec.executed)
	}
}

func TestHandleInputUpdate_RiskRejectStopsLoop(t *testing.T) {
	e, prob, strat, risk, exec, _ := newEngineForTest()
	risk.err = errors.New("nope")
	prob.onUpdate = func(ev core.Event) (Observation, bool) {
		return Observation{MarketID: "m1"}, true
	}
	strat.onUpdateFn = func(core.Event, Observation, state.Snapshot) []OrderIntent {
		return []OrderIntent{{
			MarketID: "m1",
			TokenID:  "tk1",
			Price:    0.5,
			Size:     1,
			Side:     orders.BUY,
		}}
	}

	e.handleInputUpdate(core.Event{Type: core.EventMarket})

	if len(exec.executed) != 0 {
		t.Fatalf("exec should not fire on risk reject, got %d batches", len(exec.executed))
	}
	if e.riskRejected.Load() == 0 {
		t.Fatal("riskRejected counter should increment")
	}
}

func TestSubmitIntents_EmptyShortCircuit(t *testing.T) {
	e, _, _, risk, exec, st := newEngineForTest()
	if !e.submitIntents(nil, st.Snapshot(), nil) {
		t.Fatal("expected true on empty intents")
	}
	if risk.checks != 0 || len(exec.executed) != 0 {
		t.Fatalf("risk=%d exec=%d", risk.checks, len(exec.executed))
	}
}

func TestSubmitIntents_RiskRejectStopsExec(t *testing.T) {
	e, _, _, risk, exec, st := newEngineForTest()
	risk.err = errors.New("rejected")
	intents := []OrderIntent{{
		MarketID: "m1",
		TokenID:  "tk1",
		Price:    0.5,
		Size:     1,
		Side:     orders.BUY,
	}}
	if e.submitIntents(intents, st.Snapshot(), nil) {
		t.Fatal("expected false on risk reject")
	}
	if len(exec.executed) != 0 {
		t.Fatalf("exec should not be called, got %d", len(exec.executed))
	}
	if e.riskRejected.Load() != 1 {
		t.Fatalf("expected riskRejected=1, got %d", e.riskRejected.Load())
	}
}

func TestSubmitIntents_CancelPassesThroughWithoutProvisional(t *testing.T) {
	e, _, _, _, exec, st := newEngineForTest()
	intents := []OrderIntent{{Action: OrderIntentActionCancel, OrderID: "o1"}}
	if !e.submitIntents(intents, st.Snapshot(), nil) {
		t.Fatal("expected true")
	}
	if len(exec.executed) != 1 || len(exec.executed[0]) != 1 ||
		exec.executed[0][0].Action != OrderIntentActionCancel {
		t.Fatalf("expected cancel passed through, got %+v", exec.executed)
	}
	snap := st.Snapshot()
	if snap.Balance.Reserved != 0 {
		t.Fatalf("cancel should not reserve, got reserved=%v", snap.Balance.Reserved)
	}
}

func TestSubmitIntents_PlaceCreatesProvisionalAndSubmits(t *testing.T) {
	e, _, _, _, exec, st := newEngineForTest()
	intents := []OrderIntent{{
		MarketID: "m1",
		TokenID:  "tk1",
		Price:    0.5,
		Size:     2,
		Side:     orders.BUY,
	}}
	if !e.submitIntents(intents, st.Snapshot(), nil) {
		t.Fatal("expected true")
	}
	if len(exec.executed) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(exec.executed))
	}
	if exec.executed[0][0].Action != OrderIntentActionPlace {
		t.Fatalf("expected action defaulted to PLACE, got %v", exec.executed[0][0].Action)
	}
	if exec.executed[0][0].IntentID == "" {
		t.Fatal("expected IntentID auto-assigned")
	}
	snap := st.Snapshot()
	if snap.Balance.Reserved <= 0 {
		t.Fatalf("expected provisional reservation, got reserved=%v", snap.Balance.Reserved)
	}
	if e.ordersSent.Load() != 1 {
		t.Fatalf("expected ordersSent=1, got %d", e.ordersSent.Load())
	}
}

func TestSubmitIntents_PreserveExplicitIntentID(t *testing.T) {
	e, _, _, _, exec, st := newEngineForTest()
	intents := []OrderIntent{{
		Action:   OrderIntentActionPlace,
		MarketID: "m1",
		TokenID:  "tk1",
		Price:    0.5,
		Size:     1,
		Side:     orders.BUY,
		IntentID: "explicit-1",
	}}
	if !e.submitIntents(intents, st.Snapshot(), nil) {
		t.Fatal("expected true")
	}
	if exec.executed[0][0].IntentID != "explicit-1" {
		t.Fatalf("expected explicit IntentID preserved, got %s", exec.executed[0][0].IntentID)
	}
}

func TestSubmitIntents_PlaceFailsProvisionalSkipsExec(t *testing.T) {
	// Empty TokenID will fail TryReserveProvisional → intent dropped.
	e, _, _, _, exec, st := newEngineForTest()
	intents := []OrderIntent{{
		MarketID: "m1",
		TokenID:  "", // invalid
		Price:    0.5,
		Size:     1,
		Side:     orders.BUY,
	}}
	if !e.submitIntents(intents, st.Snapshot(), nil) {
		t.Fatal("expected true (function returns true when all intents skipped)")
	}
	if len(exec.executed) != 0 {
		t.Fatalf("exec should not be called when no intents survive, got %d", len(exec.executed))
	}
	if e.riskRejected.Load() == 0 {
		t.Fatal("riskRejected should increment when provisional reserve fails")
	}
}

func TestHandleStrategyTick_FiresOnTickStrategies(t *testing.T) {
	e, prob, strat, _, exec, _ := newEngineForTest()
	prob.snapshotResult = Observation{MarketID: "m1"}
	prob.snapshotOk = true
	strat.onTickFn = func(now time.Time, o Observation, s state.Snapshot) []OrderIntent {
		return []OrderIntent{{Action: OrderIntentActionCancel, OrderID: "o1"}}
	}

	e.handleStrategyTick(time.Now())

	if len(exec.executed) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(exec.executed))
	}
}

func TestHandleStrategyTick_NoStrategiesNoop(t *testing.T) {
	e, _, _, _, exec, _ := newEngineForTest()
	e.Strategies = nil
	e.handleStrategyTick(time.Now())
	if len(exec.executed) != 0 {
		t.Fatalf("expected no exec, got %d", len(exec.executed))
	}
}

func TestHandleStrategyTick_NoSnapshotNoop(t *testing.T) {
	e, prob, strat, _, exec, _ := newEngineForTest()
	prob.snapshotOk = false
	strat.onTickFn = func(time.Time, Observation, state.Snapshot) []OrderIntent {
		t.Fatal("OnTick should not be called when no snapshot")
		return nil
	}
	e.handleStrategyTick(time.Now())
	if len(exec.executed) != 0 {
		t.Fatalf("expected no exec, got %d", len(exec.executed))
	}
}

func TestHandleStrategyTick_SkipsNonTickStrategy(t *testing.T) {
	e, prob, _, _, exec, _ := newEngineForTest()
	prob.snapshotResult = Observation{MarketID: "m1"}
	prob.snapshotOk = true
	e.Strategies = []Strategy{&plainStrategy{}}
	e.handleStrategyTick(time.Now())
	if len(exec.executed) != 0 {
		t.Fatalf("plain strategy should not be ticked, got %d batches", len(exec.executed))
	}
}

func TestHandleStrategyTick_EmptyIntentsContinues(t *testing.T) {
	e, prob, _, _, exec, _ := newEngineForTest()
	prob.snapshotResult = Observation{MarketID: "m1"}
	prob.snapshotOk = true
	tick1 := &fakeStrategy{onTickFn: func(time.Time, Observation, state.Snapshot) []OrderIntent {
		return nil
	}}
	tick2 := &fakeStrategy{onTickFn: func(time.Time, Observation, state.Snapshot) []OrderIntent {
		return []OrderIntent{{Action: OrderIntentActionCancel, OrderID: "o-x"}}
	}}
	e.Strategies = []Strategy{tick1, tick2}
	e.handleStrategyTick(time.Now())
	if len(exec.executed) != 1 {
		t.Fatalf("expected second tick to fire, got %d batches", len(exec.executed))
	}
}

func TestHandleStrategyTick_RiskRejectAbortsLoop(t *testing.T) {
	e, prob, _, risk, exec, _ := newEngineForTest()
	prob.snapshotResult = Observation{MarketID: "m1"}
	prob.snapshotOk = true
	risk.err = errors.New("blocked")
	tick1 := &fakeStrategy{onTickFn: func(time.Time, Observation, state.Snapshot) []OrderIntent {
		return []OrderIntent{{Action: OrderIntentActionCancel, OrderID: "first"}}
	}}
	tick2Called := false
	tick2 := &fakeStrategy{onTickFn: func(time.Time, Observation, state.Snapshot) []OrderIntent {
		tick2Called = true
		return nil
	}}
	e.Strategies = []Strategy{tick1, tick2}
	e.handleStrategyTick(time.Now())
	if len(exec.executed) != 0 {
		t.Fatalf("exec should not be called on risk reject, got %d batches", len(exec.executed))
	}
	if tick2Called {
		t.Fatal("loop should abort after submitIntents returns false")
	}
}

func TestHandleExecutionAwareStrategy_FiresOnExecution(t *testing.T) {
	e, prob, strat, _, exec, _ := newEngineForTest()
	prob.snapshotResult = Observation{MarketID: "m1"}
	prob.snapshotOk = true
	strat.onExecFn = func(ev core.ExecutionEvent, o Observation, s state.Snapshot) []OrderIntent {
		return []OrderIntent{{Action: OrderIntentActionCancel, OrderID: "o1"}}
	}

	e.handleExecutionAwareStrategy(core.ExecutionEvent{OrderID: "o1", Status: core.ExecutionStatusFilled})

	if len(exec.executed) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(exec.executed))
	}
}

func TestHandleExecutionAwareStrategy_NilProbabilityPublishesRisk(t *testing.T) {
	e, _, _, _, _, _ := newEngineForTest()
	e.Probability = nil
	ch, cancel := e.Bus.SubscribeWithCancel()
	defer cancel()

	e.handleExecutionAwareStrategy(core.ExecutionEvent{OrderID: "o1", Status: core.ExecutionStatusFilled})

	waitForEventType(t, ch, core.EventRisk, time.Second)
}

func TestHandleExecutionAwareStrategy_NoStrategiesPublishesRisk(t *testing.T) {
	e, _, _, _, _, _ := newEngineForTest()
	e.Strategies = nil
	ch, cancel := e.Bus.SubscribeWithCancel()
	defer cancel()

	e.handleExecutionAwareStrategy(core.ExecutionEvent{OrderID: "o1", Status: core.ExecutionStatusFilled})

	waitForEventType(t, ch, core.EventRisk, time.Second)
}

func TestHandleExecutionAwareStrategy_NoSnapshotSilentlyReturns(t *testing.T) {
	e, prob, _, _, exec, _ := newEngineForTest()
	prob.snapshotOk = false
	e.handleExecutionAwareStrategy(core.ExecutionEvent{OrderID: "o1", Status: core.ExecutionStatusFilled})
	if len(exec.executed) != 0 {
		t.Fatalf("expected no exec, got %d", len(exec.executed))
	}
}

func TestHandleExecutionAwareStrategy_SkipsPlainStrategy(t *testing.T) {
	e, prob, _, _, exec, _ := newEngineForTest()
	prob.snapshotResult = Observation{MarketID: "m1"}
	prob.snapshotOk = true
	e.Strategies = []Strategy{&plainStrategy{}}
	e.handleExecutionAwareStrategy(core.ExecutionEvent{OrderID: "o1"})
	if len(exec.executed) != 0 {
		t.Fatalf("plain strategy should not be invoked, got %d batches", len(exec.executed))
	}
}

func TestHandleExecutionAwareStrategy_EmptyIntentsContinues(t *testing.T) {
	e, prob, _, _, exec, _ := newEngineForTest()
	prob.snapshotResult = Observation{MarketID: "m1"}
	prob.snapshotOk = true
	exec1 := &fakeStrategy{onExecFn: func(core.ExecutionEvent, Observation, state.Snapshot) []OrderIntent {
		return nil
	}}
	exec2 := &fakeStrategy{onExecFn: func(core.ExecutionEvent, Observation, state.Snapshot) []OrderIntent {
		return []OrderIntent{{Action: OrderIntentActionCancel, OrderID: "o-y"}}
	}}
	e.Strategies = []Strategy{exec1, exec2}
	e.handleExecutionAwareStrategy(core.ExecutionEvent{OrderID: "o1"})
	if len(exec.executed) != 1 {
		t.Fatalf("expected second exec-aware to fire, got %d batches", len(exec.executed))
	}
}

func TestHandleExecutionAwareStrategy_RiskRejectAbortsLoop(t *testing.T) {
	e, prob, _, risk, exec, _ := newEngineForTest()
	prob.snapshotResult = Observation{MarketID: "m1"}
	prob.snapshotOk = true
	risk.err = errors.New("blocked")
	first := &fakeStrategy{onExecFn: func(core.ExecutionEvent, Observation, state.Snapshot) []OrderIntent {
		return []OrderIntent{{Action: OrderIntentActionCancel, OrderID: "first"}}
	}}
	secondCalled := false
	second := &fakeStrategy{onExecFn: func(core.ExecutionEvent, Observation, state.Snapshot) []OrderIntent {
		secondCalled = true
		return nil
	}}
	e.Strategies = []Strategy{first, second}
	e.handleExecutionAwareStrategy(core.ExecutionEvent{OrderID: "o1"})
	if len(exec.executed) != 0 {
		t.Fatalf("exec should not fire on risk reject, got %d", len(exec.executed))
	}
	if secondCalled {
		t.Fatal("loop should abort after submitIntents returns false")
	}
}

func TestCurrentObservation_NilProbabilityReturnsFalse(t *testing.T) {
	e, _, _, _, _, _ := newEngineForTest()
	e.Probability = nil
	if _, ok := e.currentObservation(); ok {
		t.Fatal("expected false when probability is nil")
	}
}

func TestCurrentObservation_NonProviderReturnsFalse(t *testing.T) {
	e, _, _, _, _, _ := newEngineForTest()
	e.Probability = &fakeProbabilityNoSnapshot{}
	if _, ok := e.currentObservation(); ok {
		t.Fatal("expected false when probability is not a snapshot provider")
	}
}

func TestCurrentObservation_ProviderForwards(t *testing.T) {
	e, prob, _, _, _, _ := newEngineForTest()
	prob.snapshotResult = Observation{MarketID: "mX"}
	prob.snapshotOk = true
	obs, ok := e.currentObservation()
	if !ok {
		t.Fatal("expected ok")
	}
	if obs.MarketID != "mX" {
		t.Fatalf("expected MarketID=mX, got %s", obs.MarketID)
	}
}

func TestBuildMidPrices(t *testing.T) {
	obs := Observation{
		Tokens: map[string]Token{
			"tk1": {Id: "tk1", AskPrice: 0.6, BidPrice: 0.5},
			"tk2": {Id: "tk2", AskPrice: 0, BidPrice: 0.4},  // skipped: zero ask
			"tk3": {Id: "tk3", AskPrice: 0.3, BidPrice: 0},  // skipped: zero bid
			"tk4": {Id: "tk4", AskPrice: 0.55, BidPrice: 0.45},
		},
	}
	mids := buildMidPrices(obs)
	if got := mids["tk1"]; got != 0.55 {
		t.Fatalf("tk1: expected 0.55, got %v", got)
	}
	if _, ok := mids["tk2"]; ok {
		t.Fatal("tk2 should be skipped")
	}
	if _, ok := mids["tk3"]; ok {
		t.Fatal("tk3 should be skipped")
	}
	if got := mids["tk4"]; got != 0.5 {
		t.Fatalf("tk4: expected 0.5, got %v", got)
	}
	if len(mids) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(mids))
	}
}

func TestBuildMidPrices_Empty(t *testing.T) {
	mids := buildMidPrices(Observation{})
	if len(mids) != 0 {
		t.Fatalf("expected empty, got %d entries", len(mids))
	}
}
