package strategy

import (
	"polypilot/core"
	"polypilot/runtime"

	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

type MockStrategy struct {
	Bus *core.EventBus
}

func (s *MockStrategy) Init(bus *core.EventBus) {
	s.Bus = bus
}

func (s *MockStrategy) OnUpdate(e core.Event, m runtime.Observation) []runtime.OrderIntent {
	if e.Type != core.EventOrderBook {
		return nil
	}

	market := e.Data.(sdk.OrderBook)

	return []runtime.OrderIntent{
		{
			MarketID: market.Market,
			TokenID:  market.AssetId,
			Price:    0.4,
			Side:     core.SideBuy,
			Size:     5,
		}}
}
