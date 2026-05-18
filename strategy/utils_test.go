package strategy

import (
	"testing"

	"github.com/xiangxn/polypilot/state"

	"github.com/xiangxn/go-polymarket-sdk/orders"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

func TestCalculateMarketPrice_BuyFromAsks(t *testing.T) {
	book := sdk.OrderBook{
		Asks: []orders.Book{{Price: 0.5, Size: 10}, {Price: 0.51, Size: 5}},
	}
	p, err := CalculateMarketPrice(book, orders.BUY, 8, orders.MARKET_FAK)
	if err != nil || p <= 0 {
		t.Fatalf("got p=%v err=%v", p, err)
	}
}

func TestCalculateMarketPrice_BuyEmptyAsks(t *testing.T) {
	if _, err := CalculateMarketPrice(sdk.OrderBook{}, orders.BUY, 5, orders.MARKET_FAK); err == nil {
		t.Fatal("expected no match err")
	}
}

func TestCalculateMarketPrice_SellFromBids(t *testing.T) {
	book := sdk.OrderBook{
		Bids: []orders.Book{{Price: 0.48, Size: 10}, {Price: 0.47, Size: 5}},
	}
	p, err := CalculateMarketPrice(book, orders.SELL, 8, orders.MARKET_FAK)
	if err != nil || p <= 0 {
		t.Fatalf("got p=%v err=%v", p, err)
	}
}

func TestCalculateMarketPrice_SellEmptyBids(t *testing.T) {
	if _, err := CalculateMarketPrice(sdk.OrderBook{}, orders.SELL, 5, orders.MARKET_FAK); err == nil {
		t.Fatal("expected no match err")
	}
}

func TestBuildCancelIntent_FiltersByToken(t *testing.T) {
	os := map[string]state.OrderReservation{
		"o1": {OrderID: "o1", TokenID: "tkA"},
		"o2": {OrderID: "o2", TokenID: "tkB"},
		"o3": {OrderID: "o3", TokenID: "tkA"},
	}
	got := BuildCancelIntent("tkA", os)
	if len(got) != 2 {
		t.Fatalf("expected 2 ids, got %v", got)
	}
}

func TestBuildCancelIntent_NoMatch(t *testing.T) {
	os := map[string]state.OrderReservation{
		"o1": {OrderID: "o1", TokenID: "tkA"},
	}
	got := BuildCancelIntent("tkZ", os)
	if len(got) != 0 {
		t.Fatalf("expected 0 ids, got %v", got)
	}
}

func TestTopNGreaterThan(t *testing.T) {
	if !TopNGreaterThan([]float64{1, 2, 3, 4}, 2, 0) {
		t.Fatal("first 2 > 0")
	}
	if TopNGreaterThan([]float64{1, -1, 2}, 2, 0) {
		t.Fatal("first 2 has -1")
	}
	if TopNGreaterThan([]float64{1, 2}, 5, 0) {
		t.Fatal("not enough data")
	}
}

func TestLastNGreaterThan(t *testing.T) {
	if !LastNGreaterThan([]float64{0, 0, 3, 4}, 2, 1) {
		t.Fatal("last 2 abs > 1")
	}
	if LastNGreaterThan([]float64{0, 0, 0.5, 0.5}, 2, 1) {
		t.Fatal("last 2 abs not > 1")
	}
	if LastNGreaterThan([]float64{1, 2}, 5, 0) {
		t.Fatal("not enough data")
	}
}

func TestGetBestPrice(t *testing.T) {
	book := []orders.Book{{Price: 0.49}, {Price: 0.5}}
	if p := GetBestPrice(book); p != 0.5 {
		t.Fatalf("got %v", p)
	}
	if p := GetBestPrice(nil); p != 0 {
		t.Fatalf("expected 0 for empty book, got %v", p)
	}
}

func TestGetOrderRemainingSize(t *testing.T) {
	os := map[string]state.OrderReservation{
		"o1": {Side: orders.BUY, TokenID: "tkA", RemainingSize: 5},
		"o2": {Side: orders.BUY, TokenID: "tkA", RemainingSize: 3},
		"o3": {Side: orders.SELL, TokenID: "tkA", RemainingSize: 2},
		"o4": {Side: orders.BUY, TokenID: "tkA", RemainingSize: 0}, // filtered (RemainingSize=0)
	}
	if s := GetOrderRemainingSize("tkA", orders.BUY, os); s != 8 {
		t.Fatalf("got %v", s)
	}
	if s := GetOrderRemainingSize("tkB", orders.BUY, os); s != 0 {
		t.Fatalf("got %v", s)
	}
	if s := GetOrderRemainingSize("tkA", orders.SELL, os); s != 2 {
		t.Fatalf("got %v", s)
	}
}
