package market

import (
	"context"
	"testing"
	"time"

	"github.com/xiangxn/polypilot/core"
)

func TestMockPriceFeed_Init(t *testing.T) {
	bus := core.NewEventBus()
	t.Cleanup(bus.Close)
	f := &MockPriceFeed{}
	f.Init(bus)
	if f.Bus != bus {
		t.Fatal("Init did not set Bus")
	}
}

func TestMockPriceFeed_PublishesEvents(t *testing.T) {
	bus := core.NewEventBus()
	t.Cleanup(bus.Close)
	ch := bus.Subscribe()
	f := &MockPriceFeed{}
	f.Init(bus)

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	f.Start(ctx)

	// Defaults should be applied inside Start.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type != core.EventMarket {
				continue
			}
			me, ok := ev.Data.(core.MarketEvent)
			if !ok {
				t.Fatalf("expected MarketEvent, got %T", ev.Data)
			}
			if me.MarketID != "market-1" {
				t.Fatalf("default MarketID not applied: %q", me.MarketID)
			}
			if me.TokenID != "token-1" {
				t.Fatalf("default TokenID not applied: %q", me.TokenID)
			}
			if me.Price < 0.01 || me.Price > 0.99 {
				t.Fatalf("price out of clamped range: %v", me.Price)
			}
			if me.Timestamp == 0 {
				t.Fatal("Timestamp not set")
			}
			return
		case <-deadline:
			t.Fatal("timeout, no event published")
		}
	}
}

func TestMockPriceFeed_CustomMarketTokenID(t *testing.T) {
	bus := core.NewEventBus()
	t.Cleanup(bus.Close)
	ch := bus.Subscribe()
	f := &MockPriceFeed{MarketID: "custom-m", TokenID: "custom-t"}
	f.Init(bus)

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	f.Start(ctx)

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-ch:
			if me, ok := ev.Data.(core.MarketEvent); ok {
				if me.MarketID != "custom-m" || me.TokenID != "custom-t" {
					t.Fatalf("custom id not propagated, got %+v", me)
				}
				return
			}
		case <-deadline:
			t.Fatal("timeout, no event with MarketEvent payload")
		}
	}
}

func TestMockPriceFeed_StopsOnContextCancel(t *testing.T) {
	bus := core.NewEventBus()
	t.Cleanup(bus.Close)
	ch := bus.Subscribe()
	f := &MockPriceFeed{}
	f.Init(bus)

	ctx, cancel := context.WithCancel(context.Background())
	f.Start(ctx)

	// Allow at least one event to fire (ticker is 1s — wait slightly over).
	select {
	case <-ch:
	case <-time.After(1300 * time.Millisecond):
		// No event by deadline — still proceed to cancel and verify clean shutdown.
	}

	cancel()
	// Drain a bit; the goroutine should have exited so no further sends occur
	// past one more tick at most. We're only asserting no panic / no deadlock.
	time.Sleep(50 * time.Millisecond)
}
