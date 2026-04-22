package indicators

import (
	"math"
	"sync"
)

type Tick struct {
	Price     float64
	Timestamp int64 // ms
}

type Bar struct {
	Second int64
	Price  float64
}

type ZScore struct {
	mu sync.Mutex

	series []Bar

	windowSize int // e.g. 60 seconds
	lastSec    int64
	lastPrice  float64
}

func NewZScore(windowSize int) *ZScore {
	return &ZScore{
		series:     make([]Bar, 0, windowSize),
		windowSize: windowSize,
	}
}

func (s *ZScore) OnTick(t Tick) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sec := t.Timestamp / 1000

	// first tick
	if s.lastSec == 0 {
		s.lastSec = sec
		s.lastPrice = t.Price
		return
	}

	// same second → update last price
	if sec == s.lastSec {
		s.lastPrice = t.Price
		return
	}

	// fill missing seconds
	for i := s.lastSec + 1; i < sec; i++ {
		s.pushBar(i, s.lastPrice)
	}

	// push last second
	s.pushBar(s.lastSec, s.lastPrice)

	// update state
	s.lastSec = sec
	s.lastPrice = t.Price
}

func (s *ZScore) pushBar(sec int64, price float64) {
	s.series = append(s.series, Bar{
		Second: sec,
		Price:  price,
	})

	if len(s.series) > s.windowSize {
		s.series = s.series[1:]
	}
}

func (s *ZScore) returns() []float64 {
	if len(s.series) < 2 {
		return nil
	}

	r := make([]float64, 0, len(s.series)-1)

	for i := 1; i < len(s.series); i++ {
		p1 := s.series[i-1].Price
		p2 := s.series[i].Price

		if p1 == 0 {
			continue
		}

		ret := math.Log(p2 / p1)
		r = append(r, ret)
	}

	return r
}

func std(data []float64) float64 {
	if len(data) == 0 {
		return 0
	}

	var sum float64
	for _, v := range data {
		sum += v
	}
	mean := sum / float64(len(data))

	var variance float64
	for _, v := range data {
		diff := v - mean
		variance += diff * diff
	}

	variance /= float64(len(data))
	return math.Sqrt(variance)
}

func (s *ZScore) WindowSize() int {
	return s.windowSize
}

func (s *ZScore) Sigma() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	r := s.returns()
	return std(r)
}

func (s *ZScore) ZScore(currentPrice, startPrice, remainingSeconds float64) float64 {
	if startPrice == 0 || remainingSeconds <= 0 {
		return 0
	}

	// log return
	logReturn := math.Log(currentPrice / startPrice)

	sigma := math.Max(s.Sigma(), 0.00001)
	// 时间缩放
	scaledSigma := sigma * math.Sqrt(remainingSeconds)

	if scaledSigma == 0 {
		return 0
	}

	return logReturn / scaledSigma
}

func (s *ZScore) IsReady() bool {
	count := 0
	s.mu.Lock()
	count = len(s.series)
	s.mu.Unlock()

	return count >= s.windowSize/2
}
