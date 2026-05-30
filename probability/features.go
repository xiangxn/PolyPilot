package probability

import (
	"github.com/xiangxn/polypilot/runtime"
)

// fillFeaturesLocked 读取 e.market 与 signal 相关字段填充 Observation 的 Features。
// 调用方必须已经持有 e.mu（读锁或写锁），否则与 OnUpdate 写路径存在数据竞争。
func (e *Engine) fillFeaturesLocked(obs *runtime.Observation, market *marketState, signal *signalState) {
	obs.Features = make(map[string]any)
	latestZ := signal.latestZ.Load()
	obs.Features["latestZ"] = latestZ
	obs.Features["zWindows"] = signal.zWindows.Last(10)
	openPrice := market.openPrice
	latestPrice := signal.latestPrice.Load()
	obs.Features["openPrice"] = openPrice
	obs.Features["latestPrice"] = latestPrice
	obs.Features["endTime"] = market.endTime
	obs.Features["diffPrice"] = latestPrice - openPrice
}
