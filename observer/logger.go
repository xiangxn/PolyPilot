package observer

import (
	"context"
	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/logx"

	"github.com/tidwall/gjson"
)

var log = logx.Module("observer")

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
		log.Info().
			Str("event", string(e.Type)).
			Str("question", data.Get("question").String()).
			Str("end_date", data.Get("endDate").String()).
			Msg("observer event")
	case core.EventExecution:
		data := e.Data.(core.ExecutionEvent)
		log.Info().
			Str("event", string(e.Type)).
			Str("order_id", data.OrderID).
			Str("market_id", data.MarketID).
			Str("token_id", data.TokenID).
			Str("status", string(data.Status)).
			Str("side", string(data.Side)).
			Float64("price", data.Price).
			Float64("requested_size", data.RequestedSize).
			Float64("filled_size", data.FilledSize).
			Str("reason", data.Reason).
			Time("at", data.At).
			Msg("observer event")
	case core.EventRisk:
		data := e.Data.(core.RiskEvent)
		log.Info().
			Str("event", string(e.Type)).
			Str("reason", data.Reason).
			Time("at", data.At).
			Msg("observer event")
	case core.EventMetrics:
		data := e.Data.(core.MetricsEvent)
		log.Info().
			Str("event", string(e.Type)).
			Uint64("input_events", data.InputEvents).
			Uint64("ticks", data.Ticks).
			Uint64("execution_events", data.ExecutionEvents).
			Uint64("execution_accepted", data.ExecutionAccepted).
			Uint64("execution_filled", data.ExecutionFilled).
			Uint64("execution_rejected", data.ExecutionRejected).
			Uint64("execution_buffered", data.ExecutionBuffered).
			Uint64("execution_expired", data.ExecutionExpired).
			Int("pending_orders", data.PendingOrders).
			Uint64("risk_rejected", data.RiskRejected).
			Uint64("orders_sent", data.OrdersSent).
			Uint64("bus_published", data.BusPublished).
			Uint64("bus_dropped", data.BusDropped).
			Int("subscribers", data.BusSubscribers).
			Float64("balance_available", data.BalanceAvailable).
			Float64("balance_reserved", data.BalanceReserved).
			Time("at", data.At).
			Msg("observer event")
	}
}
