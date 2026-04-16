package execution

import "time"

type AdapterOrder struct {
	ClientOrderID string
	MarketID      string
	TokenID       string
	Price         float64
	Size          float64
	Side          string
}

type AdapterExecution struct {
	ClientOrderID   string
	ExchangeOrderID string
	MarketID        string
	TokenID         string
	Price           float64
	RequestedSize   float64
	FilledSize      float64
	Status          string
	Reason          string
	At              time.Time
}

type ExchangeAdapter interface {
	Submit(order AdapterOrder) (exchangeOrderID string, err error)
	Cancel(clientOrderID string) error
	Query(clientOrderID string) (AdapterExecution, error)
}
