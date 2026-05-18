package runtime

import (
	"time"

	"github.com/xiangxn/polypilot/core"
)

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

	mids := map[string]float64{}
	if obs, ok := e.currentObservation(); ok {
		mids = buildMidPrices(obs)
	}
	unreal := e.State.UnrealizedPnL(mids)

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
			UnrealizedPnL:     unreal,
			DailyPnL:          snapshot.DailyPnL,
			ReconcileRuns:     e.reconcileRuns.Load(),
			ReconcileDiffs:    e.reconcileDiffs.Load(),
			At:                time.Now().UTC(),
		},
	})
}

// RecordReconcile is called from outside (e.g., main.go's OnReport callback)
// after each reconcile pass to update internal metrics counters.
func (e *Engine) RecordReconcile(diffs int) {
	e.reconcileRuns.Add(1)
	if diffs > 0 {
		e.reconcileDiffs.Add(uint64(diffs))
	}
}
