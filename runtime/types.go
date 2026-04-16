package runtime

import (
	"context"
	"polypilot/core"
	"polypilot/state"
	"polypilot/store"
	"sync"
	"sync/atomic"
	"time"
)

type Observation struct {
	Probability float64
	Price       float64
	At          int64
	Data        any
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
	OnUpdate(ev core.Event) (Observation, bool)
}

type Strategy interface {
	Init(bus *core.EventBus)
	OnUpdate(e core.Event, m Observation) []OrderIntent
}

type RiskManager interface {
	Check(orders []OrderIntent, snapshot state.Snapshot) error
}

type Executor interface {
	Init(bus *core.EventBus)
	Execute(orders []OrderIntent)
}

type pendingExecution struct {
	events    []core.ExecutionEvent
	firstSeen time.Time
}

type Engine struct {
	Bus   *core.EventBus
	State *state.State
	Risk  RiskManager
	Exec  Executor

	Feeds       []Feed
	Observers   []Observer
	Probability Probability
	Strategies  []Strategy

	PendingEventTTL   time.Duration
	FinalizedOrderTTL time.Duration
	SnapshotInterval  time.Duration
	SQLitePath        string

	OrderStore     store.OrderStore
	ExecutionStore store.ExecutionStore
	StateStore     store.StateStore

	ticks             atomic.Uint64
	marketEvents      atomic.Uint64
	executionEvents   atomic.Uint64
	executionAccepted atomic.Uint64
	executionFilled   atomic.Uint64
	executionRejected atomic.Uint64
	executionBuffered atomic.Uint64
	executionExpired  atomic.Uint64
	riskRejected      atomic.Uint64
	ordersSent        atomic.Uint64

	orderMu sync.RWMutex

	acceptedOrders map[string]struct{}
	finalized      map[string]struct{}
	finalizedAt    map[string]time.Time
	pendingByOrder map[string]pendingExecution
}

type Context struct {
	Probability float64
	TimeLeft    time.Duration
}

type OrderIntent struct {
	MarketID string
	TokenID  string
	Price    float64
	Side     string
	Size     float64
}
