package observer

import (
	"context"
	"polypilot/core"
	"polypilot/internal/logx"

	"github.com/polymarket/go-order-utils/pkg/model"
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
		side := "BUY"
		if data.Side == model.SELL {
			side = "SELL"
		}
		log.Info().
			Str("event", string(e.Type)).
			Str("order_id", data.OrderID).
			Str("market_id", data.MarketID).
			Str("token_id", data.TokenID).
			Str("status", string(data.Status)).
			Str("side", side).
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
			Int64("input_events", int64(data.InputEvents)).
			Int64("ticks", int64(data.Ticks)).
			Int64("execution_events", int64(data.ExecutionEvents)).
			Int64("execution_accepted", int64(data.ExecutionAccepted)).
			Int64("execution_filled", int64(data.ExecutionFilled)).
			Int64("execution_rejected", int64(data.ExecutionRejected)).
			Int64("execution_buffered", int64(data.ExecutionBuffered)).
			Int64("execution_expired", int64(data.ExecutionExpired)).
			Int64("pending_orders", int64(data.PendingOrders)).
			Int64("risk_rejected", int64(data.RiskRejected)).
			Int64("orders_sent", int64(data.OrdersSent)).
			Int64("bus_published", int64(data.BusPublished)).
			Int64("bus_dropped", int64(data.BusDropped)).
			Int64("subscribers", int64(data.BusSubscribers)).
			Float64("balance_available", data.BalanceAvailable).
			Float64("balance_reserved", data.BalanceReserved).
			Time("at", data.At).
			Msg("observer event")
	}
}
