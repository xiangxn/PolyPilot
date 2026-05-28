package state

import (
	"time"

	"github.com/xiangxn/polypilot/core"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

// ApplyFill applies a fill event to the reservation identified by orderID.
// On BUY fills, updates the token's weighted-average AvgCost and increments
// TotalBought (sets AvgCostKnown=true). On SELL fills with AvgCostKnown,
// realized PnL = (fillPrice - AvgCost) * filledSize is accumulated into the
// State's UTC daily PnL counter (auto-reset on day rollover).
//
// When the order's RemainingSize falls to <= core.FloatEpsilon, the reservation
// is deleted.
func (s *State) ApplyFill(orderID, marketID, tokenID string, side orders.Side, filledSize, fillPrice float64) error {
	if orderID == "" {
		return core.ErrReservationNotFound
	}
	if filledSize <= 0 {
		return core.ErrInvalidSize
	}
	if side != orders.BUY && side != orders.SELL {
		return core.ErrInvalidSide
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	res, exists := s.orderReservations[orderID]
	if !exists {
		return core.ErrReservationNotFound
	}
	if res.MarketID != marketID || res.TokenID != tokenID {
		return core.ErrFillMarketTokenMismatch
	}
	if res.Side != side {
		return core.ErrFillSideMismatch
	}
	if filledSize > res.RemainingSize+core.FloatEpsilon {
		return core.ErrFillExceedsRemaining
	}
	if fillPrice <= 0 {
		fillPrice = res.Price
	}

	consumed := core.RequiredCollateral(side, fillPrice, filledSize)
	if consumed > res.Reserved {
		consumed = res.Reserved
	}

	res.RemainingSize -= filledSize
	if res.RemainingSize < 0 {
		res.RemainingSize = 0
	}
	res.Reserved -= consumed
	if res.Reserved < 0 {
		res.Reserved = 0
	}

	s.ensureTokenPositions()
	k := tokenKey(res.TokenID)
	tp := s.position.Tokens[k]

	switch side {
	case orders.BUY:
		s.balance.Reserved -= consumed
		if s.balance.Reserved < 0 {
			s.balance.Reserved = 0
		}
		// weighted average AvgCost. When no prior BUYs exist, the first fill's
		// price is exactly the avg cost (avoids float division artifacts).
		newTotal := tp.TotalBought + filledSize
		if tp.TotalBought <= 0 {
			tp.AvgCost = fillPrice
		} else if newTotal > 0 {
			tp.AvgCost = (tp.AvgCost*tp.TotalBought + fillPrice*filledSize) / newTotal
		}
		tp.TotalBought = newTotal
		tp.AvgCostKnown = true
		tp.Available += filledSize
	case orders.SELL:
		tp.Reserved -= consumed
		if tp.Reserved < 0 {
			tp.Reserved = 0
		}
		// realized PnL only when AvgCostKnown
		if tp.AvgCostKnown {
			realized := (fillPrice - tp.AvgCost) * filledSize
			s.addDailyPnLLocked(realized)
		}
		proceeds := fillPrice * filledSize
		s.balance.Available += proceeds
	}
	s.position.Tokens[k] = tp

	if res.RemainingSize <= core.FloatEpsilon {
		delete(s.orderReservations, orderID)
	} else {
		s.orderReservations[orderID] = res
	}
	return nil
}

// addDailyPnLLocked maintains a UTC date stamp and resets on day rollover.
// Caller must hold s.mu.Lock.
func (s *State) addDailyPnLLocked(delta float64) {
	today := time.Now().UTC().Format("2006-01-02")
	if s.dailyPnLDate != today {
		s.dailyPnL = 0
		s.dailyPnLDate = today
	}
	s.dailyPnL += delta
}
