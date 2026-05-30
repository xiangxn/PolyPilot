package probability

import (
	"context"
	"sync"
	"time"

	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/indicators"
	"github.com/xiangxn/polypilot/internal/atomicx"
	"github.com/xiangxn/polypilot/internal/buffer"
	"github.com/xiangxn/polypilot/runtime"
	"github.com/xiangxn/polypilot/utils"

	"github.com/tidwall/gjson"

	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

type Engine struct {
	Symbol string
	market *utils.SafeState[marketState]
	signal *utils.SafeState[signalState]
	// tokenId -> runtime.Token
	tokens sync.Map
	// tokenId -> sdk.BookStore
	books  sync.Map
	client *sdk.PolymarketClient
}

type marketState struct {
	marketId  string
	openPrice float64
	endTime   int64
	tokenIDs  []string
}

type signalState struct {
	latestPrice atomicx.Float64
	zscore      *indicators.ZScore
	latestZ     atomicx.Float64
	zWindows    *buffer.RingBuffer
	latestProb  atomicx.Float64
}

// NewEngine constructs a probability Engine that uses the provided Polymarket
// SDK client for RPC calls (order books, open price). Passing nil falls back
// to sdk.NewClient(sdk.DefaultConfig()) at first use — useful for tests.
func NewEngine(symbol string, client *sdk.PolymarketClient) *Engine {
	return &Engine{Symbol: symbol, client: client,
		market: utils.NewSafeState(marketState{}),
		signal: utils.NewSafeState(signalState{
			zscore:   indicators.NewZScore(60),
			zWindows: buffer.NewRingBuffer(60),
		}),
	}
}

func (e *Engine) Init(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var signal signalState
				e.signal.Read(func(v signalState) {
					signal = v
				})
				z := signal.latestZ.Load()
				signal.zWindows.Add(z)
			}
		}
	}()
}

// 只有EventMarket，EventOrderBook事件会向下传递
func (e *Engine) OnUpdate(ev core.Event) (runtime.Observation, bool) {
	switch ev.Type {
	case core.EventMarket:
		// 如果有新的市场开始就更新当前市场信息
		obj, ok := ev.Data.(gjson.Result)
		if !ok {
			return runtime.Observation{}, false
		}
		conditionID := obj.Get("conditionId").String()

		var market marketState
		e.market.Read(func(v marketState) {
			market = v
		})

		if market.marketId != conditionID {
			market.marketId = conditionID

			prep := e.prepareReset(obj) // RPC outside lock
			if prep == nil {
				return runtime.Observation{}, false
			}

			if e.resetForNewMarketLocked(&market, prep) {
				e.market.Replace(market)
				return e.CurrentObservation()
			}
		}

		return runtime.Observation{}, false

	case core.EventOrderBook:
		orderBook, ok := ev.Data.(*sdk.OrderBook)
		if !ok || orderBook == nil {
			return runtime.Observation{}, false
		}

		var market marketState
		e.market.Read(func(v marketState) {
			market = v
		})

		if market.openPrice == 0 || market.endTime == 0 {
			return runtime.Observation{}, false
		}

		// 更新 orderbook
		e.updateOrderBook(orderBook.AssetId, func(old *sdk.OrderBook) *sdk.OrderBook {
			return orderBook
		})

		// 更新Tokens
		e.updateToken(orderBook)

		return e.CurrentObservation()
	case core.EventExternalPrice:
		data, ok := ev.Data.(sdk.ExternalPrice)
		if !ok {
			return runtime.Observation{}, false
		}

		var market marketState
		e.market.Read(func(v marketState) {
			market = v
		})

		if market.openPrice == 0 || market.endTime == 0 {
			return runtime.Observation{}, false
		}

		var signal signalState
		e.signal.Read(func(v signalState) {
			signal = v
		})

		signal.latestPrice.Store(data.Price)
		signal.zscore.OnTick(indicators.Tick{Price: data.Price, Timestamp: data.Timestamp})
		if signal.zscore.IsReady() {
			timeLeft := market.endTime/1000 - time.Now().Unix()
			if timeLeft >= 1 {
				z := signal.zscore.ZScore(data.Price, market.openPrice, float64(timeLeft))
				signal.latestZ.Store(z)
			}
		}
	case core.EventProbability:
		prop, ok := ev.Data.(float64)
		if ok {
			var signal signalState
			e.signal.Read(func(v signalState) {
				signal = v
			})
			signal.latestProb.Store(prop)
		}
	}
	return runtime.Observation{}, false
}

func (e *Engine) CurrentObservation() (runtime.Observation, bool) {

	var market marketState
	e.market.Read(func(v marketState) {
		market = v
	})

	if market.endTime == 0 || market.openPrice == 0 {
		return runtime.Observation{}, false
	}

	var signal signalState
	e.signal.Read(func(v signalState) {
		signal = v
	})
	obs := runtime.Observation{
		At:          time.Now().Unix(),
		MarketID:    market.marketId,
		TimeLeftSec: market.endTime/1000 - time.Now().Unix(),
		Probability: signal.latestProb.Load(),
		Tokens:      e.getTokens(market.tokenIDs),
		TokenIds:    make([]string, len(market.tokenIDs)),
		GetOrderBook: func(tID string) *sdk.OrderBook {
			return e.GetOrderBook(tID)
		},
		FetchPrices: func(obj *gjson.Result) (float64, float64) {
			return e.fetchPrices(obj)
		},
	}
	if len(market.tokenIDs) > 0 {
		copy(obs.TokenIds, market.tokenIDs)
	}

	e.fillFeaturesLocked(&obs, &market, &signal)

	return obs, true
}
