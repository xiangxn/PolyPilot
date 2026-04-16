package core

const (
	SideBuy  = "BUY"
	SideSell = "SELL"
)

const (
	EventMarket    EventType = "MARKET"
	EventOrderBook EventType = "ORDERBOOK"
	EventSignal    EventType = "SIGNAL"
	EventOrder     EventType = "ORDER"
	EventExecution EventType = "EXECUTION"
	EventRisk      EventType = "RISK"
	EventStrategy  EventType = "STRATEGY"
	EventSystem    EventType = "SYSTEM"
	EventMetrics   EventType = "METRICS"
)

const (
	ExecutionStatusAccepted        ExecutionStatus = "ACCEPTED"
	ExecutionStatusPartiallyFilled ExecutionStatus = "PARTIALLY_FILLED"
	ExecutionStatusFilled          ExecutionStatus = "FILLED"
	ExecutionStatusCancelled       ExecutionStatus = "CANCELLED"
	ExecutionStatusRejected        ExecutionStatus = "REJECTED"
)
