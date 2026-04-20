package runtime

import (
	"context"
	"fmt"
	"math"
	"polypilot/core"
	"polypilot/state"
	"polypilot/store"
	"time"

	"github.com/polymarket/go-order-utils/pkg/model"
)

const (
	defaultPendingEventTTL   = 30 * time.Second
	defaultFinalizedOrderTTL = 10 * time.Minute
	defaultSnapshotInterval  = 10 * time.Second
	defaultSQLitePath        = "data/polymarket.db"
)

func (e *Engine) Start(ctx context.Context) {
	if e.State == nil || e.Risk == nil || e.Exec == nil {
		return
	}
	e.Bus = core.NewEventBus()

	e.initOrderTracking()
	if !e.initStores() {
		return
	}
	e.restoreFromStore()
	e.State.StartBalanceSync(ctx)

	for _, ob := range e.Observers {
		if ob == nil {
			continue
		}
		ob.Init(e.Bus)
		ob.Start(ctx)
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
					if data.OrderID != "" && e.ExecutionStore != nil {
						if err := e.ExecutionStore.AppendExecution(data); err != nil {
							e.publishRisk(fmt.Sprintf("persist execution failed order=%s reason=%s", data.OrderID, err.Error()))
						}
					}
					e.handleExecutionEvent(data, true)
					e.upsertOrderRecord(data)
				}
			}
		}
	}()

	for _, feed := range e.Feeds {
		if feed == nil {
			continue
		}
		feed.Start(ctx)
	}

	cleanupTicker := time.NewTicker(1 * time.Second)
	metricsTicker := time.NewTicker(10 * time.Second)
	snapshotTicker := time.NewTicker(e.SnapshotInterval)
	defer cleanupTicker.Stop()
	defer metricsTicker.Stop()
	defer snapshotTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			e.saveStateSnapshot(time.Now())
			return
		case <-cleanupTicker.C:
			e.cleanupTracking(time.Now())
		case <-metricsTicker.C:
			e.cleanupTracking(time.Now())
			e.publishMetrics()
		case now := <-snapshotTicker.C:
			e.saveStateSnapshot(now)
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

	for _, strategy := range e.Strategies {
		if strategy == nil {
			continue
		}
		intents := strategy.OnUpdate(ev, obs)
		if len(intents) == 0 {
			continue
		}

		snapshot := e.State.Snapshot()
		if err := e.Risk.Check(intents, snapshot); err != nil {
			e.riskRejected.Add(1)
			e.publishRisk(err.Error())
			return
		}

		e.ordersSent.Add(uint64(len(intents)))
		e.Exec.Execute(intents)
	}

}

func (e *Engine) initOrderTracking() {
	if e.PendingEventTTL <= 0 {
		e.PendingEventTTL = defaultPendingEventTTL
	}
	if e.FinalizedOrderTTL <= 0 {
		e.FinalizedOrderTTL = defaultFinalizedOrderTTL
	}
	if e.SnapshotInterval <= 0 {
		e.SnapshotInterval = defaultSnapshotInterval
	}
	if e.SQLitePath == "" {
		e.SQLitePath = defaultSQLitePath
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
		if data.Status == core.ExecutionStatusRejected && data.Reason != "" {
			e.executionRejected.Add(1)
			e.publishRisk(fmt.Sprintf("execution rejected reason=%s", data.Reason))
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
		if err := e.State.ReserveOrder(data.OrderID, data.MarketID, data.TokenID, data.Side, data.Price, data.RequestedSize); err != nil && err.Error() != "order already reserved" {
			e.publishRisk(fmt.Sprintf("reserve failed order=%s reason=%s", data.OrderID, err.Error()))
		}
		e.replayPending(data.OrderID)

	case core.ExecutionStatusPartiallyFilled, core.ExecutionStatusFilled:
		if !e.hasAccepted(data.OrderID) {
			e.bufferExecution(data)
			return
		}
		e.executionFilled.Add(1)
		if data.FilledSize > 0 {
			if err := e.State.ApplyFill(data.OrderID, data.MarketID, data.TokenID, data.Side, data.FilledSize); err != nil {
				e.publishRisk(fmt.Sprintf("fill apply failed order=%s reason=%s", data.OrderID, err.Error()))
				return
			}
		}
		if data.Status == core.ExecutionStatusFilled {
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

func (e *Engine) initStores() bool {
	if e.OrderStore != nil && e.ExecutionStore != nil && e.StateStore != nil {
		return true
	}

	orderStore, executionStore, stateStore, err := store.NewSQLiteStores(e.SQLitePath)
	if err != nil {
		e.publishRisk(fmt.Sprintf("init sqlite stores failed path=%s reason=%s", e.SQLitePath, err.Error()))
		return false
	}
	if e.OrderStore == nil {
		e.OrderStore = orderStore
	}
	if e.ExecutionStore == nil {
		e.ExecutionStore = executionStore
	}
	if e.StateStore == nil {
		e.StateStore = stateStore
	}
	return true
}

func (e *Engine) restoreFromStore() {
	restoredFromSnapshot := false

	if e.StateStore != nil && e.OrderStore != nil {
		snapshotRec, ok, err := e.StateStore.LoadLatestSnapshot()
		if err != nil {
			e.publishRisk(fmt.Sprintf("load snapshot failed reason=%s", err.Error()))
			return
		}
		if ok {
			openOrders, err := e.OrderStore.ListOpenOrders()
			if err != nil {
				e.publishRisk(fmt.Sprintf("load open orders failed reason=%s", err.Error()))
				return
			}

			reservations := make([]state.ReservationSnapshot, 0, len(openOrders))
			for _, order := range openOrders {
				if order.RemainingSize <= 0 {
					continue
				}
				reserved := order.Reserved
				if reserved <= 0 {
					reserved = requiredReservedForOrder(order.Side, order.Price, order.RemainingSize)
				}
				if reserved <= 0 {
					continue
				}
				reservations = append(reservations, state.ReservationSnapshot{
					OrderID:       order.OrderID,
					MarketID:      order.MarketID,
					TokenID:       order.TokenID,
					Side:          order.Side,
					Price:         order.Price,
					RemainingSize: order.RemainingSize,
					Reserved:      reserved,
				})
			}

			tokenPositions := make(map[string]state.TokenPosition, len(snapshotRec.Tokens))
			for tokenID, tp := range snapshotRec.Tokens {
				tokenPositions[tokenID] = state.TokenPosition{
					Available: tp.Available,
					Reserved:  tp.Reserved,
				}
			}

			e.State.Restore(state.Snapshot{
				Position: state.Position{Tokens: tokenPositions},
				Balance: state.Balance{
					Available:  snapshotRec.Available,
					Reserved:   snapshotRec.Reserved,
					MinBalance: snapshotRec.MinBalance,
				},
			}, reservations)
			e.restoreOpenOrdersTracking(openOrders)
			restoredFromSnapshot = true
		}
	}

	if restoredFromSnapshot {
		return
	}

	if e.ExecutionStore != nil {
		executions, err := e.ExecutionStore.ListExecutionsSince(0)
		if err != nil {
			e.publishRisk(fmt.Sprintf("load execution log failed reason=%s", err.Error()))
			return
		}
		for _, ev := range executions {
			e.handleExecutionEvent(ev, false)
		}
	}
}

func (e *Engine) restoreOpenOrdersTracking(openOrders []store.OrderRecord) {
	for _, order := range openOrders {
		switch order.Status {
		case core.ExecutionStatusAccepted, core.ExecutionStatusPartiallyFilled:
			e.markAccepted(order.OrderID)
		}
	}
}

func (e *Engine) saveStateSnapshot(now time.Time) {
	if e.StateStore == nil {
		return
	}
	snap := e.State.Snapshot()
	tokens := make(map[string]store.TokenPositionRecord, len(snap.Position.Tokens))
	for tokenID, tp := range snap.Position.Tokens {
		tokens[tokenID] = store.TokenPositionRecord{
			Available: tp.Available,
			Reserved:  tp.Reserved,
		}
	}
	rec := store.SnapshotRecord{
		Available:  snap.Balance.Available,
		Reserved:   snap.Balance.Reserved,
		MinBalance: snap.Balance.MinBalance,
		Tokens:     tokens,
		At:         now.UnixNano(),
	}
	if err := e.StateStore.SaveSnapshot(rec); err != nil {
		e.publishRisk(fmt.Sprintf("save snapshot failed reason=%s", err.Error()))
	}
}

func (e *Engine) upsertOrderRecord(data core.ExecutionEvent) {
	if e.OrderStore == nil || data.OrderID == "" {
		return
	}

	now := time.Now().UnixNano()
	rec, ok, err := e.OrderStore.GetOrder(data.OrderID)
	if err != nil {
		e.publishRisk(fmt.Sprintf("load order failed order=%s reason=%s", data.OrderID, err.Error()))
		return
	}
	if !ok {
		rec = store.OrderRecord{OrderID: data.OrderID}
	}

	if data.MarketID != "" {
		rec.MarketID = data.MarketID
	}
	if data.TokenID != "" {
		rec.TokenID = data.TokenID
	}

	rec.Side = data.Side

	if data.Price > 0 {
		rec.Price = data.Price
	}
	if data.RequestedSize > 0 {
		rec.RequestedSize = data.RequestedSize
	}

	switch data.Status {
	case core.ExecutionStatusAccepted:
		rec.RemainingSize = data.RequestedSize
		rec.Reserved = requiredReservedForOrder(data.Side, data.Price, rec.RemainingSize)
	case core.ExecutionStatusPartiallyFilled:
		filled := data.FilledSize
		if filled < 0 {
			filled = 0
		}
		rec.RemainingSize = math.Max(0, rec.RemainingSize-filled)
		rec.Reserved = requiredReservedForOrder(rec.Side, rec.Price, rec.RemainingSize)
	case core.ExecutionStatusFilled, core.ExecutionStatusCancelled, core.ExecutionStatusRejected:
		rec.RemainingSize = 0
		rec.Reserved = 0
	}

	rec.Status = data.Status
	rec.UpdatedAt = now

	if err := e.OrderStore.UpsertOrder(rec); err != nil {
		e.publishRisk(fmt.Sprintf("save order failed order=%s reason=%s", data.OrderID, err.Error()))
	}
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
