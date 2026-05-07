package strategy

import (
	"context"
	"math"

	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/internal/prices"
	"github.com/xiangxn/polypilot/logx"
	"github.com/xiangxn/polypilot/runtime"
	"github.com/xiangxn/polypilot/state"

	"github.com/spf13/viper"
	"github.com/tidwall/gjson"
	"github.com/xiangxn/go-polymarket-sdk/orders"
	"github.com/xiangxn/go-polymarket-sdk/utils"
)

const PlacePrice = 0.35

var log = logx.Module("strategy")

type Strategy struct {
	Bus     *core.EventBus
	markets *MarketQueue

	config StrategyConfig
}

type StrategyConfig struct {
	TimeLeftSec int64   `mapstructure:"timeleft_sec"`
	MinInPrice  float64 `mapstructure:"min_in_price"`
	InPrice     float64 `mapstructure:"in_price"`
	InSize      float64 `mapstructure:"in_size"`
	MinZ        float64 `mapstructure:"min_z"`
	ZAgo        int     `mapstructure:"z_ago"`
}

func DefaultStrategyConfig() StrategyConfig {
	return StrategyConfig{
		TimeLeftSec: 240,
		MinInPrice:  0.4,
		InPrice:     0.35,
		InSize:      5,
		MinZ:        2.3,
		ZAgo:        5,
	}
}

func (s *Strategy) Init(bus *core.EventBus, ctx context.Context, cfg *viper.Viper) {
	s.Bus = bus

	sc := DefaultStrategyConfig()
	if cfg != nil {
		if sub := cfg.Sub("strategies.strategy"); sub != nil {
			sub.Unmarshal(&sc)
		}
	}
	s.config = sc
	s.markets = NewMarketQueue(3)
}

func (s *Strategy) OnExecution(ev core.ExecutionEvent, o runtime.Observation, snap state.Snapshot) []runtime.OrderIntent {
	// 订单执行失败时，如果还有单边挂单就取消
	if ev.Status == core.ExecutionStatusRejected && ev.Reason == core.ExecutionReasonTradeFailed {
		market, exists := s.markets.Get(ev.MarketID)
		if !exists {
			return nil
		}
		tokenKeys := utils.GetStringArray(market, "clobTokenIds")
		cancelId := ""
		if tokenKeys[0] == ev.TokenID {
			cancelId = tokenKeys[1]
		} else {
			cancelId = tokenKeys[0]
		}
		if cancelId != "" {
			ins := make([]runtime.OrderIntent, 0)
			orderIds := buildCancelIntent(cancelId, snap.Orders)
			// 如果有挂单就取消
			for _, oId := range orderIds {
				ins = append(ins, runtime.OrderIntent{
					Action:  runtime.OrderIntentActionCancel,
					OrderID: oId,
				})
			}
			// 如果有持仓就清仓
			if pos, exists := snap.Position.Tokens[cancelId]; exists {
				if pos.Available > 0 {
					ins = append(ins, runtime.OrderIntent{
						MarketID: ev.MarketID,
						TokenID:  cancelId,
						Price:    s.config.InPrice,
						Side:     orders.SELL,
						Size:     pos.Available,
					})
				}
			}
			return ins
		}
	}
	return nil
}

func (s *Strategy) OnUpdate(e core.Event, o runtime.Observation, stateSnap state.Snapshot) []runtime.OrderIntent {
	// log.Printf("Observation: %+v", o)
	switch e.Type {
	case core.EventMarket:
		obj, ok := e.Data.(gjson.Result)
		if !ok {
			return nil
		}

		// 剩余时间不足时不下单
		if o.TimeLeftSec < s.config.TimeLeftSec {
			return nil
		}
		okPrice := true
		for _, v := range o.Tokens {
			if v.AskPrice < s.config.MinInPrice {
				okPrice = false
			}
		}
		// 价格太低时不下单
		if !okPrice {
			return nil
		}

		s.markets.Add(o.MarketID, &obj)

		ins := make([]runtime.OrderIntent, 0, len(o.Tokens))
		for _, t := range o.Tokens {
			ins = append(ins, runtime.OrderIntent{
				MarketID: o.MarketID,
				TokenID:  t.Id,
				Price:    s.config.InPrice,
				Side:     orders.BUY,
				Size:     s.config.InSize,
			})
		}
		return ins

	case core.EventOrderBook:
		market, exists := s.markets.Get(o.MarketID)
		if !exists {
			return nil
		}
		// 判断zscore等信息是否应该止损
		openPrice := o.Features["openPrice"].(float64)
		if openPrice <= 0 { // 过滤掉数据未准备好的情况
			return nil
		}

		latestPrice := o.Features["latestPrice"].(float64)
		latestZ := o.Features["latestZ"].(float64)
		zWindows := o.Features["zWindows"].([]float64)

		// log.Printf("latestZ: %f, openPrice: %f, latestPrice: %f, ", latestZ, openPrice, latestPrice)

		// 实现止损/止盈逻辑
		ins := make([]runtime.OrderIntent, 0)
		if o.TimeLeftSec > 5 { // 只操作最后5秒之前
			tokenKeys := utils.GetStringArray(market, "clobTokenIds")
			upToken := o.Tokens[tokenKeys[0]]
			downToken := o.Tokens[tokenKeys[1]]

			upPos, okUp := stateSnap.Position.Tokens[upToken.Id]
			downPos, okDown := stateSnap.Position.Tokens[downToken.Id]

			// up, dp := 0.0, 0.0
			// if okUp {
			// 	up = upPos.Available
			// }
			// if okDown {
			// 	dp = downPos.Available
			// }
			lnt := LastNGreaterThan(zWindows, s.config.ZAgo, s.config.MinZ)
			// log.Printf("LZ: %f, LNT: %v, UPos: %f, DPos: %f, Ask: %f, PD: %f", latestZ, lnt, up, dp, upToken.AskPrice, latestPrice-openPrice)
			if math.Abs(latestZ) > s.config.MinZ && lnt { // 价格出现单边趋势
				// 判断当前涨跌
				up := false // 默认为跌
				if latestPrice >= openPrice {
					up = true // 判断为涨
				}
				if up { // 涨了，需要判断当前持仓，根据情况处理持仓
					if !okUp && okDown { // 只有down仓,止损
						if downPos.Available > 0 {
							orderbook := o.GetOrderBook(downToken.Id)
							price, err := prices.CalculateMarketPrice(*orderbook, orders.SELL, downPos.Available, orders.MARKET_FAK)
							if err == nil {
								// 止损单
								ins = append(ins, runtime.OrderIntent{
									MarketID: o.MarketID,
									TokenID:  downToken.Id,
									Price:    price,
									Side:     orders.SELL,
									Size:     downPos.Available,
								})
								// 取消挂单
								orderIds := buildCancelIntent(upToken.Id, stateSnap.Orders)
								for _, oId := range orderIds {
									ins = append(ins, runtime.OrderIntent{
										Action:  runtime.OrderIntentActionCancel,
										OrderID: oId,
									})
								}
							} else {
								log.Error().Err(err).Str("token_id", downToken.Id).Msg("calculate market price failed")
							}
							log.Info().Float64("LZ", latestZ).Float64("PD", latestPrice-openPrice).Float64("UpBid", upToken.BidPrice).Float64("DownBid", downToken.BidPrice).Float64("SP", price).Msg("触发止损")
						}
					}
				} else { // 跌了
					if okUp && !okDown { // 只有up仓，止损
						if upPos.Available > 0 {
							orderbook := o.GetOrderBook(upToken.Id)
							price, err := prices.CalculateMarketPrice(*orderbook, orders.SELL, upPos.Available, orders.MARKET_FAK)
							if err == nil {
								// 止损单
								ins = append(ins, runtime.OrderIntent{
									MarketID: o.MarketID,
									TokenID:  upToken.Id,
									Price:    price,
									Side:     orders.SELL,
									Size:     upPos.Available,
								})
								// 取消挂单
								orderIds := buildCancelIntent(downToken.Id, stateSnap.Orders)
								for _, oId := range orderIds {
									ins = append(ins, runtime.OrderIntent{
										Action:  runtime.OrderIntentActionCancel,
										OrderID: oId,
									})
								}
							} else {
								log.Error().Err(err).Str("token_id", upToken.Id).Msg("calculate market price failed")
							}
							log.Info().Float64("LZ", latestZ).Float64("PD", latestPrice-openPrice).Float64("UpBid", upToken.BidPrice).Float64("DownBid", downToken.BidPrice).Float64("SP", price).Msg("触发止损")
						}
					}
				}
			}
		}
		return ins
	}

	return nil

}

func buildCancelIntent(tokenId string, orders map[string]state.OrderReservation) []string {
	orderIds := []string{}
	for _, o := range orders {
		if o.TokenID == tokenId {
			orderIds = append(orderIds, o.OrderID)
		}
	}
	return orderIds
}

func TopNGreaterThan(arr []float64, n int, threshold float64) bool {
	if len(arr) < n {
		return false
	}
	for i := range n {
		if arr[i] <= threshold {
			return false
		}
	}
	return true
}

func LastNGreaterThan(arr []float64, n int, threshold float64) bool {
	if len(arr) < n {
		return false
	}

	start := len(arr) - n
	for _, v := range arr[start:] {
		if math.Abs(v) <= threshold {
			return false
		}
	}
	return true
}
