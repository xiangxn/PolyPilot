package prices

import (
	"fmt"

	"github.com/polymarket/go-order-utils/pkg/model"
	"github.com/xiangxn/go-polymarket-sdk/orders"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

func CalculateMarketPrice(books sdk.OrderBook, side model.Side, amount float64, orderType orders.MarketOrderType) (float64, error) {
	if side == model.BUY {
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
