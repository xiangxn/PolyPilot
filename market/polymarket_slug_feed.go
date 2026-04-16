package market

import (
	"context"
	"fmt"
	"os"
	"polypilot/core"
	"strings"
	"time"

	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
	"github.com/xiangxn/go-polymarket-sdk/utils"
)

const (
	defaultSlugPrefix      = "btc-updown-5m"
	defaultWindowMinutes   = 5
	defaultFallbackPrice   = 0.5
	defaultReadonlyPrivKey = "1111111111111111111111111111111111111111111111111111111111111111"
)

type SlugMarket struct {
	Slug             string
	MarketID         string
	TokenIDs         []string
	Prices           []float64
	EndDate          int64
	ResolutionSource string
	TickSize         float64
	NegRisk          bool
	StartDate        int64
	Closed           bool
	Outcomes         []string
}

type PolymarketSlugFeed struct {
	Bus *core.EventBus

	MarketMonitor *sdk.MarketMonitor
	Config        *sdk.Config

	SignerKey string

	SlugPrefix    string
	WindowMinutes int
}

func (f *PolymarketSlugFeed) Init(bus *core.EventBus) {
	f.Bus = bus
}

func (f *PolymarketSlugFeed) Start(ctx context.Context) {
	if f.Bus == nil {
		return
	}
	f.ensureDefaults()
	if f.MarketMonitor == nil {
		cfg := f.Config
		if cfg == nil {
			cfg = sdk.DefaultConfig()
		}
		client := sdk.NewClient(f.resolveSignerKey(), cfg)
		f.MarketMonitor = sdk.NewMarketMonitor(cfg.Polymarket.ClobWSBaseURL, client)
	}

	obChan := f.MarketMonitor.Subscribe()

	go f.MarketMonitor.Run(ctx)

	go func() {
		for {
			slug := f.slugFor(time.Now())
			market, err := f.FetchMarketBySlug(slug)
			if err != nil {
				return
			}

			f.MarketMonitor.SubscribeTokens(market.TokenIDs...)

			deadline := time.UnixMilli(market.EndDate + 200)
			d := time.Until(deadline)
			timer := time.NewTimer(d)

		inner:
			for {
				select {
				case <-ctx.Done():
					return
				case orderBook := <-obChan:
					f.Bus.Publish(core.Event{
						Type: core.EventOrderBook,
						Data: orderBook,
					})
				case <-timer.C:
					timer.Stop()
					f.MarketMonitor.Reset()
					break inner
				}
			}
		}
	}()
}

func (f *PolymarketSlugFeed) FetchMarketBySlug(slug string) (*SlugMarket, error) {
	if strings.TrimSpace(slug) == "" {
		return nil, fmt.Errorf("slug cannot be empty")
	}

	client := f.MarketMonitor.GetClient()
	result, err := client.FetchMarketBySlug(slug)
	if err != nil {
		return nil, err
	}

	marketID := result.Get("conditionId").String()
	tokenIDs := utils.GetStringArray(result, "clobTokenIds")
	if len(tokenIDs) == 0 {
		return nil, fmt.Errorf("no clob token ids in market slug=%s", slug)
	}

	prices := utils.GetFloatArray(result, "outcomePrices")

	endDate, err := utils.ToTimestamp(result.Get("endDate").String())
	if err != nil {
		return nil, fmt.Errorf("invalid market endDate slug=%s: %w", slug, err)
	}

	startDate, err := utils.ToTimestamp(result.Get("startDate").String())
	if err != nil {
		return nil, fmt.Errorf("invalid market startDate slug=%s: %w", slug, err)
	}

	outcomes := utils.GetStringArray(result, "outcomes")

	resolutionSource := result.Get("resolutionSource").String()
	tickSize := result.Get("orderPriceMinTickSize").Float()
	negRisk := result.Get("negRisk").Bool()
	feesEnabled := result.Get("feesEnabled").Bool()
	for _, tokenID := range tokenIDs {
		client.SetTickSize(tokenID, tickSize)
		client.SetNegRisk(tokenID, negRisk)
		if !feesEnabled {
			client.SetFeeRateBps(tokenID, 0)
		} else {
			client.GetFeeRateBps(tokenID)
		}
	}

	return &SlugMarket{
		Slug:             slug,
		MarketID:         marketID,
		TokenIDs:         tokenIDs,
		Prices:           prices,
		EndDate:          endDate,
		TickSize:         tickSize,
		NegRisk:          negRisk,
		ResolutionSource: resolutionSource,
		StartDate:        startDate,
		Closed:           result.Get("closed").Bool(),
		Outcomes:         outcomes,
	}, nil
}

func (f *PolymarketSlugFeed) slugFor(now time.Time) string {
	window := f.WindowMinutes * 60
	if window <= 0 {
		window = defaultWindowMinutes * 60
	}
	ts := now.Unix() / int64(window) * int64(window)
	return fmt.Sprintf("%s-%d", f.SlugPrefix, ts)
}

func (f *PolymarketSlugFeed) ensureDefaults() {
	if f.SlugPrefix == "" {
		f.SlugPrefix = defaultSlugPrefix
	}
	if f.WindowMinutes <= 0 {
		f.WindowMinutes = defaultWindowMinutes
	}
}

func (f *PolymarketSlugFeed) resolveSignerKey() string {
	if f.SignerKey != "" {
		return strings.TrimPrefix(f.SignerKey, "0x")
	}
	if key := os.Getenv("POLYMARKET_SIGNER_KEY"); key != "" {
		return strings.TrimPrefix(key, "0x")
	}
	if key := os.Getenv("SIGNERKEY"); key != "" {
		return strings.TrimPrefix(key, "0x")
	}
	return defaultReadonlyPrivKey
}
