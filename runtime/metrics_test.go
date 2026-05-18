package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/state"
)

func TestPublishMetricsEmitsEvent(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch, cancel := bus.SubscribeWithCancel()
	defer cancel()

	st := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, nil)
	st.Restore(state.Snapshot{Balance: state.Balance{Available: 50, Reserved: 5}})
	prob := &fakeProbability{snapshotOk: false}
	e := &Engine{Bus: bus, State: st, Probability: prob}
	e.initOrderTracking()

	// Bump some counters so the published metrics carry non-zero data.
	e.ticks.Add(3)
	e.inputEvents.Add(2)
	e.executionAccepted.Add(1)
	e.RecordReconcile(7)

	e.publishMetrics()

	ctx, ctxCancel := context.WithTimeout(context.Background(), time.Second)
	defer ctxCancel()
	for {
		select {
		case ev := <-ch:
			if ev.Type != core.EventMetrics {
				continue
			}
			m, ok := ev.Data.(core.MetricsEvent)
			if !ok {
				t.Fatalf("expected MetricsEvent, got %T", ev.Data)
			}
			if m.Ticks != 3 {
				t.Fatalf("expected Ticks=3, got %d", m.Ticks)
			}
			if m.BalanceAvailable != 50 || m.BalanceReserved != 5 {
				t.Fatalf("expected balance carried, got avail=%v reserved=%v", m.BalanceAvailable, m.BalanceReserved)
			}
			if m.ReconcileRuns != 1 || m.ReconcileDiffs != 7 {
				t.Fatalf("expected reconcile counters carried, got runs=%d diffs=%d", m.ReconcileRuns, m.ReconcileDiffs)
			}
			return
		case <-ctx.Done():
			t.Fatal("timeout waiting for metrics event")
		}
	}
}

func TestPublishMetrics_UsesSnapshotProviderWhenAvailable(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch, cancel := bus.SubscribeWithCancel()
	defer cancel()
	st := state.NewStateWithBalanceSync(state.BalanceSyncConfig{}, nil)
	st.Restore(state.Snapshot{Balance: state.Balance{Available: 100}})
	prob := &fakeProbability{
		snapshotOk: true,
		snapshotResult: Observation{
			Tokens: map[string]Token{
				"tk1": {Id: "tk1", AskPrice: 0.6, BidPrice: 0.5},
			},
		},
	}
	e := &Engine{Bus: bus, State: st, Probability: prob}
	e.initOrderTracking()

	e.publishMetrics()

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case ev := <-ch:
			if ev.Type != core.EventMetrics {
				continue
			}
			if _, ok := ev.Data.(core.MetricsEvent); !ok {
				t.Fatalf("expected MetricsEvent, got %T", ev.Data)
			}
			return
		case <-deadline.C:
			t.Fatal("timeout")
		}
	}
}

func TestPublishRisk_EmitsRiskEvent(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch, cancel := bus.SubscribeWithCancel()
	defer cancel()

	e := &Engine{Bus: bus}
	e.publishRisk("danger")

	select {
	case ev := <-ch:
		if ev.Type != core.EventRisk {
			t.Fatalf("expected EventRisk, got %v", ev.Type)
		}
		r, ok := ev.Data.(core.RiskEvent)
		if !ok {
			t.Fatalf("expected RiskEvent, got %T", ev.Data)
		}
		if r.Reason != "danger" {
			t.Fatalf("expected reason='danger', got %q", r.Reason)
		}
		if r.At.IsZero() {
			t.Fatal("expected At populated")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestPublishRisk_NilBusNoop(t *testing.T) {
	e := &Engine{Bus: nil}
	// no panic, no events
	e.publishRisk("ignored")
}

func TestRecordReconcile_DiffsAccumulate(t *testing.T) {
	e := &Engine{}
	e.RecordReconcile(5)
	e.RecordReconcile(0) // 0 diffs still counts as a run
	e.RecordReconcile(3)
	if got := e.reconcileRuns.Load(); got != 3 {
		t.Fatalf("expected reconcileRuns=3, got %d", got)
	}
	if got := e.reconcileDiffs.Load(); got != 8 {
		t.Fatalf("expected reconcileDiffs=8, got %d", got)
	}
}

func TestRecordReconcile_NegativeIgnored(t *testing.T) {
	e := &Engine{}
	e.RecordReconcile(-3)
	if e.reconcileRuns.Load() != 1 {
		t.Fatalf("expected run counted, got %d", e.reconcileRuns.Load())
	}
	if e.reconcileDiffs.Load() != 0 {
		t.Fatalf("expected diffs unchanged on negative input, got %d", e.reconcileDiffs.Load())
	}
}

func TestInitConfig_NoViperUsesDefaults(t *testing.T) {
	e := &Engine{}
	e.initConfig()
	if e.StrategyTickInterval != 0 {
		t.Fatalf("expected zero tick interval, got %v", e.StrategyTickInterval)
	}
	if e.MetricsInterval != defaultMetricsInterval {
		t.Fatalf("expected default metrics interval, got %v", e.MetricsInterval)
	}
}

func TestInitConfig_ViperHonored(t *testing.T) {
	v := viper.New()
	v.Set("runtime.strategy_tick_interval", "250ms")
	v.Set("runtime.metrics_interval", "2m")
	e := &Engine{Config: v}
	e.initConfig()
	if e.StrategyTickInterval != 250*time.Millisecond {
		t.Fatalf("expected 250ms tick, got %v", e.StrategyTickInterval)
	}
	if e.MetricsInterval != 2*time.Minute {
		t.Fatalf("expected 2m metrics, got %v", e.MetricsInterval)
	}
}

func TestInitConfig_ViperZeroMetricsUsesDefault(t *testing.T) {
	v := viper.New()
	v.Set("runtime.strategy_tick_interval", "100ms")
	// metrics_interval intentionally unset → defaults to zero → fallback
	e := &Engine{Config: v}
	e.initConfig()
	if e.StrategyTickInterval != 100*time.Millisecond {
		t.Fatalf("expected 100ms tick, got %v", e.StrategyTickInterval)
	}
	if e.MetricsInterval != defaultMetricsInterval {
		t.Fatalf("expected default metrics interval fallback, got %v", e.MetricsInterval)
	}
}

func TestHasTickStrategy(t *testing.T) {
	e := &Engine{}
	if e.hasTickStrategy() {
		t.Fatal("empty strategies should report false")
	}
	e.Strategies = []Strategy{nil, &plainStrategy{}}
	if e.hasTickStrategy() {
		t.Fatal("plain strategy should not count as tick strategy")
	}
	e.Strategies = []Strategy{nil, &plainStrategy{}, &fakeStrategy{}}
	if !e.hasTickStrategy() {
		t.Fatal("fakeStrategy implements TickStrategy → should report true")
	}
}

func TestEngineClose_ClosesBus(t *testing.T) {
	bus := core.NewEventBus()
	e := &Engine{Bus: bus}
	e.Close()
	// Publishing to a closed bus is a noop; subscribe afterward yields a
	// closed channel.
	ch := bus.Subscribe()
	if _, ok := <-ch; ok {
		t.Fatal("expected channel closed after bus close")
	}
}

func TestEngineClose_NilBusNoop(t *testing.T) {
	e := &Engine{}
	// Should not panic.
	e.Close()
}
