package state

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
	appconfig "polypilot/internal/config"
	utils "polypilot/internal/multicall"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

type MulticallBalanceReader struct {
	rpcURL string
	chain  int64
	token  common.Address
	wallet common.Address
}

func NewMulticallBalanceReader(rpcURL string, chainID *big.Int, tokenHex, walletHex string) (BalanceReader, error) {
	if rpcURL == "" || tokenHex == "" || walletHex == "" {
		return nil, errors.New("missing multicall balance reader config")
	}
	if chainID == nil {
		return nil, errors.New("missing chain id")
	}
	if !common.IsHexAddress(tokenHex) {
		return nil, errors.New("invalid collateral token address")
	}
	if !common.IsHexAddress(walletHex) {
		return nil, errors.New("invalid funder address")
	}

	return &MulticallBalanceReader{
		rpcURL: rpcURL,
		chain:  chainID.Int64(),
		token:  common.HexToAddress(tokenHex),
		wallet: common.HexToAddress(walletHex),
	}, nil
}

func BuildMulticallBalanceSyncConfig(cfg appconfig.Config) (BalanceSyncConfig, error) {
	if !cfg.BalanceSync.Enabled {
		return BalanceSyncConfig{}, nil
	}
	if cfg.SDKConfig.Polymarket.FunderAddress == "" {
		return BalanceSyncConfig{}, errors.New("missing polymarket funder address")
	}

	reader, err := NewMulticallBalanceReader(
		cfg.ChainRPCURL,
		big.NewInt(cfg.SDKConfig.Polymarket.ChainID),
		cfg.BalanceSync.CollateralToken,
		cfg.SDKConfig.Polymarket.FunderAddress,
	)
	if err != nil {
		return BalanceSyncConfig{}, fmt.Errorf("invalid balance sync config: %w", err)
	}

	return BalanceSyncConfig{
		Enabled:    true,
		Reader:     reader,
		Interval:   cfg.BalanceSync.Interval,
		Epsilon:    cfg.BalanceSync.Epsilon,
		MinBalance: cfg.BalanceSync.MinBalance,
	}, nil
}

func (r *MulticallBalanceReader) ReadOnchainBalance(ctx context.Context) (float64, error) {
	client, err := ethclient.DialContext(ctx, r.rpcURL)
	if err != nil {
		return 0, err
	}
	defer client.Close()

	info, err := utils.FetchERC20InfoMulticall3(ctx, client, r.chain, r.token, r.wallet)
	if err != nil {
		return 0, err
	}
	log.Printf("[MulticallBalanceReader] %s %f", r.wallet, info.Float())
	return info.Float(), nil
}
