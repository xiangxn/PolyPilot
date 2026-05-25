package state

import (
	"errors"

	"github.com/xiangxn/polypilot/core"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

// AttachOrder unifies the two paths that produce an OrderReservation:
//
//  1. ConfirmProvisional — a local intent (intentID != "") confirmed by exchange ACK.
//     Converts the matching ProvisionalReservation into an OrderReservation
//     without double-reserving balance/position.
//  2. Direct ReserveOrder — WS LIVE arrives before HTTP ACK (intentID == ""),
//     so the local provisional doesn't exist yet; creates a fresh reservation.
//
// If orderID already exists, returns core.ErrOrderAlreadyReserved without
// modifying state (idempotent for retry-on-WS scenarios).
func (s *State) AttachOrder(intentID, orderID, marketID, tokenID string,
	side orders.Side, price, requestedSize float64) error {
	if err := validateOrderArgs(orderID, marketID, tokenID, side, price, requestedSize); err != nil {
		return err
	}
	reserved := core.RequiredCollateral(side, price, requestedSize)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.orderReservations[orderID]; exists {
		// idempotent: if intentID matches an existing provisional, release it
		// so that we don't keep both Provisional and Order reservations.
		if intentID != "" {
			if p, ok := s.provisionalReservations[intentID]; ok {
				delete(s.provisionalReservations, intentID)
				s.ensureTokenPositions()
				s.releaseReservedLocked(p.Side, p.TokenID, p.Reserved)
			}
		}
		return core.ErrOrderAlreadyReserved
	}

	s.ensureTokenPositions()

	if intentID != "" {
		if p, ok := s.provisionalReservations[intentID]; ok {
			delete(s.provisionalReservations, intentID)
			s.orderReservations[orderID] = OrderReservation{
				OrderID:       orderID,
				MarketID:      p.MarketID,
				TokenID:       p.TokenID,
				Side:          p.Side,
				Price:         p.Price,
				RemainingSize: p.RemainingSize,
				Reserved:      p.Reserved,
			}
			return nil
		}
	}

	// fresh reservation (no provisional)
	switch side {
	case orders.BUY:
		if s.balance.Available+core.FloatEpsilon < reserved {
			return core.ErrInsufficientBalance
		}
		s.balance.Available -= reserved
		s.balance.Reserved += reserved
	case orders.SELL:
		k := tokenKey(tokenID)
		tp := s.position.Tokens[k]
		if tp.Available+core.FloatEpsilon < requestedSize {
			return core.ErrInsufficientPosition
		}
		tp.Available -= requestedSize
		tp.Reserved += requestedSize
		if tp.Available < 0 {
			tp.Available = 0
		}
		s.position.Tokens[k] = tp
	}

	s.orderReservations[orderID] = OrderReservation{
		OrderID:       orderID,
		MarketID:      marketID,
		TokenID:       tokenID,
		Side:          side,
		Price:         price,
		RemainingSize: requestedSize,
		Reserved:      reserved,
	}
	return nil
}

// AttachExternalOrder is called by reconcile when an order is found on
// Polymarket but not present locally (user manually placed it on the website,
// or local state was lost). Marks ExternalOrigin=true. Idempotent.
func (s *State) AttachExternalOrder(orderID, marketID, tokenID string,
	side orders.Side, price, remainingSize float64) error {
	if err := validateOrderArgs(orderID, marketID, tokenID, side, price, remainingSize); err != nil {
		return err
	}
	reserved := core.RequiredCollateral(side, price, remainingSize)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.orderReservations[orderID]; exists {
		return nil // idempotent
	}
	s.ensureTokenPositions()

	switch side {
	case orders.BUY:
		if s.balance.Available+core.FloatEpsilon < reserved {
			return core.ErrInsufficientBalance
		}
		s.balance.Available -= reserved
		s.balance.Reserved += reserved
	case orders.SELL:
		k := tokenKey(tokenID)
		tp := s.position.Tokens[k]
		if tp.Available+core.FloatEpsilon < remainingSize {
			return core.ErrInsufficientPosition
		}
		tp.Available -= remainingSize
		tp.Reserved += remainingSize
		s.position.Tokens[k] = tp
	}

	s.orderReservations[orderID] = OrderReservation{
		OrderID:        orderID,
		MarketID:       marketID,
		TokenID:        tokenID,
		Side:           side,
		Price:          price,
		RemainingSize:  remainingSize,
		Reserved:       reserved,
		ExternalOrigin: true,
	}
	return nil
}

func validateOrderArgs(orderID, marketID, tokenID string, side orders.Side, price, size float64) error {
	switch {
	case orderID == "":
		return errors.New("empty order id")
	case marketID == "":
		return core.ErrInvalidMarket
	case tokenID == "":
		return core.ErrInvalidToken
	case size <= 0:
		return core.ErrInvalidSize
	case price <= 0 || price >= 1:
		return core.ErrInvalidPrice
	case side != orders.BUY && side != orders.SELL:
		return core.ErrInvalidSide
	}
	return nil
}
