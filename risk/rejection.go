package risk

import "fmt"

// RejectionType is an enumeration of why the risk engine rejected an order.
// Use in metrics aggregation, alerting, and dashboards instead of free-form
// reason strings.
type RejectionType string

const (
	RejectInsufficientBalance  RejectionType = "INSUFFICIENT_BALANCE"
	RejectBelowMinReserve      RejectionType = "BELOW_MIN_RESERVE"
	RejectInsufficientPosition RejectionType = "INSUFFICIENT_POSITION"
	RejectExposureCap          RejectionType = "EXPOSURE_CAP"
	RejectSlippage             RejectionType = "SLIPPAGE"
	RejectCooldown             RejectionType = "COOLDOWN"
	RejectDailyLoss            RejectionType = "DAILY_LOSS"
	RejectMaxOpenOrders        RejectionType = "MAX_OPEN_ORDERS"
	RejectInvalidIntent        RejectionType = "INVALID_INTENT"
)

// Rejection is the error returned by Engine.Check when one of the risk caps
// trips. Callers should use errors.As to extract Type for aggregation.
type Rejection struct {
	Type   RejectionType
	Detail string
}

func (r *Rejection) Error() string {
	return fmt.Sprintf("%s: %s", r.Type, r.Detail)
}

func reject(t RejectionType, format string, args ...any) *Rejection {
	return &Rejection{Type: t, Detail: fmt.Sprintf(format, args...)}
}
