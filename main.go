package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"polypilot/execution"
	appconfig "polypilot/internal/config"
	"polypilot/market"
	"polypilot/observer"
	"polypilot/probability"
	"polypilot/risk"
	"polypilot/runtime"
	"polypilot/state"
	"polypilot/strategy"
	"syscall"

	"github.com/joho/godotenv"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

func main() {
	_ = godotenv.Load()

	cfg, err := appconfig.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config failed: %v\n", err)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	balanceSyncCfg, err := state.BuildMulticallBalanceSyncConfig(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init state failed: %v\n", err)
		return
	}

	sharedClient := sdk.NewClient(&cfg.SDKConfig)

	engine := &runtime.Engine{
		State: state.NewState(balanceSyncCfg, state.NewPolymarketStateClient(sharedClient, &cfg.SDKConfig.Polymarket, 0)),
		Risk:  &risk.Engine{},
		Exec: &execution.Executor{
			Client: sharedClient,
			Config: &cfg.SDKConfig,
		},
		Feeds: []runtime.Feed{&market.PolymarketSlugFeed{
			SlugPrefix:    "btc-updown-5m",
			Config:        &cfg.SDKConfig,
			WindowMinutes: 5,
		}, &market.CryptoPriceFeed{MonitoSymble: "btc", MonitorType: sdk.MonitorChainlink}},
		Observers:   []runtime.Observer{&observer.Logger{}},
		Probability: &probability.Engine{},
		Strategies:  []runtime.Strategy{&strategy.Strategy{}},
	}

	engine.Start(ctx)
}
