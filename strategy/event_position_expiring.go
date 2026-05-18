package strategy

import (
	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/runtime"
	"github.com/xiangxn/polypilot/state"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

// OnPositionExpiring closes out any open positions and cancels standing orders
// for tokens in an expiring market. Should be invoked by the runtime when an
// EventPositionExpiring fires.
func (s *Strategy) OnPositionExpiring(ev core.PositionExpiringEvent, snap state.Snapshot) []runtime.OrderIntent {
	out := make([]runtime.OrderIntent, 0, len(ev.TokenIDs)*2)
	for _, tk := range ev.TokenIDs {
		avail := ev.Available[tk]
		if avail > 0 {
			out = append(out, runtime.OrderIntent{
				MarketID: ev.MarketID,
				TokenID:  tk,
				Price:    s.config.InPrice,
				Side:     orders.SELL,
				Size:     avail,
			})
		}
		for _, oid := range BuildCancelIntent(tk, snap.Orders) {
			out = append(out, runtime.OrderIntent{
				Action:  runtime.OrderIntentActionCancel,
				OrderID: oid,
			})
		}
	}
	return out
}
