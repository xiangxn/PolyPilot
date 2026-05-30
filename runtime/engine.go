package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
	"github.com/xiangxn/polypilot/core"
)

const (
	defaultPendingEventTTL     = 30 * time.Second
	defaultFinalizedOrderTTL   = 10 * time.Minute
	defaultProvisionalOrderTTL = 5 * time.Second

	defaultMetricsInterval = 5 * time.Minute
)

func (e *Engine) initConfig() {

	if e.Config == nil {
		e.StrategyTickInterval = 0
		e.MetricsInterval = defaultMetricsInterval
	} else {
		e.StrategyTickInterval = e.Config.GetDuration("runtime.strategy_tick_interval")
		mi := e.Config.GetDuration("runtime.metrics_interval")
		if mi == 0 {
			e.MetricsInterval = defaultMetricsInterval
		} else {
			e.MetricsInterval = mi
		}
	}
}

func (e *Engine) Start(ctx context.Context) {
	if e.State == nil || e.Risk == nil || e.Exec == nil {
		return
	}
	e.Bus = core.NewEventBus()

	e.initConfig()

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
		strategy.Init(e.Bus, ctx, e.Config)
	}

	ch, cancel := e.Bus.SubscribeWithCancel()
	go func() {
		defer cancel()

		var strategyTickC <-chan time.Time
		var strategyTicker *time.Ticker
		if e.StrategyTickInterval > 0 && e.hasTickStrategy() {
			strategyTicker = time.NewTicker(e.StrategyTickInterval)
			strategyTickC = strategyTicker.C
			defer strategyTicker.Stop()
		}

		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				switch ev.Type {
				case core.EventMarket, core.EventOrderBook, core.EventExternalPrice, core.EventProbability:
					e.inputEvents.Add(1)
					e.handleInputUpdate(ev)

				case core.EventExecution:
					data, ok := ev.Data.(core.ExecutionEvent)
					if !ok {
						e.publishRisk("invalid execution event payload")
						continue
					}

					e.handleExecutionEvent(data, true)
					e.handleExecutionAwareStrategy(data)
				case core.EventMarketResolved:
					info, ok := ev.Data.(*sdk.ResolvedInfo)
					if ok {
						for _, s := range e.Strategies {
							strategy, ok := s.(MarketResolved)
							if ok {
								strategy.OnResolved(info)
							}
						}
					}
				}
			case now := <-strategyTickC:
				e.handleStrategyTick(now)
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
	metricsTicker := time.NewTicker(e.MetricsInterval)
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

func (e *Engine) hasTickStrategy() bool {
	for _, s := range e.Strategies {
		if s == nil {
			continue
		}
		if _, ok := s.(TickStrategy); ok {
			return true
		}
	}
	return false
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
		if err := e.State.AttachOrder(data.ParentOrderID, data.OrderID, data.MarketID, data.TokenID,
			data.Side, data.Price, data.RequestedSize); err != nil &&
			!errors.Is(err, core.ErrOrderAlreadyReserved) {
			e.publishRisk(fmt.Sprintf("attach failed order=%s reason=%s", data.OrderID, err.Error()))
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
