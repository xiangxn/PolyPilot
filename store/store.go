package store

import (
	"polypilot/core"

	"github.com/polymarket/go-order-utils/pkg/model"
)

type OrderRecord struct {
	OrderID       string
	MarketID      string
	TokenID       string
	Side          model.Side
	Price         float64
	RequestedSize float64
	RemainingSize float64
	Reserved      float64
	Status        core.ExecutionStatus
	UpdatedAt     int64
}

type TokenPositionRecord struct {
	Available float64
	Reserved  float64
}

type SnapshotRecord struct {
	Available float64
	Reserved  float64
	Tokens    map[string]TokenPositionRecord
	At        int64
}

type OrderStore interface {
	UpsertOrder(rec OrderRecord) error
	GetOrder(orderID string) (OrderRecord, bool, error)
	ListOpenOrders() ([]OrderRecord, error)
}

type ExecutionStore interface {
	AppendExecution(ev core.ExecutionEvent) error
	ListExecutionsSince(unixNano int64) ([]core.ExecutionEvent, error)
}

type StateStore interface {
	SaveSnapshot(s SnapshotRecord) error
	LoadLatestSnapshot() (SnapshotRecord, bool, error)
}
