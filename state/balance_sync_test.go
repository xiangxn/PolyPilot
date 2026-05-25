package state

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type fakeBalanceReader struct {
	val   float64
	err   error
	calls atomic.Int32
}

func (f *fakeBalanceReader) ReadOnchainBalance(_ context.Context) (float64, error) {
	f.calls.Add(1)
	return f.val, f.err
}

func TestNormalizeBalanceSyncConfig_DisabledPassThrough(t *testing.T) {
	cfg := BalanceSyncConfig{Enabled: false, Interval: 0, Epsilon: 0}
	out := normalizeBalanceSyncConfig(cfg)
	if out.Interval != 0 || out.Epsilon != 0 {
		t.Fatalf("expected disabled config unchanged, got %+v", out)
	}
}

func TestNormalizeBalanceSyncConfig_EnabledFillsDefaults(t *testing.T) {
	cfg := BalanceSyncConfig{Enabled: true, Interval: 0, Epsilon: 0}
	out := normalizeBalanceSyncConfig(cfg)
	if out.Interval != defaultBalanceSyncInterval {
		t.Fatalf("expected default interval, got %v", out.Interval)
	}
	if out.Epsilon != defaultBalanceSyncEpsilon {
		t.Fatalf("expected default epsilon, got %v", out.Epsilon)
	}
}

func TestNormalizeBalanceSyncConfig_EnabledKeepsOverrides(t *testing.T) {
	cfg := BalanceSyncConfig{Enabled: true, Interval: 3 * time.Second, Epsilon: 0.5}
	out := normalizeBalanceSyncConfig(cfg)
	if out.Interval != 3*time.Second {
		t.Fatalf("interval overrides: got %v", out.Interval)
	}
	if out.Epsilon != 0.5 {
		t.Fatalf("epsilon overrides: got %v", out.Epsilon)
	}
}

func TestSyncBalanceOnce_NilStateReturnsZero(t *testing.T) {
	var s *State
	evt := s.SyncBalanceOnce(context.Background())
	if evt.Changed || evt.Err != nil {
		t.Fatalf("expected zero event, got %+v", evt)
	}
}

func TestSyncBalanceOnce_NilReaderReturnsZero(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	evt := s.SyncBalanceOnce(context.Background())
	if evt.Changed || evt.Err != nil {
		t.Fatalf("expected zero event when reader nil, got %+v", evt)
	}
}

func TestSyncBalanceOnce_HappyPath(t *testing.T) {
	reader := &fakeBalanceReader{val: 100}
	s := NewStateWithBalanceSync(BalanceSyncConfig{
		Enabled:  true,
		Reader:   reader,
		Interval: time.Second,
		Epsilon:  1e-3,
	}, nil)
	s.Restore(Snapshot{Balance: Balance{Available: 0, Reserved: 10}})
	evt := s.SyncBalanceOnce(context.Background())
	if !evt.Changed || evt.OnchainTotal != 100 {
		t.Fatalf("got %+v", evt)
	}
	if got := s.Snapshot().Balance.Available; got != 90 {
		t.Fatalf("expected available=90, got %v", got)
	}
}

func TestSyncBalanceOnce_ReaderError(t *testing.T) {
	reader := &fakeBalanceReader{err: errors.New("boom")}
	s := NewStateWithBalanceSync(BalanceSyncConfig{Enabled: true, Reader: reader, Epsilon: 1e-3}, nil)
	evt := s.SyncBalanceOnce(context.Background())
	if evt.Err == nil {
		t.Fatal("expected error")
	}
}

func TestSyncBalanceOnce_NoChangeSuppressesOnEvent(t *testing.T) {
	reader := &fakeBalanceReader{val: 60}
	called := atomic.Int32{}
	s := NewStateWithBalanceSync(BalanceSyncConfig{
		Enabled: true,
		Reader:  reader,
		Epsilon: 1e-3,
		OnEvent: func(_ BalanceSyncEvent) { called.Add(1) },
	}, nil)
	// available=50, reserved=10 → newAvailable=50, drift=0 → not changed
	s.Restore(Snapshot{Balance: Balance{Available: 50, Reserved: 10}})
	evt := s.SyncBalanceOnce(context.Background())
	if evt.Changed {
		t.Fatalf("expected not changed: %+v", evt)
	}
	if called.Load() != 0 {
		t.Fatalf("OnEvent should not fire when no change and no err, got %d calls", called.Load())
	}
}

func TestSyncBalanceOnce_ChangedTriggersOnEvent(t *testing.T) {
	reader := &fakeBalanceReader{val: 200}
	called := atomic.Int32{}
	s := NewStateWithBalanceSync(BalanceSyncConfig{
		Enabled: true,
		Reader:  reader,
		Epsilon: 1e-3,
		OnEvent: func(_ BalanceSyncEvent) { called.Add(1) },
	}, nil)
	s.Restore(Snapshot{Balance: Balance{Available: 50, Reserved: 10}})
	evt := s.SyncBalanceOnce(context.Background())
	if !evt.Changed {
		t.Fatal("expected changed")
	}
	if called.Load() != 1 {
		t.Fatalf("OnEvent should fire once, got %d", called.Load())
	}
}

func TestSyncBalanceOnce_ErrTriggersOnEvent(t *testing.T) {
	reader := &fakeBalanceReader{err: errors.New("rpc-fail")}
	called := atomic.Int32{}
	s := NewStateWithBalanceSync(BalanceSyncConfig{
		Enabled: true,
		Reader:  reader,
		Epsilon: 1e-3,
		OnEvent: func(_ BalanceSyncEvent) { called.Add(1) },
	}, nil)
	evt := s.SyncBalanceOnce(context.Background())
	if evt.Err == nil {
		t.Fatal("expected err")
	}
	if called.Load() != 1 {
		t.Fatalf("OnEvent should fire once on err, got %d", called.Load())
	}
}

func TestStartBalanceSync_NilStateNoOp(t *testing.T) {
	var s *State
	// must not panic
	s.StartBalanceSync(context.Background())
}

func TestStartBalanceSync_DisabledNoOp(t *testing.T) {
	reader := &fakeBalanceReader{val: 50}
	s := NewStateWithBalanceSync(BalanceSyncConfig{Enabled: false, Reader: reader, Interval: 10 * time.Millisecond}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	s.StartBalanceSync(ctx)
	time.Sleep(60 * time.Millisecond)
	cancel()
	if reader.calls.Load() != 0 {
		t.Fatalf("expected no calls when disabled, got %d", reader.calls.Load())
	}
}

func TestStartBalanceSync_NilReaderNoOp(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{Enabled: true, Interval: 10 * time.Millisecond}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	// must not panic
	s.StartBalanceSync(ctx)
	time.Sleep(40 * time.Millisecond)
	cancel()
}

func TestStartBalanceSync_TickerCallsReader(t *testing.T) {
	reader := &fakeBalanceReader{val: 50}
	s := NewStateWithBalanceSync(BalanceSyncConfig{
		Enabled:  true,
		Reader:   reader,
		Interval: 20 * time.Millisecond,
		Epsilon:  1e-3,
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	s.StartBalanceSync(ctx)
	// also a second call: should be a no-op via sync.Once
	s.StartBalanceSync(ctx)
	time.Sleep(120 * time.Millisecond)
	cancel()
	if reader.calls.Load() < 2 {
		t.Fatalf("expected multiple calls, got %d", reader.calls.Load())
	}
	// give the goroutine a moment to exit cleanly
	time.Sleep(30 * time.Millisecond)
}

func TestStartBalanceSync_ContextCancelStopsLoop(t *testing.T) {
	reader := &fakeBalanceReader{val: 50}
	s := NewStateWithBalanceSync(BalanceSyncConfig{
		Enabled:  true,
		Reader:   reader,
		Interval: 15 * time.Millisecond,
		Epsilon:  1e-3,
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	s.StartBalanceSync(ctx)
	time.Sleep(60 * time.Millisecond)
	cancel()
	pre := reader.calls.Load()
	time.Sleep(120 * time.Millisecond)
	post := reader.calls.Load()
	// allow at most one extra call from a ticker firing between cancel & exit
	if post-pre > 1 {
		t.Fatalf("loop should stop after cancel: pre=%d post=%d", pre, post)
	}
}
