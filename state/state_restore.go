package state

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

const defaultPositionsAPILimit = 500

func (s *State) RestoreFromExchange(ctx context.Context) ([]string, error) {
	_ = ctx
	if s == nil {
		return nil, fmt.Errorf("state is nil")
	}

	s.SyncBalanceOnce(ctx)

	openOrders, err := s.restoreClient.GetOpenOrders()
	if err != nil {
		s.restoreClient.Redeem(ctx, s.onRedeemSuccess)
		return nil, fmt.Errorf("fetch open orders failed: %w", err)
	}

	positions, err := s.restoreClient.GetPositions()
	if err != nil {
		s.restoreClient.Redeem(ctx, s.onRedeemSuccess)
		return nil, fmt.Errorf("fetch positions failed: %w", err)
	}

	tokenPositions := make(map[string]TokenPosition)
	if positions != nil {
		positionItems := positions.Array()
		if len(positionItems) == 0 {
			positionItems = positions.Get("data").Array()
		}
		for _, item := range positionItems {
			tokenID := strings.TrimSpace(item.Get("asset").String())
			if tokenID == "" {
				tokenID = strings.TrimSpace(item.Get("assetId").String())
			}
			if tokenID == "" {
				tokenID = strings.TrimSpace(item.Get("asset_id").String())
			}
			if tokenID == "" {
				tokenID = strings.TrimSpace(item.Get("tokenId").String())
			}
			if tokenID == "" {
				continue
			}

			size := item.Get("size").Float()
			if size <= 0 {
				continue
			}
			tp := tokenPositions[tokenID]
			tp.Available += size
			tokenPositions[tokenID] = tp
		}
	}

	reservations := make([]OrderReservation, 0, len(openOrders))
	orderIDs := make([]string, 0, len(openOrders))
	seen := make(map[string]struct{}, len(openOrders))
	for _, order := range openOrders {
		side := orders.Side(order.Side)
		remainingSize := math.Max(0, order.OriginalSize-order.SizeMatched)
		if remainingSize <= 0 {
			continue
		}
		reserved := requiredCollateral(side, order.Price, remainingSize)
		if reserved <= 0 {
			continue
		}
		orderID := strings.TrimSpace(order.Id)
		if orderID != "" {
			if _, ok := seen[orderID]; !ok {
				seen[orderID] = struct{}{}
				orderIDs = append(orderIDs, orderID)
				reservations = append(reservations, OrderReservation{
					OrderID:       orderID,
					MarketID:      order.Market,
					TokenID:       order.AssetId,
					Side:          side,
					Price:         order.Price,
					RemainingSize: remainingSize,
					Reserved:      reserved,
				})
			}
		}
	}

	s.mu.RLock()
	minBalance := s.balance.MinBalance
	available := s.balance.Available
	reserved := s.balance.Reserved
	s.mu.RUnlock()

	s.Restore(Snapshot{
		Position: Position{Tokens: tokenPositions},
		Balance: Balance{
			Available:  available,
			Reserved:   reserved,
			MinBalance: minBalance,
		},
		Orders: mapReservationsByID(reservations),
	})

	s.restoreClient.Redeem(ctx, s.onRedeemSuccess)

	return orderIDs, nil
}

func (s *State) onRedeemSuccess(tokenIDs []string) {
	if s == nil || len(tokenIDs) == 0 {
		return
	}

	s.ClearRedeemedPositions(tokenIDs)
	log.Info().Int("token_ids", len(tokenIDs)).Msg("positions cleared after redeem")
}

func mapReservationsByID(reservations []OrderReservation) map[string]OrderReservation {
	out := make(map[string]OrderReservation, len(reservations))
	for _, r := range reservations {
		if strings.TrimSpace(r.OrderID) == "" {
			continue
		}
		out[r.OrderID] = r
	}
	return out
}
