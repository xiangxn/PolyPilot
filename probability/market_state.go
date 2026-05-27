package probability

import (
	"time"

	"github.com/xiangxn/polypilot/runtime"

	"github.com/tidwall/gjson"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
	"github.com/xiangxn/go-polymarket-sdk/utils"
)

// resetPrep holds RPC-derived data needed to reset market state.
type resetPrep struct {
	endTime   int64
	openPrice float64
	tokenIDs  []string
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

	cpm := sdk.NewCryptoPriceMonitor(client, sdk.MonitorChainlink, e.Symbol)
	openPrice := cpm.FetchOpenPrice(&obj)
	if openPrice == 0 {
		return nil
	}
	return &resetPrep{endTime: endTime, openPrice: openPrice, tokenIDs: tokenIDs}
}

// resetForNewMarketLocked applies pre-fetched market data to the engine.
// Caller MUST hold e.mu.Lock(). RPC calls happen in prepareReset, not here.
func (e *Engine) resetForNewMarketLocked(obj gjson.Result, prep *resetPrep) (runtime.Observation, bool) {
	if prep == nil {
		return runtime.Observation{}, false
	}
	e.signal.latestZ.Store(0)

	e.market.endTime = prep.endTime
	e.market.tokenIDs = prep.tokenIDs
	e.market.openPrice = prep.openPrice
	e.market.raw = &obj
	e.generation.Add(1)

	if e.signal.zWindows != nil {
		e.signal.zWindows.Reset()
	}

	tokens := make(map[string]runtime.Token)
	for _, t := range prep.tokenIDs {
		token := &runtime.Token{Id: t}
		e.tokens.Store(t, token)
		tokens[t] = *token
	}
	obs := runtime.Observation{
		At:          time.Now().Unix(),
		MarketID:    obj.Get("conditionId").String(),
		Tokens:      tokens,
		TokenIds:    append([]string(nil), prep.tokenIDs...),
		TimeLeftSec: prep.endTime/1000 - time.Now().Unix(),
	}
	return obs, true
}
