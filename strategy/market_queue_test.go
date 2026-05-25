package strategy

import (
	"testing"

	"github.com/xiangxn/polypilot/market"
)

func TestMarketQueue_LRU(t *testing.T) {
	q := NewQueueMap[market.SlugMarket](2)
	q.Add("m1", market.SlugMarket{MarketID: "m1"})
	q.Add("m2", market.SlugMarket{MarketID: "m2"})
	q.Add("m3", market.SlugMarket{MarketID: "m3"})
	if _, ok := q.Get("m1"); ok {
		t.Fatal("m1 should be evicted")
	}
	if _, ok := q.Get("m3"); !ok {
		t.Fatal("m3 should exist")
	}
}

func TestMarketQueue_UpdateInPlace(t *testing.T) {
	q := NewQueueMap[market.SlugMarket](2)
	q.Add("m1", market.SlugMarket{MarketID: "m1", TickSize: 0.01})
	q.Add("m1", market.SlugMarket{MarketID: "m1", TickSize: 0.001})
	got, _ := q.Get("m1")
	if got.TickSize != 0.001 {
		t.Fatalf("expected updated tickSize, got %v", got.TickSize)
	}
}
