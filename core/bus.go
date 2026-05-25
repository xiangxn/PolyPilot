package core

import (
	"sync"
	"sync/atomic"
)

type BusStats struct {
	Subscribers int
	Published   uint64
	Dropped     uint64
}

type EventBus struct {
	mu               sync.RWMutex
	subs             map[uint64]chan Event
	nextID           uint64
	closed           bool
	published        atomic.Uint64
	dropped          atomic.Uint64
	lastWarnedAtDrop atomic.Uint64

	// DropThreshold: if > 0, OnDropThreshold is invoked every time the total
	// dropped count crosses another DropThreshold boundary. Zero disables.
	DropThreshold uint64
	// OnDropThreshold is called with the cumulative dropped count when a
	// threshold boundary is crossed. Invoked in a goroutine; must not block
	// indefinitely.
	OnDropThreshold func(droppedTotal uint64)
}

func NewEventBus() *EventBus {
	return &EventBus{subs: make(map[uint64]chan Event)}
}

func (b *EventBus) Subscribe() chan Event {
	ch, _ := b.SubscribeWithCancel()
	return ch
}

func (b *EventBus) SubscribeWithCancel() (chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		ch := make(chan Event)
		close(ch)
		return ch, func() {}
	}

	id := b.nextID
	b.nextID++

	ch := make(chan Event, 1024)
	b.subs[id] = ch

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, id)
			b.mu.Unlock()
		})
	}

	return ch, cancel
}

func (b *EventBus) Publish(e Event) {
	b.published.Add(1)

	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return
	}

	for _, s := range b.subs {
		select {
		case s <- e:
		default:
			b.dropped.Add(1)
			if b.DropThreshold > 0 && b.OnDropThreshold != nil {
				total := b.dropped.Load()
				last := b.lastWarnedAtDrop.Load()
				if total/b.DropThreshold > last/b.DropThreshold {
					if b.lastWarnedAtDrop.CompareAndSwap(last, total) {
						go b.OnDropThreshold(total)
					}
				}
			}
		}
	}
}

func (b *EventBus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}
	b.closed = true

	for id, ch := range b.subs {
		close(ch)
		delete(b.subs, id)
	}
}

func (b *EventBus) Stats() BusStats {
	b.mu.RLock()
	subscribers := len(b.subs)
	b.mu.RUnlock()

	return BusStats{
		Subscribers: subscribers,
		Published:   b.published.Load(),
		Dropped:     b.dropped.Load(),
	}
}
