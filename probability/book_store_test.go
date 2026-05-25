package probability

import (
	"sync"
	"testing"

	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

func TestUpdateOrderBook_ConcurrentRace(t *testing.T) {
	e := &Engine{}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				e.updateOrderBook("tk1", func(old *sdk.OrderBook) *sdk.OrderBook {
					return &sdk.OrderBook{AssetId: "tk1"}
				})
				_ = e.GetOrderBook("tk1")
			}
		}(i)
	}
	wg.Wait()
}

func TestGetOrderBook_StaleReturnsNil(t *testing.T) {
	e := &Engine{}
	e.updateOrderBook("tk1", func(old *sdk.OrderBook) *sdk.OrderBook {
		return &sdk.OrderBook{AssetId: "tk1", Latency: 9999}
	})
	if e.GetOrderBook("tk1") != nil {
		t.Fatal("stale book should return nil")
	}
}
