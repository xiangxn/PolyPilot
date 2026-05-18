package state

import (
	"context"
	"errors"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/xiangxn/go-polymarket-sdk/orders"
)

type fakeExchangeClient struct {
	openOrders []orders.OpenOrder
	positions  *gjson.Result
	err        error
}

func (f *fakeExchangeClient) GetOpenOrders() ([]orders.OpenOrder, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.openOrders, nil
}

func (f *fakeExchangeClient) GetPositions() (*gjson.Result, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.positions, nil
}

func (f *fakeExchangeClient) Redeem(ctx context.Context, onSuccess func([]string)) {}

func TestReconcile_LocalHasRemoteMissing_ReleasesLocal(t *testing.T) {
	fake := &fakeExchangeClient{} // remote returns nothing
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	s.Restore(Snapshot{Balance: Balance{Available: 100}})
	if err := s.AttachOrder("", "stale1", "m1", "tk1", orders.BUY, 0.5, 10); err != nil {
		t.Fatal(err)
	}
	rep := s.ReconcileWithExchange(context.Background())
	if rep.OrdersRemoved != 1 {
		t.Fatalf("expected 1 removed, got %+v", rep)
	}
	snap := s.Snapshot()
	if _, ok := snap.Orders["stale1"]; ok {
		t.Fatal("stale order should be removed")
	}
	if snap.Balance.Reserved != 0 {
		t.Fatalf("reserved should be released, got %v", snap.Balance.Reserved)
	}
}

func TestReconcile_LocalMissingRemoteHas_AttachesExternal(t *testing.T) {
	fake := &fakeExchangeClient{
		openOrders: []orders.OpenOrder{
			{Id: "ext1", Market: "m1", AssetId: "tk1", Side: "BUY", Price: 0.5, OriginalSize: 8, SizeMatched: 0},
		},
	}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	s.Restore(Snapshot{Balance: Balance{Available: 100}})
	rep := s.ReconcileWithExchange(context.Background())
	if rep.OrdersAdded != 1 {
		t.Fatalf("expected 1 added, got %+v", rep)
	}
	r := s.Snapshot().Orders["ext1"]
	if !r.ExternalOrigin {
		t.Fatal("expected ExternalOrigin=true")
	}
}

func TestReconcile_RemoteHasUpdatedPrice_UpdatesLocal(t *testing.T) {
	fake := &fakeExchangeClient{
		openOrders: []orders.OpenOrder{
			{Id: "o1", Market: "m1", AssetId: "tk1", Side: "BUY", Price: 0.6, OriginalSize: 10, SizeMatched: 3},
		},
	}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	s.Restore(Snapshot{Balance: Balance{Available: 100}})
	if err := s.AttachOrder("", "o1", "m1", "tk1", orders.BUY, 0.5, 10); err != nil {
		t.Fatal(err)
	}
	rep := s.ReconcileWithExchange(context.Background())
	if rep.OrdersUpdated != 1 {
		t.Fatalf("expected 1 updated, got %+v", rep)
	}
	r := s.Snapshot().Orders["o1"]
	if r.Price != 0.6 {
		t.Fatalf("price should be 0.6 got %v", r.Price)
	}
	if r.RemainingSize != 7 {
		t.Fatalf("remaining should be 7 got %v", r.RemainingSize)
	}
}

func TestReconcile_FailureReturnsErr(t *testing.T) {
	fake := &fakeExchangeClient{err: errors.New("boom")}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	rep := s.ReconcileWithExchange(context.Background())
	if rep.Err == nil {
		t.Fatal("expected error report")
	}
}

func TestTriggerReconcile_Deduplicates(t *testing.T) {
	fake := &fakeExchangeClient{}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	for i := 0; i < 5; i++ {
		s.TriggerReconcile()
	}
	// drain the channel; should have at most 1 pending
	count := 0
	for {
		select {
		case <-s.reconcileTrigger:
			count++
		default:
			if count > 1 {
				t.Fatalf("trigger channel should dedupe, got %d", count)
			}
			return
		}
	}
}

func TestReconcile_PositionAdded(t *testing.T) {
	jsonStr := `[{"asset":"tk-new","size":7}]`
	pos := gjson.Parse(jsonStr)
	fake := &fakeExchangeClient{positions: &pos}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	s.Restore(Snapshot{Balance: Balance{Available: 100}})
	rep := s.ReconcileWithExchange(context.Background())
	if rep.PositionsAdded != 1 {
		t.Fatalf("expected 1 position added, got %+v", rep)
	}
	tp := s.Snapshot().Position.Tokens["tk-new"]
	if tp.Available != 7 || tp.AvgCostKnown {
		t.Fatalf("got %+v, expected Available=7 AvgCostKnown=false", tp)
	}
}

func TestReconcile_PositionRemoved(t *testing.T) {
	fake := &fakeExchangeClient{} // remote: no positions
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	s.Restore(Snapshot{
		Balance:  Balance{Available: 100},
		Position: Position{Tokens: map[string]TokenPosition{"tk-stale": {Available: 5}}},
	})
	rep := s.ReconcileWithExchange(context.Background())
	if rep.PositionsRemoved != 1 {
		t.Fatalf("expected 1 removed, got %+v", rep)
	}
	if _, ok := s.Snapshot().Position.Tokens["tk-stale"]; ok {
		t.Fatal("stale position should be removed")
	}
}
