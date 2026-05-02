package config

import (
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/xiangxn/polypilot/logx"

	"github.com/go-viper/mapstructure/v2"
	pgc "github.com/ivanzzeth/polymarket-go-contracts/v2"
	"github.com/spf13/viper"
	sdkmodel "github.com/xiangxn/go-polymarket-sdk/model"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
	pmutils "github.com/xiangxn/go-polymarket-sdk/utils"
	"golang.org/x/term"
)

type Config struct {
	ChainRPCURL string             `mapstructure:"chain_rpc_url"`
	BalanceSync BalanceSyncConfig  `mapstructure:"balance_sync"`
	Logging     logx.LoggingConfig `mapstructure:"logging"`
	SDKConfig   sdk.Config         `mapstructure:"sdk_config"`
}

type BalanceSyncConfig struct {
	Enabled         bool          `mapstructure:"enabled"`
	MinBalance      float64       `mapstructure:"min_balance"`
	Interval        time.Duration `mapstructure:"interval"`
	Epsilon         float64       `mapstructure:"epsilon"`
	CollateralToken string        `mapstructure:"collateral_token"`
}

func Load() (Config, *viper.Viper, error) {
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
			return Config{}, nil, fmt.Errorf("read config.yaml failed: %w", err)
		}
	}

	defaultSDKCfg := sdk.DefaultConfig()
	cfg := Config{
		ChainRPCURL: "https://polygon.drpc.org",
		Logging:     logx.DefaultConfig(),
	}
	if defaultSDKCfg != nil {
		cfg.SDKConfig = *defaultSDKCfg
	}
	if err := v.Unmarshal(&cfg, viper.DecodeHook(
		mapstructure.StringToTimeDurationHookFunc(),
	)); err != nil {
		return Config{}, nil, fmt.Errorf("decode config failed: %w", err)
	}

	if cfg.BalanceSync.CollateralToken == "" {
		c := pgc.GetContractConfig(big.NewInt(cfg.SDKConfig.Polymarket.ChainID))
		cfg.BalanceSync.CollateralToken = c.CollateralToken.Hex()
	}

	if err := decryptSensitiveFields(&cfg); err != nil {
		return Config{}, nil, err
	}
	if cfg.SDKConfig.Polymarket.OwnerKey != "" {
		cfg.SDKConfig.Polymarket.OwnerKey = strings.TrimPrefix(strings.TrimSpace(cfg.SDKConfig.Polymarket.OwnerKey), "0x")
	}

	return cfg, v, nil
}

func decryptSensitiveFields(cfg *Config) error {
	type decryptTarget struct {
		label string
		value *string
	}

	targets := []decryptTarget{{label: "sdk_config.polymarket.owner_key", value: &cfg.SDKConfig.Polymarket.OwnerKey}}
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
	appendCredTargets("sdk_config.polymarket.clob_creds", cfg.SDKConfig.Polymarket.CLOBCreds)
	appendCredTargets("sdk_config.polymarket.builder_creds", cfg.SDKConfig.Polymarket.BuilderCreds)

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
	if envPassword := strings.TrimSpace(os.Getenv("PM_CONFIG_DECRYPT_PASSWORD")); envPassword != "" {
		return envPassword, nil
	}

	fmt.Fprint(os.Stdout, "请输入启动密码: ")
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
