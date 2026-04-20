package state

import (
	"context"
	"time"
)

const (
	defaultBalanceSyncInterval = 5 * time.Second
	defaultBalanceSyncEpsilon  = 1e-6
)

type BalanceReader interface {
	ReadOnchainBalance(ctx context.Context) (float64, error)
}

type BalanceSyncEvent struct {
	OnchainTotal float64
	Drift        float64
	Changed      bool
	Err          error
}

type BalanceSyncConfig struct {
	Enabled    bool
	Reader     BalanceReader
	Interval   time.Duration
	Epsilon    float64
	MinBalance float64
	OnEvent    func(BalanceSyncEvent)
}

func normalizeBalanceSyncConfig(cfg BalanceSyncConfig) BalanceSyncConfig {
	if !cfg.Enabled {
		return cfg
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultBalanceSyncInterval
	}
	if cfg.Epsilon <= 0 {
		cfg.Epsilon = defaultBalanceSyncEpsilon
	}
	return cfg
}

func (s *State) StartBalanceSync(ctx context.Context) {
	if s == nil {
		return
	}
	if !s.balanceSync.Enabled || s.balanceSync.Reader == nil {
		return
	}

	s.balanceSyncRun.Do(func() {
		go func() {
			ticker := time.NewTicker(s.balanceSync.Interval)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					s.SyncOnchainBalanceOnce(ctx)
				}
			}
		}()
	})
}

func (s *State) SyncOnchainBalanceOnce(ctx context.Context) BalanceSyncEvent {
	if s == nil || s.balanceSync.Reader == nil {
		return BalanceSyncEvent{}
	}

	onchainTotal, err := s.balanceSync.Reader.ReadOnchainBalance(ctx)
	if err != nil {
		evt := BalanceSyncEvent{Err: err}
		if s.balanceSync.OnEvent != nil {
			s.balanceSync.OnEvent(evt)
		}
		return evt
	}

	changed, drift := s.ReconcileOnchainBalance(onchainTotal, s.balanceSync.Epsilon)
	evt := BalanceSyncEvent{
		OnchainTotal: onchainTotal,
		Drift:        drift,
		Changed:      changed,
	}
	if s.balanceSync.OnEvent != nil && (evt.Changed || evt.Err != nil) {
		s.balanceSync.OnEvent(evt)
	}

	return evt
}
