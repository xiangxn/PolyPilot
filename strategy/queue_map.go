package strategy

import "sync"

type QueueMap[T any] struct {
	mu    sync.RWMutex
	m     map[string]T
	queue []string
	max   int
}

func NewQueueMap[T any](max int) *QueueMap[T] {
	if max <= 0 {
		panic("max must be > 0")
	}

	return &QueueMap[T]{
		m:     make(map[string]T, max),
		queue: make([]string, 0, max),
		max:   max,
	}
}

// Add
func (c *QueueMap[T]) Add(key string, value T) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 已存在 → 更新 value
	if _, ok := c.m[key]; ok {
		c.m[key] = value
		return
	}

	// 超限 → 删除最旧
	if len(c.queue) >= c.max {
		oldest := c.queue[0]
		c.queue = c.queue[1:]
		delete(c.m, oldest)
	}

	// 新增
	c.queue = append(c.queue, key)
	c.m[key] = value
}

// Get
func (c *QueueMap[T]) Get(key string) (T, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	v, ok := c.m[key]
	return v, ok
}

// Delete
func (c *QueueMap[T]) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.m[key]; !ok {
		return
	}

	delete(c.m, key)

	for i, v := range c.queue {
		if v == key {
			c.queue = append(c.queue[:i], c.queue[i+1:]...)
			break
		}
	}
}

// Len
func (c *QueueMap[T]) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.queue)
}

// Has
func (c *QueueMap[T]) Has(key string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	_, ok := c.m[key]
	return ok
}

// Keys
func (c *QueueMap[T]) Keys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	res := make([]string, len(c.queue))
	copy(res, c.queue)

	return res
}

// Clear
func (c *QueueMap[T]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.m = make(map[string]T, c.max)
	c.queue = c.queue[:0]
}
