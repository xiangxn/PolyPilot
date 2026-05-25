package core

import (
	"errors"
	"fmt"
	"testing"
)

func TestSentinelErrorsAreDistinct(t *testing.T) {
	all := []error{
		ErrOrderAlreadyReserved, ErrIntentAlreadyReserved, ErrReservationNotFound,
		ErrInsufficientBalance, ErrInsufficientPosition, ErrBelowMinReserve,
		ErrInvalidPrice, ErrInvalidSize, ErrInvalidSide,
		ErrInvalidMarket, ErrInvalidToken,
		ErrFillExceedsRemaining, ErrFillMarketTokenMismatch, ErrFillSideMismatch,
		ErrReconcileFailed,
	}
	seen := make(map[string]struct{}, len(all))
	for _, e := range all {
		if _, dup := seen[e.Error()]; dup {
			t.Fatalf("duplicate sentinel message: %q", e.Error())
		}
		seen[e.Error()] = struct{}{}
	}
}

func TestSentinelErrorsWrapAndUnwrap(t *testing.T) {
	wrapped := fmt.Errorf("context: %w", ErrInsufficientBalance)
	if !errors.Is(wrapped, ErrInsufficientBalance) {
		t.Fatalf("errors.Is should match wrapped sentinel")
	}
}
