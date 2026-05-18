package market

import (
	"context"
	"testing"
	"time"

	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
	"github.com/xiangxn/polypilot/core"
)

func TestCryptoPriceFeed_Init_DefaultConfig(t *testing.T) {
	bus := core.NewEventBus()
	t.Cleanup(bus.Close)
	f := &CryptoPriceFeed{
		MonitoSymble: "BTC",
		MonitorType:  sdk.MonitorAll,
	}
	f.Init(bus)
	if f.Bus != bus {
		t.Fatal("Init should set Bus")
	}
	if f.cryptoPriceMonitor == nil {
		t.Fatal("Init should construct the price monitor")
	}
}

func TestCryptoPriceFeed_Init_ProvidedConfig(t *testing.T) {
	bus := core.NewEventBus()
	t.Cleanup(bus.Close)
	cfg := sdk.DefaultConfig()
	f := &CryptoPriceFeed{
		MonitoSymble: "ETH",
		MonitorType:  sdk.MonitorAll,
		Config:       cfg,
	}
	f.Init(bus)
	if f.Bus != bus {
		t.Fatal("Init should set Bus")
	}
	if f.cryptoPriceMonitor == nil {
		t.Fatal("Init should construct the price monitor when Config is supplied")
	}
}

// TestCryptoPriceFeed_Start_NilBusIsNoOp checks the early-return branch
// (without triggering any network activity).
func TestCryptoPriceFeed_Start_NilBusIsNoOp(t *testing.T) {
	f := &CryptoPriceFeed{}
	// Bus is nil — Start should return synchronously without panic and without
	// dereferencing cryptoPriceMonitor.
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.Start(context.Background())
	}()
	select {
	case <-done:
		// ok
	case <-time.After(300 * time.Millisecond):
		t.Fatal("Start with nil Bus should return immediately")
	}
}

// TestCryptoPriceFeed_Start_PreCancelledContext drives Start with a context
// that is already cancelled. The cryptoPriceMonitor.Run goroutine should
// observe ctx.Done() quickly, and the forwarder goroutine returns via the
// ctx.Done() branch. We never reach the data-publishing branch (no WS data).
func TestCryptoPriceFeed_Start_PreCancelledContext(t *testing.T) {
	// Use an unroutable WS URL so even if Run() attempts to connect it
	// will fail fast.
	cfg := sdk.DefaultConfig()
	cfg.Polymarket.ClobWSBaseURL = "ws://127.0.0.1:1"
	cfg.Polymarket.LiveWSBaseURL = "ws://127.0.0.1:1"

	bus := core.NewEventBus()
	t.Cleanup(bus.Close)
	f := &CryptoPriceFeed{
		MonitoSymble: "BTC",
		MonitorType:  sdk.MonitorAll,
		Config:       cfg,
	}
	f.Init(bus)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	f.Start(ctx)

	// Let the forwarder goroutine observe ctx.Done() and return.
	time.Sleep(100 * time.Millisecond)
}
