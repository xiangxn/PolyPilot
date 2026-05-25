package probability

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/runtime"

	"github.com/tidwall/gjson"
	"github.com/xiangxn/go-polymarket-sdk/orders"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

// TestRace_OnUpdateVsCurrentObservation 并发触发 OnUpdate(EventOrderBook/EventSignal)
// 与 CurrentObservation，必须在 -race 下不报告 data race。
//
// 复现的并发场景：
//   - runtime 事件循环 goroutine 调用 OnUpdate 写 token.items / market.*
//   - runtime ticker goroutine 调用 CurrentObservation 读 token.items / market.*
func TestRace_OnUpdateVsCurrentObservation(t *testing.T) {
	e := &Engine{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e.Init(ctx)

	// 手动初始化 market 状态，绕过 resetForNewMarket 中的 RPC 调用；
	// setup 阶段在 main goroutine 中完成，写者/读者 spawn 前不存在并发访问。
	raw := gjson.Parse(`{"conditionId":"cond-1"}`)
	e.market.raw = &raw
	e.market.endTime = time.Now().Add(time.Hour).UnixMilli()
	e.market.openPrice = 100.0
	e.market.tokenIDs = []string{"tk1", "tk2"}
	e.token.items = map[string]runtime.Token{
		"tk1": {Id: "tk1", AskPrice: 0.5, BidPrice: 0.49},
		"tk2": {Id: "tk2", AskPrice: 0.5, BidPrice: 0.49},
	}

	var wg sync.WaitGroup
	done := make(chan struct{})

	spawn := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					fn()
				}
			}
		}()
	}

	// 写者 1: EventOrderBook → 写 token.items[tk1]
	spawn(func() {
		ob := &sdk.OrderBook{
			AssetId:   "tk1",
			Market:    "cond-1",
			Timestamp: time.Now().UnixMilli(),
			Asks:      []orders.Book{{Price: 0.5, Size: 100}},
			Bids:      []orders.Book{{Price: 0.49, Size: 100}},
		}
		e.OnUpdate(core.Event{Type: core.EventOrderBook, Data: ob})
	})

	// 写者 2: EventOrderBook → 写 token.items[tk2]
	spawn(func() {
		ob := &sdk.OrderBook{
			AssetId:   "tk2",
			Market:    "cond-1",
			Timestamp: time.Now().UnixMilli(),
			Asks:      []orders.Book{{Price: 0.51, Size: 80}},
			Bids:      []orders.Book{{Price: 0.5, Size: 80}},
		}
		e.OnUpdate(core.Event{Type: core.EventOrderBook, Data: ob})
	})

	// 写者 3: EventSignal → 写 signal.latestPrice / latestZ (atomic) + 读 market.openPrice
	spawn(func() {
		e.OnUpdate(core.Event{
			Type: core.EventExternalPrice,
			Data: sdk.ExternalPrice{Price: 100.5, Timestamp: time.Now().UnixMilli()},
		})
	})

	// 读者 1/2: CurrentObservation 读 market.* + token.items
	spawn(func() {
		_, _ = e.CurrentObservation()
	})
	spawn(func() {
		_, _ = e.CurrentObservation()
	})

	time.Sleep(100 * time.Millisecond)
	close(done)
	wg.Wait()
}
