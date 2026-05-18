package execution

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/runtime"

	"github.com/xiangxn/go-polymarket-sdk/constants"
	"github.com/xiangxn/go-polymarket-sdk/orders"
)

func (e *Executor) submitSplits(intents []runtime.OrderIntent) {
	if len(intents) == 0 {
		return
	}
	for _, intent := range intents {
		size := math.Trunc(intent.Size*1e6) / 1e6
		tlen := 2
		orderTmps := make([]string, 0, tlen)
		newIntents := make([]runtime.OrderIntent, 0, tlen)
		for _, t := range intent.Tokens {
			newIntent := runtime.OrderIntent{
				Action:   runtime.OrderIntentActionPlace,
				MarketID: intent.MarketID,
				TokenID:  t,
				Price:    0.5,
				Side:     orders.BUY,
				Size:     size,
			}
			orderId := fmt.Sprintf("%d_%s", time.Now().UnixNano(), t)
			orderTmps = append(orderTmps, orderId)
			newIntents = append(newIntents, newIntent)
			e.trackPostedOrder(orderId, newIntent)
			e.publishAcceptedFromPost(newIntent, orderId, time.Now())
		}
		result, err := e.relayClient.SplitTokens(intent.MarketID, strconv.FormatFloat(size, 'f', constants.CollateralTokenDecimals, 64), false)
		if err != nil {
			log.Error().AnErr("err", err).Msg("split token failed")
			for i, o := range orderTmps {
				in := newIntents[i]
				e.publish(core.ExecutionEvent{
					OrderID:       o,
					MarketID:      in.MarketID,
					TokenID:       in.TokenID,
					Price:         in.Price,
					Side:          in.Side,
					RequestedSize: in.Size,
					Status:        core.ExecutionStatusRejected,
					Reason:        fmt.Sprintf("split token failed: %v", err),
					At:            time.Now(),
				})
			}
			continue
		}
		log.Info().Str("State", result.State).Str("Hash", result.Hash).Msg("submitSplits result")
		if result.State == "STATE_NEW" {
			for i, o := range orderTmps {
				in := newIntents[i]
				e.publish(core.ExecutionEvent{
					OrderID:       o,
					MarketID:      in.MarketID,
					TokenID:       in.TokenID,
					Price:         in.Price,
					Side:          in.Side,
					RequestedSize: in.Size,
					FilledSize:    in.Size,
					Status:        core.ExecutionStatusFilled,
					At:            time.Now(),
				})
			}
		} else {
			for i, o := range orderTmps {
				in := newIntents[i]
				e.publish(core.ExecutionEvent{
					OrderID:       o,
					MarketID:      in.MarketID,
					TokenID:       in.TokenID,
					Price:         in.Price,
					Side:          in.Side,
					RequestedSize: in.Size,
					FilledSize:    in.Size,
					Status:        core.ExecutionStatusRejected,
					Reason:        fmt.Sprintf("split token failed: %s", result.State),
					At:            time.Now(),
				})
			}
		}
	}
}

func (e *Executor) submitMerges(intents []runtime.OrderIntent) {
	if len(intents) == 0 {
		return
	}
	for _, intent := range intents {
		size := math.Trunc(intent.Size*1e6) / 1e6
		tlen := 2
		orderTmps := make([]string, 0, tlen)
		newIntents := make([]runtime.OrderIntent, 0, tlen)
		for _, t := range intent.Tokens {
			newIntent := runtime.OrderIntent{
				Action:   runtime.OrderIntentActionPlace,
				MarketID: intent.MarketID,
				TokenID:  t,
				Price:    0.5,
				Side:     orders.SELL,
				Size:     size,
			}
			orderId := fmt.Sprintf("%d_%s", time.Now().UnixNano(), t)
			orderTmps = append(orderTmps, orderId)
			newIntents = append(newIntents, newIntent)
			e.trackPostedOrder(orderId, newIntent)
			e.publishAcceptedFromPost(newIntent, orderId, time.Now())
		}
		result, err := e.relayClient.MergeTokens(intent.MarketID, strconv.FormatFloat(size, 'f', constants.CollateralTokenDecimals, 64), false)
		if err != nil {
			log.Error().AnErr("err", err).Msg("merge token failed")
			for i, o := range orderTmps {
				in := newIntents[i]
				e.publish(core.ExecutionEvent{
					OrderID:       o,
					MarketID:      in.MarketID,
					TokenID:       in.TokenID,
					Price:         in.Price,
					Side:          in.Side,
					RequestedSize: in.Size,
					Status:        core.ExecutionStatusRejected,
					Reason:        fmt.Sprintf("merge token failed: %v", err),
					At:            time.Now(),
				})
			}
			continue
		}
		log.Info().Str("State", result.State).Str("Hash", result.Hash).Msg("submitMerges result")
		if result.State == "STATE_NEW" {
			for i, o := range orderTmps {
				in := newIntents[i]
				e.publish(core.ExecutionEvent{
					OrderID:       o,
					MarketID:      in.MarketID,
					TokenID:       in.TokenID,
					Price:         in.Price,
					Side:          in.Side,
					RequestedSize: in.Size,
					FilledSize:    in.Size,
					Status:        core.ExecutionStatusFilled,
					At:            time.Now(),
				})
			}
		} else {
			for i, o := range orderTmps {
				in := newIntents[i]
				e.publish(core.ExecutionEvent{
					OrderID:       o,
					MarketID:      in.MarketID,
					TokenID:       in.TokenID,
					Price:         in.Price,
					Side:          in.Side,
					RequestedSize: in.Size,
					FilledSize:    in.Size,
					Status:        core.ExecutionStatusRejected,
					Reason:        fmt.Sprintf("merge token failed: %s", result.State),
					At:            time.Now(),
				})
			}
		}
	}
}
