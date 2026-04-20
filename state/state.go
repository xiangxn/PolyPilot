package state

import (
	"errors"
	"maps"
	"sync"

	"github.com/polymarket/go-order-utils/pkg/model"
)

const floatEpsilon = 1e-9

type TokenPosition struct {
	Available float64
	Reserved  float64
}

type Position struct {
	Tokens map[string]TokenPosition
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
	Side          model.Side
	Price         float64
	RemainingSize float64
	Reserved      float64
}

type orderReservation struct {
	MarketID      string
	TokenID       string
	Side          model.Side
	Price         float64
	RemainingSize float64
	Reserved      float64
}

type State struct {
	mu             sync.RWMutex
	position       Position
	balance        Balance
	minBalance     float64
	reservations   map[string]orderReservation
	balanceSync    BalanceSyncConfig
	balanceSyncRun sync.Once
}

func NewState(minBalance float64, opts ...Option) *State {
	if minBalance < 0 {
		minBalance = 0
	}
	s := &State{
		position:     Position{Tokens: make(map[string]TokenPosition)},
		balance:      Balance{Available: 0, Reserved: 0},
		minBalance:   minBalance,
		reservations: make(map[string]orderReservation),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	if s.balanceSync.Enabled {
		if s.balanceSync.Interval <= 0 {
			s.balanceSync.Interval = defaultBalanceSyncInterval
		}
		if s.balanceSync.Epsilon <= 0 {
			s.balanceSync.Epsilon = defaultBalanceSyncEpsilon
		}
	}

	return s
}

func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return Snapshot{
		Position: Position{
			Tokens: cloneTokenPositions(s.position.Tokens),
		},
		Balance: s.balance,
	}
}

func (s *State) Restore(snapshot Snapshot, reservations []ReservationSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.position = Position{
		Tokens: cloneTokenPositions(snapshot.Position.Tokens),
	}
	if s.position.Tokens == nil {
		s.position.Tokens = make(map[string]TokenPosition)
	}

	s.balance = Balance{Available: snapshot.Balance.Available, Reserved: 0}
	s.reservations = make(map[string]orderReservation, len(reservations))
	for _, r := range reservations {
		if r.OrderID == "" {
			continue
		}
		if r.RemainingSize <= 0 {
			continue
		}
		if r.Reserved < 0 {
			r.Reserved = 0
		}
		s.reservations[r.OrderID] = orderReservation{
			MarketID:      r.MarketID,
			TokenID:       r.TokenID,
			Side:          r.Side,
			Price:         r.Price,
			RemainingSize: r.RemainingSize,
			Reserved:      r.Reserved,
		}

		switch r.Side {
		case model.BUY:
			s.balance.Reserved += r.Reserved
		case model.SELL:
			k := tokenKey(r.TokenID)
			tp := s.position.Tokens[k]
			tp.Reserved += r.Reserved
			s.position.Tokens[k] = tp
		}
	}
}

func (s *State) ReserveOrder(orderID, marketID, tokenID string, side model.Side, price, requestedSize float64) error {
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
	if side != model.BUY && side != model.SELL {
		return errors.New("invalid side")
	}

	reservedAmount := requiredCollateral(side, price, requestedSize)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.reservations[orderID]; exists {
		return errors.New("order already reserved")
	}

	s.ensureTokenPositions()
	if side == model.BUY {
		if s.balance.Available <= s.minBalance+floatEpsilon {
			return errors.New("available balance reached minimum reserve")
		}
		if s.balance.Available+floatEpsilon < reservedAmount {
			return errors.New("insufficient available balance for reserve")
		}
		if s.balance.Available-reservedAmount <= s.minBalance+floatEpsilon {
			return errors.New("order would reduce available balance below minimum reserve")
		}
		s.balance.Available -= reservedAmount
		s.balance.Reserved += reservedAmount
	} else {
		k := tokenKey(tokenID)
		tp := s.position.Tokens[k]
		if tp.Available+floatEpsilon < requestedSize {
			return errors.New("insufficient token position for sell reserve")
		}
		tp.Available -= requestedSize
		tp.Reserved += requestedSize
		if tp.Available < 0 {
			tp.Available = 0
		}
		s.position.Tokens[k] = tp
	}

	s.reservations[orderID] = orderReservation{
		MarketID:      marketID,
		TokenID:       tokenID,
		Side:          side,
		Price:         price,
		RemainingSize: requestedSize,
		Reserved:      reservedAmount,
	}

	return nil
}

func (s *State) ApplyFill(orderID, marketID, tokenID string, side model.Side, filledSize float64) error {
	if orderID == "" {
		return errors.New("empty order id")
	}
	if filledSize <= 0 {
		return errors.New("invalid filled size")
	}
	if side != model.BUY && side != model.SELL {
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
	if filledSize > res.RemainingSize+floatEpsilon {
		return errors.New("filled size exceeds remaining size")
	}

	consumed := requiredCollateral(side, res.Price, filledSize)
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
	switch side {
	case model.BUY:
		s.balance.Reserved -= consumed
		if s.balance.Reserved < 0 {
			s.balance.Reserved = 0
		}

		k := tokenKey(res.TokenID)
		tp := s.position.Tokens[k]
		tp.Available += filledSize
		s.position.Tokens[k] = tp
	case model.SELL:
		k := tokenKey(res.TokenID)
		tp := s.position.Tokens[k]
		tp.Reserved -= consumed
		if tp.Reserved < 0 {
			tp.Reserved = 0
		}
		s.position.Tokens[k] = tp

		proceeds := res.Price * filledSize
		s.balance.Available += proceeds
	}

	if res.RemainingSize <= floatEpsilon {
		delete(s.reservations, orderID)
	} else {
		s.reservations[orderID] = res
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

	s.ensureTokenPositions()
	switch res.Side {
	case model.BUY:
		s.balance.Reserved -= res.Reserved
		s.balance.Available += res.Reserved
		if s.balance.Reserved < 0 {
			s.balance.Reserved = 0
		}
	case model.SELL:
		k := tokenKey(res.TokenID)
		tp := s.position.Tokens[k]
		tp.Reserved -= res.Reserved
		tp.Available += res.Reserved
		if tp.Reserved < 0 {
			tp.Reserved = 0
		}
		s.position.Tokens[k] = tp
	}

	delete(s.reservations, orderID)
}

func (s *State) ReconcileOnchainBalance(onchainTotal float64, epsilon float64) (changed bool, drift float64) {
	if onchainTotal < 0 {
		onchainTotal = 0
	}
	if epsilon < 0 {
		epsilon = 0
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	newAvailable := onchainTotal - s.balance.Reserved
	if newAvailable < 0 {
		newAvailable = 0
	}

	drift = newAvailable - s.balance.Available
	if drift < 0 {
		drift = -drift
	}
	if drift <= epsilon {
		return false, drift
	}

	s.balance.Available = newAvailable
	return true, drift
}

func requiredCollateral(side model.Side, price, size float64) float64 {
	switch side {
	case model.BUY:
		return size * price
	case model.SELL:
		return size
	default:
		return 0
	}
}

func tokenKey(tokenID string) string {
	return tokenID
}

func cloneTokenPositions(src map[string]TokenPosition) map[string]TokenPosition {
	if len(src) == 0 {
		return map[string]TokenPosition{}
	}
	dst := make(map[string]TokenPosition, len(src))
	maps.Copy(dst, src)

	return dst
}

func (s *State) ensureTokenPositions() {
	if s.position.Tokens == nil {
		s.position.Tokens = make(map[string]TokenPosition)
	}
}
