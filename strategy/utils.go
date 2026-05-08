package strategy

import (
	"fmt"
	"math"

	"github.com/xiangxn/go-polymarket-sdk/orders"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
	"github.com/xiangxn/polypilot/state"
)

func CalculateMarketPrice(books sdk.OrderBook, side orders.Side, amount float64, orderType orders.MarketOrderType) (float64, error) {
	if side == orders.BUY {
		if len(books.Asks) == 0 {
			return 0, fmt.Errorf("no match")
		}
		return orders.CalculateBuyMarketPrice(books.Asks, amount, orderType)
	} else {
		if len(books.Bids) == 0 {
			return 0, fmt.Errorf("no match")
		}
		return orders.CalculateSellMarketPrice(books.Bids, amount, orderType)
	}
}

func BuildCancelIntent(tokenId string, orders map[string]state.OrderReservation) []string {
	orderIds := []string{}
	for _, o := range orders {
		if o.TokenID == tokenId {
			orderIds = append(orderIds, o.OrderID)
		}
	}
	return orderIds
}

func TopNGreaterThan(arr []float64, n int, threshold float64) bool {
	if len(arr) < n {
		return false
	}
	for i := range n {
		if arr[i] <= threshold {
			return false
		}
	}
	return true
}

func LastNGreaterThan(arr []float64, n int, threshold float64) bool {
	if len(arr) < n {
		return false
	}

	start := len(arr) - n
	for _, v := range arr[start:] {
		if math.Abs(v) <= threshold {
			return false
		}
	}
	return true
}

func GetBestPrice(book []orders.Book) float64 {
	var bidPrice float64
	bLen := len(book)
	if bLen > 0 {
		bidPrice = book[bLen-1].Price
	}
	return bidPrice
}

func GetOrderRemainingSize(tokenId string, side orders.Side, os map[string]state.OrderReservation) float64 {
	var rs float64
	for _, o := range os {
		if o.Side == side && o.RemainingSize > 0 && o.TokenID == tokenId {
			rs += o.RemainingSize
		}
	}
	return rs
}
