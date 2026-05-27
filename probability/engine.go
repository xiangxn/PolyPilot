package probability

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/indicators"
	"github.com/xiangxn/polypilot/internal/atomicx"
	"github.com/xiangxn/polypilot/internal/buffer"
	"github.com/xiangxn/polypilot/logx"
	"github.com/xiangxn/polypilot/runtime"

	"github.com/tidwall/gjson"

	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

var log = logx.Module("probability")

// Engine 不可在多个独立 goroutine 中直接读写其字段。所有非 atomic
// 字段（market.*、token.items）的并发访问由 mu 串行化：
//   - 写路径：OnUpdate 的 EventMarket / EventOrderBook 分支（Lock）
//   - 读路径：CurrentObservation、EventSignal 分支（RLock）
//
// book.* 与 signal.zscore / signal.zWindows 各自持有内部锁，
// signal.latestPrice / signal.latestZ 使用 atomicx.Float64，
// 这些字段不受 mu 保护。
type Engine struct {
	Symbol string
	mu     sync.RWMutex
	market marketState
	signal signalState
	// tokenId -> *runtime.Token
	tokens sync.Map
	// tokenId -> *sdk.BookStore
	books      sync.Map
	client     *sdk.PolymarketClient
	generation atomic.Uint64
}

type marketState struct {
	raw       *gjson.Result
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
	return &Engine{Symbol: symbol, client: client}
}

func (e *Engine) Init(ctx context.Context) {
	e.signal.zscore = indicators.NewZScore(60)
	e.signal.zWindows = buffer.NewRingBuffer(e.signal.zscore.WindowSize())

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e.signal.zWindows != nil {
					z := e.signal.latestZ.Load()
					e.signal.zWindows.Add(z)
				}
			}
		}
	}()
}

func (e *Engine) OnUpdate(ev core.Event) (runtime.Observation, bool) {
	switch ev.Type {
	case core.EventMarket:
		obj, ok := ev.Data.(gjson.Result)
		if !ok {
			return runtime.Observation{}, false
		}
		conditionID := obj.Get("conditionId").String()

		e.mu.RLock()
		needReset := e.market.raw == nil || conditionID != e.market.raw.Get("conditionId").String() || e.market.openPrice == 0
		gen := e.generation.Load()
		e.mu.RUnlock()

		if !needReset {
			return runtime.Observation{}, false
		}

		prep := e.prepareReset(obj) // RPC outside lock
		if prep == nil {
			return runtime.Observation{}, false
		}

		e.mu.Lock()
		defer e.mu.Unlock()
		if e.generation.Load() != gen {
			// another reset happened while we were doing RPC; skip
			return runtime.Observation{}, false
		}
		return e.resetForNewMarketLocked(obj, prep)

	case core.EventOrderBook:
		orderBook, ok := ev.Data.(*sdk.OrderBook)
		if !ok || orderBook == nil {
			return runtime.Observation{}, false
		}

		// 更新orderbook
		e.updateOrderBook(orderBook.AssetId, func(old *sdk.OrderBook) *sdk.OrderBook {
			return orderBook
		})

		if e.market.raw == nil || e.market.openPrice == 0 || e.market.endTime == 0 {
			return runtime.Observation{}, false
		}

		// 更新Tokens
		value, ok := e.tokens.Load(orderBook.AssetId)
		if !ok {
			return runtime.Observation{}, false
		}
		token := value.(*runtime.Token)

		if len(orderBook.Asks) > 0 {
			token.AskPrice = orderBook.Asks[len(orderBook.Asks)-1].Price
		}
		if len(orderBook.Bids) > 0 {
			token.BidPrice = orderBook.Bids[len(orderBook.Bids)-1].Price
		}

		tokens := make(map[string]runtime.Token)
		for _, t := range e.market.tokenIDs {
			v, _ := e.tokens.Load(t)
			tokens[t] = *v.(*runtime.Token)
		}
		var obs runtime.Observation
		obs.At = orderBook.Timestamp
		obs.MarketID = orderBook.Market
		obs.TimeLeftSec = e.market.endTime/1000 - time.Now().Unix()
		obs.Probability = e.signal.latestProb.Load()
		obs.Tokens = tokens
		obs.TokenIds = make([]string, len(e.market.tokenIDs))
		copy(obs.TokenIds, e.market.tokenIDs)
		obs.GetOrderBook = func(tID string) *sdk.OrderBook {
			return e.GetOrderBook(tID)
		}
		e.fillFeaturesLocked(&obs)
		return obs, true
	case core.EventExternalPrice:
		data, ok := ev.Data.(sdk.ExternalPrice)
		if !ok {
			return runtime.Observation{}, false
		}

		e.mu.RLock()
		openPrice := e.market.openPrice
		endTime := e.market.endTime
		e.mu.RUnlock()

		if openPrice == 0 {
			return runtime.Observation{}, false
		}

		e.signal.latestPrice.Store(data.Price)
		e.signal.zscore.OnTick(indicators.Tick{Price: data.Price, Timestamp: data.Timestamp})
		if e.signal.zscore.IsReady() {
			timeLeft := endTime/1000 - time.Now().Unix()
			if timeLeft >= 1 {
				z := e.signal.zscore.ZScore(data.Price, openPrice, float64(timeLeft))
				e.signal.latestZ.Store(z)
			}
		}
	case core.EventProbability:
		prop, ok := ev.Data.(float64)
		if ok {
			e.signal.latestProb.Store(prop)
		}
	}
	return runtime.Observation{}, false
}

func (e *Engine) CurrentObservation() (runtime.Observation, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.market.raw == nil || e.market.endTime == 0 {
		return runtime.Observation{}, false
	}

	tokens := make(map[string]runtime.Token)
	for _, t := range e.market.tokenIDs {
		v, _ := e.tokens.Load(t)
		tokens[t] = *v.(*runtime.Token)
	}
	obs := runtime.Observation{
		At:          time.Now().Unix(),
		MarketID:    e.market.raw.Get("conditionId").String(),
		TimeLeftSec: e.market.endTime/1000 - time.Now().Unix(),
		Probability: e.signal.latestProb.Load(),
		Tokens:      tokens,
		TokenIds:    make([]string, len(e.market.tokenIDs)),
		GetOrderBook: func(tID string) *sdk.OrderBook {
			return e.GetOrderBook(tID)
		},
	}
	if len(e.market.tokenIDs) > 0 {
		copy(obs.TokenIds, e.market.tokenIDs)
	}

	e.fillFeaturesLocked(&obs)

	return obs, true
}
