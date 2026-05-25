package core

import "errors"

// Sentinel errors used across packages. Callers must compare with errors.Is,
// never with err.Error() string equality.
var (
	ErrOrderAlreadyReserved    = errors.New("order already reserved")
	ErrIntentAlreadyReserved   = errors.New("intent already reserved")
	ErrReservationNotFound     = errors.New("reservation not found")
	ErrInsufficientBalance     = errors.New("insufficient available balance")
	ErrInsufficientPosition    = errors.New("insufficient token position")
	ErrBelowMinReserve         = errors.New("balance reached minimum reserve")
	ErrInvalidPrice            = errors.New("invalid price")
	ErrInvalidSize             = errors.New("invalid size")
	ErrInvalidSide             = errors.New("invalid side")
	ErrInvalidMarket           = errors.New("invalid market id")
	ErrInvalidToken            = errors.New("invalid token id")
	ErrFillExceedsRemaining    = errors.New("filled size exceeds remaining size")
	ErrFillMarketTokenMismatch = errors.New("fill market/token mismatch")
	ErrFillSideMismatch        = errors.New("fill side mismatch")
	ErrReconcileFailed         = errors.New("reconcile failed")
)
