package state

import (
	"context"
	"testing"
	"time"

	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

func TestNewPolymarketStateClient_FieldsSet(t *testing.T) {
	cfg := &sdk.PolymarketConfig{FunderAddress: "0xabc"}
	p := NewPolymarketStateClient(nil, cfg, 250, true)
	if p == nil {
		t.Fatal("expected non-nil client")
	}
	if p.PositionLimits != 250 {
		t.Fatalf("position limits mismatch: %d", p.PositionLimits)
	}
	if !p.RedeemEnabled {
		t.Fatal("expected RedeemEnabled=true")
	}
	if p.SDKConfig != cfg {
		t.Fatal("expected SDKConfig to be stored by reference")
	}
}

func TestRedeem_DisabledShortCircuits(t *testing.T) {
	p := &PolymarketStateClient{RedeemEnabled: false}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	// Should return immediately; no goroutine spinning
	p.Redeem(ctx, nil)
	// Trivially pass — if it blocked, the timeout would have killed test
}

func TestRedeem_NilClientShortCircuits(t *testing.T) {
	var p *PolymarketStateClient
	// must not panic
	p.Redeem(context.Background(), nil)
}

func TestGetPositions_NilSDKConfig(t *testing.T) {
	p := &PolymarketStateClient{
		// Client is non-nil but SDKConfig is nil
		Client: &sdk.PolymarketClient{},
	}
	_, err := p.GetPositions()
	if err == nil {
		t.Fatal("expected error when SDKConfig nil")
	}
}

func TestGetPositions_NilClient(t *testing.T) {
	p := &PolymarketStateClient{}
	_, err := p.GetPositions()
	if err == nil {
		t.Fatal("expected error when client nil")
	}
}

func TestGetPositions_EmptyFunderAddress(t *testing.T) {
	p := &PolymarketStateClient{
		Client:    &sdk.PolymarketClient{},
		SDKConfig: &sdk.PolymarketConfig{},
	}
	_, err := p.GetPositions()
	if err == nil {
		t.Fatal("expected error when FunderAddress empty")
	}
}

func TestRedeemOnce_NilClient(t *testing.T) {
	p := &PolymarketStateClient{SDKConfig: &sdk.PolymarketConfig{}}
	_, err := p.redeemOnce()
	if err == nil {
		t.Fatal("expected error when client nil")
	}
}

func TestRedeemOnce_EmptyFunderAddress(t *testing.T) {
	// to skip the Client nil check, set a non-nil client
	p := &PolymarketStateClient{
		Client:    &sdk.PolymarketClient{},
		SDKConfig: &sdk.PolymarketConfig{FunderAddress: ""},
	}
	_, err := p.redeemOnce()
	if err == nil {
		t.Fatal("expected error when FunderAddress empty")
	}
}

func TestRedeemOnce_EmptyOwnerKey(t *testing.T) {
	p := &PolymarketStateClient{
		Client:    &sdk.PolymarketClient{},
		SDKConfig: &sdk.PolymarketConfig{FunderAddress: "0xabc", OwnerKey: ""},
	}
	_, err := p.redeemOnce()
	if err == nil {
		t.Fatal("expected error when OwnerKey empty")
	}
}

func TestPositionsAPILimit_DefaultWhenZeroOrNegative(t *testing.T) {
	if got := positionsAPILimit(0); got != defaultPositionsAPILimit {
		t.Fatalf("expected default %d, got %d", defaultPositionsAPILimit, got)
	}
	if got := positionsAPILimit(-1); got != defaultPositionsAPILimit {
		t.Fatalf("expected default %d for negative, got %d", defaultPositionsAPILimit, got)
	}
}

func TestPositionsAPILimit_PositivePassthrough(t *testing.T) {
	if got := positionsAPILimit(150); got != 150 {
		t.Fatalf("expected 150, got %d", got)
	}
}

func TestRedeem_EnabledButNoClient_GoroutineRunsAndLogsError(t *testing.T) {
	// RedeemEnabled=true triggers the goroutine; nil Client → redeemOnce returns an err
	// (and the goroutine then waits on a 20-minute ticker until ctx is canceled).
	p := &PolymarketStateClient{
		RedeemEnabled: true,
		SDKConfig:     &sdk.PolymarketConfig{},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	called := make(chan struct{}, 1)
	cb := func(tokenIDs []string) {
		select {
		case called <- struct{}{}:
		default:
		}
	}
	p.Redeem(ctx, cb)
	// Wait for context to be canceled so the goroutine exits gracefully.
	<-ctx.Done()
	// onRedeemSuccess shouldn't have been invoked since redeemOnce errored.
	select {
	case <-called:
		t.Fatal("onRedeemSuccess should not fire when redeemOnce errors")
	default:
	}
	// give the goroutine a moment to exit
	time.Sleep(20 * time.Millisecond)
}
