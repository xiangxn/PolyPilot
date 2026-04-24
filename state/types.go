package state

import (
	"context"
	"sync"
	"time"

	"github.com/polymarket/go-order-utils/pkg/model"
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

	balanceSync    BalanceSyncConfig
	balanceSyncRun sync.Once
	restoreClient  ExchangeStateClient
}

type OrderReservation struct {
	OrderID       string
	MarketID      string
	TokenID       string
	Side          model.Side
	Price         float64
	RemainingSize float64
	Reserved      float64
}

type ProvisionalReservation struct {
	IntentID      string
	MarketID      string
	TokenID       string
	Side          model.Side
	Price         float64
	RemainingSize float64
	Reserved      float64
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

type TokenPosition struct {
	Available float64
	Reserved  float64
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
	Orders map[string]OrderReservation
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
