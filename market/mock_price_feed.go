package market

import (
	"context"
	"math/rand"
	"polypilot/core"
	"time"
)

type MockPriceFeed struct {
	Bus      *core.EventBus
	MarketID string
	TokenID  string
}

func (p *MockPriceFeed) Init(bus *core.EventBus) {
	p.Bus = bus
}

func (p *MockPriceFeed) Start(ctx context.Context) {
	if p.MarketID == "" {
		p.MarketID = "market-1"
	}
	if p.TokenID == "" {
		p.TokenID = "token-1"
	}

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		price := 0.50
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				price += (rand.Float64()*0.04 - 0.02)
				if price < 0.01 {
					price = 0.01
				}
				if price > 0.99 {
					price = 0.99
				}
				p.Bus.Publish(core.Event{
					Type: core.EventMarket,
					Data: core.MarketEvent{MarketID: p.MarketID, TokenID: p.TokenID, Price: price, Timestamp: time.Now().UnixMilli()},
				})
			}
		}
	}()
}
