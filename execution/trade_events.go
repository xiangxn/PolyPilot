package execution

import (
	"context"
	"fmt"
	"strings"

	"github.com/xiangxn/polypilot/core"

	"github.com/xiangxn/go-polymarket-sdk/model"
	"github.com/xiangxn/go-polymarket-sdk/orders"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

func (e *Executor) consumeTradeEvents(ctx context.Context) {
	if e.TradeMonitor == nil {
		return
	}
	ch := e.TradeMonitor.SubscribeEvents()
	for {
		select {
		case <-ctx.Done():
			_ = e.TradeMonitor.Close()
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			e.handleTradeEvent(ev)
		}
	}
}

func (e *Executor) handleTradeEvent(ev sdk.TradeEvent) {
	if ev.ParseErr != nil {
		log.Error().Err(ev.ParseErr).Msg("trade monitor parse error")
		return
	}

	switch ev.EventType {
	case sdk.TradeEventTypeOrder:
		if ev.Order != nil {
			e.onOrderEvent(ev.Order)
		}
	case sdk.TradeEventTypeTrade:
		if ev.Trade != nil {
			msg, _ := ev.Raw.MarshalJSON()
			log.Debug().Float64("size", ev.Trade.Size).Msg(fmt.Sprintf("onTradeEvent: %s", msg))
			e.onTradeEvent(ev.Trade)
		}
	}
}

func (e *Executor) onOrderEvent(o *model.WSOrder) {
	if o == nil || strings.TrimSpace(o.Id) == "" || !e.isOwnOwner(o.Owner) {
		return
	}

	// External order detection: if WS reports an order we never posted, trigger reconcile.
	e.mu.Lock()
	_, known := e.tracked[o.Id]
	e.mu.Unlock()
	if !known && e.Reconcile != nil {
		go e.Reconcile()
	}

	side := orders.Side(o.Side)
	at := parseEventTime(o.Timestamp)
	status := strings.ToUpper(strings.TrimSpace(o.Status))

	var out []core.ExecutionEvent
	e.mu.Lock()
	t := e.getOrCreateTracked(o.Id)
	t.MarketID = firstNonEmpty(o.Market, t.MarketID)
	t.TokenID = firstNonEmpty(o.AssetId, t.TokenID)
	t.Side = side
	if o.Price > 0 {
		t.Price = o.Price
	}
	if o.OriginalSize > 0 {
		t.RequestedSize = o.OriginalSize
	}

	switch status {
	case "LIVE":
		if ev, ok := e.buildAcceptedEvent(o.Id, t, at); ok {
			out = append(out, ev)
			t.Accepted = true
		}
	case "CANCELED", "CANCELED_MARKET_RESOLVED":
		if !t.Finalized {
			out = append(out, core.ExecutionEvent{
				OrderID:       o.Id,
				MarketID:      t.MarketID,
				TokenID:       t.TokenID,
				Price:         t.Price,
				Side:          t.Side,
				RequestedSize: t.RequestedSize,
				FilledSize:    0,
				Status:        core.ExecutionStatusCancelled,
				At:            at,
			})
			t.Finalized = true
		}
	default:
		log.Info().Any("WSOrder", *o).Msg("onOrderEvent default case")
	}
	e.mu.Unlock()

	for _, item := range out {
		e.publish(item)
	}
}

func (e *Executor) onTradeEvent(ti *model.WSTrade) {
	if ti == nil {
		return
	}
	status := strings.ToUpper(strings.TrimSpace(ti.Status))
	at := parseEventTime(ti.Timestamp)

	type fill struct {
		orderID string
		market  string
		tokenID string
		side    orders.Side
		price   float64
		size    float64
	}

	fills := make([]fill, 0, 1+len(ti.MakerOrders))
	if strings.TrimSpace(ti.TakerOrderId) != "" && e.isOwnOwner(ti.Owner) {
		fills = append(fills, fill{
			orderID: ti.TakerOrderId,
			market:  ti.Market,
			tokenID: ti.AssetId,
			side:    orders.Side(ti.Side),
			price:   ti.Price,
			size:    ti.Size,
		})
	}
	for _, mo := range ti.MakerOrders {
		side := orders.Side(mo.Side)
		if strings.TrimSpace(mo.OrderId) == "" || !e.isOwnOwner(mo.Owner) {
			continue
		}
		fills = append(fills, fill{
			orderID: mo.OrderId,
			market:  ti.Market,
			tokenID: mo.AssetId,
			side:    side,
			price:   mo.Price,
			size:    mo.MatchedAmount,
		})
	}

	// External order detection: emit one Reconcile if any fill carries an
	// orderID we never posted (i.e., a manually-placed external order).
	fired := false
	for _, f := range fills {
		if fired {
			break
		}
		e.mu.Lock()
		_, known := e.tracked[f.orderID]
		e.mu.Unlock()
		if !known && e.Reconcile != nil {
			go e.Reconcile()
			fired = true
		}
	}

	var out []core.ExecutionEvent
	e.mu.Lock()
	for _, f := range fills {
		tracked := e.getOrCreateTracked(f.orderID)
		if tracked.Finalized {
			continue
		}
		tracked.MarketID = firstNonEmpty(f.market, tracked.MarketID)
		tracked.TokenID = firstNonEmpty(f.tokenID, tracked.TokenID)
		tracked.Side = f.side
		if f.price > 0 {
			tracked.Price = f.price
		}

		switch status {
		case "MINED":
			if ti.Id != "" {
				if tracked.SeenTradeIDs == nil {
					tracked.SeenTradeIDs = make(map[string]struct{})
				}
				if _, exists := tracked.SeenTradeIDs[ti.Id]; exists {
					continue
				}
				tracked.SeenTradeIDs[ti.Id] = struct{}{}
			}
			out = append(out, e.buildFillEventsFromDelta(f.orderID, tracked, f.size, at)...)
		case "FAILED":
			out = append(out, core.ExecutionEvent{
				OrderID:       f.orderID,
				MarketID:      tracked.MarketID,
				TokenID:       tracked.TokenID,
				Price:         tracked.Price,
				Side:          tracked.Side,
				RequestedSize: tracked.RequestedSize,
				FilledSize:    0,
				Status:        core.ExecutionStatusRejected,
				Reason:        core.ExecutionReasonTradeFailed,
				At:            at,
			})
			tracked.Finalized = true
		}
	}
	e.mu.Unlock()

	for _, item := range out {
		e.publish(item)
	}
}
