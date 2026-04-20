package utils

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

var multicall3 = map[int64]common.Address{
	1:     common.HexToAddress("0xcA11bde05977b3631167028862bE2a173976CA11"), // Ethereum
	56:    common.HexToAddress("0xcA11bde05977b3631167028862bE2a173976CA11"), // BSC
	137:   common.HexToAddress("0xcA11bde05977b3631167028862bE2a173976CA11"), // Polygon
	42161: common.HexToAddress("0xcA11bde05977b3631167028862bE2a173976CA11"), // Arbitrum
}

const erc20ABI = `[
  {"name":"balanceOf","type":"function","stateMutability":"view",
   "inputs":[{"name":"owner","type":"address"}],
   "outputs":[{"type":"uint256"}]},
  {"name":"decimals","type":"function","stateMutability":"view",
   "inputs":[],
   "outputs":[{"type":"uint8"}]},
  {"name":"symbol","type":"function","stateMutability":"view",
   "inputs":[],
   "outputs":[{"type":"string"}]}
]`

const multicall3ABI = `[
  {
    "inputs":[
      {"components":[
        {"name":"target","type":"address"},
        {"name":"allowFailure","type":"bool"},
        {"name":"callData","type":"bytes"}
      ],"name":"calls","type":"tuple[]"}
    ],
    "name":"aggregate3",
    "outputs":[
      {"components":[
        {"name":"success","type":"bool"},
        {"name":"returnData","type":"bytes"}
      ],"type":"tuple[]"}
    ],
    "stateMutability":"payable",
    "type":"function"
  }
]`

type ERC20Info struct {
	Token    common.Address
	Balance  *big.Int
	Decimals uint8
	Symbol   string
}

func FetchERC20InfoMulticall3(
	ctx context.Context,
	client *ethclient.Client,
	chainID int64,
	token common.Address,
	user common.Address,
) (*ERC20Info, error) {

	mcAddr, ok := multicall3[chainID]
	if !ok {
		return nil, errors.New("multicall3 not configured for chain")
	}

	// log.Printf("token: %s, user: %s", token.String(), user.String())

	erc20Parsed, _ := abi.JSON(strings.NewReader(erc20ABI))
	mcParsed, _ := abi.JSON(strings.NewReader(multicall3ABI))

	// --- pack calls ---
	balanceCall, _ := erc20Parsed.Pack("balanceOf", user)
	decimalsCall, _ := erc20Parsed.Pack("decimals")
	symbolCall, _ := erc20Parsed.Pack("symbol")

	type call struct {
		Target       common.Address
		AllowFailure bool
		CallData     []byte
	}

	calls := []call{
		{token, true, balanceCall},
		{token, true, decimalsCall},
		{token, true, symbolCall},
	}

	data, err := mcParsed.Pack("aggregate3", calls)
	if err != nil {
		return nil, err
	}

	msg := ethereum.CallMsg{
		To:   &mcAddr,
		Data: data,
	}

	raw, err := client.CallContract(ctx, msg, nil)
	if err != nil {
		return nil, err
	}

	var results []struct {
		Success    bool
		ReturnData []byte
	}

	if err := mcParsed.UnpackIntoInterface(&results, "aggregate3", raw); err != nil {
		return nil, err
	}

	// log.Printf("results: %+v", results)

	info := &ERC20Info{
		Token: token,
	}

	// balanceOf
	if results[0].Success {
		var out *big.Int
		_ = erc20Parsed.UnpackIntoInterface(&out, "balanceOf", results[0].ReturnData)
		info.Balance = out
	} else {
		info.Balance = big.NewInt(0)
	}

	// decimals
	if results[1].Success {
		_ = erc20Parsed.UnpackIntoInterface(&info.Decimals, "decimals", results[1].ReturnData)
	} else {
		info.Decimals = 18
	}

	// symbol
	if results[2].Success {
		_ = erc20Parsed.UnpackIntoInterface(&info.Symbol, "symbol", results[2].ReturnData)
	}

	return info, nil
}

func (e *ERC20Info) Float() float64 {
	f := new(big.Float).SetInt(e.Balance)
	scale := new(big.Float).SetFloat64(math.Pow10(int(e.Decimals)))
	f.Quo(f, scale)
	result, _ := f.Float64()
	return result
}

func (e *ERC20Info) String() string {
	f := e.Float()
	return fmt.Sprintf("%s %s", strconv.FormatFloat(f, 'f', int(e.Decimals), 64), e.Symbol)
}
