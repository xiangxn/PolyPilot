//go:build !race
// +build !race

// Init tests are guarded by !race because the SDK's *TradeMonitor has a known
// data race between Run (writes tm.ws) and Close (reads tm.ws). That race is in
// production SDK code we can't modify; the tests cover Init under non-race
// builds only.

package execution

import (
	"context"
	"testing"
	"time"

	"github.com/xiangxn/polypilot/core"

	"github.com/xiangxn/go-polymarket-sdk/orders"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

func TestInit_DefaultsApplied(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()

	cfg := sdk.DefaultConfig()
	cfg.Polymarket.ClobBaseURL = "http://127.0.0.1:1"
	cfg.Polymarket.ClobWSBaseURL = "ws://127.0.0.1:1"
	cfg.Polymarket.RelayerBaseURL = "http://127.0.0.1:1"

	exec := &Executor{Config: cfg, DryRun: true}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	exec.Init(bus, ctx)

	if exec.Bus != bus {
		t.Fatal("expected Bus to be set")
	}
	if exec.OrderType != orders.GTC {
		t.Fatalf("expected GTC default, got %v", exec.OrderType)
	}
	if exec.tracked == nil {
		t.Fatal("expected tracked map initialized")
	}
	if exec.ExecutionQueueSize != defaultExecutionQueue {
		t.Fatalf("expected default queue size, got %d", exec.ExecutionQueueSize)
	}
	if exec.queue == nil {
		t.Fatal("expected queue initialized")
	}
	if exec.Client == nil {
		t.Fatal("expected Client initialized")
	}
	if exec.relayClient == nil {
		t.Fatal("expected relayClient initialized")
	}
	if exec.TradeMonitor == nil {
		t.Fatal("expected TradeMonitor initialized from cfg")
	}

	cancel()
	time.Sleep(50 * time.Millisecond)
}

func TestInit_RespectsExistingOrderTypeAndQueueSize(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()

	cfg := sdk.DefaultConfig()
	cfg.Polymarket.ClobBaseURL = "http://127.0.0.1:1"
	cfg.Polymarket.ClobWSBaseURL = "ws://127.0.0.1:1"
	cfg.Polymarket.RelayerBaseURL = "http://127.0.0.1:1"

	exec := &Executor{
		Config:             cfg,
		OrderType:          orders.GTC,
		ExecutionQueueSize: 64,
		DryRun:             true,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exec.Init(bus, ctx)

	if exec.ExecutionQueueSize != 64 {
		t.Fatalf("expected queue size preserved, got %d", exec.ExecutionQueueSize)
	}
	cancel()
	time.Sleep(50 * time.Millisecond)
}

func TestInit_IsIdempotent(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()

	cfg := sdk.DefaultConfig()
	cfg.Polymarket.ClobBaseURL = "http://127.0.0.1:1"
	cfg.Polymarket.ClobWSBaseURL = "ws://127.0.0.1:1"
	cfg.Polymarket.RelayerBaseURL = "http://127.0.0.1:1"

	exec := &Executor{Config: cfg, DryRun: true}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exec.Init(bus, ctx)
	client1 := exec.Client
	q1 := exec.queue

	// Second call: startOnce/workerOnce protected → no re-init
	exec.Init(bus, ctx)
	if exec.Client != client1 {
		t.Fatal("expected Client unchanged across Init calls")
	}
	if exec.queue != q1 {
		t.Fatal("expected queue unchanged across Init calls")
	}
	cancel()
	time.Sleep(50 * time.Millisecond)
}

// TestInit_NilConfigUsesDefault covers the "cfg = sdk.DefaultConfig()" branch
// inside startOnce — when e.Config is nil, Init falls back to defaults.
func TestInit_NilConfigUsesDefault(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()

	// Config = nil → Init uses sdk.DefaultConfig() inside startOnce
	exec := &Executor{Config: nil, DryRun: true}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exec.Init(bus, ctx)

	if exec.Client == nil {
		t.Fatal("expected Client initialized from default config")
	}
	if exec.relayClient == nil {
		t.Fatal("expected relayClient initialized from default config")
	}
	if exec.TradeMonitor == nil {
		t.Fatal("expected TradeMonitor initialized from default config")
	}

	cancel()
	time.Sleep(50 * time.Millisecond)
}
