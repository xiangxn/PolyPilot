package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xiangxn/polypilot/config"
	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/execution"
	"github.com/xiangxn/polypilot/logx"
	"github.com/xiangxn/polypilot/market"
	"github.com/xiangxn/polypilot/observer"
	"github.com/xiangxn/polypilot/probability"
	"github.com/xiangxn/polypilot/risk"
	"github.com/xiangxn/polypilot/runtime"
	"github.com/xiangxn/polypilot/state"
	"github.com/xiangxn/polypilot/strategy"

	"github.com/joho/godotenv"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

func main() {
	_ = godotenv.Load()

	cfg, viper, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config failed: %v\n", err)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdown, err := logx.Bootstrap(ctx, cfg.Logging, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init logger failed: %v\n", err)
		return
	}
	defer shutdown()

	sharedClient := sdk.NewClient(&cfg.SDKConfig)

	st, err := state.NewState(cfg, state.NewPolymarketStateClient(sharedClient, &cfg.SDKConfig.Polymarket, 0, cfg.Redeem.Enabled))
	if err != nil {
		fmt.Fprintf(os.Stderr, "init state failed: %v\n", err)
		return
	}

	engine := &runtime.Engine{
		Config: viper,
		State:  st,
		Risk: &risk.Engine{
			MaxDailyLoss:         cfg.Risk.MaxDailyLoss,
			MaxExposurePerMarket: cfg.Risk.MaxExposurePerMarket,
			MaxSlippageBps:       cfg.Risk.MaxSlippageBps,
			MaxOpenOrders:        cfg.Risk.MaxOpenOrders,
			MarketCooldown:       cfg.Risk.MarketCooldown,
		},
		Exec: &execution.Executor{
			Client:    sharedClient,
			Config:    &cfg.SDKConfig,
			Reconcile: st.TriggerReconcile,
		},
		Feeds: []runtime.Feed{
			&market.PolymarketSlugFeed{
				SlugPrefix:    "btc-updown-5m",
				Config:        &cfg.SDKConfig,
				WindowMinutes: 5,
			},
			&market.CryptoPriceFeed{MonitoSymble: "btc", MonitorType: sdk.MonitorChainlink},
		},
		Observers:   []runtime.Observer{&observer.Logger{}},
		Probability: probability.NewEngine(sharedClient),
		Strategies:  []runtime.Strategy{&strategy.Strategy{}},
	}

	// Start reconcile loop (publishes EventReconcile + records metrics).
	// Bus is initialized inside engine.Start(ctx). Because StartReconcileLoop
	// launches a goroutine that may fire before engine.Start runs, the OnReport
	// callback guards against a nil Bus.
	st.StartReconcileLoop(ctx, state.ReconcileConfig{
		Enabled:      true,
		Interval:     cfg.Reconcile.Interval,
		RetryBackoff: cfg.Reconcile.RetryBackoff,
		OnReport: func(rep state.ReconcileReport) {
			if engine.Bus == nil {
				return // engine not yet started; metrics will pick up on next tick
			}
			totalAdded := rep.OrdersAdded + rep.PositionsAdded
			totalRemoved := rep.OrdersRemoved + rep.PositionsRemoved
			totalUpdated := rep.OrdersUpdated + rep.PositionsUpdated
			engine.RecordReconcile(totalAdded + totalRemoved + totalUpdated)
			engine.Bus.Publish(core.Event{Type: core.EventReconcile, Data: core.ReconcileEvent{
				Type:       "BOTH",
				Added:      totalAdded,
				Removed:    totalRemoved,
				Updated:    totalUpdated,
				DurationMs: rep.DurationMs,
				Err:        rep.Err,
				At:         time.Now().UTC(),
			}})
		},
	})

	engine.Start(ctx)
}
