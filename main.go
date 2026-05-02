package main

import (
	"context"
	"fmt"
	"github.com/xiangxn/polypilot/config"
	"github.com/xiangxn/polypilot/execution"
	"github.com/xiangxn/polypilot/logx"
	"github.com/xiangxn/polypilot/market"
	"github.com/xiangxn/polypilot/observer"
	"github.com/xiangxn/polypilot/probability"
	"github.com/xiangxn/polypilot/risk"
	"github.com/xiangxn/polypilot/runtime"
	"github.com/xiangxn/polypilot/state"
	"github.com/xiangxn/polypilot/strategy"
	"os"
	"os/signal"
	"syscall"

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

	st, err := state.NewState(cfg, state.NewPolymarketStateClient(sharedClient, &cfg.SDKConfig.Polymarket, 0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "init state failed: %v\n", err)
		return
	}

	engine := &runtime.Engine{
		Config: viper,
		State:  st,
		Risk:   &risk.Engine{},
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
