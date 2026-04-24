package state

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/tidwall/gjson"
	"github.com/xiangxn/go-polymarket-sdk/builder"
	"github.com/xiangxn/go-polymarket-sdk/constants"
	"github.com/xiangxn/go-polymarket-sdk/orders"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
	"github.com/xiangxn/go-polymarket-sdk/utils"
)

type PolymarketStateClient struct {
	Client         *sdk.PolymarketClient
	PositionLimits int
	SDKConfig      *sdk.PolymarketConfig
}

func NewPolymarketStateClient(client *sdk.PolymarketClient, config *sdk.PolymarketConfig, positionLimits int) *PolymarketStateClient {
	return &PolymarketStateClient{
		Client:         client,
		PositionLimits: positionLimits,
		SDKConfig:      config,
	}
}

func (p *PolymarketStateClient) GetOpenOrders() ([]orders.OpenOrder, error) {
	return p.Client.GetOpenOrders(nil, false, nil)
}

func (p *PolymarketStateClient) GetPositions() (*gjson.Result, error) {
	if p == nil || p.Client == nil {
		return nil, fmt.Errorf("polymarket client is nil")
	}
	if p.SDKConfig == nil {
		return nil, fmt.Errorf("sdk config is nil")
	}
	if p.SDKConfig.FunderAddress == "" {
		return nil, fmt.Errorf("FUNDERADDRESS is empty")
	}
	return p.Client.SearchPositions(p.SDKConfig.FunderAddress, false, positionsAPILimit(p.PositionLimits))
}

func (p *PolymarketStateClient) Redeem(ctx context.Context, onRedeemSuccess func(tokenIDs []string)) {
	go func() {
		log.Info().Msg("redeem loop start")
		defer log.Info().Msg("redeem loop exit")

		run := func() {
			tokenIDs, err := p.redeemOnce()
			if err != nil {
				log.Error().Err(err).Msg("redeem failed")
				return
			}
			if len(tokenIDs) > 0 && onRedeemSuccess != nil {
				onRedeemSuccess(tokenIDs)
			}
		}

		run()
		ticker := time.NewTicker(20 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}

func (p *PolymarketStateClient) redeemOnce() ([]string, error) {
	if p.Client == nil {
		return nil, fmt.Errorf("polymarket client is nil")
	}

	if p.SDKConfig.FunderAddress == "" {
		return nil, fmt.Errorf("FUNDERADDRESS is empty")
	}

	if p.SDKConfig.OwnerKey == "" {
		return nil, fmt.Errorf("SIGNERKEY is empty")
	}

	positions, err := p.Client.SearchPositions(p.SDKConfig.FunderAddress, true, 500)
	if err != nil {
		return nil, err
	}

	conditionIds := make([]string, 0, len(positions.Array()))
	negRisks := make([]bool, 0, len(positions.Array()))
	amounts := make([][]*big.Int, 0, len(positions.Array()))
	redeemedTokenIDs := make([]string, 0, len(positions.Array()))
	redeemedTokenSeen := make(map[string]struct{}, len(positions.Array()))

	for _, position := range positions.Array() {
		conditionIds = append(conditionIds, position.Get("conditionId").String())
		negRisk := position.Get("negativeRisk").Bool()

		tokenID := position.Get("asset").String()
		if tokenID == "" {
			tokenID = position.Get("assetId").String()
		}
		if tokenID == "" {
			tokenID = position.Get("asset_id").String()
		}
		if tokenID == "" {
			tokenID = position.Get("tokenId").String()
		}
		if tokenID != "" {
			if _, exists := redeemedTokenSeen[tokenID]; !exists {
				redeemedTokenSeen[tokenID] = struct{}{}
				redeemedTokenIDs = append(redeemedTokenIDs, tokenID)
			}
		}

		if negRisk {
			ams := []*big.Int{new(big.Int), new(big.Int)}
			value, parseErr := utils.ParseUnits(position.Get("size").String(), constants.CollateralTokenDecimals)
			if parseErr != nil {
				return nil, parseErr
			}
			idx := int(position.Get("outcomeIndex").Int())
			if idx < 0 || idx >= len(ams) {
				return nil, fmt.Errorf("invalid outcomeIndex: %d", idx)
			}
			ams[idx] = value
			amounts = append(amounts, ams)
		} else {
			amounts = append(amounts, []*big.Int{})
		}
		negRisks = append(negRisks, negRisk)
	}

	if len(conditionIds) == 0 {
		log.Debug().Msg("redeem skipped: no redeemable positions")
		return nil, nil
	}

	builderCreds := p.SDKConfig.BuilderCreds
	if builderCreds.Key == "" || builderCreds.Secret == "" || builderCreds.Passphrase == "" {
		return nil, fmt.Errorf("builder creds are empty, please set BUILDER_API_KEY/BUILDER_SECRET/BUILDER_PASSPHRASE")
	}

	relayClient := builder.NewRelayClient(p.SDKConfig.RelayerBaseURL, p.SDKConfig.OwnerKey, p.SDKConfig.ChainID, builderCreds, nil)
	_, err = relayClient.RedeemBatch(conditionIds, negRisks, amounts, nil)
	if err != nil {
		return nil, err
	}

	log.Info().Int("positions", len(conditionIds)).Msg("redeem success")
	return redeemedTokenIDs, nil
}

func positionsAPILimit(limit int) int {
	if limit > 0 {
		return limit
	}
	return defaultPositionsAPILimit
}
