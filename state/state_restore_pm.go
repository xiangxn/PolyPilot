package state

import (
	"github.com/tidwall/gjson"
	"github.com/xiangxn/go-polymarket-sdk/orders"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

type PolymarketStateClient struct {
	Client         *sdk.PolymarketClient
	PositionLimits int
}

func NewPolymarketStateClient(client *sdk.PolymarketClient, positionLimits int) *PolymarketStateClient {
	return &PolymarketStateClient{
		Client:         client,
		PositionLimits: positionLimits,
	}
}

func (p *PolymarketStateClient) GetOpenOrders() ([]orders.OpenOrder, error) {
	return p.Client.GetOpenOrders(nil, false, nil)
}

func (p *PolymarketStateClient) GetPositions() (*gjson.Result, error) {
	return p.Client.SearchPositions("", false, positionsAPILimit(p.PositionLimits))
}

func positionsAPILimit(limit int) int {
	if limit > 0 {
		return limit
	}
	return defaultPositionsAPILimit
}
