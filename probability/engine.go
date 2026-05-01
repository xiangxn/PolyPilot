package probability

import (
	"context"
	"maps"
	"polypilot/core"
	"polypilot/indicators"
	"polypilot/internal/atomicx"
	"polypilot/internal/buffer"
	"polypilot/runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tidwall/gjson"
	"github.com/xiangxn/go-polymarket-sdk/orders"
	"github.com/xiangxn/go-polymarket-sdk/utils"

	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

type Engine struct {
	market      *gjson.Result
	openPrice   float64
	latestPrice atomicx.Float64
	endTime     int64

	zscore *indicators.ZScore

	latestZ  atomicx.Float64
	zWindows *buffer.RingBuffer

	tokens map[string]runtime.Token

	booksMu sync.RWMutex
	books   map[string]*atomic.Value

	tokenIds []string
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
	e.books = make(map[string]*atomic.Value)

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
		if ok && (e.market == nil || conditionId != e.market.Get("conditionId").String() || e.openPrice == 0) {
			e.latestZ.Store(0)
			e.tokens = make(map[string]runtime.Token, 2)
			e.booksMu.Lock()
			e.books = make(map[string]*atomic.Value)
			e.booksMu.Unlock()

			t, err := utils.ToTimestamp(obj.Get("endDate").String())
			if err != nil {
				e.endTime = 0
			} else {
				e.endTime = t
			}
			e.tokenIds = utils.GetStringArray(&obj, "clobTokenIds")
			client := sdk.NewClient(sdk.DefaultConfig())
			cpm := sdk.NewCryptoPriceMonitor(client, sdk.MonitorChainlink, "btc")
			obs, err := client.GetOrderBooks([]sdk.BookParams{{TokenId: e.tokenIds[0]}, {TokenId: e.tokenIds[1]}})
			if err != nil {
				return runtime.Observation{}, false
			}

			e.openPrice = cpm.FetchOpenPrice(&obj)
			if e.openPrice == 0 {
				return runtime.Observation{}, false
			}

			e.market = &obj
			for _, o := range obs {
				ap, bp := 0.0, 0.0
				if len(o.Asks) > 0 {
					ap = o.Asks[len(o.Asks)-1].Price
				}
				if len(o.Bids) > 0 {
					bp = o.Bids[len(o.Bids)-1].Price
				}
				e.tokens[o.AssetId] = runtime.Token{
					Id:       o.AssetId,
					AskPrice: ap,
					BidPrice: bp,
				}
			}

			if e.zscore == nil {
				e.zscore = indicators.NewZScore(60)
			}

			if e.zWindows == nil {
				e.zWindows = buffer.NewRingBuffer(e.zscore.WindowSize())
			} else {
				e.zWindows.Reset()
			}
			return runtime.Observation{
				At:          time.Now().Unix(),
				MarketID:    conditionId,
				Tokens:      CopyMap(e.tokens),
				TimeLeftSec: e.endTime/1000 - time.Now().Unix(),
			}, true
		}
	case core.EventOrderBook:
		if e.market != nil && e.openPrice != 0 && e.endTime != 0 && e.zscore.IsReady() {
			var obs runtime.Observation
			orderBook, ok := ev.Data.(sdk.OrderBook)
			if !ok {
				return runtime.Observation{}, false
			}

			token, ok := e.tokens[orderBook.AssetId]
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

			e.tokens[orderBook.AssetId] = token

			obs.At = orderBook.Timestamp
			obs.MarketID = orderBook.Market
			obs.TimeLeftSec = e.endTime/1000 - time.Now().Unix()
			obs.Tokens = CopyMap(e.tokens)
			obs.GetOrderBook = func(tId string) *sdk.OrderBook {
				return e.GetOrderBook(tId)
			}

			obs.Features = make(map[string]any)
			obs.Features["latestZ"] = e.latestZ.Load()
			if e.zWindows != nil {
				obs.Features["zWindows"] = e.zWindows.Last(10) // 最后10s的zscore值
			}
			obs.Features["openPrice"] = e.openPrice
			obs.Features["latestPrice"] = e.latestPrice.Load()
			obs.Features["endTime"] = e.endTime

			if ob := e.GetOrderBook(e.tokenIds[0]); ob == nil {
				obs.Features["imBalance"] = float64(0)
			} else {
				obs.Features["imBalance"] = indicators.CalcImBalance(ob, 3)
			}

			return obs, true
		}
	case core.EventSignal:
		data, ok := ev.Data.(sdk.ExternalPrice)
		if ok && e.openPrice != 0 {
			e.latestPrice.Store(data.Price)
			e.zscore.OnTick(indicators.Tick{Price: data.Price, Timestamp: data.Timestamp})
			if e.zscore.IsReady() { // 为有效数据后才开始记录zscore (这里为窗口大小的一半)
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

/**
* 不要在任何地方修改返回的数据(高风险,这样做是为了高性能)
**/
func (e *Engine) GetOrderBook(tokenId string) *sdk.OrderBook {
	e.booksMu.RLock()
	v := e.books[tokenId]
	e.booksMu.RUnlock()

	if v == nil {
		return nil
	}

	ob, _ := v.Load().(*sdk.OrderBook)
	return ob
}

func (e *Engine) getBook(tokenId string) *atomic.Value {
	e.booksMu.RLock()
	v, ok := e.books[tokenId]
	e.booksMu.RUnlock()

	if ok {
		return v
	}

	e.booksMu.Lock()
	defer e.booksMu.Unlock()

	// double check
	if v, ok = e.books[tokenId]; ok {
		return v
	}

	v = &atomic.Value{}
	v.Store((*sdk.OrderBook)(nil))

	e.books[tokenId] = v
	return v
}

func (e *Engine) updateOrderBook(tokenId string, fn func(old *sdk.OrderBook) *sdk.OrderBook) {
	v := e.getBook(tokenId)
	if v != nil {
		old, _ := v.Load().(*sdk.OrderBook)
		newOB := fn(old)
		v.Store(newOB)
	} else {
		newOB := fn(nil)
		v.Store(newOB)
	}
}

func CopyOrderBook(src sdk.OrderBook) sdk.OrderBook {
	dst := src // 先浅拷贝一层

	// 深拷贝 Bids
	if src.Bids != nil {
		dst.Bids = make([]orders.Book, len(src.Bids))
		copy(dst.Bids, src.Bids)
	}

	// 深拷贝 Asks
	if src.Asks != nil {
		dst.Asks = make([]orders.Book, len(src.Asks))
		copy(dst.Asks, src.Asks)
	}

	return dst
}
