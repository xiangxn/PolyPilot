package buffer

import (
	"sync"
	"testing"
)

func TestRingBufferAddAndValues(t *testing.T) {
	r := NewRingBuffer(3)
	r.Add(1)
	r.Add(2)
	r.Add(3)
	got := r.Values()
	if len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Fatalf("got %v", got)
	}
	r.Add(4)
	got = r.Values()
	if len(got) != 3 || got[0] != 2 || got[2] != 4 {
		t.Fatalf("after overflow got %v", got)
	}
}

func TestRingBufferLast(t *testing.T) {
	r := NewRingBuffer(5)
	for i := 1; i <= 5; i++ {
		r.Add(float64(i))
	}
	got := r.Last(3)
	if len(got) != 3 || got[0] != 3 || got[2] != 5 {
		t.Fatalf("got %v", got)
	}
}

func TestRingBufferReset(t *testing.T) {
	r := NewRingBuffer(3)
	r.Add(1)
	r.Reset()
	if r.Len() != 0 {
		t.Fatalf("expected len=0 after reset")
	}
}

func TestRingBufferLatestEmpty(t *testing.T) {
	r := NewRingBuffer(3)
	if _, ok := r.Latest(); ok {
		t.Fatalf("empty should return ok=false")
	}
}

func TestRingBufferConcurrentRace(t *testing.T) {
	r := NewRingBuffer(100)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				r.Add(float64(i))
				_ = r.Last(10)
			}
		}()
	}
	wg.Wait()
}
