package runtime

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/state"

	"github.com/spf13/viper"
	"github.com/xiangxn/go-polymarket-sdk/orders"
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

type ProbabilitySnapshotProvider interface {
	CurrentObservation() (Observation, bool)
}

type Strategy interface {
	Init(bus *core.EventBus, ctx context.Context, cfg *viper.Viper)
	OnUpdate(e core.Event, o Observation, stateSnap state.Snapshot) []OrderIntent
}

/*
使用时注意：需要在策略中缓存state自己来处理下单去重的问题。
这是独立于数据事件的一个ticker驱动
*/
type TickStrategy interface {
	OnTick(now time.Time, o Observation, stateSnap state.Snapshot) []OrderIntent
}

type ExecutionAwareStrategy interface {
	OnExecution(ev core.ExecutionEvent, o Observation, snap state.Snapshot) []OrderIntent
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

	PendingEventTTL      time.Duration
	FinalizedOrderTTL    time.Duration
	ProvisionalOrderTTL  time.Duration
	StrategyTickInterval time.Duration

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
	OrderIntentActionSplit  OrderIntentAction = "SPLIT"
	OrderIntentActionMerge  OrderIntentAction = "MERGE"
)

type OrderIntent struct {
	Action OrderIntentAction

	// PLACE 必填
	MarketID string
	TokenID  string
	Price    float64
	Side     orders.Side

	// SPLIT,MERGE时为amount
	Size     float64
	IntentID string

	// CANCEL 必填（交易所订单 ID）
	OrderID string

	// SPLIT,MERGE时需要所有tokenIds
	Tokens []string
}
