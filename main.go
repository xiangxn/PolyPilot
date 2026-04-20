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

	engine := &runtime.Engine{
		State: state.NewState(balanceSyncCfg),
		Risk:  &risk.Engine{},
		Exec: &execution.Executor{
			Config:    &sdk.Config{Polymarket: cfg.Polymarket},
			SignerKey: cfg.SignerKey,
		},
		SQLitePath: cfg.SQLitePath,
		Feeds: []runtime.Feed{&market.PolymarketSlugFeed{
			SlugPrefix:    "btc-updown-5m",
			WindowMinutes: 5,
			SignerKey:     cfg.SignerKey,
		}, &market.CryptoPriceFeed{MonitoSymble: "btc", MonitorType: sdk.MonitorChainlink}},
		Observers:   []runtime.Observer{&observer.Logger{}},
		Probability: &probability.Engine{},
		Strategies:  []runtime.Strategy{&strategy.Strategy{}},
	}

	engine.Start(ctx)
}
