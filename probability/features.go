package probability

import (
	"github.com/xiangxn/polypilot/indicators"
	"github.com/xiangxn/polypilot/runtime"
)

// fillFeaturesLocked 读取 e.market 与 signal 相关字段填充 Observation 的 Features。
// 调用方必须已经持有 e.mu（读锁或写锁），否则与 OnUpdate 写路径存在数据竞争。
func (e *Engine) fillFeaturesLocked(obs *runtime.Observation) {
	obs.Features = make(map[string]any)
	latestZ := e.signal.latestZ.Load()
	obs.Probability = Phi(latestZ)
	obs.Features["latestZ"] = latestZ
	if e.signal.zWindows != nil {
		obs.Features["zWindows"] = e.signal.zWindows.Last(10)
	}
	openPrice := e.market.openPrice
	latestPrice := e.signal.latestPrice.Load()
	obs.Features["openPrice"] = openPrice
	obs.Features["latestPrice"] = latestPrice
	obs.Features["endTime"] = e.market.endTime
	obs.Features["diffPrice"] = latestPrice - openPrice

	if len(e.market.tokenIDs) > 0 {
		if ob := e.GetOrderBook(e.market.tokenIDs[0]); ob == nil {
			obs.Features["imBalance"] = float64(0)
		} else {
			obs.Features["imBalance"] = indicators.CalcImBalance(ob, 3)
		}
	}
}
