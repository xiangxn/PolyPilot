package execution

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/logx"
	"github.com/xiangxn/polypilot/runtime"

	"github.com/xiangxn/go-polymarket-sdk/orders"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
	"github.com/xiangxn/go-polymarket-sdk/relayer"
)

const (
	floatEpsilon          = 1e-9
	defaultExecutionQueue = 1024
)

var log = logx.Module("executor")

type trackedOrder struct {
	MarketID      string
	TokenID       string
	Side          orders.Side
	Price         float64
	RequestedSize float64
	FilledSize    float64
	Accepted      bool
	Finalized     bool
	SeenTradeIDs  map[string]struct{}
}

type preparedPlacement struct {
	intent    runtime.OrderIntent
	order     *orders.SignedOrder
	orderType orders.OrderType
}

type Executor struct {
	Bus *core.EventBus

	Client        *sdk.PolymarketClient
	TradeMonitor  *sdk.TradeMonitor
	Config        *sdk.Config
	DeferExec     bool
	DryRun        bool // when true, all placements publish Accepted+Filled without hitting Polymarket
	SignatureType orders.SignatureType

	// Reconcile is invoked when the executor sees a trade event for an
	// orderID it doesn't track (i.e., a manually-placed external order on
	// Polymarket). State will be reconciled in response.
	Reconcile func()

	relayClient *relayer.RelayClient // cached at Init

	ExecutionQueueSize int

	startOnce  sync.Once
	workerOnce sync.Once
	mu         sync.Mutex
	tracked    map[string]*trackedOrder
	queue      chan []runtime.OrderIntent
}

func (e *Executor) Init(bus *core.EventBus, ctx context.Context) {
	e.Bus = bus
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
		if e.relayClient == nil && cfg != nil {
			p := cfg.Polymarket
			e.relayClient = relayer.NewRelayClient(p.RelayerBaseURL, p.OwnerKey, p.ChainID, p.BuilderCreds, nil, p.RelayerKey)
		}
		if e.TradeMonitor == nil && cfg != nil {
			e.TradeMonitor = sdk.NewTradeMonitor(cfg.Polymarket.ClobWSBaseURL, cfg.Polymarket.CLOBCreds)
		}
		if e.TradeMonitor == nil {
			return
		}

		go func() {
			if err := e.TradeMonitor.Run(ctx); err != nil && ctx.Err() == nil {
				log.Error().Err(err).Msg("trade monitor stopped")
			}
		}()
		go e.consumeTradeEvents(ctx)
	})
}

func (e *Executor) Execute(intents []runtime.OrderIntent) {
	if len(intents) == 0 {
		return
	}
	if !e.DryRun && e.Client == nil {
		return
	}

	if e.DryRun {
		now := time.Now()
		for _, in := range intents {
			if in.Action == runtime.OrderIntentActionCancel {
				continue // dry-run cancel is a no-op
			}
			orderID := fmt.Sprintf("dryrun-%d-%s", time.Now().UnixNano(), in.TokenID)
			e.publish(core.ExecutionEvent{
				ParentOrderID: in.IntentID,
				OrderID:       orderID,
				MarketID:      in.MarketID,
				TokenID:       in.TokenID,
				Price:         in.Price,
				Side:          in.Side,
				RequestedSize: in.Size,
				Status:        core.ExecutionStatusAccepted,
				At:            now,
			})
			e.publish(core.ExecutionEvent{
				ParentOrderID: in.IntentID,
				OrderID:       orderID,
				MarketID:      in.MarketID,
				TokenID:       in.TokenID,
				Price:         in.Price,
				Side:          in.Side,
				RequestedSize: in.Size,
				FilledSize:    in.Size,
				Status:        core.ExecutionStatusFilled,
				At:            now,
			})
		}
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
					ParentOrderID: in.IntentID,
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
				log.Warn().Msg("skip cancel intent: empty order id")
				continue
			}
			validated = append(validated, in)
		case runtime.OrderIntentActionSplit, runtime.OrderIntentActionMerge:
			if in.Size <= 0 {
				log.Warn().Str("Action", string(action)).Float64("Size", in.Size).Msg("skip split/merge intent: size ≤ 0")
				continue
			}
			tlen := len(in.Tokens)
			if tlen != 2 { // 目前只支持二元的split与merge
				log.Warn().Int("tokens", tlen).Msg("merge tokens != 2")
				continue
			}
			validated = append(validated, in)
		default:
			e.publish(core.ExecutionEvent{
				ParentOrderID: in.IntentID,
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
		return
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
			// drain remaining batches and reject them so callers see the shutdown.
			e.drainQueueOnShutdown()
			return
		case batch := <-e.queue:
			if len(batch) == 0 {
				continue
			}
			var placements, cancels, splits, merges []runtime.OrderIntent
			for _, in := range batch {
				switch in.Action {
				case runtime.OrderIntentActionCancel:
					cancels = append(cancels, in)
				case runtime.OrderIntentActionSplit:
					splits = append(splits, in)
				case runtime.OrderIntentActionMerge:
					merges = append(merges, in)
				default:
					placements = append(placements, in)
				}
			}
			e.submitPlacements(placements)
			e.submitCancels(cancels)
			e.submitSplits(splits)
			e.submitMerges(merges)
		}
	}
}

// drainQueueOnShutdown rejects every remaining batch in the queue with a
// "shutting down" reason. Non-blocking — returns as soon as the queue is
// empty. Extracted from consumeExecuteQueue so it can be unit-tested
// without spinning up the worker goroutine.
func (e *Executor) drainQueueOnShutdown() {
	if e.queue == nil {
		return
	}
	for {
		select {
		case batch := <-e.queue:
			e.rejectBatch(batch, "shutting down")
		default:
			return
		}
	}
}

func (e *Executor) rejectBatch(intents []runtime.OrderIntent, reason string) {
	now := time.Now()
	for _, in := range intents {
		ev := core.ExecutionEvent{
			ParentOrderID: in.IntentID,
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

func (e *Executor) publish(data core.ExecutionEvent) {
	if e.Bus != nil {
		e.Bus.Publish(core.Event{Type: core.EventExecution, Data: data})
	}
}

func (e *Executor) ownKey() string {
	if e == nil || e.Config == nil {
		return ""
	}
	return strings.TrimSpace(e.Config.Polymarket.CLOBCreds.Key)
}

func (e *Executor) isOwnOwner(owner string) bool {
	key := e.ownKey()
	if key == "" {
		return true
	}
	return strings.TrimSpace(owner) == key
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
	if in.Side != orders.BUY && in.Side != orders.SELL {
		return fmt.Errorf("invalid order side")
	}
	switch in.OrderType {
	case "", orders.GTC, orders.FOK, orders.GTD, orders.FAK:
		return nil
	default:
		return fmt.Errorf("invalid order type")
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
