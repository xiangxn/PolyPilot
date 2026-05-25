package strategy

import (
	"context"
	"testing"

	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/runtime"
	"github.com/xiangxn/polypilot/state"

	"github.com/spf13/viper"
)

func TestOnUpdate_OrderBook_SafeWithMissingFeatures(t *testing.T) {
	s := &Strategy{}
	s.Init(core.NewEventBus(), context.Background(), viper.New())
	obs := runtime.Observation{
		MarketID: "m1",
		Features: map[string]any{}, // no openPrice
	}
	if got := s.OnUpdate(core.Event{Type: core.EventOrderBook}, obs, state.Snapshot{}); len(got) != 0 {
		t.Fatalf("expected no intents, got %d", len(got))
	}
}

func TestInit_NilViperUsesDefaults(t *testing.T) {
	s := &Strategy{}
	s.Init(core.NewEventBus(), context.Background(), nil)
	if s.config.InPrice <= 0 || s.markets == nil {
		t.Fatalf("expected defaults, got %+v", s.config)
	}
}
