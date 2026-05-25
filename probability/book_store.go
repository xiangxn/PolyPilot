package probability

import (
	"math"
	"sync/atomic"

	"github.com/xiangxn/go-polymarket-sdk/orders"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

/**
* 不要在任何地方修改返回的数据(高风险,这样做是为了高性能)
**/
func (e *Engine) GetOrderBook(tokenId string) *sdk.OrderBook {
	e.book.mu.RLock()
	v, exists := e.book.books[tokenId]
	e.book.mu.RUnlock()

	if !exists || v == nil {
		return nil
	}

	ob, _ := v.Load().(*sdk.OrderBook)
	if ob == nil {
		return nil
	}
	// check stale
	if ob.Latency > 500 {
		log.Warn().Str("market", ob.Market).Int64("timestamp", ob.Timestamp).Int64("latency", ob.Latency).Msg("orderbook latency")
		return nil
	}
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

	if e.book.books == nil {
		e.book.books = make(map[string]*atomic.Value)
	}
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

func Phi(z float64) float64 {
	return 0.5 * (1.0 + math.Erf(z/math.Sqrt2))
}
