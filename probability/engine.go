package probability

import (
	"context"
	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/indicators"
	"github.com/xiangxn/polypilot/internal/atomicx"
	"github.com/xiangxn/polypilot/internal/buffer"
	"github.com/xiangxn/polypilot/runtime"
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tidwall/gjson"
	"github.com/xiangxn/go-polymarket-sdk/orders"
	"github.com/xiangxn/go-polymarket-sdk/utils"

	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

type Engine struct {
	market marketState
	signal signalState
	token  tokenState
	book   bookState
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
}

type tokenState struct {
	items map[string]runtime.Token
}

type bookState struct {
	mu    sync.RWMutex
	books map[string]*atomic.Value
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
	e.token.items = make(map[string]runtime.Token, 2)
	e.book.books = make(map[string]*atomic.Value)
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
		if e.market.raw == nil || conditionID != e.market.raw.Get("conditionId").String() || e.market.openPrice == 0 {
			return e.resetForNewMarket(obj)
		}
	case core.EventOrderBook:
		if e.market.raw != nil && e.market.openPrice != 0 && e.market.endTime != 0 && e.signal.zscore.IsReady() {
			var obs runtime.Observation
			orderBook, ok := ev.Data.(sdk.OrderBook)
			if !ok {
				return runtime.Observation{}, false
			}

			token, ok := e.token.items[orderBook.AssetId]
			if !ok {
				return runtime.Observation{}, false
			}

			e.updateOrderBook(orderBook.AssetId, func(old *sdk.OrderBook) *sdk.OrderBook {
				var new sdk.OrderBook
				if old == nil {
					new.AssetId = orderBook.AssetId
					new.Market = orderBook.Market
					new.Timestamp = orderBook.Timestamp
					new.Asks = append([]orders.Book(nil), orderBook.Asks...)
					new.Bids = append([]orders.Book(nil), orderBook.Bids...)
				} else {
					new = CopyOrderBook(*old)
					new.Asks = append([]orders.Book(nil), orderBook.Asks...)
					new.Bids = append([]orders.Book(nil), orderBook.Bids...)
				}
				return &new
			})

			if len(orderBook.Asks) > 0 {
				token.AskPrice = orderBook.Asks[len(orderBook.Asks)-1].Price
			}
			if len(orderBook.Bids) > 0 {
				token.BidPrice = orderBook.Bids[len(orderBook.Bids)-1].Price
			}

			e.token.items[orderBook.AssetId] = token

			obs.At = orderBook.Timestamp
			obs.MarketID = orderBook.Market
			obs.TimeLeftSec = e.market.endTime/1000 - time.Now().Unix()
			obs.Tokens = CopyMap(e.token.items)
			obs.GetOrderBook = func(tID string) *sdk.OrderBook {
				return e.GetOrderBook(tID)
			}

			e.fillFeatures(&obs)

			return obs, true
		}
	case core.EventSignal:
		data, ok := ev.Data.(sdk.ExternalPrice)
		if ok && e.market.openPrice != 0 {
			e.signal.latestPrice.Store(data.Price)
			e.signal.zscore.OnTick(indicators.Tick{Price: data.Price, Timestamp: data.Timestamp})
			if e.signal.zscore.IsReady() {
				timeLeft := e.market.endTime/1000 - time.Now().Unix()
				if timeLeft >= 1 {
					z := e.signal.zscore.ZScore(data.Price, e.market.openPrice, float64(timeLeft))
					e.signal.latestZ.Store(z)
				}
			}
		}
	}
	return runtime.Observation{}, false
}

func (e *Engine) CurrentObservation() (runtime.Observation, bool) {
	if e.market.raw == nil || e.market.endTime == 0 {
		return runtime.Observation{}, false
	}

	obs := runtime.Observation{
		At:          time.Now().Unix(),
		MarketID:    e.market.raw.Get("conditionId").String(),
		TimeLeftSec: e.market.endTime/1000 - time.Now().Unix(),
		Tokens:      CopyMap(e.token.items),
		GetOrderBook: func(tID string) *sdk.OrderBook {
			return e.GetOrderBook(tID)
		},
	}

	e.fillFeatures(&obs)

	return obs, true
}

func (e *Engine) fillFeatures(obs *runtime.Observation) {
	obs.Features = make(map[string]any)
	obs.Features["latestZ"] = e.signal.latestZ.Load()
	if e.signal.zWindows != nil {
		obs.Features["zWindows"] = e.signal.zWindows.Last(10)
	}
	obs.Features["openPrice"] = e.market.openPrice
	obs.Features["latestPrice"] = e.signal.latestPrice.Load()
	obs.Features["endTime"] = e.market.endTime

	if len(e.market.tokenIDs) > 0 {
		if ob := e.GetOrderBook(e.market.tokenIDs[0]); ob == nil {
			obs.Features["imBalance"] = float64(0)
		} else {
			obs.Features["imBalance"] = indicators.CalcImBalance(ob, 3)
		}
	}
}

func (e *Engine) resetForNewMarket(obj gjson.Result) (runtime.Observation, bool) {
	e.signal.latestZ.Store(0)
	e.token.items = make(map[string]runtime.Token, 2)
	e.book.mu.Lock()
	e.book.books = make(map[string]*atomic.Value)
	e.book.mu.Unlock()

	t, err := utils.ToTimestamp(obj.Get("endDate").String())
	if err != nil {
		e.market.endTime = 0
	} else {
		e.market.endTime = t
	}
	e.market.tokenIDs = utils.GetStringArray(&obj, "clobTokenIds")
	if len(e.market.tokenIDs) < 2 {
		return runtime.Observation{}, false
	}

	client := sdk.NewClient(sdk.DefaultConfig())
	cpm := sdk.NewCryptoPriceMonitor(client, sdk.MonitorChainlink, "btc")
	obs, err := client.GetOrderBooks([]sdk.BookParams{{TokenId: e.market.tokenIDs[0]}, {TokenId: e.market.tokenIDs[1]}})
	if err != nil {
		return runtime.Observation{}, false
	}

	e.market.openPrice = cpm.FetchOpenPrice(&obj)
	if e.market.openPrice == 0 {
		return runtime.Observation{}, false
	}

	e.market.raw = &obj
	for _, o := range obs {
		ap, bp := 0.0, 0.0
		if len(o.Asks) > 0 {
			ap = o.Asks[len(o.Asks)-1].Price
		}
		if len(o.Bids) > 0 {
			bp = o.Bids[len(o.Bids)-1].Price
		}
		e.token.items[o.AssetId] = runtime.Token{
			Id:       o.AssetId,
			AskPrice: ap,
			BidPrice: bp,
		}
	}

	if e.signal.zWindows != nil {
		e.signal.zWindows.Reset()
	}

	conditionID := obj.Get("conditionId").String()
	return runtime.Observation{
		At:          time.Now().Unix(),
		MarketID:    conditionID,
		Tokens:      CopyMap(e.token.items),
		TimeLeftSec: e.market.endTime/1000 - time.Now().Unix(),
	}, true
}

/**
* 不要在任何地方修改返回的数据(高风险,这样做是为了高性能)
**/
func (e *Engine) GetOrderBook(tokenId string) *sdk.OrderBook {
	e.book.mu.RLock()
	v := e.book.books[tokenId]
	e.book.mu.RUnlock()

	if v == nil {
		return nil
	}

	ob, _ := v.Load().(*sdk.OrderBook)
	return ob
}

func (e *Engine) getBook(tokenId string) *atomic.Value {
	e.book.mu.RLock()
	v, ok := e.book.books[tokenId]
	e.book.mu.RUnlock()

	if ok {
		return v
	}

	e.book.mu.Lock()
	defer e.book.mu.Unlock()

	if v, ok = e.book.books[tokenId]; ok {
		return v
	}

	v = &atomic.Value{}
	v.Store((*sdk.OrderBook)(nil))
	e.book.books[tokenId] = v
	return v
}

func (e *Engine) updateOrderBook(tokenId string, fn func(old *sdk.OrderBook) *sdk.OrderBook) {
	v := e.getBook(tokenId)
	old, _ := v.Load().(*sdk.OrderBook)
	newOB := fn(old)
	v.Store(newOB)
}

func CopyOrderBook(src sdk.OrderBook) sdk.OrderBook {
	dst := src

	if src.Bids != nil {
		dst.Bids = make([]orders.Book, len(src.Bids))
		copy(dst.Bids, src.Bids)
	}

	if src.Asks != nil {
		dst.Asks = make([]orders.Book, len(src.Asks))
		copy(dst.Asks, src.Asks)
	}

	return dst
}
