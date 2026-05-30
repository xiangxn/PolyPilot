package probability

import (
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

/**
* 不要在任何地方修改返回的数据(高风险,这样做是为了高性能)
**/
func (e *Engine) GetOrderBook(tokenId string) *sdk.OrderBook {
	v, exists := e.books.Load(tokenId)

	if !exists || v == nil {
		return nil
	}

	store, ok := v.(sdk.BookStore)
	if !ok {
		return nil
	}
	// check stale
	ob := store.Load()
	if ob == nil {
		return nil
	}
	if ob.Latency > 100 {
		log.Debug().Str("market", ob.Market).Int64("timestamp", ob.Timestamp).Int64("latency", ob.Latency).Msg("orderbook latency")
		return nil
	}
	return ob
}

func (e *Engine) updateOrderBook(tokenId string, fn func(old *sdk.OrderBook) *sdk.OrderBook) {
	v, _ := e.books.LoadOrStore(tokenId, sdk.BookStore{})
	store := v.(sdk.BookStore)

	old := store.Load()
	newOB := fn(old)
	if old != nil {
		if newOB.Timestamp < old.Timestamp {
			return
		}
	}
	store.Store(newOB)
	e.books.Store(tokenId, store)
}
