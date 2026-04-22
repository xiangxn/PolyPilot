package strategy

import (
	"context"
	"log"
	"polypilot/core"
	"polypilot/runtime"
	"polypilot/state"

	"github.com/polymarket/go-order-utils/pkg/model"
	"github.com/tidwall/gjson"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
	"github.com/xiangxn/go-polymarket-sdk/utils"
)

const PlacePrice = 0.35

type Strategy struct {
	Bus    *core.EventBus
	market *gjson.Result
}

func (s *Strategy) Init(bus *core.EventBus, ctx context.Context) {
	s.Bus = bus

}

func (s *Strategy) OnUpdate(e core.Event, o runtime.Observation, stateSnap state.Snapshot) []runtime.OrderIntent {
	// log.Printf("Observation: %+v", o)
	switch e.Type {
	case core.EventMarket:
		obj, ok := e.Data.(gjson.Result)
		if !ok {
			return nil
		}

		s.market = nil

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

		s.market = &obj

		// ins := make([]runtime.OrderIntent, 0, len(o.Tokens))
		// for _, t := range o.Tokens {
		// 	ins = append(ins, runtime.OrderIntent{
		// 		MarketID: o.MarketID,
		// 		TokenID:  t.Id,
		// 		Price:    0.35,
		// 		Side:     model.BUY,
		// 		Size:     5,
		// 	})
		// }
		// return ins

	case core.EventOrderBook:
		// 判断zscore等信息是否应该止损

		orderBook := e.Data.(sdk.OrderBook)
		openPrice := o.Features["openPrice"].(float64)
		latestPrice := o.Features["latestPrice"].(float64)
		latestZ := o.Features["latestZ"].(float64)
		zWindows := o.Features["zWindows"].([]float64)

		log.Printf("openPrice: %f, latestPrice: %f", openPrice, latestPrice)
		log.Printf("latestZ: %f, zWindows: %v, orderBook: %v", latestZ, zWindows, orderBook)

		if openPrice <= 0 { // 过滤掉数据未准备好的情况
			return nil
		}
		// TODO: 实现止损/止盈逻辑
		ins := make([]runtime.OrderIntent, 0)
		if o.TimeLeftSec > 5 { // 只操作最后5秒之前
			if latestZ > 2.3 { // 价格出现单边趋势
				// 判断当前涨跌
				up := false // 默认为跌
				if latestPrice >= openPrice {
					up = true // 判断为涨
				}

				tokenKeys := utils.Keys(o.Tokens)
				upToken := o.Tokens[tokenKeys[0]]
				downToken := o.Tokens[tokenKeys[1]]

				if up { // 涨了，需要判断当前持仓，根据情况处理持仓
					upPos, okUp := stateSnap.Position.Tokens[upToken.Id]
					downPos, okDown := stateSnap.Position.Tokens[downToken.Id]
					if okUp { // 如果有up持仓
						if upToken.BidPrice > PlacePrice*2 { // 已经翻倍，可以止盈了
							ins = append(ins, runtime.OrderIntent{
								MarketID: o.MarketID,
								TokenID:  upToken.Id,
								Price:    0.35,
								Side:     model.SELL,
								Size:     upPos.Available,
							})
						}
					}
					if okDown { // 如果有down持仓，止损
						ins = append(ins, runtime.OrderIntent{
							MarketID: o.MarketID,
							TokenID:  downToken.Id,
							Price:    0.01,
							Side:     model.SELL,
							Size:     downPos.Available,
						})
					}

				} else { // 跌了

				}
			}

		}
		return ins
	}

	return nil

}
