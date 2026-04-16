package state

import (
	"errors"
	"polypilot/core"
	"sync"
)

type Position struct {
	Buy  float64
	Sell float64
}

type Balance struct {
	Available float64
	Reserved  float64
}

type Snapshot struct {
	Position Position
	Balance  Balance
}

type ReservationSnapshot struct {
	OrderID       string
	MarketID      string
	TokenID       string
	Side          string
	Price         float64
	RemainingSize float64
	Reserved      float64
}

type orderReservation struct {
	MarketID      string
	TokenID       string
	Side          string
	Price         float64
	RemainingSize float64
	Reserved      float64
}

type State struct {
	mu           sync.RWMutex
	position     Position
	balance      Balance
	reservations map[string]orderReservation
}

func NewState(initialAvailable float64) *State {
	return &State{
		balance:      Balance{Available: initialAvailable, Reserved: 0},
		reservations: make(map[string]orderReservation),
	}
}

func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return Snapshot{
		Position: s.position,
		Balance:  s.balance,
	}
}

func (s *State) Restore(snapshot Snapshot, reservations []ReservationSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.position = snapshot.Position
	s.balance = snapshot.Balance
	s.reservations = make(map[string]orderReservation, len(reservations))
	for _, r := range reservations {
		if r.OrderID == "" {
			continue
		}
		s.reservations[r.OrderID] = orderReservation{
			MarketID:      r.MarketID,
			TokenID:       r.TokenID,
			Side:          r.Side,
			Price:         r.Price,
			RemainingSize: r.RemainingSize,
			Reserved:      r.Reserved,
		}
	}
}

func (s *State) ReserveOrder(orderID, marketID, tokenID, side string, price, requestedSize float64) error {
	if orderID == "" {
		return errors.New("empty order id")
	}
	if marketID == "" {
		return errors.New("empty market id")
	}
	if tokenID == "" {
		return errors.New("empty token id")
	}
	if requestedSize <= 0 {
		return errors.New("invalid requested size")
	}
	if price <= 0 || price >= 1 {
		return errors.New("invalid price")
	}
	if side != core.SideBuy && side != core.SideSell {
		return errors.New("invalid side")
	}

	required := requiredCollateral(side, price, requestedSize)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.reservations[orderID]; exists {
		return errors.New("order already reserved")
	}
	if s.balance.Available < required {
		return errors.New("insufficient available balance for reserve")
	}

	s.balance.Available -= required
	s.balance.Reserved += required
	s.reservations[orderID] = orderReservation{
		MarketID:      marketID,
		TokenID:       tokenID,
		Side:          side,
		Price:         price,
		RemainingSize: requestedSize,
		Reserved:      required,
	}

	return nil
}

func (s *State) ApplyFill(orderID, marketID, tokenID, side string, filledSize float64) error {
	if orderID == "" {
		return errors.New("empty order id")
	}
	if filledSize <= 0 {
		return errors.New("invalid filled size")
	}
	if side != core.SideBuy && side != core.SideSell {
		return errors.New("invalid side")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	res, exists := s.reservations[orderID]
	if !exists {
		return errors.New("reservation not found")
	}
	if res.MarketID != marketID || res.TokenID != tokenID {
		return errors.New("fill market/token mismatch")
	}
	if res.Side != side {
		return errors.New("fill side mismatch")
	}
	if filledSize > res.RemainingSize {
		return errors.New("filled size exceeds remaining size")
	}

	consumed := requiredCollateral(side, res.Price, filledSize)
	if consumed > res.Reserved {
		consumed = res.Reserved
	}

	res.RemainingSize -= filledSize
	res.Reserved -= consumed
	s.balance.Reserved -= consumed

	switch side {
	case core.SideBuy:
		s.position.Buy += filledSize
	case core.SideSell:
		s.position.Sell += filledSize
	}

	if res.RemainingSize <= 1e-9 || res.Reserved <= 1e-9 {
		delete(s.reservations, orderID)
	} else {
		s.reservations[orderID] = res
	}

	if s.balance.Reserved < 0 {
		s.balance.Reserved = 0
	}

	return nil
}

func (s *State) ReleaseOrder(orderID string) {
	if orderID == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	res, exists := s.reservations[orderID]
	if !exists {
		return
	}

	s.balance.Reserved -= res.Reserved
	s.balance.Available += res.Reserved
	if s.balance.Reserved < 0 {
		s.balance.Reserved = 0
	}
	delete(s.reservations, orderID)
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
