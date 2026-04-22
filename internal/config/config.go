package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
	sdkmodel "github.com/xiangxn/go-polymarket-sdk/model"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
	pmutils "github.com/xiangxn/go-polymarket-sdk/utils"
	"golang.org/x/term"
)

type Config struct {
	SignerKey   string               `mapstructure:"signer_key"`
	ChainRPCURL string               `mapstructure:"chain_rpc_url"`
	BalanceSync BalanceSyncConfig    `mapstructure:"balance_sync"`
	Polymarket  sdk.PolymarketConfig `mapstructure:"polymarket"`
}

type BalanceSyncConfig struct {
	Enabled         bool          `mapstructure:"enabled"`
	MinBalance      float64       `mapstructure:"min_balance"`
	Interval        time.Duration `mapstructure:"interval"`
	Epsilon         float64       `mapstructure:"epsilon"`
	CollateralToken string        `mapstructure:"collateral_token"`
}

func Load() (Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.SetEnvPrefix("PM")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return Config{}, fmt.Errorf("read config.yaml failed: %w", err)
		}
	}

	defaultSDKCfg := sdk.DefaultConfig()
	cfg := Config{ChainRPCURL: "https://polygon.drpc.org"}
	if defaultSDKCfg != nil {
		cfg.Polymarket = defaultSDKCfg.Polymarket
	}
	if err := v.Unmarshal(&cfg, viper.DecodeHook(
		mapstructure.StringToTimeDurationHookFunc(),
	)); err != nil {
		return Config{}, fmt.Errorf("decode config failed: %w", err)
	}

	if err := decryptSensitiveFields(&cfg); err != nil {
		return Config{}, err
	}
	if cfg.SignerKey != "" {
		cfg.SignerKey = strings.TrimPrefix(strings.TrimSpace(cfg.SignerKey), "0x")
	}

	return cfg, nil
}

func decryptSensitiveFields(cfg *Config) error {
	type decryptTarget struct {
		label string
		value *string
	}

	targets := []decryptTarget{{label: "signer_key", value: &cfg.SignerKey}}
	appendCredTargets := func(prefix string, creds *sdkmodel.ApiKeyCreds) {
		if creds == nil {
			return
		}
		targets = append(targets,
			decryptTarget{label: prefix + ".key", value: &creds.Key},
			decryptTarget{label: prefix + ".secret", value: &creds.Secret},
			decryptTarget{label: prefix + ".passphrase", value: &creds.Passphrase},
		)
	}
	appendCredTargets("polymarket.clob_creds", cfg.Polymarket.CLOBCreds)
	appendCredTargets("polymarket.builder_creds", cfg.Polymarket.BuilderCreds)

	hasEncrypted := false
	for i := range targets {
		if strings.TrimSpace(*targets[i].value) != "" {
			hasEncrypted = true
			break
		}
	}
	if !hasEncrypted {
		return nil
	}

	password, err := readDecryptPassword()
	if err != nil {
		return err
	}
	encryptor := pmutils.NewEncryptor(password)

	for i := range targets {
		raw := strings.TrimSpace(*targets[i].value)
		if raw == "" {
			continue
		}
		decrypted, err := encryptor.Decrypt(raw)
		if err != nil {
			return fmt.Errorf("decrypt %s failed: %w", targets[i].label, err)
		}
		*targets[i].value = strings.TrimSpace(decrypted)
	}
	return nil
}

func readDecryptPassword() (string, error) {
	fmt.Fprint(os.Stdout, "请输入配置解密密码: ")
	passwordBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stdout)
	if err != nil {
		return "", fmt.Errorf("read config decrypt password failed: %w", err)
	}
	password := strings.TrimSpace(string(passwordBytes))
	if password == "" {
		return "", errors.New("config decrypt password cannot be empty")
	}
	return password, nil
}
