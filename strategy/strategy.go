package strategy

import (
	"context"
	"log"
	"polypilot/core"
	"polypilot/runtime"
	"polypilot/state"

	"github.com/polymarket/go-order-utils/pkg/model"
	"github.com/tidwall/gjson"
)

type Strategy struct {
	Bus    *core.EventBus
	market *gjson.Result
}

func (s *Strategy) Init(bus *core.EventBus, ctx context.Context) {
	s.Bus = bus

}

func (s *Strategy) OnUpdate(e core.Event, o runtime.Observation, stateSnap state.Snapshot) []runtime.OrderIntent {
	log.Printf("Observation: %+v", o)
	switch e.Type {
	case core.EventMarket:
		obj, ok := e.Data.(gjson.Result)
		if !ok {
			return nil
		}
		s.market = &obj
		// 剩余时间不足时不下单
		if o.TimeLeftSec < 240 {
			return nil
		}
		okPrice := true
		for _, v := range o.Tokens {
			if v.AskPrice < 0.4 {
				okPrice = false
			}
		}
		// 价格太低时不下单
		if !okPrice {
			return nil
		}

		ins := make([]runtime.OrderIntent, 0, len(o.Tokens))
		for _, t := range o.Tokens {
			ins = append(ins, runtime.OrderIntent{
				MarketID: o.MarketID,
				TokenID:  t.Id,
				Price:    0.35,
				Side:     model.BUY,
				Size:     5,
			})
		}
		return ins
	case core.EventOrderBook:
		// 判断zscore等信息是否应该止损
		latestZ := o.Features["latestZ"].(float64)
		zWindows := o.Features["zWindows"].([]float64)
		// TODO: 实现止损/止盈逻辑
		log.Printf("latestZ: %f, zWindows: %v", latestZ, zWindows)
	}

	return nil

}
