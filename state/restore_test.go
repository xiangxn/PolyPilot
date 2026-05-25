package state

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/xiangxn/go-polymarket-sdk/orders"
)

// restoreClient implements ExchangeStateClient with configurable behavior.
type restoreClient struct {
	openOrders    []orders.OpenOrder
	openOrdersErr error
	positions     *gjson.Result
	positionsErr  error
	redeemCalls   atomic.Int32
	emitTokens    []string // when set, Redeem invokes onRedeemSuccess with these IDs
}

func (r *restoreClient) GetOpenOrders() ([]orders.OpenOrder, error) {
	return r.openOrders, r.openOrdersErr
}

func (r *restoreClient) GetPositions() (*gjson.Result, error) {
	return r.positions, r.positionsErr
}

func (r *restoreClient) Redeem(_ context.Context, onRedeemSuccess func(tokenIDs []string)) {
	r.redeemCalls.Add(1)
	if onRedeemSuccess != nil && len(r.emitTokens) > 0 {
		onRedeemSuccess(r.emitTokens)
	}
}

func TestRestoreFromExchange_NilState(t *testing.T) {
	var s *State
	if _, err := s.RestoreFromExchange(context.Background()); err == nil {
		t.Fatal("expected error for nil state")
	}
}

func TestRestoreFromExchange_OpenOrdersError(t *testing.T) {
	client := &restoreClient{openOrdersErr: errors.New("boom")}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, client)
	_, err := s.RestoreFromExchange(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if client.redeemCalls.Load() != 1 {
		t.Fatalf("expected Redeem to be called once on err, got %d", client.redeemCalls.Load())
	}
}

func TestRestoreFromExchange_PositionsError(t *testing.T) {
	client := &restoreClient{positionsErr: errors.New("positions boom")}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, client)
	_, err := s.RestoreFromExchange(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if client.redeemCalls.Load() != 1 {
		t.Fatalf("expected Redeem to be called on positions err, got %d", client.redeemCalls.Load())
	}
}

func TestRestoreFromExchange_HappyPath(t *testing.T) {
	pos := gjson.Parse(`[{"asset":"tk-a","size":3},{"asset":"tk-b","size":5}]`)
	client := &restoreClient{
		positions: &pos,
		openOrders: []orders.OpenOrder{
			{Id: "o1", Market: "m1", AssetId: "tkA", Side: "BUY", Price: 0.5, OriginalSize: 10, SizeMatched: 0},
		},
	}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, client)
	s.Restore(Snapshot{Balance: Balance{Available: 200, Reserved: 0, MinBalance: 10}})

	orderIDs, err := s.RestoreFromExchange(context.Background())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(orderIDs) != 1 || orderIDs[0] != "o1" {
		t.Fatalf("expected [o1], got %+v", orderIDs)
	}
	snap := s.Snapshot()
	if snap.Position.Tokens["tk-a"].Available != 3 {
		t.Fatalf("tk-a: %+v", snap.Position.Tokens["tk-a"])
	}
	if snap.Position.Tokens["tk-b"].Available != 5 {
		t.Fatalf("tk-b: %+v", snap.Position.Tokens["tk-b"])
	}
	if _, ok := snap.Orders["o1"]; !ok {
		t.Fatal("expected o1 in orders")
	}
	if snap.Balance.MinBalance != 10 {
		t.Fatalf("MinBalance must be preserved across Restore, got %v", snap.Balance.MinBalance)
	}
	if client.redeemCalls.Load() != 1 {
		t.Fatalf("expected Redeem to be called, got %d", client.redeemCalls.Load())
	}
}

func TestRestoreFromExchange_PositionsNilSkipsBuild(t *testing.T) {
	// positions == nil should be tolerated (no panic, no tokens added)
	client := &restoreClient{positions: nil}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, client)
	s.Restore(Snapshot{Balance: Balance{Available: 100}})
	_, err := s.RestoreFromExchange(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(s.Snapshot().Position.Tokens) != 0 {
		t.Fatalf("expected empty positions, got %+v", s.Snapshot().Position.Tokens)
	}
}

func TestRestoreFromExchange_PositionsAlternativeKeys(t *testing.T) {
	pos := gjson.Parse(`[
		{"assetId":"tk-1","size":1},
		{"asset_id":"tk-2","size":2},
		{"tokenId":"tk-3","size":3}
	]`)
	client := &restoreClient{positions: &pos}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, client)
	s.Restore(Snapshot{Balance: Balance{Available: 100}})
	_, err := s.RestoreFromExchange(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	snap := s.Snapshot()
	if snap.Position.Tokens["tk-1"].Available != 1 {
		t.Fatalf("tk-1: %+v", snap.Position.Tokens["tk-1"])
	}
	if snap.Position.Tokens["tk-2"].Available != 2 {
		t.Fatalf("tk-2: %+v", snap.Position.Tokens["tk-2"])
	}
	if snap.Position.Tokens["tk-3"].Available != 3 {
		t.Fatalf("tk-3: %+v", snap.Position.Tokens["tk-3"])
	}
}

func TestRestoreFromExchange_SkipsZeroSizeAndEmptyToken(t *testing.T) {
	pos := gjson.Parse(`[
		{"asset":"","size":5},
		{"asset":"tk-ok","size":0},
		{"asset":"tk-ok2","size":4}
	]`)
	client := &restoreClient{positions: &pos}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, client)
	s.Restore(Snapshot{Balance: Balance{Available: 100}})
	_, err := s.RestoreFromExchange(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	snap := s.Snapshot()
	if len(snap.Position.Tokens) != 1 {
		t.Fatalf("expected 1 valid token, got %d", len(snap.Position.Tokens))
	}
	if snap.Position.Tokens["tk-ok2"].Available != 4 {
		t.Fatalf("tk-ok2 mismatch: %+v", snap.Position.Tokens["tk-ok2"])
	}
}

func TestRestoreFromExchange_DedupesOpenOrdersBySameID(t *testing.T) {
	client := &restoreClient{
		openOrders: []orders.OpenOrder{
			{Id: "o1", Market: "m1", AssetId: "tk1", Side: "BUY", Price: 0.4, OriginalSize: 10, SizeMatched: 0},
			{Id: "o1", Market: "m1", AssetId: "tk1", Side: "BUY", Price: 0.4, OriginalSize: 10, SizeMatched: 0}, // dup
		},
	}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, client)
	s.Restore(Snapshot{Balance: Balance{Available: 100}})
	ids, err := s.RestoreFromExchange(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected dedupe → 1 ID, got %+v", ids)
	}
}

func TestRestoreFromExchange_SkipsZeroRemainingAndEmptyID(t *testing.T) {
	client := &restoreClient{
		openOrders: []orders.OpenOrder{
			{Id: "", Market: "m1", AssetId: "tk1", Side: "BUY", Price: 0.4, OriginalSize: 10, SizeMatched: 0},  // empty id
			{Id: "o2", Market: "m1", AssetId: "tk1", Side: "BUY", Price: 0.4, OriginalSize: 5, SizeMatched: 5}, // fully matched
			{Id: "o3", Market: "m1", AssetId: "tk1", Side: "BUY", Price: 0.4, OriginalSize: 8, SizeMatched: 0}, // valid
		},
	}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, client)
	s.Restore(Snapshot{Balance: Balance{Available: 100}})
	ids, err := s.RestoreFromExchange(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(ids) != 1 || ids[0] != "o3" {
		t.Fatalf("expected only o3, got %+v", ids)
	}
}

func TestRestoreFromExchange_RedeemSuccessClearsTokens(t *testing.T) {
	client := &restoreClient{
		emitTokens: []string{"tk-cleared"},
	}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, client)
	s.Restore(Snapshot{
		Balance:  Balance{Available: 100},
		Position: Position{Tokens: map[string]TokenPosition{"tk-cleared": {Available: 5}}},
	})
	_, err := s.RestoreFromExchange(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if _, ok := s.Snapshot().Position.Tokens["tk-cleared"]; ok {
		t.Fatal("tk-cleared should be removed by onRedeemSuccess callback")
	}
}

func TestOnRedeemSuccess_NilState(t *testing.T) {
	var s *State
	// must not panic
	s.onRedeemSuccess([]string{"tk1"})
}

func TestOnRedeemSuccess_EmptyTokens(t *testing.T) {
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	// must not panic
	s.onRedeemSuccess(nil)
}

func TestMapReservationsByID_SkipsEmptyIDs(t *testing.T) {
	got := mapReservationsByID([]OrderReservation{
		{OrderID: "", MarketID: "m1"},
		{OrderID: "   ", MarketID: "m1"},
		{OrderID: "o1", MarketID: "m1"},
	})
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if _, ok := got["o1"]; !ok {
		t.Fatal("expected o1 entry")
	}
}
