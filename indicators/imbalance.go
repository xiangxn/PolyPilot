package indicators

import (
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

// CalcImBalance 计算盘口不平衡度：
// imbalance = (sumBidSize - sumAskSize) / (sumBidSize + sumAskSize)
// 其中 Bids/Asks 的 best level 都按 len-1 处理，向前回溯取 topN 档。
func CalcImBalance(orderBook *sdk.OrderBook, topN int) float64 {
	if orderBook == nil || topN <= 0 {
		return 0
	}

	bids := orderBook.Bids
	asks := orderBook.Asks

	// ===== 极端盘口处理 =====
	if len(bids) == 0 && len(asks) == 0 {
		return 0
	}
	if len(bids) == 0 {
		return -1
	}
	if len(asks) == 0 {
		return 1
	}

	bidDepth := min(topN, len(bids))
	askDepth := min(topN, len(asks))

	var bidSizeSum float64
	for i := range bidDepth {
		idx := len(bids) - 1 - i
		bidSizeSum += bids[idx].Size
	}

	var askSizeSum float64
	for i := range askDepth {
		idx := len(asks) - 1 - i
		askSizeSum += asks[idx].Size
	}

	total := bidSizeSum + askSizeSum
	if total <= 0 {
		return 0
	}

	return (bidSizeSum - askSizeSum) / total
}
