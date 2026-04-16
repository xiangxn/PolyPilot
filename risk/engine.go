package risk

import (
	"errors"
	"fmt"
	"math"
	"polypilot/core"
	"polypilot/runtime"
	"polypilot/state"
)

type Engine struct{}

func (r *Engine) Check(orders []runtime.OrderIntent, s state.Snapshot) error {
	if len(orders) == 0 {
		return nil
	}

	var required float64
	for _, o := range orders {
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
		case core.SideBuy, core.SideSell:
		default:
			return errors.New("invalid order side")
		}

		required += requiredCollateral(o.Side, o.Price, o.Size)
	}

	if s.Balance.Available < required {
		return fmt.Errorf("insufficient available balance: need %.2f, have %.2f", required, s.Balance.Available)
	}

	return nil
}

func isTickAligned(price, tick float64) bool {
	if tick <= 0 {
		return true
	}
	steps := math.Round(price / tick)
	aligned := steps * tick
	return math.Abs(price-aligned) <= 1e-9
}

func requiredCollateral(side string, price, size float64) float64 {
	switch side {
	case core.SideBuy:
		return size * price
	case core.SideSell:
		return 0
	default:
		return 0
	}
}
