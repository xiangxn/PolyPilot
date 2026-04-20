package indicators

import "sync"

// RingBuffer 是一个固定大小的线程安全环形缓冲区（float64版）
// 适用于：时间序列、行情数据、指标窗口等
type RingBuffer struct {
	mu    sync.RWMutex
	buf   []float64
	size  int
	idx   int // 下一个写入位置
	count int // 当前有效数据量（<= size）
}

// NewRingBuffer 创建一个固定容量的环形缓冲区
func NewRingBuffer(size int) *RingBuffer {
	if size <= 0 {
		panic("ring buffer size must be > 0")
	}

	return &RingBuffer{
		buf:  make([]float64, size),
		size: size,
	}
}

// Add 写入一个值（O(1)）
// 会覆盖最旧的数据
func (r *RingBuffer) Add(v float64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.buf[r.idx] = v
	r.idx = (r.idx + 1) % r.size

	if r.count < r.size {
		r.count++
	}
}

// Values 返回当前所有数据（按时间顺序：旧 → 新）
// 返回的是拷贝，外部安全使用
func (r *RingBuffer) Values() []float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]float64, r.count)

	for i := 0; i < r.count; i++ {
		idx := (r.idx - r.count + i + r.size) % r.size
		result[i] = r.buf[idx]
	}

	return result
}

// Last 返回最近 n 个数据（按时间顺序：旧 → 新）
func (r *RingBuffer) Last(n int) []float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if n <= 0 || r.count == 0 {
		return nil
	}

	if n > r.count {
		n = r.count
	}

	result := make([]float64, n)

	for i := 0; i < n; i++ {
		idx := (r.idx - n + i + r.size) % r.size
		result[i] = r.buf[idx]
	}

	return result
}

// Latest 获取最新值（O(1)）
func (r *RingBuffer) Latest() (float64, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.count == 0 {
		return 0, false
	}

	idx := (r.idx - 1 + r.size) % r.size
	return r.buf[idx], true
}

// Len 返回当前有效数据数量
func (r *RingBuffer) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.count
}

// Cap 返回容量
func (r *RingBuffer) Cap() int {
	return r.size
}

// IsFull 是否已满
func (r *RingBuffer) IsFull() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.count == r.size
}

// Reset 清空缓冲区（不释放内存）
func (r *RingBuffer) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.idx = 0
	r.count = 0
}
