package execution

import (
	"fmt"
	"polypilot/core"
	"polypilot/runtime"
	"sync/atomic"
	"time"
)

type Executor struct {
	Bus *core.EventBus
	seq atomic.Uint64
}

func (e *Executor) Init(bus *core.EventBus) {
	e.Bus = bus
}

func (e *Executor) Execute(orders []runtime.OrderIntent) {
	if len(orders) == 0 {
		return
	}

	for _, o := range orders {
		switch o.Side {
		case core.SideBuy, core.SideSell:
			e.submitSingle(o)
		default:
			e.publish(core.ExecutionEvent{
				OrderID:       e.nextOrderID(),
				MarketID:      o.MarketID,
				TokenID:       o.TokenID,
				Price:         o.Price,
				Side:          o.Side,
				RequestedSize: o.Size,
				FilledSize:    0,
				Status:        core.ExecutionStatusRejected,
				Reason:        "unsupported order side",
				At:            time.Now(),
			})
		}
	}
}

func (e *Executor) submitSingle(o runtime.OrderIntent) {
	orderID := e.nextOrderID()
	now := time.Now()

	e.publish(core.ExecutionEvent{
		OrderID:       orderID,
		MarketID:      o.MarketID,
		TokenID:       o.TokenID,
		Price:         o.Price,
		Side:          o.Side,
		RequestedSize: o.Size,
		FilledSize:    0,
		Status:        core.ExecutionStatusAccepted,
		At:            now,
	})

	e.publish(core.ExecutionEvent{
		OrderID:       orderID,
		MarketID:      o.MarketID,
		TokenID:       o.TokenID,
		Price:         o.Price,
		Side:          o.Side,
		RequestedSize: o.Size,
		FilledSize:    o.Size,
		Status:        core.ExecutionStatusFilled,
		At:            time.Now(),
	})
}

func (e *Executor) publish(data core.ExecutionEvent) {
	if e.Bus != nil {
		e.Bus.Publish(core.Event{Type: core.EventExecution, Data: data})
	}
}

func (e *Executor) nextOrderID() string {
	id := e.seq.Add(1)
	return fmt.Sprintf("ord-%d", id)
}
