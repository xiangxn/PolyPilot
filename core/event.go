package core

import "time"

type EventType string

type Event struct {
	Type EventType
	Data any
}

type MarketEvent struct {
	MarketID  string
	TokenID   string
	Price     float64
	Timestamp int64
}

type ExecutionStatus string

type ExecutionEvent struct {
	OrderID       string
	ParentOrderID string
	MarketID      string
	TokenID       string
	Price         float64
	Side          string
	RequestedSize float64
	FilledSize    float64
	Status        ExecutionStatus
	Reason        string
	At            time.Time
}

type RiskEvent struct {
	Reason string
	At     time.Time
}

type MetricsEvent struct {
	Ticks             uint64
	MarketEvents      uint64
	ExecutionEvents   uint64
	ExecutionAccepted uint64
	ExecutionFilled   uint64
	ExecutionRejected uint64
	ExecutionBuffered uint64
	ExecutionExpired  uint64
	PendingOrders     int
	RiskRejected      uint64
	OrdersSent        uint64
	BusPublished      uint64
	BusDropped        uint64
	BusSubscribers    int
	BalanceAvailable  float64
	BalanceReserved   float64
	At                time.Time
}
