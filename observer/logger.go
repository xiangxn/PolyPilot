package observer

import (
	"context"
	"log"
	"polypilot/core"
	"time"
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
	switch data := e.Data.(type) {
	case core.MarketEvent:
		log.Printf("event=%s price=%.4f at=%d", e.Type, data.Price, data.Timestamp)
	case core.ExecutionEvent:
		log.Printf("event=%s order_id=%s market_id=%s token_id=%s status=%s side=%s price=%.4f req=%.2f filled=%.2f reason=%q at=%s",
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
	case core.RiskEvent:
		log.Printf("event=%s reason=%q at=%s", e.Type, data.Reason, data.At.Format(time.RFC3339))
	case core.MetricsEvent:
		log.Printf("event=%s ticks=%d market=%d execution=%d accepted=%d filled=%d rejected=%d buffered=%d expired=%d pending_orders=%d risk_rejected=%d orders_sent=%d bus_published=%d bus_dropped=%d subscribers=%d available=%.2f reserved=%.2f at=%s",
			e.Type,
			data.Ticks,
			data.MarketEvents,
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
		log.Printf("event=%s data=%v", e.Type, e.Data)
	}
}
