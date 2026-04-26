package runtime

import (
	"context"
	"polypilot/core"
	"polypilot/state"
	"sync"
	"sync/atomic"
	"time"

	"github.com/polymarket/go-order-utils/pkg/model"
	"github.com/spf13/viper"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

type Token struct {
	Id       string
	AskPrice float64
	BidPrice float64
}

type Observation struct {
	At       int64
	MarketID string
	// tokenId -> Token
	Tokens      map[string]Token
	Probability float64
	TimeLeftSec int64
	Confidence  float64

	// 可扩展特征（按命名空间 key）
	Features map[string]any

	GetOrderBook func(tokenId string) *sdk.OrderBook
}

type Feed interface {
	Init(bus *core.EventBus)
	Start(ctx context.Context)
}

type Observer interface {
	Init(bus *core.EventBus)
	Start(ctx context.Context)
}

type Probability interface {
	Init(ctx context.Context)
	OnUpdate(ev core.Event) (Observation, bool)
}

type Strategy interface {
	Init(bus *core.EventBus, ctx context.Context, cfg *viper.Viper)
	OnUpdate(e core.Event, o Observation, stateSnap state.Snapshot) []OrderIntent
}

type ExecutionAwareStrategy interface {
	OnExecution(ev core.ExecutionEvent, snap state.Snapshot) []OrderIntent
}

type RiskManager interface {
	Check(orders []OrderIntent, snapshot state.Snapshot) error
}

type Executor interface {
	Init(bus *core.EventBus, ctx context.Context)
	Execute(orders []OrderIntent)
}

type pendingExecution struct {
	events    []core.ExecutionEvent
	firstSeen time.Time
}

type Engine struct {
	Bus    *core.EventBus
	State  *state.State
	Risk   RiskManager
	Exec   Executor
	Config *viper.Viper

	Feeds       []Feed
	Observers   []Observer
	Probability Probability
	Strategies  []Strategy

	PendingEventTTL     time.Duration
	FinalizedOrderTTL   time.Duration
	ProvisionalOrderTTL time.Duration

	ticks             atomic.Uint64
	inputEvents       atomic.Uint64
	executionEvents   atomic.Uint64
	executionAccepted atomic.Uint64
	executionFilled   atomic.Uint64
	executionRejected atomic.Uint64
	executionBuffered atomic.Uint64
	executionExpired  atomic.Uint64
	riskRejected      atomic.Uint64
	ordersSent        atomic.Uint64

	orderMu   sync.RWMutex
	intentSeq atomic.Uint64

	acceptedOrders map[string]struct{}
	finalized      map[string]struct{}
	finalizedAt    map[string]time.Time
	pendingByOrder map[string]pendingExecution
}

type Context struct {
	Probability float64
	TimeLeft    time.Duration
}

type OrderIntentAction string

const (
	OrderIntentActionPlace  OrderIntentAction = "PLACE"
	OrderIntentActionCancel OrderIntentAction = "CANCEL"
)

type OrderIntent struct {
	Action OrderIntentAction

	// PLACE 必填
	MarketID string
	TokenID  string
	Price    float64
	Side     model.Side
	Size     float64
	IntentID string

	// CANCEL 必填（交易所订单 ID）
	OrderID string
}
