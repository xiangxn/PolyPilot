package state

import (
	"context"
	"testing"
	"time"

	"github.com/xiangxn/polypilot/core"
)

func TestPositionExpiring_OnlyFiresOnce(t *testing.T) {
	s := newStateWithBalance(t, 100)
	bus := core.NewEventBus()
	t.Cleanup(bus.Close)
	endsAt := time.Now().Add(15 * time.Second).UnixMilli()
	s.RegisterMarketExpiry("m1", endsAt, []string{"tk1"})

	ch, cancel := bus.SubscribeWithCancel()
	t.Cleanup(cancel)
	ctx, ctxCancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	t.Cleanup(ctxCancel)

	go s.StartPositionExpiringLoop(ctx, bus, 50*time.Millisecond, 30*time.Second)

	fires := 0
	deadline := time.After(700 * time.Millisecond)
	for {
		select {
		case ev := <-ch:
			if ev.Type == core.EventPositionExpiring {
				fires++
			}
		case <-deadline:
			if fires != 1 {
				t.Fatalf("expected exactly 1 fire, got %d", fires)
			}
			return
		}
	}
}

func TestStartPositionExpiringLoop_DefaultsAppliedForZeroParams(t *testing.T) {
	// Use a far-future market so we won't actually wait for a fire,
	// just verify the loop starts with defaults and exits cleanly.
	s := newStateWithBalance(t, 100)
	bus := core.NewEventBus()
	t.Cleanup(bus.Close)
	s.RegisterMarketExpiry("m1", time.Now().Add(2*time.Hour).UnixMilli(), []string{"tk1"})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// tick=0 → falls back to 1s default; warnBefore=0 → falls back to 30s.
		s.StartPositionExpiringLoop(ctx, bus, 0, 0)
	}()
	select {
	case <-done:
		// good — loop exited on context done
	case <-time.After(2 * time.Second):
		t.Fatal("loop didn't exit after context cancel")
	}
}

func TestRegisterMarketExpiry_OverridesExisting(t *testing.T) {
	s := newStateWithBalance(t, 100)
	s.RegisterMarketExpiry("m1", 1000, []string{"tk1"})
	s.RegisterMarketExpiry("m1", 2000, []string{"tk1", "tk2"})

	s.expiryMu.Lock()
	m := s.expiryMarkets["m1"]
	s.expiryMu.Unlock()

	if m == nil || m.endTime != 2000 {
		t.Fatalf("expected endTime=2000, got %+v", m)
	}
	if len(m.tokenIDs) != 2 {
		t.Fatalf("expected 2 token IDs, got %v", m.tokenIDs)
	}
}

func TestCheckExpiry_NilTokens(t *testing.T) {
	// snapshotTokenAvailable should handle missing tokens gracefully.
	s := newStateWithBalance(t, 100)
	bus := core.NewEventBus()
	t.Cleanup(bus.Close)
	// register with token IDs that are NOT in the position map → should still fire but with 0 available
	s.RegisterMarketExpiry("m1", time.Now().Add(5*time.Second).UnixMilli(), []string{"unknown-token"})
	s.checkExpiry(bus, 30*time.Second)
}
