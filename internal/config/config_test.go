package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
	pmutils "github.com/xiangxn/go-polymarket-sdk/utils"
)

const testDecryptPassword = "unit-test-password"

func TestLoad_FromYAMLAndSDKDefaults(t *testing.T) {
	tmp := t.TempDir()
	writeConfig(t, tmp, fmt.Sprintf(`sqlite_path: "data/test.db"
chain_rpc_url: "https://example-rpc"
balance_sync:
  enabled: true
  interval: "7s"
  epsilon: 0.01
  collateral_token: "0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174"
sdk_config:
  polymarket:
    owner_key: "%s"
`, mustEncrypt(t, testDecryptPassword, "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")))

	cfg := loadFromDir(t, tmp)

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
	if cfg.SDKConfig.Polymarket.ClobBaseURL != defaults.Polymarket.ClobBaseURL {
		t.Fatalf("polymarket.clob_base_url should fallback to sdk default")
	}
	if cfg.SDKConfig.Polymarket.ChainID == 0 || defaults.Polymarket.ChainID == 0 || cfg.SDKConfig.Polymarket.ChainID != defaults.Polymarket.ChainID {
		t.Fatalf("polymarket.chain_id should fallback to sdk default")
	}
}

func TestLoad_EnvOverride(t *testing.T) {
	tmp := t.TempDir()
	writeConfig(t, tmp, fmt.Sprintf(`chain_rpc_url: "https://yaml-rpc"
balance_sync:
  enabled: false
  interval: "5s"
sdk_config:
  polymarket:
    owner_key: "%s"
`, mustEncrypt(t, testDecryptPassword, "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")))

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

func TestLoad_EnvOverride_SDKConfig(t *testing.T) {
	tmp := t.TempDir()
	writeConfig(t, tmp, fmt.Sprintf(`sdk_config:
  polymarket:
    funder_address: "0xyaml-funder"
    owner_key: "%s"
`, mustEncrypt(t, testDecryptPassword, "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")))

	t.Setenv("PM_SDK_CONFIG_POLYMARKET_FUNDER_ADDRESS", "0xenv-funder")
	t.Setenv("PM_SDK_CONFIG_POLYMARKET_OWNER_KEY", mustEncrypt(t, testDecryptPassword, "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"))

	cfg := loadFromDir(t, tmp)

	if cfg.SDKConfig.Polymarket.FunderAddress != "0xenv-funder" {
		t.Fatalf("env should override sdk_config.polymarket.funder_address, got: %s", cfg.SDKConfig.Polymarket.FunderAddress)
	}
	if cfg.SDKConfig.Polymarket.OwnerKey != "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee" {
		t.Fatalf("env should override and decrypt sdk_config.polymarket.owner_key, got: %q", cfg.SDKConfig.Polymarket.OwnerKey)
	}
}

func TestLoad_MinimalConfig_UsesInternalDefault(t *testing.T) {
	tmp := t.TempDir()
	writeConfig(t, tmp, fmt.Sprintf(`sdk_config:
  polymarket:
    owner_key: "%s"
`, mustEncrypt(t, testDecryptPassword, "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")))

	cfg := loadFromDir(t, tmp)

	if cfg.ChainRPCURL != "https://polygon.drpc.org" {
		t.Fatalf("default chain_rpc_url mismatch: %s", cfg.ChainRPCURL)
	}
}

func TestLoad_DecryptSensitiveFields_WithEnvPassword(t *testing.T) {
	tmp := t.TempDir()
	password := testDecryptPassword

	plainOwnerKey := "0xabc123"
	plainKey := "clob-key"
	plainSecret := "clob-secret"
	plainPassphrase := "clob-passphrase"

	writeConfig(t, tmp, fmt.Sprintf(`sdk_config:
  polymarket:
    owner_key: "%s"
    clob_creds:
      key: "%s"
      secret: "%s"
      passphrase: "%s"
`,
		mustEncrypt(t, password, plainOwnerKey),
		mustEncrypt(t, password, plainKey),
		mustEncrypt(t, password, plainSecret),
		mustEncrypt(t, password, plainPassphrase),
	))

	cfg := loadFromDir(t, tmp)

	if cfg.SDKConfig.Polymarket.OwnerKey != "abc123" {
		t.Fatalf("owner_key should be decrypted and trim 0x, got: %q", cfg.SDKConfig.Polymarket.OwnerKey)
	}
	if cfg.SDKConfig.Polymarket.CLOBCreds == nil {
		t.Fatalf("clob_creds should not be nil after load")
	}
	if cfg.SDKConfig.Polymarket.CLOBCreds.Key != plainKey {
		t.Fatalf("clob_creds.key decrypt mismatch: %q", cfg.SDKConfig.Polymarket.CLOBCreds.Key)
	}
	if cfg.SDKConfig.Polymarket.CLOBCreds.Secret != plainSecret {
		t.Fatalf("clob_creds.secret decrypt mismatch: %q", cfg.SDKConfig.Polymarket.CLOBCreds.Secret)
	}
	if cfg.SDKConfig.Polymarket.CLOBCreds.Passphrase != plainPassphrase {
		t.Fatalf("clob_creds.passphrase decrypt mismatch: %q", cfg.SDKConfig.Polymarket.CLOBCreds.Passphrase)
	}
}

func loadFromDir(t *testing.T, dir string) Config {
	t.Helper()
	t.Setenv("PM_CONFIG_DECRYPT_PASSWORD", testDecryptPassword)
	return loadFromDirRaw(t, dir)
}

func loadFromDirRaw(t *testing.T, dir string) Config {
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

func mustEncrypt(t *testing.T, password, plain string) string {
	t.Helper()
	encryptor := pmutils.NewEncryptor(password)
	cipher, err := encryptor.Encrypt(plain)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}
	return cipher
}

func TestReadDecryptPassword_FromEnv(t *testing.T) {
	t.Setenv("PM_CONFIG_DECRYPT_PASSWORD", "  test-password  ")

	pwd, err := readDecryptPassword()
	if err != nil {
		t.Fatalf("readDecryptPassword failed: %v", err)
	}
	if pwd != "test-password" {
		t.Fatalf("env decrypt password mismatch: %q", pwd)
	}
}

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config.yaml failed: %v", err)
	}
}
