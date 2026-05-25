package core

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBusFanOut(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()
	ch1 := bus.Subscribe()
	ch2 := bus.Subscribe()

	bus.Publish(Event{Type: EventMarket, Data: "x"})
	for _, ch := range []chan Event{ch1, ch2} {
		select {
		case ev := <-ch:
			if ev.Data != "x" {
				t.Fatalf("got %v want x", ev.Data)
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout on subscriber receive")
		}
	}
}

func TestBusDroppedCounter(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()
	_ = bus.Subscribe() // never read
	for i := 0; i < 2000; i++ {
		bus.Publish(Event{Type: EventMarket})
	}
	if bus.Stats().Dropped == 0 {
		t.Fatalf("expected dropped > 0 when subscriber stalls")
	}
}

func TestBusDropThresholdWarning(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()
	bus.DropThreshold = 100

	var warnings atomic.Int32
	bus.OnDropThreshold = func(count uint64) { warnings.Add(1) }

	_ = bus.Subscribe() // stall (subscriber buffer 1024)
	// publish well beyond buffer + threshold to guarantee multiple boundary crossings
	for i := 0; i < 3000; i++ {
		bus.Publish(Event{Type: EventMarket})
	}
	// allow async warning goroutines to fire
	time.Sleep(100 * time.Millisecond)
	if warnings.Load() == 0 {
		t.Fatalf("expected DropThreshold warning, got dropped=%d", bus.Stats().Dropped)
	}
}

func TestBusConcurrentPublishSubscribe(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()
	var wg sync.WaitGroup
	done := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, cancel := bus.SubscribeWithCancel()
			defer cancel()
			for {
				select {
				case <-done:
					return
				case <-ch:
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			bus.Publish(Event{Type: EventMarket})
		}
	}()

	time.Sleep(50 * time.Millisecond)
	close(done)
	wg.Wait()
}
