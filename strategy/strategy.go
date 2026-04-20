package strategy

import (
	"context"
	"log"
	"polypilot/core"
	"polypilot/runtime"

	"github.com/polymarket/go-order-utils/pkg/model"
	"github.com/tidwall/gjson"
)

const defaultReadonlyPrivKey = "1111111111111111111111111111111111111111111111111111111111111111"
const defaultWindowSize = 60
const defaultCoin = "BTC"

type Strategy struct {
	Bus    *core.EventBus
	market *gjson.Result
}

func (s *Strategy) Init(bus *core.EventBus, ctx context.Context) {
	s.Bus = bus

}

func (s *Strategy) OnUpdate(e core.Event, m runtime.Observation) []runtime.OrderIntent {
	log.Printf("Observation: %+v", m)
	switch e.Type {
	case core.EventMarket:
		obj, ok := e.Data.(gjson.Result)
		if !ok {
			return nil
		}
		s.market = &obj
		// 剩余时间不足时不下单
		if m.TimeLeftSec < 240 {
			return nil
		}
		okPrice := true
		for _, v := range m.Tokens {
			if v.AskPrice < 0.4 {
				okPrice = false
			}
		}
		// 价格太低时不下单
		if !okPrice {
			return nil
		}

		ins := make([]runtime.OrderIntent, 0, len(m.Tokens))
		for _, t := range m.Tokens {
			ins = append(ins, runtime.OrderIntent{
				MarketID: m.MarketID,
				TokenID:  t.Id,
				Price:    0.35,
				Side:     model.BUY,
				Size:     5,
			})
		}
		return ins
	case core.EventOrderBook:
		// 判断zscore等信息是否应该止损
		latestZ := m.Features["latestZ"].(float64)
		zWindows := m.Features["zWindows"].([]float64)

		log.Printf("latestZ: %f, zWindows: %v", latestZ, zWindows)
	}

	return nil

}
