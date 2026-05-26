package strategy

import "sync"

type Cloneable[T any] interface {
	Clone() T
}

type SafeState[T any] struct {
	mu sync.RWMutex
	v  T
}

func NewSafeState[T any](v T) *SafeState[T] {
	s := &SafeState[T]{
		v: v,
	}

	return s
}

func (s *SafeState[T]) Read(fn func(v T)) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	fn(s.v)
}

func (s *SafeState[T]) Update(fn func(v *T)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	fn(&s.v)

}

func (s *SafeState[T]) Replace(v T) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.v = v
}

func Clone[T Cloneable[T]](s *SafeState[T]) T {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.v.Clone()
}
