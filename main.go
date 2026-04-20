package main

import (
	"context"
	"os"
	"os/signal"
	"polypilot/execution"
	"polypilot/market"
	"polypilot/observer"
	"polypilot/probability"
	"polypilot/risk"
	"polypilot/runtime"
	"polypilot/state"
	"polypilot/strategy"
	"syscall"

	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	engine := &runtime.Engine{
		State:      state.NewState(1000),
		Risk:       &risk.Engine{},
		Exec:       &execution.Executor{},
		SQLitePath: os.Getenv("POLYMARKET_SQLITE_PATH"),
		Feeds: []runtime.Feed{&market.PolymarketSlugFeed{
			SlugPrefix:    "btc-updown-5m",
			WindowMinutes: 5,
			SignerKey:     os.Getenv("POLYMARKET_SIGNER_KEY"),
		}, &market.CryptoPriceFeed{MonitoSymble: "btc", MonitorType: sdk.MonitorChainlink}},
		Observers:   []runtime.Observer{&observer.Logger{}},
		Probability: &probability.Engine{},
		Strategies:  []runtime.Strategy{&strategy.Strategy{}},
	}

	engine.Start(ctx)
}
