package core

const (
	SideBuy  = "BUY"
	SideSell = "SELL"
)

const (
	// 创建、更新、结束、结算
	EventMarket EventType = "MARKET"
	// 订单薄更新
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
