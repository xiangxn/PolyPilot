package state

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/xiangxn/polypilot/core"

	"github.com/tidwall/gjson"
	"github.com/xiangxn/go-polymarket-sdk/orders"
)

// TriggerReconcile signals an immediate non-periodic reconcile pass.
// Non-blocking; if a trigger is already pending it's coalesced.
func (s *State) TriggerReconcile() {
	select {
	case s.reconcileTrigger <- struct{}{}:
	default:
	}
}

// StartReconcileLoop runs ReconcileWithExchange every cfg.Interval and also
// whenever TriggerReconcile fires. Must be started at most once; subsequent
// calls return immediately.
func (s *State) StartReconcileLoop(ctx context.Context, cfg ReconcileConfig) {
	if !cfg.Enabled || s == nil || s.restoreClient == nil {
		return
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runReconcileWithRetry(ctx, cfg)
			case <-s.reconcileTrigger:
				s.runReconcileWithRetry(ctx, cfg)
			}
		}
	}()
}

func (s *State) runReconcileWithRetry(ctx context.Context, cfg ReconcileConfig) {
	rep := s.ReconcileWithExchange(ctx)
	if rep.Err != nil {
		for _, wait := range cfg.RetryBackoff {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			rep = s.ReconcileWithExchange(ctx)
			if rep.Err == nil {
				break
			}
		}
	}
	if cfg.OnReport != nil {
		cfg.OnReport(rep)
	}
}

// ReconcileWithExchange runs one reconcile pass against the exchange.
// Polymarket is authoritative: local-only orders are released, remote-only
// orders are attached with ExternalOrigin=true, mismatched orders are updated.
func (s *State) ReconcileWithExchange(ctx context.Context) ReconcileReport {
	start := time.Now()
	rep := ReconcileReport{}

	if s == nil || s.restoreClient == nil {
		rep.Err = core.ErrReconcileFailed
		return rep
	}

	openOrders, err := s.restoreClient.GetOpenOrders()
	if err != nil {
		rep.Err = err
		return rep
	}
	positions, err := s.restoreClient.GetPositions()
	if err != nil {
		rep.Err = err
		return rep
	}

	rep.OrdersAdded, rep.OrdersRemoved, rep.OrdersUpdated = s.reconcileOrders(openOrders)
	rep.PositionsAdded, rep.PositionsRemoved, rep.PositionsUpdated = s.reconcilePositions(positions)
	rep.DurationMs = time.Since(start).Milliseconds()
	return rep
}

func (s *State) reconcileOrders(remote []orders.OpenOrder) (added, removed, updated int) {
	remoteByID := make(map[string]orders.OpenOrder, len(remote))
	for _, o := range remote {
		id := strings.TrimSpace(o.Id)
		if id == "" {
			continue
		}
		remoteByID[id] = o
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureTokenPositions()

	// 1. local-only → release
	for orderID, local := range s.orderReservations {
		if _, ok := remoteByID[orderID]; !ok {
			s.releaseOrderLocked(orderID, local)
			removed++
		}
	}

	// 2. remote → add or update
	for orderID, ro := range remoteByID {
		remaining := math.Max(0, ro.OriginalSize-ro.SizeMatched)
		if remaining <= 0 {
			continue
		}
		local, exists := s.orderReservations[orderID]
		if !exists {
			if err := s.attachExternalLocked(orderID, ro.Market, ro.AssetId,
				orders.Side(ro.Side), ro.Price, remaining); err == nil {
				added++
			}
			continue
		}
		if math.Abs(local.RemainingSize-remaining) > core.FloatEpsilon ||
			math.Abs(local.Price-ro.Price) > core.FloatEpsilon {
			local.RemainingSize = remaining
			local.Price = ro.Price
			s.orderReservations[orderID] = local
			updated++
		}
	}
	return
}

func (s *State) releaseOrderLocked(orderID string, r OrderReservation) {
	switch r.Side {
	case orders.BUY:
		s.balance.Reserved -= r.Reserved
		s.balance.Available += r.Reserved
		if s.balance.Reserved < 0 {
			s.balance.Reserved = 0
		}
	case orders.SELL:
		k := tokenKey(r.TokenID)
		tp := s.position.Tokens[k]
		tp.Reserved -= r.Reserved
		tp.Available += r.Reserved
		if tp.Reserved < 0 {
			tp.Reserved = 0
		}
		s.position.Tokens[k] = tp
	}
	delete(s.orderReservations, orderID)
}

// attachExternalLocked is the lock-already-held version of AttachExternalOrder.
// Used by reconcile inside its own lock scope.
func (s *State) attachExternalLocked(orderID, marketID, tokenID string,
	side orders.Side, price, remainingSize float64) error {
	if orderID == "" || marketID == "" || tokenID == "" ||
		remainingSize <= 0 || price <= 0 || price >= 1 {
		return core.ErrReconcileFailed
	}
	if _, exists := s.orderReservations[orderID]; exists {
		return nil // idempotent
	}
	switch side {
	case orders.BUY:
		reserved := price * remainingSize
		s.balance.Available -= reserved
		s.balance.Reserved += reserved
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
	case orders.SELL:
		k := tokenKey(tokenID)
		tp := s.position.Tokens[k]
		tp.Available -= remainingSize
		tp.Reserved += remainingSize
		s.position.Tokens[k] = tp
		s.orderReservations[orderID] = OrderReservation{
			OrderID:        orderID,
			MarketID:       marketID,
			TokenID:        tokenID,
			Side:           side,
			Price:          price,
			RemainingSize:  remainingSize,
			Reserved:       remainingSize,
			ExternalOrigin: true,
		}
	default:
		return core.ErrReconcileFailed
	}
	return nil
}

func (s *State) reconcilePositions(remote *gjson.Result) (added, removed, updated int) {
	remoteByToken := make(map[string]float64)
	if remote != nil {
		items := remote.Array()
		if len(items) == 0 {
			items = remote.Get("data").Array()
		}
		for _, item := range items {
			tokenID := firstNonEmptyJSON(item, "asset", "assetId", "asset_id", "tokenId")
			if tokenID == "" {
				continue
			}
			sz := item.Get("size").Float()
			if sz <= 0 {
				continue
			}
			remoteByToken[tokenID] += sz
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureTokenPositions()

	// 1. local-only → clear (likely redeemed externally)
	for tk := range s.position.Tokens {
		if _, ok := remoteByToken[tk]; !ok {
			delete(s.position.Tokens, tk)
			removed++
		}
	}
	// 2. remote → add or update
	for tk, sz := range remoteByToken {
		tp, exists := s.position.Tokens[tk]
		if !exists {
			s.position.Tokens[tk] = TokenPosition{Available: sz, AvgCostKnown: false}
			added++
			continue
		}
		if math.Abs(tp.Available+tp.Reserved-sz) > core.FloatEpsilon {
			diff := sz - (tp.Available + tp.Reserved)
			tp.Available += diff
			if tp.Available < 0 {
				tp.Available = 0
			}
			s.position.Tokens[tk] = tp
			updated++
		}
	}
	return
}

func firstNonEmptyJSON(item gjson.Result, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(item.Get(k).String()); v != "" {
			return v
		}
	}
	return ""
}
