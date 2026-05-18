package state

import (
	"context"
	"time"

	"github.com/xiangxn/polypilot/core"
)

// RegisterMarketExpiry tells the position-expiring loop that a market's
// reserve windows ends at endTimeMs (UnixMilli). Callers (typically the
// Feed or Probability engine) should register markets whose positions
// might need protective close-out before market end.
func (s *State) RegisterMarketExpiry(marketID string, endTimeMs int64, tokenIDs []string) {
	s.expiryMu.Lock()
	defer s.expiryMu.Unlock()
	if s.expiryMarkets == nil {
		s.expiryMarkets = make(map[string]*expiringMarket)
	}
	s.expiryMarkets[marketID] = &expiringMarket{
		endTime:  endTimeMs,
		tokenIDs: append([]string(nil), tokenIDs...),
	}
}

// StartPositionExpiringLoop polls every `tick` interval and publishes a
// PositionExpiringEvent for each registered market whose end-time falls
// within `warnBefore` of now. Each market fires at most once.
func (s *State) StartPositionExpiringLoop(ctx context.Context, bus *core.EventBus, tick, warnBefore time.Duration) {
	if tick <= 0 {
		tick = time.Second
	}
	if warnBefore <= 0 {
		warnBefore = 30 * time.Second
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.checkExpiry(bus, warnBefore)
		}
	}
}

func (s *State) checkExpiry(bus *core.EventBus, warnBefore time.Duration) {
	nowMs := time.Now().UnixMilli()
	cutoff := nowMs + warnBefore.Milliseconds()

	s.expiryMu.Lock()
	var toFire []core.PositionExpiringEvent
	for mid, m := range s.expiryMarkets {
		if m.fired || m.endTime > cutoff {
			continue
		}
		avail := s.snapshotTokenAvailable(m.tokenIDs)
		toFire = append(toFire, core.PositionExpiringEvent{
			MarketID:  mid,
			EndTime:   m.endTime,
			TokenIDs:  append([]string(nil), m.tokenIDs...),
			Available: avail,
		})
		m.fired = true
	}
	s.expiryMu.Unlock()

	for _, ev := range toFire {
		bus.Publish(core.Event{Type: core.EventPositionExpiring, Data: ev})
	}
}

func (s *State) snapshotTokenAvailable(tokenIDs []string) map[string]float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]float64, len(tokenIDs))
	for _, tk := range tokenIDs {
		out[tk] = s.position.Tokens[tk].Available
	}
	return out
}
