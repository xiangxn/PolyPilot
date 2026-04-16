package strategy

import (
	"polypilot/core"
	"polypilot/runtime"
)

type MockStrategy struct {
	Bus *core.EventBus
}

func (s *MockStrategy) Init(bus *core.EventBus) {
	s.Bus = bus
}

func (s *MockStrategy) OnUpdate(e core.Event, m runtime.Observation) []runtime.OrderIntent {
	if e.Type != core.EventMarket {
		return nil
	}

	market := e.Data.(core.MarketEvent)

	return []runtime.OrderIntent{
		{
			MarketID: market.MarketID,
			TokenID:  market.TokenID,
			Price:    0.4,
			Side:     core.SideBuy,
			Size:     5,
		}}
}
