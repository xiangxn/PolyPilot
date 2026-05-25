package state

import (
	"context"
	"sync"
	"time"

	"github.com/tidwall/gjson"
	"github.com/xiangxn/go-polymarket-sdk/orders"
)

type State struct {
	mu       sync.RWMutex
	position Position
	balance  Balance

	// orderId -> orderReservation
	orderReservations map[string]OrderReservation
	// intentId -> provisional reservation
	provisionalReservations map[string]ProvisionalReservation

	// daily PnL tracking (UTC)
	dailyPnL     float64
	dailyPnLDate string

	balanceSync    BalanceSyncConfig
	balanceSyncRun sync.Once
	restoreClient  ExchangeStateClient

	// reconcile signal channel (capacity 1, deduplicated)
	reconcileTrigger chan struct{}

	// 仓位临近到期预警：marketID -> expiringMarket
	expiryMu      sync.Mutex
	expiryMarkets map[string]*expiringMarket
}

type expiringMarket struct {
	endTime  int64
	tokenIDs []string
	fired    bool
}

type OrderReservation struct {
	OrderID        string
	MarketID       string
	TokenID        string
	Side           orders.Side
	Price          float64
	RemainingSize  float64
	Reserved       float64
	ExternalOrigin bool // true when reconciled in from Polymarket without local intent
}

type ProvisionalReservation struct {
	IntentID      string
	MarketID      string
	TokenID       string
	Side          orders.Side
	Price         float64
	RemainingSize float64
	Reserved      float64
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

type TokenPosition struct {
	Available    float64
	Reserved     float64
	AvgCost      float64
	AvgCostKnown bool    // false → exclude from UnrealizedPnL
	TotalBought  float64 // cumulative BUY size (for weighted average)
}

type Position struct {
	Tokens map[string]TokenPosition
}

type Balance struct {
	Available  float64
	Reserved   float64
	MinBalance float64
}

type Snapshot struct {
	Position Position
	Balance  Balance
	// orderId -> orderReservation
	Orders         map[string]OrderReservation
	DailyPnL       float64
	DailyPnLDate   string // "2006-01-02" UTC
	OpenOrderCount int
}

type ExchangeStateClient interface {
	GetOpenOrders() ([]orders.OpenOrder, error)
	GetPositions() (*gjson.Result, error)
	Redeem(ctx context.Context, onRedeemSuccess func(tokenIDs []string))
}

type BalanceReader interface {
	ReadOnchainBalance(ctx context.Context) (float64, error)
}

type BalanceSyncEvent struct {
	OnchainTotal float64
	Drift        float64
	Changed      bool
	Err          error
}

type BalanceSyncConfig struct {
	Enabled    bool
	Reader     BalanceReader
	Interval   time.Duration
	Epsilon    float64
	MinBalance float64
	OnEvent    func(BalanceSyncEvent)
}

type ReconcileReport struct {
	OrdersAdded      int
	OrdersRemoved    int
	OrdersUpdated    int
	PositionsAdded   int
	PositionsRemoved int
	PositionsUpdated int
	DurationMs       int64
	Err              error
}

type ReconcileConfig struct {
	Enabled      bool
	Interval     time.Duration
	RetryBackoff []time.Duration // empty → no retry
	OnReport     func(ReconcileReport)
}
