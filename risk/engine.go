package risk

import (
	"errors"
	"fmt"
	"polypilot/runtime"
	"polypilot/state"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

const floatEpsilon = 1e-9

type Engine struct {
}

func (r *Engine) Check(orderIntents []runtime.OrderIntent, s state.Snapshot) error {
	if len(orderIntents) == 0 {
		return nil
	}

	var buyRequired float64
	sellRequiredByToken := make(map[string]float64)

	for _, o := range orderIntents {
		action := o.Action
		if action == "" {
			action = runtime.OrderIntentActionPlace
		}

		switch action {
		case runtime.OrderIntentActionCancel:
			if o.OrderID == "" {
				return errors.New("invalid cancel order id")
			}
			continue
		case runtime.OrderIntentActionPlace:
			// continue below
		default:
			return errors.New("invalid order action")
		}

		if o.MarketID == "" {
			return errors.New("invalid market id")
		}
		if o.TokenID == "" {
			return errors.New("invalid token id")
		}
		if o.Size <= 0 {
			return errors.New("invalid order size")
		}
		if o.Price <= 0 || o.Price >= 1 {
			return errors.New("invalid order price")
		}

		switch o.Side {
		case orders.BUY:
			buyRequired += requiredCollateral(o.Side, o.Price, o.Size)
		case orders.SELL:
			sellRequiredByToken[o.TokenID] += requiredCollateral(o.Side, o.Price, o.Size)
		default:
			return errors.New("invalid order side")
		}
	}

	if buyRequired > 0 {
		if s.Balance.Available <= s.Balance.MinBalance+floatEpsilon {
			return fmt.Errorf("available balance reached minimum reserve: min %.2f, have %.2f", s.Balance.MinBalance, s.Balance.Available)
		}
		if s.Balance.Available+floatEpsilon < buyRequired {
			return fmt.Errorf("insufficient available balance: need %.2f, have %.2f", buyRequired, s.Balance.Available)
		}
		if s.Balance.Available-buyRequired <= s.Balance.MinBalance+floatEpsilon {
			return fmt.Errorf("order would reduce available balance below minimum reserve: min %.2f, post-order %.2f", s.Balance.MinBalance, s.Balance.Available-buyRequired)
		}
	}

	for tokenID, requiredSize := range sellRequiredByToken {
		available := s.Position.Tokens[tokenID].Available
		if available < requiredSize {
			return fmt.Errorf("insufficient token position for sell: token=%s need %.4f, have %.4f", tokenID, requiredSize, available)
		}
	}

	return nil
}

func requiredCollateral(side orders.Side, price, size float64) float64 {
	switch side {
	case orders.BUY:
		return size * price
	case orders.SELL:
		return size
	default:
		return 0
	}
}
