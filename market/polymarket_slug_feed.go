package market

import (
	"context"
	"fmt"
	"os"
	"polypilot/core"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
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
	Slug     string
	MarketID string
	TokenIDs []string
	Prices   []float64
	EndDate  int64
	TickSize float64
	NegRisk  bool
}

type PolymarketSlugFeed struct {
	Bus *core.EventBus

	MarketMonitor *sdk.MarketMonitor
	Config        *sdk.Config

	SignerKey string

	SlugPrefix    string
	WindowMinutes int

	mu          sync.Mutex
	currentSlug string
	currentEnd  time.Time
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
			slug := f.activeSlug(time.Now())
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
	tokenIDs := parseStringArray(result.Get("clobTokenIds"))
	if len(tokenIDs) == 0 {
		return nil, fmt.Errorf("no clob token ids in market slug=%s", slug)
	}

	prices := parseFloatArray(result.Get("outcomePrices"))

	endDate, err := utils.ToTimestamp(result.Get("endDate").String())
	if err != nil {
		return nil, fmt.Errorf("invalid market endDate slug=%s: %w", slug, err)
	}

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
		Slug:     slug,
		MarketID: marketID,
		TokenIDs: tokenIDs,
		Prices:   prices,
		EndDate:  endDate,
		TickSize: tickSize,
		NegRisk:  negRisk,
	}, nil
}

func (f *PolymarketSlugFeed) activeSlug(now time.Time) string {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.currentSlug == "" || (!f.currentEnd.IsZero() && !now.Before(f.currentEnd)) {
		f.currentSlug = f.slugFor(now)
	}
	return f.currentSlug
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

func parseStringArray(v gjson.Result) []string {
	if !v.Exists() {
		return nil
	}

	s := strings.TrimSpace(v.String())
	if s != "" {
		parsed := gjson.Parse(s)
		if parsed.IsArray() {
			items := parsed.Array()
			res := make([]string, 0, len(items))
			for _, item := range items {
				if t := item.String(); t != "" {
					res = append(res, t)
				}
			}
			return res
		}
	}

	arr := v.Array()
	if len(arr) > 0 {
		res := make([]string, 0, len(arr))
		for _, item := range arr {
			if item.IsArray() {
				nested := item.Array()
				for _, n := range nested {
					if t := n.String(); t != "" {
						res = append(res, t)
					}
				}
				continue
			}
			if t := strings.TrimSpace(item.String()); t != "" {
				res = append(res, t)
			}
		}
		if len(res) > 0 {
			return res
		}
	}

	if s == "" {
		return nil
	}
	return []string{s}
}

func parseFloatArray(v gjson.Result) []float64 {
	items := parseStringArray(v)
	if len(items) == 0 {
		return nil
	}
	res := make([]float64, 0, len(items))
	for _, item := range items {
		n, err := strconv.ParseFloat(item, 64)
		if err == nil {
			res = append(res, n)
		}
	}
	return res
}

func parseEndTime(endDate string) (time.Time, error) {
	endDate = strings.TrimSpace(endDate)
	if endDate == "" {
		return time.Time{}, fmt.Errorf("empty endDate")
	}
	if t, err := time.Parse(time.RFC3339, endDate); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339Nano, endDate)
}
