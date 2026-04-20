package execution

import (
	"testing"
	"time"

	"polypilot/core"
	"polypilot/runtime"

	"github.com/polymarket/go-order-utils/pkg/model"
	sdkmodel "github.com/xiangxn/go-polymarket-sdk/model"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

func TestExecute_InvalidPlacementPublishesRejectedWithoutOrderID(t *testing.T) {
	bus := core.NewEventBus()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, Client: &sdk.PolymarketClient{}}

	exec.Execute([]runtime.OrderIntent{{
		Action:   runtime.OrderIntentActionPlace,
		MarketID: "m1",
		TokenID:  "",
		Price:    0.4,
		Side:     model.BUY,
		Size:     1,
	}})

	ev := mustRecvExecutionEvent(t, ch)
	if ev.Status != core.ExecutionStatusRejected {
		t.Fatalf("unexpected status: %s", ev.Status)
	}
	if ev.OrderID != "" {
		t.Fatalf("expected empty order id, got: %s", ev.OrderID)
	}
}

func TestExecute_EmptyCancelIntentPublishesNothing(t *testing.T) {
	bus := core.NewEventBus()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, Client: &sdk.PolymarketClient{}}

	exec.Execute([]runtime.OrderIntent{{
		Action:  runtime.OrderIntentActionCancel,
		OrderID: "",
	}})

	assertNoExecutionEvent(t, ch)
}

func TestOnOrderEvent_MatchedPublishesNothing(t *testing.T) {
	bus := core.NewEventBus()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}

	exec.onOrderEvent(&sdkmodel.WSOrder{
		Id:           "ord-1",
		Market:       "m1",
		AssetId:      "tk1",
		Side:         "BUY",
		Price:        0.33,
		OriginalSize: 10,
		SizeMatched:  4,
		Status:       "MATCHED",
		Timestamp:    time.Now().Unix(),
	})

	assertNoExecutionEvent(t, ch)
}

func TestOnOrderEvent_CanceledEmitsCancelled(t *testing.T) {
	bus := core.NewEventBus()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}

	exec.onOrderEvent(&sdkmodel.WSOrder{
		Id:           "ord-2",
		Market:       "m1",
		AssetId:      "tk1",
		Side:         "BUY",
		Price:        0.4,
		OriginalSize: 10,
		SizeMatched:  2,
		Status:       "CANCELED",
		Timestamp:    time.Now().Unix(),
	})

	cancelled := mustRecvExecutionEvent(t, ch)
	if cancelled.Status != core.ExecutionStatusCancelled {
		t.Fatalf("unexpected status: %s", cancelled.Status)
	}
	if cancelled.OrderID != "ord-2" {
		t.Fatalf("unexpected order id: %s", cancelled.OrderID)
	}
}

func TestOnTradeEvent_DeduplicateTradeID(t *testing.T) {
	bus := core.NewEventBus()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}

	exec.onOrderEvent(&sdkmodel.WSOrder{
		Id:           "ord-3",
		Market:       "m1",
		AssetId:      "tk1",
		Side:         "BUY",
		Price:        0.5,
		OriginalSize: 10,
		SizeMatched:  0,
		Status:       "LIVE",
		Timestamp:    time.Now().Unix(),
	})
	_ = mustRecvExecutionEvent(t, ch)

	exec.onTradeEvent(&sdkmodel.WSTrade{
		Id:           "tr-1",
		Market:       "m1",
		AssetId:      "tk1",
		Side:         "BUY",
		Price:        0.5,
		Size:         3,
		Status:       "MINED",
		TakerOrderId: "ord-3",
		Timestamp:    time.Now().Unix(),
	})
	fill1 := mustRecvExecutionEvent(t, ch)
	if fill1.Status != core.ExecutionStatusPartiallyFilled || fill1.FilledSize != 3 {
		t.Fatalf("unexpected first fill: status=%s filled=%v", fill1.Status, fill1.FilledSize)
	}

	exec.onTradeEvent(&sdkmodel.WSTrade{
		Id:           "tr-1",
		Market:       "m1",
		AssetId:      "tk1",
		Side:         "BUY",
		Price:        0.5,
		Size:         3,
		Status:       "MINED",
		TakerOrderId: "ord-3",
		Timestamp:    time.Now().Unix(),
	})
	assertNoExecutionEvent(t, ch)

	exec.onTradeEvent(&sdkmodel.WSTrade{
		Id:           "tr-2",
		Market:       "m1",
		AssetId:      "tk1",
		Side:         "BUY",
		Price:        0.5,
		Size:         7,
		Status:       "MINED",
		TakerOrderId: "ord-3",
		Timestamp:    time.Now().Unix(),
	})
	fill2 := mustRecvExecutionEvent(t, ch)
	if fill2.Status != core.ExecutionStatusFilled || fill2.FilledSize != 7 {
		t.Fatalf("unexpected second fill: status=%s filled=%v", fill2.Status, fill2.FilledSize)
	}
}

func TestOnTradeEvent_FailedEmitsRejectedWithOrderID(t *testing.T) {
	bus := core.NewEventBus()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}

	exec.onOrderEvent(&sdkmodel.WSOrder{
		Id:           "ord-4",
		Market:       "m1",
		AssetId:      "tk1",
		Side:         "BUY",
		Price:        0.42,
		OriginalSize: 5,
		Status:       "LIVE",
		Timestamp:    time.Now().Unix(),
	})
	_ = mustRecvExecutionEvent(t, ch)

	exec.onTradeEvent(&sdkmodel.WSTrade{
		Id:           "tr-fail-1",
		Market:       "m1",
		AssetId:      "tk1",
		Side:         "BUY",
		Price:        0.42,
		Size:         1,
		Status:       "FAILED",
		TakerOrderId: "ord-4",
		Timestamp:    time.Now().Unix(),
	})

	rej := mustRecvExecutionEvent(t, ch)
	if rej.Status != core.ExecutionStatusRejected {
		t.Fatalf("unexpected status: %s", rej.Status)
	}
	if rej.OrderID != "ord-4" {
		t.Fatalf("unexpected order id: %s", rej.OrderID)
	}
}

func TestOnTradeEvent_UnknownOrderStillPublishesFill(t *testing.T) {
	bus := core.NewEventBus()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus}

	exec.onTradeEvent(&sdkmodel.WSTrade{
		Id:           "tr-unknown-1",
		Market:       "m1",
		AssetId:      "tk-unknown",
		Side:         "BUY",
		Price:        0.41,
		Size:         2,
		Status:       "MINED",
		TakerOrderId: "ord-unknown-1",
		Timestamp:    time.Now().Unix(),
	})

	fill := mustRecvExecutionEvent(t, ch)
	if fill.Status != core.ExecutionStatusPartiallyFilled {
		t.Fatalf("unexpected status: %s", fill.Status)
	}
	if fill.OrderID != "ord-unknown-1" {
		t.Fatalf("unexpected order id: %s", fill.OrderID)
	}
	if fill.MarketID != "m1" || fill.TokenID != "tk-unknown" {
		t.Fatalf("unexpected market/token: %+v", fill)
	}
	if fill.Side != model.BUY || fill.Price != 0.41 || fill.FilledSize != 2 {
		t.Fatalf("unexpected fill fields: %+v", fill)
	}
}

func mustRecvExecutionEvent(t *testing.T, ch <-chan core.Event) core.ExecutionEvent {
	t.Helper()
	select {
	case ev := <-ch:
		if ev.Type != core.EventExecution {
			t.Fatalf("unexpected event type: %s", ev.Type)
		}
		data, ok := ev.Data.(core.ExecutionEvent)
		if !ok {
			t.Fatalf("unexpected event payload type: %T", ev.Data)
		}
		return data
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for execution event")
		return core.ExecutionEvent{}
	}
}

func assertNoExecutionEvent(t *testing.T, ch <-chan core.Event) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event: %+v", ev)
	default:
	}
}
