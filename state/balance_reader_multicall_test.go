package state

import (
	"context"
	"math/big"
	"testing"
	"time"

	appconfig "github.com/xiangxn/polypilot/config"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

func TestNewMulticallBalanceReader_MissingFields(t *testing.T) {
	// empty rpcURL
	if _, err := NewMulticallBalanceReader("", big.NewInt(137), "0x0000000000000000000000000000000000000000", "0x0000000000000000000000000000000000000000"); err == nil {
		t.Fatal("expected error for empty rpcURL")
	}
	// empty tokenHex
	if _, err := NewMulticallBalanceReader("http://example", big.NewInt(137), "", "0x0000000000000000000000000000000000000000"); err == nil {
		t.Fatal("expected error for empty tokenHex")
	}
	// empty walletHex
	if _, err := NewMulticallBalanceReader("http://example", big.NewInt(137), "0x0000000000000000000000000000000000000000", ""); err == nil {
		t.Fatal("expected error for empty walletHex")
	}
}

func TestNewMulticallBalanceReader_NilChainID(t *testing.T) {
	_, err := NewMulticallBalanceReader("http://example", nil, "0x0000000000000000000000000000000000000000", "0x0000000000000000000000000000000000000000")
	if err == nil {
		t.Fatal("expected error for nil chain id")
	}
}

func TestNewMulticallBalanceReader_InvalidTokenAddress(t *testing.T) {
	_, err := NewMulticallBalanceReader("http://example", big.NewInt(137), "not-hex", "0x0000000000000000000000000000000000000000")
	if err == nil {
		t.Fatal("expected error for invalid token address")
	}
}

func TestNewMulticallBalanceReader_InvalidWalletAddress(t *testing.T) {
	_, err := NewMulticallBalanceReader("http://example", big.NewInt(137), "0x0000000000000000000000000000000000000000", "not-hex")
	if err == nil {
		t.Fatal("expected error for invalid wallet address")
	}
}

func TestNewMulticallBalanceReader_Success(t *testing.T) {
	r, err := NewMulticallBalanceReader(
		"http://example", big.NewInt(137),
		"0x0000000000000000000000000000000000000001",
		"0x0000000000000000000000000000000000000002",
	)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil reader")
	}
}

func TestBuildMulticallBalanceSyncConfig_Disabled(t *testing.T) {
	cfg := appconfig.Config{}
	out, err := BuildMulticallBalanceSyncConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out.Enabled {
		t.Fatal("expected disabled config")
	}
}

func TestBuildMulticallBalanceSyncConfig_MissingFunder(t *testing.T) {
	cfg := appconfig.Config{
		BalanceSync: appconfig.BalanceSyncConfig{Enabled: true},
	}
	_, err := BuildMulticallBalanceSyncConfig(cfg)
	if err == nil {
		t.Fatal("expected error for missing funder address")
	}
}

func TestBuildMulticallBalanceSyncConfig_InvalidConfig(t *testing.T) {
	cfg := appconfig.Config{
		BalanceSync: appconfig.BalanceSyncConfig{
			Enabled:         true,
			CollateralToken: "not-hex",
			Interval:        time.Second,
			Epsilon:         0.01,
		},
		SDKConfig: sdk.Config{
			Polymarket: sdk.PolymarketConfig{
				ChainID:       137,
				FunderAddress: "0x0000000000000000000000000000000000000001",
			},
		},
	}
	_, err := BuildMulticallBalanceSyncConfig(cfg)
	if err == nil {
		t.Fatal("expected error for invalid collateral token address")
	}
}

func TestBuildMulticallBalanceSyncConfig_Success(t *testing.T) {
	cfg := appconfig.Config{
		ChainRPCURL: "http://rpc",
		BalanceSync: appconfig.BalanceSyncConfig{
			Enabled:         true,
			CollateralToken: "0x0000000000000000000000000000000000000003",
			Interval:        2 * time.Second,
			Epsilon:         0.01,
			MinBalance:      1,
		},
		SDKConfig: sdk.Config{
			Polymarket: sdk.PolymarketConfig{
				ChainID:       137,
				FunderAddress: "0x0000000000000000000000000000000000000001",
			},
		},
	}
	out, err := BuildMulticallBalanceSyncConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !out.Enabled || out.Interval != 2*time.Second || out.Epsilon != 0.01 || out.MinBalance != 1 {
		t.Fatalf("unexpected config: %+v", out)
	}
	if out.Reader == nil {
		t.Fatal("expected reader")
	}
}

func TestNewState_Disabled(t *testing.T) {
	cfg := appconfig.Config{}
	s, err := NewState(cfg, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil state")
	}
}

func TestNewState_BuildError(t *testing.T) {
	cfg := appconfig.Config{
		BalanceSync: appconfig.BalanceSyncConfig{Enabled: true},
	}
	_, err := NewState(cfg, nil)
	if err == nil {
		t.Fatal("expected error from BuildMulticallBalanceSyncConfig")
	}
}

func TestMulticallReader_ReadOnchainBalance_BadRPC(t *testing.T) {
	r, err := NewMulticallBalanceReader(
		// invalid RPC URL → DialContext will fail
		"http://127.0.0.1:1",
		big.NewInt(137),
		"0x0000000000000000000000000000000000000001",
		"0x0000000000000000000000000000000000000002",
	)
	if err != nil {
		t.Fatalf("setup err: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, err := r.ReadOnchainBalance(ctx); err == nil {
		t.Fatal("expected error from bad RPC URL")
	}
}
