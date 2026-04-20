package atomicx

import (
	"math"
	"sync/atomic"
)

type Float64 struct {
	v uint64
}

func (f *Float64) Store(val float64) {
	atomic.StoreUint64(&f.v, math.Float64bits(val))
}

func (f *Float64) Load() float64 {
	return math.Float64frombits(atomic.LoadUint64(&f.v))
}
