package core

import "github.com/xiangxn/go-polymarket-sdk/orders"

// FloatEpsilon is the project-wide tolerance for float64 comparisons.
const FloatEpsilon = 1e-9

// RequiredCollateral returns the value to reserve for a single order intent:
// BUY needs price*size USDC; SELL needs `size` tokens (the caller subtracts
// it from the token's Available balance, not from USDC).
func RequiredCollateral(side orders.Side, price, size float64) float64 {
	switch side {
	case orders.BUY:
		return size * price
	case orders.SELL:
		return size
	default:
		return 0
	}
}
