package runtime

import (
	"context"
	"fmt"
	"polypilot/core"
	"polypilot/state"
	"time"

	"github.com/polymarket/go-order-utils/pkg/model"
)

const (
	defaultPendingEventTTL     = 30 * time.Second
	defaultFinalizedOrderTTL   = 10 * time.Minute
	defaultProvisionalOrderTTL = 5 * time.Second
)

func (e *Engine) Start(ctx context.Context) {
	if e.State == nil || e.Risk == nil || e.Exec == nil {
		return
	}
	e.Bus = core.NewEventBus()

	e.initOrderTracking()
	// 同步平台数据
	restoredOrderIDs, restoreErr := e.State.RestoreFromExchange(ctx)
	if restoreErr != nil {
		e.publishRisk(fmt.Sprintf("restore from exchange failed reason=%s", restoreErr.Error()))
	} else {
		e.restoreOpenOrdersTrackingByIDs(restoredOrderIDs)
	}

	e.State.StartBalanceSync(ctx)

	for _, ob := range e.Observers {
		if ob == nil {
			continue
		}
		ob.Init(e.Bus)
		go ob.Start(ctx)
	}
	for _, feed := range e.Feeds {
		if feed == nil {
			continue
		}
		feed.Init(e.Bus)
	}
	if e.Exec != nil {
		e.Exec.Init(e.Bus, ctx)
	}
	if e.Probability != nil {
		e.Probability.Init(ctx)
	}
	for _, strategy := range e.Strategies {
		if strategy == nil {
			continue
		}
		strategy.Init(e.Bus, ctx)
	}

	ch, cancel := e.Bus.SubscribeWithCancel()
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				switch ev.Type {
				case core.EventMarket, core.EventOrderBook, core.EventSignal:
					e.inputEvents.Add(1)
					e.handleInputUpdate(ev)

				case core.EventExecution:
					data, ok := ev.Data.(core.ExecutionEvent)
					if !ok {
						e.publishRisk("invalid execution event payload")
						continue
					}

					e.handleExecutionEvent(data, true)
				}
			}
		}
	}()

	for _, feed := range e.Feeds {
		if feed == nil {
			continue
		}
		go feed.Start(ctx)
	}

	cleanupTicker := time.NewTicker(5 * time.Second)
	metricsTicker := time.NewTicker(1 * time.Minute)
	defer cleanupTicker.Stop()
	defer metricsTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-cleanupTicker.C:
			e.cleanupTracking(time.Now())
		case <-metricsTicker.C:
			e.cleanupTracking(time.Now())
			e.publishMetrics()
		}
	}
}

func (e *Engine) Close() {
	if e.Bus != nil {
		e.Bus.Close()
	}
}

func (e *Engine) handleInputUpdate(ev core.Event) {
	if e.Probability == nil {
		e.publishRisk("probability model is nil")
		return
	}
	if len(e.Strategies) == 0 {
		e.publishRisk("strategy model is nil")
		return
	}

	obs, ok := e.Probability.OnUpdate(ev)
	if !ok {
		// e.publishRisk("invalid market event payload")
		return
	}
	e.ticks.Add(1)

	var snap state.Snapshot
	hasSnap := false
	for _, strategy := range e.Strategies {
		if strategy == nil {
			continue
		}
		if !hasSnap {
			snap = e.State.Snapshot()
			hasSnap = true
		}
		intents := strategy.OnUpdate(ev, obs, snap)
		if len(intents) == 0 {
			continue
		}

		if err := e.Risk.Check(intents, snap); err != nil {
			e.riskRejected.Add(1)
			e.publishRisk(err.Error())
			return
		}

		submit := make([]OrderIntent, 0, len(intents))
		now := time.Now()
		for _, in := range intents {
			action := in.Action
			if action == "" {
				action = OrderIntentActionPlace
				in.Action = action
			}
			if action != OrderIntentActionPlace {
				submit = append(submit, in)
				continue
			}
			if in.IntentID == "" {
				in.IntentID = e.nextIntentID()
			}
			if err := e.State.TryReserveProvisional(in.IntentID, in.MarketID, in.TokenID, in.Side, in.Price, in.Size, now, e.ProvisionalOrderTTL); err != nil {
				e.riskRejected.Add(1)
				e.publishRisk(fmt.Sprintf("provisional reserve failed intent=%s reason=%s", in.IntentID, err.Error()))
				continue
			}
			submit = append(submit, in)
		}
		if len(submit) == 0 {
			continue
		}

		e.ordersSent.Add(uint64(len(submit)))
		e.Exec.Execute(submit)
	}

}

func (e *Engine) initOrderTracking() {
	if e.PendingEventTTL <= 0 {
		e.PendingEventTTL = defaultPendingEventTTL
	}
	if e.FinalizedOrderTTL <= 0 {
		e.FinalizedOrderTTL = defaultFinalizedOrderTTL
	}
	if e.ProvisionalOrderTTL <= 0 {
		e.ProvisionalOrderTTL = defaultProvisionalOrderTTL
	}
	if e.acceptedOrders == nil {
		e.acceptedOrders = make(map[string]struct{})
	}
	if e.finalized == nil {
		e.finalized = make(map[string]struct{})
	}
	if e.finalizedAt == nil {
		e.finalizedAt = make(map[string]time.Time)
	}
	if e.pendingByOrder == nil {
		e.pendingByOrder = make(map[string]pendingExecution)
	}
}

func (e *Engine) handleExecutionEvent(data core.ExecutionEvent, count bool) {
	if count {
		e.executionEvents.Add(1)
	}

	if data.OrderID == "" {
		if data.Status == core.ExecutionStatusRejected {
			e.executionRejected.Add(1)
			if data.ParentOrderID != "" {
				e.State.ReleaseProvisional(data.ParentOrderID)
			}
			if data.Reason != "" {
				e.publishRisk(fmt.Sprintf("execution rejected reason=%s", data.Reason))
			}
		}
		return
	}

	if e.isFinalized(data.OrderID) {
		return
	}

	switch data.Status {
	case core.ExecutionStatusAccepted:
		e.executionAccepted.Add(1)
		e.markAccepted(data.OrderID)
		confirmed := false
		if data.ParentOrderID != "" {
			ok, err := e.State.ConfirmProvisional(data.ParentOrderID, data.OrderID)
			if err != nil {
				e.publishRisk(fmt.Sprintf("confirm provisional failed intent=%s order=%s reason=%s", data.ParentOrderID, data.OrderID, err.Error()))
			} else {
				confirmed = ok
			}
		}
		if !confirmed {
			if err := e.State.ReserveOrder(data.OrderID, data.MarketID, data.TokenID, data.Side, data.Price, data.RequestedSize); err != nil && err.Error() != "order already reserved" {
				e.publishRisk(fmt.Sprintf("reserve failed order=%s reason=%s", data.OrderID, err.Error()))
			}
		}
		e.replayPending(data.OrderID)

	case core.ExecutionStatusPartiallyFilled, core.ExecutionStatusFilled:
		if !e.hasAccepted(data.OrderID) {
			e.bufferExecution(data)
			return
		}
		e.executionFilled.Add(1)
		if data.FilledSize > 0 {
			if err := e.State.ApplyFill(data.OrderID, data.MarketID, data.TokenID, data.Side, data.FilledSize, data.Price); err != nil {
				e.publishRisk(fmt.Sprintf("fill apply failed order=%s reason=%s", data.OrderID, err.Error()))
				return
			}
		}
		if data.Status == core.ExecutionStatusFilled {
			e.State.ReleaseOrder(data.OrderID)
			e.finalizeOrder(data.OrderID)
		}

	case core.ExecutionStatusCancelled:
		if !e.hasAccepted(data.OrderID) {
			e.bufferExecution(data)
			return
		}
		e.State.ReleaseOrder(data.OrderID)
		e.finalizeOrder(data.OrderID)

	case core.ExecutionStatusRejected:
		e.executionRejected.Add(1)
		if data.ParentOrderID != "" {
			e.State.ReleaseProvisional(data.ParentOrderID)
		}
		if e.hasAccepted(data.OrderID) {
			e.State.ReleaseOrder(data.OrderID)
		}
		e.publishRisk(fmt.Sprintf("execution rejected order=%s reason=%s", data.OrderID, data.Reason))
		e.finalizeOrder(data.OrderID)
	}
}

func (e *Engine) isFinalized(orderID string) bool {
	e.orderMu.RLock()
	defer e.orderMu.RUnlock()
	_, ok := e.finalized[orderID]
	return ok
}

func (e *Engine) markAccepted(orderID string) {
	e.orderMu.Lock()
	defer e.orderMu.Unlock()
	e.acceptedOrders[orderID] = struct{}{}
}

func (e *Engine) hasAccepted(orderID string) bool {
	e.orderMu.RLock()
	defer e.orderMu.RUnlock()
	_, ok := e.acceptedOrders[orderID]
	return ok
}

func (e *Engine) bufferExecution(data core.ExecutionEvent) {
	e.orderMu.Lock()
	defer e.orderMu.Unlock()

	pending, ok := e.pendingByOrder[data.OrderID]
	if !ok {
		pending = pendingExecution{firstSeen: time.Now()}
	}
	pending.events = append(pending.events, data)
	e.pendingByOrder[data.OrderID] = pending
	e.executionBuffered.Add(1)
}

func (e *Engine) replayPending(orderID string) {
	e.orderMu.Lock()
	pending, ok := e.pendingByOrder[orderID]
	if ok {
		delete(e.pendingByOrder, orderID)
	}
	e.orderMu.Unlock()

	if !ok {
		return
	}

	for _, evt := range pending.events {
		e.handleExecutionEvent(evt, false)
	}
}

func (e *Engine) cleanupTracking(now time.Time) {
	e.cleanupExpiredPending(now)
	e.cleanupExpiredFinalized(now)
	e.cleanupExpiredProvisional(now)
}

func (e *Engine) cleanupExpiredPending(now time.Time) {
	if e.PendingEventTTL <= 0 {
		return
	}

	type expiredPending struct {
		orderID string
		count   int
	}

	var expired []expiredPending
	e.orderMu.Lock()
	for orderID, pending := range e.pendingByOrder {
		if now.Sub(pending.firstSeen) < e.PendingEventTTL {
			continue
		}
		delete(e.pendingByOrder, orderID)
		expired = append(expired, expiredPending{orderID: orderID, count: len(pending.events)})
	}
	e.orderMu.Unlock()

	for _, item := range expired {
		e.executionExpired.Add(uint64(item.count))
		e.publishRisk(fmt.Sprintf("drop stale pending execution order=%s buffered=%d", item.orderID, item.count))
	}
}

func (e *Engine) cleanupExpiredFinalized(now time.Time) {
	if e.FinalizedOrderTTL <= 0 {
		return
	}

	e.orderMu.Lock()
	defer e.orderMu.Unlock()

	for orderID, finalizedAt := range e.finalizedAt {
		if now.Sub(finalizedAt) < e.FinalizedOrderTTL {
			continue
		}
		delete(e.finalizedAt, orderID)
		delete(e.finalized, orderID)
	}
}

func (e *Engine) finalizeOrder(orderID string) {
	e.orderMu.Lock()
	defer e.orderMu.Unlock()
	delete(e.acceptedOrders, orderID)
	delete(e.pendingByOrder, orderID)
	e.finalized[orderID] = struct{}{}
	e.finalizedAt[orderID] = time.Now()
}

func (e *Engine) pendingOrderCount() int {
	e.orderMu.RLock()
	defer e.orderMu.RUnlock()
	return len(e.pendingByOrder)
}

func (e *Engine) restoreOpenOrdersTrackingByIDs(orderIDs []string) {
	for _, orderID := range orderIDs {
		if orderID == "" {
			continue
		}
		e.markAccepted(orderID)
	}
}

func (e *Engine) cleanupExpiredProvisional(now time.Time) {
	expired := e.State.CleanupExpiredProvisional(now)
	for _, intentID := range expired {
		e.publishRisk(fmt.Sprintf("release expired provisional reserve intent=%s", intentID))
	}
}

func (e *Engine) nextIntentID() string {
	seq := e.intentSeq.Add(1)
	return fmt.Sprintf("intent-%d-%d", time.Now().UnixNano(), seq)
}

func requiredReservedForOrder(side model.Side, price, size float64) float64 {
	switch side {
	case model.BUY:
		return price * size
	case model.SELL:
		return size
	default:
		return 0
	}
}

func (e *Engine) publishRisk(reason string) {
	if e.Bus == nil {
		return
	}
	e.Bus.Publish(core.Event{
		Type: core.EventRisk,
		Data: core.RiskEvent{Reason: reason, At: time.Now()},
	})
}

func (e *Engine) publishMetrics() {
	snapshot := e.State.Snapshot()
	busStats := e.Bus.Stats()

	e.Bus.Publish(core.Event{
		Type: core.EventMetrics,
		Data: core.MetricsEvent{
			Ticks:             e.ticks.Load(),
			InputEvents:       e.inputEvents.Load(),
			ExecutionEvents:   e.executionEvents.Load(),
			ExecutionAccepted: e.executionAccepted.Load(),
			ExecutionFilled:   e.executionFilled.Load(),
			ExecutionRejected: e.executionRejected.Load(),
			ExecutionBuffered: e.executionBuffered.Load(),
			ExecutionExpired:  e.executionExpired.Load(),
			PendingOrders:     e.pendingOrderCount(),
			RiskRejected:      e.riskRejected.Load(),
			OrdersSent:        e.ordersSent.Load(),
			BusPublished:      busStats.Published,
			BusDropped:        busStats.Dropped,
			BusSubscribers:    busStats.Subscribers,
			BalanceAvailable:  snapshot.Balance.Available,
			BalanceReserved:   snapshot.Balance.Reserved,
			At:                time.Now(),
		},
	})
}
