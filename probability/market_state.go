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

	openPrice, _ := e.fetchPrices(&obj)
	if openPrice == 0 {
		for _, backoff := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
			time.Sleep(backoff)
			openPrice, _ = e.fetchPrices(&obj)
			if openPrice > 0 {
				break
			}
		}
	}
	if openPrice == 0 {
		log.Warn().Msg("FetchOpenPrice fail")
		return nil
	}
	return &resetPrep{endTime: endTime, openPrice: openPrice, tokenIDs: tokenIDs}
}

func (e *Engine) fetchPrices(obj *gjson.Result) (float64, float64) {
	client := e.client
	if client == nil {
		client = sdk.NewClient(sdk.DefaultConfig())
	}
	cpm := sdk.NewCryptoPriceMonitor(client, sdk.MonitorChainlink, e.Symbol)
	return cpm.FetchOpenPrice(obj)
}

func (e *Engine) cleanupStoresExceptLocked(keep map[string]struct{}) {
	e.tokens.Range(func(key, _ any) bool {
		tokenID, ok := key.(string)
		if !ok {
			e.tokens.Delete(key)
			return true
		}
		if _, exists := keep[tokenID]; !exists {
			e.tokens.Delete(tokenID)
		}
		return true
	})

	e.books.Range(func(key, _ any) bool {
		tokenID, ok := key.(string)
		if !ok {
			e.books.Delete(key)
			return true
		}
		if _, exists := keep[tokenID]; !exists {
			e.books.Delete(tokenID)
		}
		return true
	})
}

// resetForNewMarketLocked applies pre-fetched market data to the engine.
// Caller MUST hold e.mu.Lock(). RPC calls happen in prepareReset, not here.
func (e *Engine) resetForNewMarketLocked(market *marketState, prep *resetPrep) bool {
	if prep == nil || market == nil {
		return false
	}

	var signal signalState
	e.signal.Read(func(v signalState) {
		signal = v
	})

	signal.latestZ.Store(0)

	market.endTime = prep.endTime
	market.tokenIDs = prep.tokenIDs
	market.openPrice = prep.openPrice

	signal.zWindows.Reset()

	keep := make(map[string]struct{}, len(prep.tokenIDs))
	for _, t := range prep.tokenIDs {
		keep[t] = struct{}{}
	}
	e.cleanupStoresExceptLocked(keep)

	for _, t := range prep.tokenIDs {
		token := runtime.Token{Id: t}
		e.tokens.Store(t, token)
	}
	return true
}
