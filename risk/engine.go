package risk

import (
	"sync"
	"time"

	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/runtime"
	"github.com/xiangxn/polypilot/state"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

// Engine enforces all risk caps in a single Check call. Zero-value Engine
// only enforces basic balance/position/intent-validity checks (all caps
// disabled with their zero values).
type Engine struct {
	MaxDailyLoss         float64       // 0 disables
	MaxExposurePerMarket float64       // 0 disables
	MaxSlippageBps       int           // 0 disables
	MaxOpenOrders        int           // 0 disables
	MarketCooldown       time.Duration // 0 disables

	mu                  sync.Mutex
	lastIntentPerMarket map[string]time.Time
}

// Check validates a batch of order intents against current state and current
// mid-prices. Returns a *Rejection on failure (errors.As-compatible) or nil.
// midPrices may be nil; slippage check is skipped for tokens with no entry.
func (r *Engine) Check(intents []runtime.OrderIntent, s state.Snapshot, midPrices map[string]float64) error {
	if len(intents) == 0 {
		return nil
	}

	// Daily-loss circuit breaker: allow CANCEL through, block everything else
	if r.MaxDailyLoss > 0 && -s.DailyPnL >= r.MaxDailyLoss {
		for _, in := range intents {
			if in.Action != runtime.OrderIntentActionCancel {
				return reject(RejectDailyLoss, "daily loss %.2f exceeds %.2f", -s.DailyPnL, r.MaxDailyLoss)
			}
		}
	}

	now := time.Now()
	var buyRequired float64
	sellRequiredByToken := make(map[string]float64)
	exposurePerMarket := make(map[string]float64)
	placeCount := 0

	for _, o := range intents {
		action := o.Action
		if action == "" {
			action = runtime.OrderIntentActionPlace
		}

		if action == runtime.OrderIntentActionCancel {
			if o.OrderID == "" {
				return reject(RejectInvalidIntent, "empty cancel order id")
			}
			continue
		}

		if o.MarketID == "" {
			return reject(RejectInvalidIntent, "empty market id")
		}
		if o.Size <= 0 {
			return reject(RejectInvalidIntent, "invalid size %v", o.Size)
		}

		switch action {
		case runtime.OrderIntentActionPlace:
			placeCount++
			if o.TokenID == "" {
				return reject(RejectInvalidIntent, "empty token id")
			}
			if o.Price <= 0 || o.Price >= 1 {
				return reject(RejectInvalidIntent, "invalid price %v", o.Price)
			}

			// slippage check
			if r.MaxSlippageBps > 0 {
				if mid, ok := midPrices[o.TokenID]; ok && mid > 0 {
					deviationBps := int(((o.Price - mid) / mid) * 10000)
					if deviationBps < 0 {
						deviationBps = -deviationBps
					}
					if deviationBps > r.MaxSlippageBps {
						return reject(RejectSlippage, "price %v deviates from mid %v by %d bps (cap %d)",
							o.Price, mid, deviationBps, r.MaxSlippageBps)
					}
				}
			}

			switch o.Side {
			case orders.BUY:
				buyRequired += core.RequiredCollateral(o.Side, o.Price, o.Size)
			case orders.SELL:
				sellRequiredByToken[o.TokenID] += core.RequiredCollateral(o.Side, o.Price, o.Size)
			default:
				return reject(RejectInvalidIntent, "invalid side %v", o.Side)
			}
			exposurePerMarket[o.MarketID] += core.RequiredCollateral(o.Side, o.Price, o.Size)

		case runtime.OrderIntentActionSplit, runtime.OrderIntentActionMerge:
			if len(o.Tokens) != 2 {
				return reject(RejectInvalidIntent, "split/merge needs exactly 2 tokens")
			}
			if action == runtime.OrderIntentActionSplit {
				buyRequired += o.Size
			} else {
				for _, t := range o.Tokens {
					sellRequiredByToken[t] += o.Size
				}
			}
		default:
			return reject(RejectInvalidIntent, "unsupported action %v", action)
		}
	}

	// cooldown (per-market, only on local PLACE)
	if r.MarketCooldown > 0 && placeCount > 0 {
		r.mu.Lock()
		if r.lastIntentPerMarket == nil {
			r.lastIntentPerMarket = make(map[string]time.Time)
		}
		for _, o := range intents {
			if o.Action != "" && o.Action != runtime.OrderIntentActionPlace {
				continue
			}
			if last, ok := r.lastIntentPerMarket[o.MarketID]; ok {
				if now.Sub(last) < r.MarketCooldown {
					r.mu.Unlock()
					return reject(RejectCooldown, "market %s within cooldown %v", o.MarketID, r.MarketCooldown)
				}
			}
		}
		r.mu.Unlock()
	}

	// max open orders (includes ExternalOrigin via Snapshot)
	if r.MaxOpenOrders > 0 && s.OpenOrderCount+placeCount > r.MaxOpenOrders {
		return reject(RejectMaxOpenOrders, "would exceed max open orders %d (have %d, adding %d)",
			r.MaxOpenOrders, s.OpenOrderCount, placeCount)
	}

	// per-market exposure cap (existing + new)
	if r.MaxExposurePerMarket > 0 {
		existing := make(map[string]float64)
		for _, ord := range s.Orders {
			existing[ord.MarketID] += ord.Reserved
		}
		for mkt, add := range exposurePerMarket {
			if existing[mkt]+add > r.MaxExposurePerMarket+core.FloatEpsilon {
				return reject(RejectExposureCap, "market %s exposure %.2f exceeds cap %.2f",
					mkt, existing[mkt]+add, r.MaxExposurePerMarket)
			}
		}
	}

	// balance + min reserve
	if buyRequired > 0 {
		if s.Balance.Available <= s.Balance.MinBalance+core.FloatEpsilon {
			return reject(RejectBelowMinReserve, "min %.2f have %.2f", s.Balance.MinBalance, s.Balance.Available)
		}
		if s.Balance.Available+core.FloatEpsilon < buyRequired {
			return reject(RejectInsufficientBalance, "need %.2f have %.2f", buyRequired, s.Balance.Available)
		}
		if s.Balance.Available-buyRequired <= s.Balance.MinBalance+core.FloatEpsilon {
			return reject(RejectBelowMinReserve, "post-order would drop below min %.2f", s.Balance.MinBalance)
		}
	}

	// per-token position
	for tokenID, requiredSize := range sellRequiredByToken {
		avail := s.Position.Tokens[tokenID].Available
		if avail < requiredSize {
			return reject(RejectInsufficientPosition, "token %s need %.4f have %.4f", tokenID, requiredSize, avail)
		}
	}

	// success: record cooldown timestamps
	if r.MarketCooldown > 0 {
		r.mu.Lock()
		for _, o := range intents {
			if o.Action != "" && o.Action != runtime.OrderIntentActionPlace {
				continue
			}
			r.lastIntentPerMarket[o.MarketID] = now
		}
		r.mu.Unlock()
	}

	return nil
}
