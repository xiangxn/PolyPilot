package observer

import (
	"context"
	"log"
	"polypilot/core"
	"time"

	"github.com/tidwall/gjson"
)

type Logger struct {
	Bus *core.EventBus
}

func (l *Logger) Init(bus *core.EventBus) {
	l.Bus = bus
}

func (l *Logger) Start(ctx context.Context) {
	ch, cancel := l.Bus.SubscribeWithCancel()
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-ch:
				if !ok {
					return
				}
				l.logEvent(e)
			}
		}
	}()
}

func (l *Logger) logEvent(e core.Event) {
	switch e.Type {
	case core.EventMarket:
		data := e.Data.(gjson.Result)
		log.Printf("[observer logger] event=%s question=%s endDate=%s", e.Type, data.Get("question").String(), data.Get("endDate").String())
	case core.EventExecution:
		data := e.Data.(core.ExecutionEvent)
		log.Printf("[observer logger] event=%s order_id=%s market_id=%s token_id=%s status=%s side=%s price=%.4f req=%.2f filled=%.2f reason=%q at=%s",
			e.Type,
			data.OrderID,
			data.MarketID,
			data.TokenID,
			data.Status,
			data.Side,
			data.Price,
			data.RequestedSize,
			data.FilledSize,
			data.Reason,
			data.At.Format(time.RFC3339),
		)
	case core.EventRisk:
		data := e.Data.(core.RiskEvent)
		log.Printf("[observer logger] event=%s reason=%q at=%s", e.Type, data.Reason, data.At.Format(time.RFC3339))
	case core.EventMetrics:
		data := e.Data.(core.MetricsEvent)
		log.Printf("[observer logger] event=%s input=%d ticks=%d execution=%d accepted=%d filled=%d rejected=%d buffered=%d expired=%d pending_orders=%d risk_rejected=%d orders_sent=%d bus_published=%d bus_dropped=%d subscribers=%d available=%.2f reserved=%.2f at=%s",
			e.Type,
			data.InputEvents,
			data.Ticks,
			data.ExecutionEvents,
			data.ExecutionAccepted,
			data.ExecutionFilled,
			data.ExecutionRejected,
			data.ExecutionBuffered,
			data.ExecutionExpired,
			data.PendingOrders,
			data.RiskRejected,
			data.OrdersSent,
			data.BusPublished,
			data.BusDropped,
			data.BusSubscribers,
			data.BalanceAvailable,
			data.BalanceReserved,
			data.At.Format(time.RFC3339),
		)
	default:
		// log.Printf("[observer logger] event=%s data=%v", e.Type, e.Data)
	}
}
