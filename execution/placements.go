package execution

import (
	"fmt"
	"strings"
	"time"

	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/runtime"

	"github.com/tidwall/gjson"
	"github.com/xiangxn/go-polymarket-sdk/orders"
)

func (e *Executor) submitPlacements(intents []runtime.OrderIntent) {
	if len(intents) == 0 {
		return
	}

	preparedOrders := make([]preparedPlacement, 0, len(intents))
	signatureType := orders.POLY_GNOSIS_SAFE
	for _, in := range intents {
		orderType := in.OrderType
		if orderType == "" {
			orderType = orders.GTC
		}
		signedOrder, err := e.Client.CreateOrder(&orders.UserOrder{
			TokenID: in.TokenID,
			Price:   in.Price,
			Size:    in.Size,
			Side:    in.Side,
		}, orders.CreateOrderOptions{SignatureType: &signatureType})
		if err != nil {
			e.publish(core.ExecutionEvent{
				ParentOrderID: in.IntentID,
				MarketID:      in.MarketID,
				TokenID:       in.TokenID,
				Price:         in.Price,
				Side:          in.Side,
				RequestedSize: in.Size,
				Status:        core.ExecutionStatusRejected,
				Reason:        fmt.Sprintf("create order failed: %v", err),
				At:            time.Now(),
			})
			continue
		}
		preparedOrders = append(preparedOrders, preparedPlacement{intent: in, order: signedOrder, orderType: orderType})
	}

	if len(preparedOrders) == 0 {
		return
	}

	if len(preparedOrders) > 1 {
		args := make([]orders.PostOrdersArgs, 0, len(preparedOrders))
		for _, po := range preparedOrders {
			args = append(args, orders.PostOrdersArgs{Order: po.order, OrderType: po.orderType})
		}

		startAt := time.Now().UnixMilli()
		results, err := e.Client.PostOrders(args, e.DeferExec)
		log.Debug().Int64("submit_start_ms", startAt).Int64("submit_end_ms", time.Now().UnixMilli()).Msg("post orders batch finished")
		if err != nil {
			now := time.Now()
			for _, po := range preparedOrders {
				e.publish(core.ExecutionEvent{
					ParentOrderID: po.intent.IntentID,
					MarketID:      po.intent.MarketID,
					TokenID:       po.intent.TokenID,
					Price:         po.intent.Price,
					Side:          po.intent.Side,
					RequestedSize: po.intent.Size,
					Status:        core.ExecutionStatusRejected,
					Reason:        fmt.Sprintf("post orders failed: %v", err),
					At:            now,
				})
			}
			return
		}

		e.handlePostOrdersResults(preparedOrders, results.Array())
		return
	}

	single := preparedOrders[0]
	startAt := time.Now().UnixMilli()
	result, err := e.Client.PostOrder(single.order, single.orderType, e.DeferExec)
	log.Debug().Int64("submit_start_ms", startAt).Int64("submit_end_ms", time.Now().UnixMilli()).Msg("post order finished")
	if err != nil {
		e.publish(core.ExecutionEvent{
			ParentOrderID: single.intent.IntentID,
			MarketID:      single.intent.MarketID,
			TokenID:       single.intent.TokenID,
			Price:         single.intent.Price,
			Side:          single.intent.Side,
			RequestedSize: single.intent.Size,
			Status:        core.ExecutionStatusRejected,
			Reason:        fmt.Sprintf("post order failed: %v", err),
			At:            time.Now(),
		})
		return
	}

	errorMsg := result.Get("errorMsg").String()
	if errorMsg != "" {
		e.publish(core.ExecutionEvent{
			ParentOrderID: single.intent.IntentID,
			MarketID:      single.intent.MarketID,
			TokenID:       single.intent.TokenID,
			Price:         single.intent.Price,
			Side:          single.intent.Side,
			RequestedSize: single.intent.Size,
			Status:        core.ExecutionStatusRejected,
			Reason:        fmt.Sprintf("post order failed: %s", errorMsg),
			At:            time.Now(),
		})
		return
	}
	orderID := strings.TrimSpace(result.Get("orderID").String())
	if orderID == "" {
		e.publish(core.ExecutionEvent{
			ParentOrderID: single.intent.IntentID,
			MarketID:      single.intent.MarketID,
			TokenID:       single.intent.TokenID,
			Price:         single.intent.Price,
			Side:          single.intent.Side,
			RequestedSize: single.intent.Size,
			Status:        core.ExecutionStatusRejected,
			Reason:        "post order failed: empty order id",
			At:            time.Now(),
		})
		return
	}
	e.trackPostedOrder(orderID, single.intent)
	e.publishAcceptedFromPost(single.intent, orderID, time.Now())
}

func (e *Executor) handlePostOrdersResults(preparedOrders []preparedPlacement, results []gjson.Result) {
	for i, po := range preparedOrders {
		if i >= len(results) {
			e.publish(core.ExecutionEvent{
				ParentOrderID: po.intent.IntentID,
				MarketID:      po.intent.MarketID,
				TokenID:       po.intent.TokenID,
				Price:         po.intent.Price,
				Side:          po.intent.Side,
				RequestedSize: po.intent.Size,
				Status:        core.ExecutionStatusRejected,
				Reason:        "post orders failed: missing result item",
				At:            time.Now(),
			})
			continue
		}
		result := results[i]
		errorMsg := result.Get("errorMsg").String()
		if errorMsg != "" {
			e.publish(core.ExecutionEvent{
				ParentOrderID: po.intent.IntentID,
				MarketID:      po.intent.MarketID,
				TokenID:       po.intent.TokenID,
				Price:         po.intent.Price,
				Side:          po.intent.Side,
				RequestedSize: po.intent.Size,
				Status:        core.ExecutionStatusRejected,
				Reason:        fmt.Sprintf("post orders failed: %s", errorMsg),
				At:            time.Now(),
			})
			continue
		}
		orderID := strings.TrimSpace(result.Get("orderID").String())
		if orderID == "" {
			e.publish(core.ExecutionEvent{
				ParentOrderID: po.intent.IntentID,
				MarketID:      po.intent.MarketID,
				TokenID:       po.intent.TokenID,
				Price:         po.intent.Price,
				Side:          po.intent.Side,
				RequestedSize: po.intent.Size,
				Status:        core.ExecutionStatusRejected,
				Reason:        "post orders failed: empty order id",
				At:            time.Now(),
			})
			continue
		}
		e.trackPostedOrder(orderID, po.intent)
		e.publishAcceptedFromPost(po.intent, orderID, time.Now())
	}
}

func (e *Executor) trackPostedOrder(orderID string, in runtime.OrderIntent) {
	if strings.TrimSpace(orderID) == "" {
		return
	}
	e.mu.Lock()
	t := e.getOrCreateTracked(orderID)
	t.MarketID = firstNonEmpty(in.MarketID, t.MarketID)
	t.TokenID = firstNonEmpty(in.TokenID, t.TokenID)
	t.Side = in.Side
	if in.Price > 0 {
		t.Price = in.Price
	}
	if in.Size > 0 {
		t.RequestedSize = in.Size
	}
	t.Accepted = true
	e.mu.Unlock()
}

func (e *Executor) publishAcceptedFromPost(in runtime.OrderIntent, orderID string, at time.Time) {
	e.publish(core.ExecutionEvent{
		ParentOrderID: in.IntentID,
		OrderID:       orderID,
		MarketID:      in.MarketID,
		TokenID:       in.TokenID,
		Price:         in.Price,
		Side:          in.Side,
		RequestedSize: in.Size,
		FilledSize:    0,
		Status:        core.ExecutionStatusAccepted,
		At:            at,
	})
}

func (e *Executor) submitCancels(intents []runtime.OrderIntent) {
	if len(intents) == 0 {
		return
	}

	if len(intents) > 1 {
		ids := make([]string, 0, len(intents))
		for _, in := range intents {
			ids = append(ids, in.OrderID)
		}
		if _, err := e.Client.CancelOrders(ids); err != nil {
			for _, in := range intents {
				e.publish(core.ExecutionEvent{
					Status: core.ExecutionStatusRejected,
					Reason: fmt.Sprintf("cancel orders failed (order=%s): %v", in.OrderID, err),
					At:     time.Now(),
				})
			}
		}
		return
	}

	in := intents[0]
	if _, err := e.Client.CancelOrder(&orders.OrderPayload{OrderID: in.OrderID}); err != nil {
		e.publish(core.ExecutionEvent{
			Status: core.ExecutionStatusRejected,
			Reason: fmt.Sprintf("cancel order failed (order=%s): %v", in.OrderID, err),
			At:     time.Now(),
		})
	}
}

func (e *Executor) buildAcceptedEvent(orderID string, t *trackedOrder, at time.Time) (core.ExecutionEvent, bool) {
	if t == nil || t.Accepted || t.MarketID == "" || t.TokenID == "" || t.Price <= 0 || t.RequestedSize <= 0 {
		return core.ExecutionEvent{}, false
	}
	if t.Side != orders.BUY && t.Side != orders.SELL {
		return core.ExecutionEvent{}, false
	}
	return core.ExecutionEvent{
		OrderID:       orderID,
		MarketID:      t.MarketID,
		TokenID:       t.TokenID,
		Price:         t.Price,
		Side:          t.Side,
		RequestedSize: t.RequestedSize,
		FilledSize:    0,
		Status:        core.ExecutionStatusAccepted,
		At:            at,
	}, true
}

func (e *Executor) buildFillEventsFromCumulative(orderID string, t *trackedOrder, cumulative float64, at time.Time) []core.ExecutionEvent {
	if t == nil {
		return nil
	}
	if cumulative < 0 {
		cumulative = 0
	}
	// polymarket有会匹配会大于RequestedSize,所以这里不再扣除
	// if t.RequestedSize > 0 && cumulative > t.RequestedSize {
	// 	cumulative = t.RequestedSize
	// }
	delta := cumulative - t.FilledSize
	if delta <= floatEpsilon {
		return nil
	}
	t.FilledSize = cumulative

	status := core.ExecutionStatusPartiallyFilled
	if t.RequestedSize > 0 && t.FilledSize+floatEpsilon >= t.RequestedSize {
		status = core.ExecutionStatusFilled
		t.Finalized = true
	}

	return []core.ExecutionEvent{{
		OrderID:       orderID,
		MarketID:      t.MarketID,
		TokenID:       t.TokenID,
		Price:         t.Price,
		Side:          t.Side,
		RequestedSize: t.RequestedSize,
		FilledSize:    delta,
		Status:        status,
		At:            at,
	}}
}

func (e *Executor) buildFillEventsFromDelta(orderID string, t *trackedOrder, delta float64, at time.Time) []core.ExecutionEvent {
	if t == nil || delta <= floatEpsilon {
		return nil
	}
	cumulative := t.FilledSize + delta
	return e.buildFillEventsFromCumulative(orderID, t, cumulative, at)
}

func (e *Executor) getOrCreateTracked(orderID string) *trackedOrder {
	if e.tracked == nil {
		e.tracked = make(map[string]*trackedOrder)
	}
	t, ok := e.tracked[orderID]
	if ok {
		return t
	}
	t = &trackedOrder{SeenTradeIDs: make(map[string]struct{})}
	e.tracked[orderID] = t
	return t
}
