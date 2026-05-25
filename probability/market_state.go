package probability

import (
	"time"

	"github.com/xiangxn/polypilot/runtime"

	"github.com/tidwall/gjson"
	"github.com/xiangxn/go-polymarket-sdk/orders"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
	"github.com/xiangxn/go-polymarket-sdk/utils"
)

// resetPrep holds RPC-derived data needed to reset market state.
type resetPrep struct {
	endTime   int64
	openPrice float64
	tokenIDs  []string
	books     []sdk.OrderBookSummary
}

// prepareReset performs all RPC calls outside the engine lock. Returns nil
// if the market is invalid or RPC fails (caller should leave state unchanged).
func (e *Engine) prepareReset(obj gjson.Result) *resetPrep {
	tokenIDs := utils.GetStringArray(&obj, "clobTokenIds")
	if len(tokenIDs) < 2 {
		return nil
	}
	endTime, err := utils.ToTimestamp(obj.Get("endDate").String())
	if err != nil {
		endTime = 0
	}
	client := e.client
	if client == nil {
		client = sdk.NewClient(sdk.DefaultConfig())
	}
	books, err := client.GetOrderBooks([]sdk.BookParams{
		{TokenId: tokenIDs[0]}, {TokenId: tokenIDs[1]},
	})
	if err != nil {
		return nil
	}
	cpm := sdk.NewCryptoPriceMonitor(client, sdk.MonitorChainlink, e.Symbol)
	openPrice := cpm.FetchOpenPrice(&obj)
	if openPrice == 0 {
		return nil
	}
	return &resetPrep{endTime: endTime, openPrice: openPrice, tokenIDs: tokenIDs, books: books}
}

// resetForNewMarketLocked applies pre-fetched market data to the engine.
// Caller MUST hold e.mu.Lock(). RPC calls happen in prepareReset, not here.
func (e *Engine) resetForNewMarketLocked(obj gjson.Result, prep *resetPrep) (runtime.Observation, bool) {
	if prep == nil {
		return runtime.Observation{}, false
	}
	e.signal.latestZ.Store(0)
	e.token.items = make(map[string]runtime.Token, 2)

	e.market.endTime = prep.endTime
	e.market.tokenIDs = prep.tokenIDs
	e.market.openPrice = prep.openPrice
	e.market.raw = &obj
	e.generation.Add(1)

	for _, o := range prep.books {
		ap, bp := 0.0, 0.0
		if len(o.Asks) > 0 {
			ap = o.Asks[len(o.Asks)-1].Price
		}
		if len(o.Bids) > 0 {
			bp = o.Bids[len(o.Bids)-1].Price
		}
		e.token.items[o.AssetId] = runtime.Token{Id: o.AssetId, AskPrice: ap, BidPrice: bp}
		ob := o
		e.updateOrderBook(o.AssetId, func(old *sdk.OrderBook) *sdk.OrderBook {
			return &sdk.OrderBook{
				AssetId:   ob.AssetId,
				Market:    ob.Market,
				Timestamp: ob.Timestamp,
				Asks:      append([]orders.Book(nil), ob.Asks...),
				Bids:      append([]orders.Book(nil), ob.Bids...),
			}
		})
	}

	if e.signal.zWindows != nil {
		e.signal.zWindows.Reset()
	}

	obs := runtime.Observation{
		At:          time.Now().Unix(),
		MarketID:    obj.Get("conditionId").String(),
		Tokens:      CopyMap(e.token.items),
		TokenIds:    append([]string(nil), prep.tokenIDs...),
		TimeLeftSec: prep.endTime/1000 - time.Now().Unix(),
	}
	return obs, true
}
