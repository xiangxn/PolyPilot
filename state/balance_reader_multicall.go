package state

import (
	"context"
	"errors"
	"math/big"
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
	if !common.IsHexAddress(tokenHex) {
		return nil, errors.New("invalid collateral token address")
	}
	if !common.IsHexAddress(walletHex) {
		return nil, errors.New("invalid wallet address")
	}

	return &MulticallBalanceReader{
		rpcURL: rpcURL,
		chain:  chainID.Int64(),
		token:  common.HexToAddress(tokenHex),
		wallet: common.HexToAddress(walletHex),
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

	return info.Float(), nil
}
