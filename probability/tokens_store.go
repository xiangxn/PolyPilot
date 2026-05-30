package probability

import (
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
	"github.com/xiangxn/polypilot/runtime"
)

func (e *Engine) updateToken(orderBook *sdk.OrderBook) {
	value, ok := e.tokens.Load(orderBook.AssetId)
	if !ok {
		return
	}
	token := value.(runtime.Token)
	token.Latency = orderBook.Latency

	if len(orderBook.Asks) > 0 {
		ob := orderBook.Asks[len(orderBook.Asks)-1]
		token.AskPrice = ob.Price
		token.AskSize = ob.Size
	}
	if len(orderBook.Bids) > 0 {
		ob := orderBook.Bids[len(orderBook.Bids)-1]
		token.BidPrice = ob.Price
		token.BidSize = ob.Size
	}
	e.tokens.Store(token.Id, token)
}

func (e *Engine) getTokens(tokenIds []string) map[string]runtime.Token {
	tokens := make(map[string]runtime.Token)
	for _, t := range tokenIds {
		v, _ := e.tokens.Load(t)
		tokens[t] = v.(runtime.Token)
	}
	return tokens
}
