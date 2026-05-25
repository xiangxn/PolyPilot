package strategy

import (
	"sync"

	"github.com/xiangxn/polypilot/market"
)

type MarketQueue struct {
	mu    sync.RWMutex
	m     map[string]market.SlugMarket
	queue []string
	max   int
}

func NewMarketQueue(max int) *MarketQueue {
	if max <= 0 {
		max = 3
	}
	return &MarketQueue{
		m:     make(map[string]market.SlugMarket, max),
		queue: make([]string, 0, max),
		max:   max,
	}
}

func (c *MarketQueue) Add(marketID string, info market.SlugMarket) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 已存在 → 直接更新 value（不改顺序）
	if _, ok := c.m[marketID]; ok {
		c.m[marketID] = info
		return
	}

	// 满了 → 淘汰最早的
	if len(c.queue) >= c.max {
		oldest := c.queue[0]
		c.queue = c.queue[1:]
		delete(c.m, oldest)
	}

	// 新增
	c.queue = append(c.queue, marketID)
	c.m[marketID] = info
}

func (c *MarketQueue) Get(marketID string) (market.SlugMarket, bool) {
	c.mu.RLock()
	info, ok := c.m[marketID]
	c.mu.RUnlock()
	return info, ok
}

func (c *MarketQueue) Delete(marketID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.m[marketID]; !ok {
		return
	}

	delete(c.m, marketID)

	// 从 queue 删除（O(n)，但 max 很小完全没问题）
	for i, v := range c.queue {
		if v == marketID {
			c.queue = append(c.queue[:i], c.queue[i+1:]...)
			break
		}
	}
}
