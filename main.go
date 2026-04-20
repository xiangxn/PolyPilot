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

	stateOpts := make([]state.Option, 0, 1)
	if cfg.BalanceSync.Enabled {
		reader, err := state.NewMulticallBalanceReader(
			cfg.ChainRPCURL,
			cfg.Polymarket.ChainID,
			cfg.BalanceSync.CollateralToken,
			*cfg.Polymarket.FunderAddress,
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid balance sync config: %v\n", err)
			return
		}

		stateOpts = append(stateOpts, state.WithBalanceSync(state.BalanceSyncConfig{
			Enabled:  true,
			Reader:   reader,
			Interval: cfg.BalanceSync.Interval,
			Epsilon:  cfg.BalanceSync.Epsilon,
		}))
	}

	engine := &runtime.Engine{
		State:      state.NewState(cfg.BalanceSync.MinBalance, stateOpts...),
		Risk:       &risk.Engine{},
		Exec:       &execution.Executor{},
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
