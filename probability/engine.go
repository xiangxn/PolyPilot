package probability

import (
	"context"
	"maps"
	"polypilot/core"
	"polypilot/indicators"
	"polypilot/internal/atomicx"
	"polypilot/runtime"
	"time"

	"github.com/tidwall/gjson"
	"github.com/xiangxn/go-polymarket-sdk/utils"

	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

type Engine struct {
	market    *gjson.Result
	openPrice float64
	endTime   int64

	zscore *indicators.ZScore

	latestZ  atomicx.Float64
	zWindows *indicators.RingBuffer

	tokens map[string]runtime.Token
}

func CopyMap[K comparable, V any](src map[K]V) map[K]V {
	if src == nil {
		return nil
	}

	dst := make(map[K]V, len(src))
	maps.Copy(dst, src)
	return dst
}

func (e *Engine) Init(ctx context.Context) {
	e.tokens = make(map[string]runtime.Token, 2)

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e.zWindows != nil {
					z := e.latestZ.Load()
					e.zWindows.Add(z)
				}
			}
		}
	}()
}

func (e *Engine) OnUpdate(ev core.Event) (runtime.Observation, bool) {
	// log.Printf("type: %v", ev.Type)
	switch ev.Type {
	case core.EventMarket:
		obj, ok := ev.Data.(gjson.Result)
		conditionId := obj.Get("conditionId").String()
		if ok && (e.market == nil || conditionId != e.market.Get("conditionId").String()) {
			e.latestZ.Store(0)
			e.tokens = make(map[string]runtime.Token, 2)
			e.market = &obj
			t, err := utils.ToTimestamp(obj.Get("endDate").String())
			if err != nil {
				e.endTime = 0
			} else {
				e.endTime = t
			}
			tokenIds := utils.GetStringArray(&obj, "clobTokenIds")
			for _, tokenId := range tokenIds {
				e.tokens[tokenId] = runtime.Token{Id: tokenId}
			}
			client := sdk.NewClient(core.DefaultReadonlyPrivKey, sdk.DefaultConfig())
			cpm := sdk.NewCryptoPriceMonitor(client, sdk.MonitorChainlink, "btc")
			e.openPrice = cpm.FetchOpenPrice(e.market)

			if e.zscore == nil {
				e.zscore = indicators.NewZScore(60)
			}

			if e.zWindows == nil {
				e.zWindows = indicators.NewRingBuffer(e.zscore.WindowSize())
			} else {
				e.zWindows.Reset()
			}
			return runtime.Observation{
				At:          time.Now().Unix(),
				MarketID:    conditionId,
				Tokens:      e.tokens,
				TimeLeftSec: e.endTime/1000 - time.Now().Unix(),
			}, true
		}
	case core.EventOrderBook:
		if e.market != nil && e.openPrice != 0 && e.endTime != 0 && e.zscore.Length() >= e.zscore.WindowSize() {
			var obs runtime.Observation
			orderBook, ok := ev.Data.(sdk.OrderBook)
			if !ok {
				return runtime.Observation{}, false
			}

			token, ok := e.tokens[orderBook.AssetId]
			if !ok {
				return runtime.Observation{}, false
			}

			if len(orderBook.Asks) > 0 {
				token.AskPrice = orderBook.Asks[len(orderBook.Asks)-1].Price
			}
			if len(orderBook.Bids) > 0 {
				token.BidPrice = orderBook.Bids[len(orderBook.Bids)-1].Price
			}

			e.tokens[orderBook.AssetId] = token

			obs.At = orderBook.Timestamp
			obs.MarketID = orderBook.Market
			obs.TimeLeftSec = e.endTime/1000 - time.Now().Unix()
			obs.Tokens = CopyMap(e.tokens)

			obs.Features = make(map[string]any)
			obs.Features["latestZ"] = e.latestZ.Load()
			if e.zWindows != nil {
				obs.Features["zWindows"] = e.zWindows.Last(10)
			}
			return obs, true
		}
	case core.EventSignal:
		data, ok := ev.Data.(sdk.ExternalPrice)
		if ok && e.openPrice != 0 {
			e.zscore.OnTick(indicators.Tick{Price: data.Price, Timestamp: data.Timestamp})
			if e.zscore.Length() >= e.zscore.WindowSize() { // 为有效数据后才开始记录zscore
				timeLeft := e.endTime/1000 - time.Now().Unix()
				if timeLeft >= 1 {
					z := e.zscore.ZScore(data.Price, e.openPrice, float64(timeLeft))
					e.latestZ.Store(z)
				}
			}
		}
	}
	return runtime.Observation{}, false
}
