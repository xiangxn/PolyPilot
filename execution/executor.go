package execution

import (
	"context"
	"fmt"
	"log"
	"polypilot/core"
	"polypilot/runtime"
	"strings"
	"sync"
	"time"

	"github.com/polymarket/go-order-utils/pkg/model"
	sdkmodel "github.com/xiangxn/go-polymarket-sdk/model"
	"github.com/xiangxn/go-polymarket-sdk/orders"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

const (
	floatEpsilon          = 1e-9
	defaultExecutionQueue = 1024
)

type trackedOrder struct {
	MarketID      string
	TokenID       string
	Side          model.Side
	Price         float64
	RequestedSize float64
	FilledSize    float64
	Accepted      bool
	Finalized     bool
	SeenTradeIDs  map[string]struct{}
}

type Executor struct {
	Bus *core.EventBus

	Client       *sdk.PolymarketClient
	TradeMonitor *sdk.TradeMonitor
	Config       *sdk.Config
	OrderType    orders.OrderType
	DeferExec    bool

	ExecutionQueueSize int

	startOnce  sync.Once
	workerOnce sync.Once
	mu         sync.Mutex
	tracked    map[string]*trackedOrder
	queue      chan []runtime.OrderIntent
}

func (e *Executor) Init(bus *core.EventBus, ctx context.Context) {
	e.Bus = bus
	if e.OrderType == "" {
		e.OrderType = orders.GTC
	}
	if e.tracked == nil {
		e.tracked = make(map[string]*trackedOrder)
	}
	if e.ExecutionQueueSize <= 0 {
		e.ExecutionQueueSize = defaultExecutionQueue
	}

	e.workerOnce.Do(func() {
		e.queue = make(chan []runtime.OrderIntent, e.ExecutionQueueSize)
		go e.consumeExecuteQueue(ctx)
	})

	e.startOnce.Do(func() {
		cfg := e.Config
		if cfg == nil {
			cfg = sdk.DefaultConfig()
		}
		if e.Client == nil {
			e.Client = sdk.NewClient(cfg)
		}
		if e.TradeMonitor == nil && cfg != nil {
			e.TradeMonitor = sdk.NewTradeMonitor(cfg.Polymarket.ClobWSBaseURL, cfg.Polymarket.CLOBCreds)
		}
		if e.TradeMonitor == nil {
			return
		}

		go func() {
			if err := e.TradeMonitor.Run(ctx); err != nil && ctx.Err() == nil {
				log.Printf("trade monitor stopped: %v", err)
			}
		}()
		go e.consumeTradeEvents(ctx)
	})
}

func (e *Executor) Execute(intents []runtime.OrderIntent) {
	if len(intents) == 0 || e.Client == nil {
		return
	}

	validated := make([]runtime.OrderIntent, 0, len(intents))
	for _, in := range intents {
		action := in.Action
		if action == "" {
			action = runtime.OrderIntentActionPlace
			in.Action = action
		}

		switch action {
		case runtime.OrderIntentActionPlace:
			if err := validatePlacement(in); err != nil {
				e.publish(core.ExecutionEvent{
					MarketID:      in.MarketID,
					TokenID:       in.TokenID,
					Price:         in.Price,
					Side:          in.Side,
					RequestedSize: in.Size,
					Status:        core.ExecutionStatusRejected,
					Reason:        err.Error(),
					At:            time.Now(),
				})
				continue
			}
			validated = append(validated, in)
		case runtime.OrderIntentActionCancel:
			if strings.TrimSpace(in.OrderID) == "" {
				log.Printf("skip cancel intent: empty order id")
				continue
			}
			validated = append(validated, in)
		default:
			e.publish(core.ExecutionEvent{
				MarketID:      in.MarketID,
				TokenID:       in.TokenID,
				Price:         in.Price,
				Side:          in.Side,
				RequestedSize: in.Size,
				Status:        core.ExecutionStatusRejected,
				Reason:        "unsupported order action",
				At:            time.Now(),
			})
		}
	}

	if len(validated) == 0 {
		return
	}
	if e.queue == nil {
		if e.ExecutionQueueSize <= 0 {
			e.ExecutionQueueSize = defaultExecutionQueue
		}
		e.workerOnce.Do(func() {
			e.queue = make(chan []runtime.OrderIntent, e.ExecutionQueueSize)
			go e.consumeExecuteQueue(context.Background())
		})
	}

	select {
	case e.queue <- validated:
	default:
		e.rejectBatch(validated, "execution queue full")
	}
}

func (e *Executor) consumeExecuteQueue(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case batch := <-e.queue:
			if len(batch) == 0 {
				continue
			}
			var placements []runtime.OrderIntent
			var cancels []runtime.OrderIntent
			for _, in := range batch {
				switch in.Action {
				case runtime.OrderIntentActionCancel:
					cancels = append(cancels, in)
				default:
					placements = append(placements, in)
				}
			}
			e.submitPlacements(placements)
			e.submitCancels(cancels)
		}
	}
}

func (e *Executor) rejectBatch(intents []runtime.OrderIntent, reason string) {
	now := time.Now()
	for _, in := range intents {
		ev := core.ExecutionEvent{
			MarketID:      in.MarketID,
			TokenID:       in.TokenID,
			Price:         in.Price,
			Side:          in.Side,
			RequestedSize: in.Size,
			Status:        core.ExecutionStatusRejected,
			Reason:        reason,
			At:            now,
		}
		if in.Action == runtime.OrderIntentActionCancel {
			ev.OrderID = in.OrderID
		}
		e.publish(ev)
	}
}

func (e *Executor) submitPlacements(intents []runtime.OrderIntent) {
	if len(intents) == 0 {
		return
	}

	type prepared struct {
		intent runtime.OrderIntent
		order  *model.SignedOrder
	}
	preparedOrders := make([]prepared, 0, len(intents))
	signatureType := model.POLY_GNOSIS_SAFE
	for _, in := range intents {
		signedOrder, err := e.Client.CreateOrder(&orders.UserOrder{
			TokenID: in.TokenID,
			Price:   in.Price,
			Size:    in.Size,
			Side:    in.Side,
		}, orders.CreateOrderOptions{SignatureType: &signatureType})
		if err != nil {
			e.publish(core.ExecutionEvent{
				MarketID:      in.MarketID,
				TokenID:       in.TokenID,
				Price:         in.Price,
				Side:          in.Side,
				RequestedSize: in.Size,
				Status:        core.ExecutionStatusRejected,
				Reason:        fmt.Sprintf("create order failed: %v", err),
				At:            time.Now(),
			})
			continue
		}
		preparedOrders = append(preparedOrders, prepared{intent: in, order: signedOrder})
	}

	if len(preparedOrders) == 0 {
		return
	}

	if len(preparedOrders) > 1 {
		args := make([]orders.PostOrdersArgs, 0, len(preparedOrders))
		for _, po := range preparedOrders {
			args = append(args, orders.PostOrdersArgs{Order: po.order, OrderType: e.OrderType})
		}

		log.Printf("[Executor] 下单时间: %d", time.Now().UnixMilli())
		results, err := e.Client.PostOrders(args, e.DeferExec)
		log.Printf("[Executor] 下单返回: %d", time.Now().UnixMilli())
		if err != nil {
			for _, po := range preparedOrders {
				e.publish(core.ExecutionEvent{
					MarketID:      po.intent.MarketID,
					TokenID:       po.intent.TokenID,
					Price:         po.intent.Price,
					Side:          po.intent.Side,
					RequestedSize: po.intent.Size,
					Status:        core.ExecutionStatusRejected,
					Reason:        fmt.Sprintf("post orders failed: %v", err),
					At:            time.Now(),
				})
			}
		} else {
			for i, result := range results.Array() {
				errorMsg := result.Get("errorMsg").String()
				if errorMsg != "" {
					po := preparedOrders[i]
					e.publish(core.ExecutionEvent{
						MarketID:      po.intent.MarketID,
						TokenID:       po.intent.TokenID,
						Price:         po.intent.Price,
						Side:          po.intent.Side,
						RequestedSize: po.intent.Size,
						Status:        core.ExecutionStatusRejected,
						Reason:        fmt.Sprintf("post orders failed: %s", errorMsg),
						At:            time.Now(),
					})
				}
			}
		}
		return
	}

	single := preparedOrders[0]
	log.Printf("[Executor] 下单时间: %d", time.Now().UnixMilli())
	result, err := e.Client.PostOrder(single.order, e.OrderType, e.DeferExec)
	log.Printf("[Executor] 下单返回: %d", time.Now().UnixMilli())
	if err != nil {
		e.publish(core.ExecutionEvent{
			MarketID:      single.intent.MarketID,
			TokenID:       single.intent.TokenID,
			Price:         single.intent.Price,
			Side:          single.intent.Side,
			RequestedSize: single.intent.Size,
			Status:        core.ExecutionStatusRejected,
			Reason:        fmt.Sprintf("post order failed: %v", err),
			At:            time.Now(),
		})
	} else {
		errorMsg := result.Get("errorMsg").String()
		e.publish(core.ExecutionEvent{
			MarketID:      single.intent.MarketID,
			TokenID:       single.intent.TokenID,
			Price:         single.intent.Price,
			Side:          single.intent.Side,
			RequestedSize: single.intent.Size,
			Status:        core.ExecutionStatusRejected,
			Reason:        fmt.Sprintf("post order failed: %s", errorMsg),
			At:            time.Now(),
		})
	}
}

func (e *Executor) submitCancels(intents []runtime.OrderIntent) {
	if len(intents) == 0 {
		return
	}

	if len(intents) > 1 {
		ids := make([]string, 0, len(intents))
		for _, in := range intents {
			ids = append(ids, in.OrderID)
		}
		if _, err := e.Client.CancelOrders(ids); err != nil {
			for _, in := range intents {
				e.publish(core.ExecutionEvent{
					Status: core.ExecutionStatusRejected,
					Reason: fmt.Sprintf("cancel orders failed (order=%s): %v", in.OrderID, err),
					At:     time.Now(),
				})
			}
		}
		return
	}

	in := intents[0]
	if _, err := e.Client.CancelOrder(&orders.OrderPayload{OrderID: in.OrderID}); err != nil {
		e.publish(core.ExecutionEvent{
			Status: core.ExecutionStatusRejected,
			Reason: fmt.Sprintf("cancel order failed (order=%s): %v", in.OrderID, err),
			At:     time.Now(),
		})
	}
}

func (e *Executor) consumeTradeEvents(ctx context.Context) {
	if e.TradeMonitor == nil {
		return
	}
	ch := e.TradeMonitor.SubscribeEvents()
	for {
		select {
		case <-ctx.Done():
			_ = e.TradeMonitor.Close()
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			e.handleTradeEvent(ev)
		}
	}
}

func (e *Executor) handleTradeEvent(ev sdk.TradeEvent) {
	if ev.ParseErr != nil {
		log.Printf("trade monitor parse error: %v", ev.ParseErr)
		return
	}

	switch ev.EventType {
	case sdk.TradeEventTypeOrder:
		if ev.Order != nil {
			e.onOrderEvent(ev.Order)
		}
	case sdk.TradeEventTypeTrade:
		if ev.Trade != nil {
			e.onTradeEvent(ev.Trade)
		}
	}
}

func (e *Executor) onOrderEvent(o *sdkmodel.WSOrder) {
	if o == nil || strings.TrimSpace(o.Id) == "" {
		return
	}

	side, ok := parseSide(o.Side)
	if !ok {
		return
	}

	at := parseEventTime(o.Timestamp)
	status := strings.ToUpper(strings.TrimSpace(o.Status))

	var out []core.ExecutionEvent
	e.mu.Lock()
	t := e.getOrCreateTracked(o.Id)
	t.MarketID = firstNonEmpty(o.Market, t.MarketID)
	t.TokenID = firstNonEmpty(o.AssetId, t.TokenID)
	t.Side = side
	if o.Price > 0 {
		t.Price = o.Price
	}
	if o.OriginalSize > 0 {
		t.RequestedSize = o.OriginalSize
	}

	switch status {
	case "LIVE":
		if ev, ok := e.buildAcceptedEvent(o.Id, t, at); ok {
			out = append(out, ev)
			t.Accepted = true
		}
	case "CANCELED":
		if !t.Finalized {
			out = append(out, core.ExecutionEvent{
				OrderID:       o.Id,
				MarketID:      t.MarketID,
				TokenID:       t.TokenID,
				Price:         t.Price,
				Side:          t.Side,
				RequestedSize: t.RequestedSize,
				FilledSize:    0,
				Status:        core.ExecutionStatusCancelled,
				At:            at,
			})
			t.Finalized = true
		}
	}
	e.mu.Unlock()

	for _, item := range out {
		e.publish(item)
	}
}

func (e *Executor) onTradeEvent(ti *sdkmodel.WSTrade) {
	if ti == nil {
		return
	}
	status := strings.ToUpper(strings.TrimSpace(ti.Status))
	at := parseEventTime(ti.Timestamp)

	type fill struct {
		orderID string
		market  string
		tokenID string
		side    model.Side
		price   float64
		size    float64
	}

	fills := make([]fill, 0, 1+len(ti.MakerOrders))
	if side, ok := parseSide(ti.Side); ok && strings.TrimSpace(ti.TakerOrderId) != "" {
		fills = append(fills, fill{
			orderID: ti.TakerOrderId,
			market:  ti.Market,
			tokenID: ti.AssetId,
			side:    side,
			price:   ti.Price,
			size:    ti.Size,
		})
	}
	for _, mo := range ti.MakerOrders {
		side, ok := parseSide(mo.Side)
		if !ok || strings.TrimSpace(mo.OrderId) == "" {
			continue
		}
		fills = append(fills, fill{
			orderID: mo.OrderId,
			market:  ti.Market,
			tokenID: mo.AssetId,
			side:    side,
			price:   mo.Price,
			size:    mo.MatchedAmount,
		})
	}

	var out []core.ExecutionEvent
	e.mu.Lock()
	for _, f := range fills {
		tracked := e.getOrCreateTracked(f.orderID)
		if tracked.Finalized {
			continue
		}
		tracked.MarketID = firstNonEmpty(f.market, tracked.MarketID)
		tracked.TokenID = firstNonEmpty(f.tokenID, tracked.TokenID)
		tracked.Side = f.side
		if f.price > 0 {
			tracked.Price = f.price
		}

		switch status {
		case "MINED":
			if ti.Id != "" {
				if tracked.SeenTradeIDs == nil {
					tracked.SeenTradeIDs = make(map[string]struct{})
				}
				if _, exists := tracked.SeenTradeIDs[ti.Id]; exists {
					continue
				}
				tracked.SeenTradeIDs[ti.Id] = struct{}{}
			}
			out = append(out, e.buildFillEventsFromDelta(f.orderID, tracked, f.size, at)...)
		case "FAILED":
			out = append(out, core.ExecutionEvent{
				OrderID:       f.orderID,
				MarketID:      tracked.MarketID,
				TokenID:       tracked.TokenID,
				Price:         tracked.Price,
				Side:          tracked.Side,
				RequestedSize: tracked.RequestedSize,
				FilledSize:    0,
				Status:        core.ExecutionStatusRejected,
				Reason:        "trade failed",
				At:            at,
			})
			tracked.Finalized = true
		}
	}
	e.mu.Unlock()

	for _, item := range out {
		e.publish(item)
	}
}

func (e *Executor) buildAcceptedEvent(orderID string, t *trackedOrder, at time.Time) (core.ExecutionEvent, bool) {
	if t == nil || t.Accepted || t.MarketID == "" || t.TokenID == "" || t.Price <= 0 || t.RequestedSize <= 0 {
		return core.ExecutionEvent{}, false
	}
	if t.Side != model.BUY && t.Side != model.SELL {
		return core.ExecutionEvent{}, false
	}
	return core.ExecutionEvent{
		OrderID:       orderID,
		MarketID:      t.MarketID,
		TokenID:       t.TokenID,
		Price:         t.Price,
		Side:          t.Side,
		RequestedSize: t.RequestedSize,
		FilledSize:    0,
		Status:        core.ExecutionStatusAccepted,
		At:            at,
	}, true
}

func (e *Executor) buildFillEventsFromCumulative(orderID string, t *trackedOrder, cumulative float64, at time.Time) []core.ExecutionEvent {
	if t == nil {
		return nil
	}
	if cumulative < 0 {
		cumulative = 0
	}
	if t.RequestedSize > 0 && cumulative > t.RequestedSize {
		cumulative = t.RequestedSize
	}
	delta := cumulative - t.FilledSize
	if delta <= floatEpsilon {
		return nil
	}
	t.FilledSize = cumulative

	status := core.ExecutionStatusPartiallyFilled
	if t.RequestedSize > 0 && t.FilledSize+floatEpsilon >= t.RequestedSize {
		status = core.ExecutionStatusFilled
		t.Finalized = true
	}

	return []core.ExecutionEvent{{
		OrderID:       orderID,
		MarketID:      t.MarketID,
		TokenID:       t.TokenID,
		Price:         t.Price,
		Side:          t.Side,
		RequestedSize: t.RequestedSize,
		FilledSize:    delta,
		Status:        status,
		At:            at,
	}}
}

func (e *Executor) buildFillEventsFromDelta(orderID string, t *trackedOrder, delta float64, at time.Time) []core.ExecutionEvent {
	if t == nil || delta <= floatEpsilon {
		return nil
	}
	cumulative := t.FilledSize + delta
	return e.buildFillEventsFromCumulative(orderID, t, cumulative, at)
}

func (e *Executor) getOrCreateTracked(orderID string) *trackedOrder {
	if e.tracked == nil {
		e.tracked = make(map[string]*trackedOrder)
	}
	t, ok := e.tracked[orderID]
	if ok {
		return t
	}
	t = &trackedOrder{SeenTradeIDs: make(map[string]struct{})}
	e.tracked[orderID] = t
	return t
}

func (e *Executor) publish(data core.ExecutionEvent) {
	if e.Bus != nil {
		e.Bus.Publish(core.Event{Type: core.EventExecution, Data: data})
	}
}

func validatePlacement(in runtime.OrderIntent) error {
	if strings.TrimSpace(in.MarketID) == "" {
		return fmt.Errorf("empty market id")
	}
	if strings.TrimSpace(in.TokenID) == "" {
		return fmt.Errorf("empty token id")
	}
	if in.Size <= 0 {
		return fmt.Errorf("invalid order size")
	}
	if in.Price <= 0 || in.Price >= 1 {
		return fmt.Errorf("invalid order price")
	}
	if in.Side != model.BUY && in.Side != model.SELL {
		return fmt.Errorf("invalid order side")
	}
	return nil
}

func parseSide(side string) (model.Side, bool) {
	switch strings.ToUpper(strings.TrimSpace(side)) {
	case "BUY":
		return model.BUY, true
	case "SELL":
		return model.SELL, true
	default:
		return 0, false
	}
}

func parseEventTime(ts int64) time.Time {
	if ts <= 0 {
		return time.Now()
	}
	if ts > 1_000_000_000_000 {
		return time.UnixMilli(ts)
	}
	return time.Unix(ts, 0)
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
