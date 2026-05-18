package runtime

import (
	"fmt"
	"time"

	"github.com/xiangxn/polypilot/core"
)

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
