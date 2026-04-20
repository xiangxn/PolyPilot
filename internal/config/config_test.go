package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

func TestLoad_FromYAMLAndSDKDefaults(t *testing.T) {
	tmp := t.TempDir()
	writeConfig(t, tmp, `sqlite_path: "data/test.db"
chain_rpc_url: "https://example-rpc"
balance_sync:
  enabled: true
  interval: "7s"
  epsilon: 0.01
  collateral_token: "0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174"
`)

	cfg := loadFromDir(t, tmp)

	if cfg.SQLitePath != "data/test.db" {
		t.Fatalf("sqlite_path mismatch: %s", cfg.SQLitePath)
	}
	if cfg.ChainRPCURL != "https://example-rpc" {
		t.Fatalf("chain_rpc_url mismatch: %s", cfg.ChainRPCURL)
	}
	if !cfg.BalanceSync.Enabled {
		t.Fatalf("balance_sync.enabled should be true")
	}
	if cfg.BalanceSync.Interval != 7*time.Second {
		t.Fatalf("balance_sync.interval mismatch: %s", cfg.BalanceSync.Interval)
	}
	if cfg.BalanceSync.Epsilon != 0.01 {
		t.Fatalf("balance_sync.epsilon mismatch: %f", cfg.BalanceSync.Epsilon)
	}

	defaults := sdk.DefaultConfig()
	if defaults == nil {
		t.Fatalf("sdk default config should not be nil")
	}
	if cfg.Polymarket.ClobBaseURL != defaults.Polymarket.ClobBaseURL {
		t.Fatalf("polymarket.clob_base_url should fallback to sdk default")
	}
	if cfg.Polymarket.ChainID == nil || defaults.Polymarket.ChainID == nil || cfg.Polymarket.ChainID.Cmp(defaults.Polymarket.ChainID) != 0 {
		t.Fatalf("polymarket.chain_id should fallback to sdk default")
	}
}

func TestLoad_EnvOverride(t *testing.T) {
	tmp := t.TempDir()
	writeConfig(t, tmp, `chain_rpc_url: "https://yaml-rpc"
balance_sync:
  enabled: false
  interval: "5s"
`)

	t.Setenv("PM_CHAIN_RPC_URL", "https://env-rpc")
	t.Setenv("PM_BALANCE_SYNC_ENABLED", "true")
	t.Setenv("PM_BALANCE_SYNC_INTERVAL", "11s")

	cfg := loadFromDir(t, tmp)

	if cfg.ChainRPCURL != "https://env-rpc" {
		t.Fatalf("env should override chain_rpc_url, got: %s", cfg.ChainRPCURL)
	}
	if !cfg.BalanceSync.Enabled {
		t.Fatalf("env should override balance_sync.enabled")
	}
	if cfg.BalanceSync.Interval != 11*time.Second {
		t.Fatalf("env should override balance_sync.interval, got: %s", cfg.BalanceSync.Interval)
	}
}

func TestLoad_NoConfigFile_UsesInternalDefault(t *testing.T) {
	tmp := t.TempDir()
	cfg := loadFromDir(t, tmp)

	if cfg.ChainRPCURL != "https://polygon.drpc.org" {
		t.Fatalf("default chain_rpc_url mismatch: %s", cfg.ChainRPCURL)
	}
}

func loadFromDir(t *testing.T, dir string) Config {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	return cfg
}

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config.yaml failed: %v", err)
	}
}
