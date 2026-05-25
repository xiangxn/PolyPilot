package atomicx

import (
	"sync"
	"testing"
)

func TestFloat64StoreLoad(t *testing.T) {
	var f Float64
	f.Store(3.14)
	if got := f.Load(); got != 3.14 {
		t.Fatalf("got %v", got)
	}
}

func TestFloat64ConcurrentRace(t *testing.T) {
	var f Float64
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(v float64) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				f.Store(v)
				_ = f.Load()
			}
		}(float64(i))
	}
	wg.Wait()
}
