package strategy

import (
	"sync"

	"github.com/tidwall/gjson"
)

type MarketQueue struct {
	mu    sync.RWMutex
	m     map[string]*gjson.Result
	queue []string
	max   int
}

func NewMarketQueue(max int) *MarketQueue {
	if max <= 0 {
		panic("max must be > 0")
	}

	return &MarketQueue{
		m:     make(map[string]*gjson.Result, max),
		queue: make([]string, 0, max),
		max:   max,
	}
}

func (c *MarketQueue) Add(marketId string, info *gjson.Result) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 已存在 → 直接更新 value（不改顺序）
	if _, ok := c.m[marketId]; ok {
		c.m[marketId] = info
		return
	}

	// 满了 → 淘汰最早的
	if len(c.queue) >= c.max {
		oldest := c.queue[0]
		c.queue = c.queue[1:]
		delete(c.m, oldest)
	}

	// 新增
	c.queue = append(c.queue, marketId)
	c.m[marketId] = info
}

func (c *MarketQueue) Get(marketId string) (*gjson.Result, bool) {
	c.mu.RLock()
	info, ok := c.m[marketId]
	c.mu.RUnlock()
	return info, ok
}

func (c *MarketQueue) Delete(marketId string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.m[marketId]; !ok {
		return
	}

	delete(c.m, marketId)

	// 从 queue 删除（O(n)，但你 max 很小完全没问题）
	for i, v := range c.queue {
		if v == marketId {
			c.queue = append(c.queue[:i], c.queue[i+1:]...)
			break
		}
	}
}
