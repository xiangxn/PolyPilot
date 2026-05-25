package indicators

import (
	"testing"

	"github.com/xiangxn/go-polymarket-sdk/orders"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

func TestCalcImBalance(t *testing.T) {
	bid := []orders.Book{{Price: 0.49, Size: 100}, {Price: 0.48, Size: 50}}
	ask := []orders.Book{{Price: 0.51, Size: 80}, {Price: 0.52, Size: 40}}
	ob := &sdk.OrderBook{Bids: bid, Asks: ask}

	im := CalcImBalance(ob, 2)
	want := (150.0 - 120.0) / (150.0 + 120.0)
	if im != want {
		t.Fatalf("im=%v want=%v", im, want)
	}
}

func TestCalcImBalance_Empty(t *testing.T) {
	if CalcImBalance(&sdk.OrderBook{}, 3) != 0 {
		t.Fatal("empty book should return 0")
	}
}

func TestCalcImBalance_OneSideEmpty(t *testing.T) {
	asksOnly := &sdk.OrderBook{Asks: []orders.Book{{Price: 0.5, Size: 1}}}
	if CalcImBalance(asksOnly, 1) != -1 {
		t.Fatal("asks-only should be -1")
	}
	bidsOnly := &sdk.OrderBook{Bids: []orders.Book{{Price: 0.5, Size: 1}}}
	if CalcImBalance(bidsOnly, 1) != 1 {
		t.Fatal("bids-only should be 1")
	}
}

func TestCalcImBalance_NilBook(t *testing.T) {
	if CalcImBalance(nil, 3) != 0 {
		t.Fatal("nil should return 0")
	}
}
