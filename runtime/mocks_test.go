package runtime

import (
	"context"
	"time"

	"github.com/spf13/viper"
	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/state"
)

// fakeProbability implements Probability + ProbabilitySnapshotProvider.
type fakeProbability struct {
	initCalls      int
	onUpdate       func(ev core.Event) (Observation, bool)
	snapshotResult Observation
	snapshotOk     bool
}

func (f *fakeProbability) Init(ctx context.Context) { f.initCalls++ }

func (f *fakeProbability) OnUpdate(ev core.Event) (Observation, bool) {
	if f.onUpdate != nil {
		return f.onUpdate(ev)
	}
	return Observation{}, false
}

func (f *fakeProbability) CurrentObservation() (Observation, bool) {
	return f.snapshotResult, f.snapshotOk
}

// fakeProbabilityNoSnapshot only implements Probability, not the snapshot
// provider — used to cover the type-assertion failure branch of
// currentObservation.
type fakeProbabilityNoSnapshot struct{}

func (f *fakeProbabilityNoSnapshot) Init(ctx context.Context) {}
func (f *fakeProbabilityNoSnapshot) OnUpdate(ev core.Event) (Observation, bool) {
	return Observation{}, false
}

// fakeStrategy implements Strategy, TickStrategy and ExecutionAwareStrategy via
// optional hooks. When a hook is nil it acts as a no-op for that interface.
type fakeStrategy struct {
	initCalls  int
	onUpdateFn func(e core.Event, o Observation, s state.Snapshot) []OrderIntent
	onTickFn   func(now time.Time, o Observation, s state.Snapshot) []OrderIntent
	onExecFn   func(ev core.ExecutionEvent, o Observation, s state.Snapshot) []OrderIntent
}

func (f *fakeStrategy) Init(bus *core.EventBus, ctx context.Context, cfg *viper.Viper) {
	f.initCalls++
}

func (f *fakeStrategy) OnUpdate(e core.Event, o Observation, s state.Snapshot) []OrderIntent {
	if f.onUpdateFn != nil {
		return f.onUpdateFn(e, o, s)
	}
	return nil
}

func (f *fakeStrategy) OnTick(now time.Time, o Observation, s state.Snapshot) []OrderIntent {
	if f.onTickFn != nil {
		return f.onTickFn(now, o, s)
	}
	return nil
}

func (f *fakeStrategy) OnExecution(ev core.ExecutionEvent, o Observation, s state.Snapshot) []OrderIntent {
	if f.onExecFn != nil {
		return f.onExecFn(ev, o, s)
	}
	return nil
}

// plainStrategy implements only Strategy — used to exercise the "not a
// TickStrategy / not an ExecutionAwareStrategy" branches.
type plainStrategy struct {
	onUpdateFn func(e core.Event, o Observation, s state.Snapshot) []OrderIntent
}

func (p *plainStrategy) Init(bus *core.EventBus, ctx context.Context, cfg *viper.Viper) {}

func (p *plainStrategy) OnUpdate(e core.Event, o Observation, s state.Snapshot) []OrderIntent {
	if p.onUpdateFn != nil {
		return p.onUpdateFn(e, o, s)
	}
	return nil
}

// fakeRisk implements RiskManager.
type fakeRisk struct {
	checks int
	err    error
}

func (f *fakeRisk) Check(intents []OrderIntent, snap state.Snapshot, mids map[string]float64) error {
	f.checks++
	return f.err
}

// fakeExec implements Executor.
type fakeExec struct {
	inits    int
	executed [][]OrderIntent
}

func (f *fakeExec) Init(bus *core.EventBus, ctx context.Context) { f.inits++ }

func (f *fakeExec) Execute(intents []OrderIntent) {
	// Copy slice to avoid sharing the underlying array with test code that
	// may reuse the same backing array.
	cp := make([]OrderIntent, len(intents))
	copy(cp, intents)
	f.executed = append(f.executed, cp)
}
