package store

import (
	"polypilot/core"
	"sync"
)

type MemoryOrderStore struct {
	mu     sync.RWMutex
	orders map[string]OrderRecord
}

type MemoryExecutionStore struct {
	mu         sync.RWMutex
	executions []core.ExecutionEvent
}

type MemoryStateStore struct {
	mu      sync.RWMutex
	latest  SnapshotRecord
	hasData bool
}

func NewMemoryOrderStore() *MemoryOrderStore {
	return &MemoryOrderStore{orders: make(map[string]OrderRecord)}
}

func NewMemoryExecutionStore() *MemoryExecutionStore {
	return &MemoryExecutionStore{}
}

func NewMemoryStateStore() *MemoryStateStore {
	return &MemoryStateStore{}
}

func (s *MemoryOrderStore) UpsertOrder(rec OrderRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orders[rec.OrderID] = rec
	return nil
}

func (s *MemoryOrderStore) GetOrder(orderID string) (OrderRecord, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.orders[orderID]
	return rec, ok, nil
}

func (s *MemoryOrderStore) ListOpenOrders() ([]OrderRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]OrderRecord, 0, len(s.orders))
	for _, rec := range s.orders {
		switch rec.Status {
		case core.ExecutionStatusFilled, core.ExecutionStatusCancelled, core.ExecutionStatusRejected:
			continue
		default:
			out = append(out, rec)
		}
	}
	return out, nil
}

func (s *MemoryExecutionStore) AppendExecution(ev core.ExecutionEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executions = append(s.executions, ev)
	return nil
}

func (s *MemoryExecutionStore) ListExecutionsSince(unixNano int64) ([]core.ExecutionEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]core.ExecutionEvent, 0, len(s.executions))
	for _, ev := range s.executions {
		if ev.At.UnixNano() < unixNano {
			continue
		}
		out = append(out, ev)
	}
	return out, nil
}

func (s *MemoryStateStore) SaveSnapshot(snapshot SnapshotRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest = snapshot
	s.hasData = true
	return nil
}

func (s *MemoryStateStore) LoadLatestSnapshot() (SnapshotRecord, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latest, s.hasData, nil
}
