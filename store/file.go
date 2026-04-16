package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"polypilot/core"
	"sync"
)

type FileOrderStore struct {
	mu     sync.RWMutex
	path   string
	orders map[string]OrderRecord
}

type FileExecutionStore struct {
	mu   sync.RWMutex
	path string
}

type FileStateStore struct {
	mu      sync.RWMutex
	path    string
	latest  SnapshotRecord
	hasData bool
}

func NewFileOrderStore(path string) (*FileOrderStore, error) {
	if err := ensureParentDir(path); err != nil {
		return nil, err
	}
	s := &FileOrderStore{path: path, orders: make(map[string]OrderRecord)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func NewFileExecutionStore(path string) (*FileExecutionStore, error) {
	if err := ensureParentDir(path); err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			return nil, err
		}
	}
	return &FileExecutionStore{path: path}, nil
}

func NewFileStateStore(path string) (*FileStateStore, error) {
	if err := ensureParentDir(path); err != nil {
		return nil, err
	}
	s := &FileStateStore{path: path}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *FileOrderStore) UpsertOrder(rec OrderRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orders[rec.OrderID] = rec
	return s.persistLocked()
}

func (s *FileOrderStore) GetOrder(orderID string) (OrderRecord, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.orders[orderID]
	return rec, ok, nil
}

func (s *FileOrderStore) ListOpenOrders() ([]OrderRecord, error) {
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

func (s *FileOrderStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	var list []OrderRecord
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	for _, rec := range list {
		if rec.OrderID == "" {
			continue
		}
		s.orders[rec.OrderID] = rec
	}
	return nil
}

func (s *FileOrderStore) persistLocked() error {
	list := make([]OrderRecord, 0, len(s.orders))
	for _, rec := range s.orders {
		list = append(list, rec)
	}
	data, err := json.Marshal(list)
	if err != nil {
		return err
	}
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.path)
}

func (s *FileExecutionStore) AppendExecution(ev core.ExecutionEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	_, err = f.Write(line)
	return err
}

func (s *FileExecutionStore) ListExecutionsSince(unixNano int64) ([]core.ExecutionEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	out := make([]core.ExecutionEvent, 0)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev core.ExecutionEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, err
		}
		if ev.At.UnixNano() < unixNano {
			continue
		}
		out = append(out, ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *FileStateStore) SaveSnapshot(snapshot SnapshotRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest = snapshot
	s.hasData = true
	return s.persistLocked()
}

func (s *FileStateStore) LoadLatestSnapshot() (SnapshotRecord, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latest, s.hasData, nil
}

func (s *FileStateStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	var rec SnapshotRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return err
	}
	s.latest = rec
	s.hasData = true
	return nil
}

func (s *FileStateStore) persistLocked() error {
	data, err := json.Marshal(s.latest)
	if err != nil {
		return err
	}
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.path)
}

func ensureParentDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}
