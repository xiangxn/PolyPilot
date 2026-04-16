package probability

import (
	"polypilot/core"
	"polypilot/runtime"

	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

type Engine struct {
	LastPrice float64
	LastAt    int64
}

func (e *Engine) OnUpdate(ev core.Event) (runtime.Observation, bool) {
	if ev.Type != core.EventOrderBook {
		return runtime.Observation{}, false
	}

	data, ok := ev.Data.(sdk.OrderBook)
	if !ok {
		return runtime.Observation{}, false
	}

	e.LastPrice = (data.Asks[0].Price + data.Bids[0].Price) / 2
	e.LastAt = data.Timestamp

	return runtime.Observation{
		Probability: e.estimate(),
		Price:       e.LastPrice,
		At:          data.Timestamp,
	}, true
}

func (e *Engine) estimate() float64 {
	if e.LastPrice <= 0 {
		return 0.5
	}
	if e.LastPrice < 0.01 {
		return 0.01
	}
	if e.LastPrice > 0.99 {
		return 0.99
	}
	return e.LastPrice
}
