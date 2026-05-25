# PolyPilot B-Level Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix 22 README review issues + add 10 strategy optimizations + Polymarket-authoritative reconciliation + complete test coverage, with no public API breakage.

**Architecture:** Bottom-up package layering. Build foundation (`core`, `indicators`, `internal`) first, then `state` (reservations/fills/PnL/reconcile), then `risk` with new hard walls, then `probability`/`execution` with DI, then `strategy`/`runtime`/`observer`. Single commit at the end after all tests + lint pass.

**Tech Stack:** Go 1.25.7, viper, phuslu/log, polymarket-sdk, ethereum/go-ethereum, redsync (indirect).

**Spec Reference:** [docs/superpowers/specs/2026-05-18-refactor-b-level.md](../specs/2026-05-18-refactor-b-level.md)

**Commit Strategy:** All work happens on `benj` branch. **No intermediate commits.** Final commit only after Task 25 verification passes.

---

## File Structure

### New files
```
core/errors.go              # sentinel errors
core/pricing.go             # RequiredCollateral + FloatEpsilon
core/bus_test.go            # EventBus tests
core/pricing_test.go        # RequiredCollateral tests
core/errors_test.go         # errors.Is round-trip

state/reservation.go        # Provisional state machine + AttachOrder + AttachExternalOrder
state/fill.go               # ApplyFill + AvgCost + dailyPnL
state/balance.go            # ReconcileOnchainBalance + OpenOrders counter
state/pnl.go                # UnrealizedPnL
state/reconcile.go          # Polymarket-authoritative reconciliation (30s + WS trigger)
state/position_expiring.go  # PositionExpiring ticker
state/*_test.go             # one test file per source file

risk/rejection.go           # RejectionType enum + Rejection struct
risk/cooldown.go            # MarketCooldown state
risk/*_test.go              # multiple test files (engine, slippage, cooldown, external_inclusion)

probability/market_state.go # resetForNewMarketLocked (split from engine.go)
probability/features.go     # fillFeaturesLocked
probability/book_store.go   # GetOrderBook/getBook/updateOrderBook
probability/*_test.go       # tests for each split file

execution/placements.go     # submitPlacements + handlePostOrdersResults
execution/splits_merges.go  # submitSplits + submitMerges + relayClient field
execution/trade_events.go   # onOrderEvent + onTradeEvent + unknown orderID → reconcile signal
execution/dryrun_test.go    # DryRun=true path
execution/shutdown_test.go  # drain queue

runtime/event_handler.go    # handleInputUpdate + handleExecutionEvent + handleStrategyTick
runtime/order_tracking.go   # accepted/finalized/pending state
runtime/metrics.go          # publishMetrics + new fields
runtime/*_test.go           # tests for each split file

strategy/event_position_expiring.go  # OnPositionExpiring handler
indicators/zscore_test.go
indicators/imbalance_test.go
internal/atomicx/float64_test.go
internal/buffer/ring_buffer_test.go
internal/multicall/multicall3_test.go
.golangci.yml
```

### Modified files
```
core/bus.go                 # add DropThreshold + dropped warning
state/state.go              # add mu fields for new state, delegate to new files
state/types.go              # add AvgCost/AvgCostKnown/ExternalOrigin/DailyPnL/OpenOrderCount fields
runtime/engine.go           # delegate to event_handler.go, order_tracking.go, metrics.go; replace string error compare
runtime/types.go            # RiskManager.Check new signature with midPrices
risk/engine.go              # new fields + new Check signature
probability/engine.go       # split into multiple files, NewEngine(client) constructor, fix log module name "observer"→"probability"
execution/executor.go       # delegate; add RelayClient field; DryRun field; shutdown drain
strategy/strategy.go        # Features type assertion safety; viper.Sub nil check; remove PlacePrice
strategy/market_queue.go    # cache SlugMarket value not *gjson.Result
market/polymarket_slug_feed.go  # retry on fetch failure (5s sleep + N attempts)
observer/logger.go          # type assertion safety; EventReconcile/EventPositionExpiring cases
config/config.go            # validate required fields at startup; risk/reconcile/redeem sections
state/state_restore_pm.go   # redeem.enabled flag
internal/multicall/multicall3.go  # log ABI Unpack errors
main.go                     # wire new dependencies (probability/Executor/Risk receive client/config)
README.md                   # add refactor record section at end
```

---

## Task Overview

| # | Task | Layer | Spec § |
|---|---|---|---|
| 1 | core/errors.go + tests | foundation | 4.1 |
| 2 | core/pricing.go + tests | foundation | 4.2 |
| 3 | core/bus.go DropThreshold + tests | foundation | 9.2 |
| 4 | indicators tests + zscore first-tick fix | foundation | 5.3 #16 |
| 5 | internal tests + multicall ABI fix | foundation | 5.3 #21 |
| 6 | state/types.go new fields | state | 6.4, 7.1 |
| 7 | state/reservation.go (Provisional + AttachOrder + AttachExternalOrder) + tests | state | 8.1 |
| 8 | state/fill.go (ApplyFill + AvgCost + dailyPnL UTC) + tests | state | 7.1, 6.4 |
| 9 | state/balance.go + OpenOrders counter + tests | state | 6.4 |
| 10 | state/pnl.go (UnrealizedPnL) + tests | state | 7.2 |
| 11 | state/reconcile.go (Polymarket authority) + tests | state | 5.4 |
| 12 | state/position_expiring.go (ticker + EventPositionExpiring) + tests | state | 7.3 |
| 13 | risk/rejection.go + risk/engine.go new signature + tests | risk | 6.1–6.3 |
| 14 | probability split: NewEngine(client) + fix log name + market_state/features/book_store + tests | probability | 5.1, 5.3 #4, 9.4 |
| 15 | execution split: RelayClient field + placements/splits_merges/trade_events + tests | execution | 5.1, 5.2 |
| 16 | execution: DryRun + shutdown drain + tests | execution | 9.1, 9.3 |
| 17 | strategy: Features safety + MarketQueue value cache + OnPositionExpiring + tests | strategy | 5.3 #2,#6,#7,#14,#15 |
| 18 | market: PolymarketSlugFeed retry + tests | market | 5.3 #3 |
| 19 | runtime split: event_handler/order_tracking/metrics + errors.Is + tests | runtime | 5.2, 5.3 #5 |
| 20 | runtime.submitIntents: midPrices map + new Risk signature | runtime | 6.3 |
| 21 | observer: type assertion safety + Reconcile/PositionExpiring cases + tests | observer | 5.3 #18, 9.5 |
| 22 | config: validate startup + risk/reconcile/redeem schema + tests | config | 5.3 #22, 6.1 |
| 23 | main.go: wire new deps | wiring | – |
| 24 | cleanup: remove PlacePrice + commented log.Printf + redeem.enabled | cleanup | 5.3 #15,#17,#20 |
| 25 | lint + test -race -cover; README refactor record; single commit | finalize | 10, 13 |

---

## TDD Loop (every task)

For every task: write test → run test → see FAIL → write impl → run test → see PASS → run `go test -race ./...` → see all green. **Do not commit between tasks.**

---

## Phase 0: Foundation (core/ + indicators/ + internal/)

### Task 1: core sentinel errors

**Files:**
- Create: `core/errors.go`
- Create: `core/errors_test.go`

- [ ] **Step 1: Write `core/errors_test.go`**

```go
package core

import (
	"errors"
	"fmt"
	"testing"
)

func TestSentinelErrorsAreDistinct(t *testing.T) {
	all := []error{
		ErrOrderAlreadyReserved, ErrIntentAlreadyReserved, ErrReservationNotFound,
		ErrInsufficientBalance, ErrInsufficientPosition, ErrBelowMinReserve,
		ErrInvalidPrice, ErrInvalidSize, ErrInvalidSide,
		ErrInvalidMarket, ErrInvalidToken,
		ErrFillExceedsRemaining, ErrFillMarketTokenMismatch, ErrFillSideMismatch,
		ErrReconcileFailed,
	}
	seen := make(map[string]struct{}, len(all))
	for _, e := range all {
		if _, dup := seen[e.Error()]; dup {
			t.Fatalf("duplicate sentinel message: %q", e.Error())
		}
		seen[e.Error()] = struct{}{}
	}
}

func TestSentinelErrorsWrapAndUnwrap(t *testing.T) {
	wrapped := fmt.Errorf("context: %w", ErrInsufficientBalance)
	if !errors.Is(wrapped, ErrInsufficientBalance) {
		t.Fatalf("errors.Is should match wrapped sentinel")
	}
}
```

- [ ] **Step 2: Run test** — `go test -run 'TestSentinel' ./core/...` → expect FAIL (undefined identifiers).

- [ ] **Step 3: Write `core/errors.go`** with all sentinels from spec § 4.1 verbatim.

- [ ] **Step 4: Run test** — `go test -run 'TestSentinel' ./core/...` → expect PASS.

---

### Task 2: core/pricing.go (RequiredCollateral)

**Files:**
- Create: `core/pricing.go`
- Create: `core/pricing_test.go`
- Modify: `state/state.go` (delete local `requiredCollateral` + `floatEpsilon`)
- Modify: `state/state_restore.go` (same)
- Modify: `risk/engine.go` (delete local `requiredCollateral` + `floatEpsilon`)

- [ ] **Step 1: Write `core/pricing_test.go`**

```go
package core

import (
	"math"
	"testing"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

func TestRequiredCollateral(t *testing.T) {
	cases := []struct {
		name  string
		side  orders.Side
		price float64
		size  float64
		want  float64
	}{
		{"BUY price*size", orders.BUY, 0.4, 5, 2.0},
		{"BUY zero price", orders.BUY, 0, 5, 0},
		{"SELL size only", orders.SELL, 0.4, 5, 5},
		{"SELL zero size", orders.SELL, 0.5, 0, 0},
		{"Invalid side", orders.Side("?"), 0.5, 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RequiredCollateral(c.side, c.price, c.size)
			if math.Abs(got-c.want) > FloatEpsilon {
				t.Fatalf("got=%v want=%v", got, c.want)
			}
		})
	}
}

func TestFloatEpsilonIsSmall(t *testing.T) {
	if FloatEpsilon <= 0 || FloatEpsilon > 1e-6 {
		t.Fatalf("FloatEpsilon out of expected range: %v", FloatEpsilon)
	}
}
```

- [ ] **Step 2: Run test** — `go test -run 'TestRequired|TestFloatEpsilon' ./core/...` → expect FAIL.

- [ ] **Step 3: Write `core/pricing.go`**

```go
package core

import "github.com/xiangxn/go-polymarket-sdk/orders"

const FloatEpsilon = 1e-9

// RequiredCollateral returns the USDC amount that must be reserved for a single
// order intent. BUY needs price*size USDC; SELL needs `size` tokens (returned
// as the token-denominated size to be subtracted from token Available).
func RequiredCollateral(side orders.Side, price, size float64) float64 {
	switch side {
	case orders.BUY:
		return size * price
	case orders.SELL:
		return size
	default:
		return 0
	}
}
```

- [ ] **Step 4: Run test** — `go test -run 'TestRequired|TestFloatEpsilon' ./core/...` → expect PASS.

- [ ] **Step 5: Replace duplicates**

In `state/state.go`: delete the `floatEpsilon` const and the `requiredCollateral` function (lines around 13 and 500). Replace all `requiredCollateral(` call sites with `core.RequiredCollateral(`. Replace `floatEpsilon` references with `core.FloatEpsilon`. Add `"github.com/xiangxn/polypilot/core"` import.

In `state/state_restore.go`: replace `requiredCollateral(` → `core.RequiredCollateral(`. Add import.

In `risk/engine.go`: delete local copies; replace call sites; add import.

- [ ] **Step 6: Run all package tests**

```
go test -race ./core/... ./state/... ./risk/...
```

Expected: PASS. If a SELL test in risk relies on the old name, it still calls `requiredCollateral` internally — only the helper is replaced, behavior identical.

---

### Task 3: core/bus.go DropThreshold + tests

**Files:**
- Modify: `core/bus.go` (add `DropThreshold` field + warning hook)
- Create: `core/bus_test.go`

- [ ] **Step 1: Write `core/bus_test.go`**

```go
package core

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBusFanOut(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()
	ch1 := bus.Subscribe()
	ch2 := bus.Subscribe()

	bus.Publish(Event{Type: EventMarket, Data: "x"})
	for _, ch := range []chan Event{ch1, ch2} {
		select {
		case ev := <-ch:
			if ev.Data != "x" {
				t.Fatalf("got %v want x", ev.Data)
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout on subscriber receive")
		}
	}
}

func TestBusDroppedCounter(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()
	_ = bus.Subscribe() // never read
	for i := 0; i < 2000; i++ {
		bus.Publish(Event{Type: EventMarket})
	}
	if bus.Stats().Dropped == 0 {
		t.Fatalf("expected dropped > 0 when subscriber stalls")
	}
}

func TestBusDropThresholdWarning(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()
	bus.DropThreshold = 10

	var warnings atomic.Int32
	bus.OnDropThreshold = func(count uint64) { warnings.Add(1) }

	_ = bus.Subscribe() // stall
	for i := 0; i < 100; i++ {
		bus.Publish(Event{Type: EventMarket})
	}
	if warnings.Load() == 0 {
		t.Fatalf("expected DropThreshold warning")
	}
}

func TestBusConcurrentPublishSubscribe(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()
	var wg sync.WaitGroup
	done := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, cancel := bus.SubscribeWithCancel()
			defer cancel()
			for {
				select {
				case <-done:
					return
				case <-ch:
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			bus.Publish(Event{Type: EventMarket})
		}
	}()

	time.Sleep(50 * time.Millisecond)
	close(done)
	wg.Wait()
}
```

- [ ] **Step 2: Run** — `go test -race ./core/...` → expect FAIL on `DropThreshold`/`OnDropThreshold` undefined.

- [ ] **Step 3: Modify `core/bus.go`**

Add fields and warning trigger (add to `EventBus` struct):

```go
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
	// threshold boundary is crossed. Must be non-blocking.
	OnDropThreshold func(droppedTotal uint64)
}
```

In `Publish`, after the `b.dropped.Add(1)` line, add:

```go
if b.DropThreshold > 0 && b.OnDropThreshold != nil {
	total := b.dropped.Load()
	last := b.lastWarnedAtDrop.Load()
	if total/b.DropThreshold > last/b.DropThreshold {
		if b.lastWarnedAtDrop.CompareAndSwap(last, total) {
			go b.OnDropThreshold(total)
		}
	}
}
```

- [ ] **Step 4: Run** — `go test -race ./core/...` → expect PASS.

---

### Task 4: indicators tests + zscore first-tick fix

**Files:**
- Create: `indicators/zscore_test.go`
- Create: `indicators/imbalance_test.go`
- Modify: `indicators/zscore.go` (verify first-tick semantics; add a documented test)

- [ ] **Step 1: Write `indicators/zscore_test.go`**

```go
package indicators

import (
	"math"
	"testing"
)

func TestZScore_OnTickFirstTickSetsBaseline(t *testing.T) {
	z := NewZScore(10)
	z.OnTick(Tick{Price: 100, Timestamp: 1_000})
	if z.IsReady() {
		t.Fatalf("should not be ready after single tick")
	}
}

func TestZScore_FillsMissingSeconds(t *testing.T) {
	z := NewZScore(5)
	z.OnTick(Tick{Price: 100, Timestamp: 1_000})
	z.OnTick(Tick{Price: 110, Timestamp: 4_000}) // jump 3 seconds
	// expect 3 bars at 100 pushed; then update to 110 stays in lastPrice
	if got := z.WindowSize(); got != 5 {
		t.Fatalf("window=%d", got)
	}
}

func TestZScore_ReadyWhenHalfFull(t *testing.T) {
	z := NewZScore(10)
	for i := int64(0); i < 6; i++ {
		z.OnTick(Tick{Price: 100 + float64(i), Timestamp: (i + 1) * 1_000})
	}
	if !z.IsReady() {
		t.Fatalf("expected ready when series >= window/2")
	}
}

func TestZScore_ZeroStartPriceOrZeroTime(t *testing.T) {
	z := NewZScore(10)
	if got := z.ZScore(100, 0, 60); got != 0 {
		t.Fatalf("expected 0 with zero startPrice, got %v", got)
	}
	if got := z.ZScore(100, 100, 0); got != 0 {
		t.Fatalf("expected 0 with zero remaining, got %v", got)
	}
}

func TestZScore_Computation(t *testing.T) {
	z := NewZScore(10)
	// feed flat price → sigma=0 → guarded to 1e-5
	for i := int64(0); i < 10; i++ {
		z.OnTick(Tick{Price: 100, Timestamp: (i + 1) * 1_000})
	}
	got := z.ZScore(101, 100, 60)
	if math.IsNaN(got) || math.IsInf(got, 0) {
		t.Fatalf("expected finite z, got %v", got)
	}
}
```

- [ ] **Step 2: Write `indicators/imbalance_test.go`**

```go
package indicators

import (
	"testing"

	"github.com/xiangxn/go-polymarket-sdk/orders"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

func TestCalcImBalance(t *testing.T) {
	bid := []orders.Book{{Price: 0.49, Size: 100}, {Price: 0.48, Size: 50}}
	ask := []orders.Book{{Price: 0.51, Size: 80}, {Price: 0.52, Size: 40}}
	ob := &sdk.OrderBook{Bids: bid, Asks: ask}

	im := CalcImBalance(ob, 2)
	want := (150.0 - 120.0) / (150.0 + 120.0)
	if im != want {
		t.Fatalf("im=%v want=%v", im, want)
	}
}

func TestCalcImBalance_Empty(t *testing.T) {
	if CalcImBalance(&sdk.OrderBook{}, 3) != 0 {
		t.Fatal("empty book should return 0")
	}
}

func TestCalcImBalance_OneSideEmpty(t *testing.T) {
	asksOnly := &sdk.OrderBook{Asks: []orders.Book{{Price: 0.5, Size: 1}}}
	if CalcImBalance(asksOnly, 1) != -1 {
		t.Fatal("asks-only should be -1")
	}
	bidsOnly := &sdk.OrderBook{Bids: []orders.Book{{Price: 0.5, Size: 1}}}
	if CalcImBalance(bidsOnly, 1) != 1 {
		t.Fatal("bids-only should be 1")
	}
}

func TestCalcImBalance_NilBook(t *testing.T) {
	if CalcImBalance(nil, 3) != 0 {
		t.Fatal("nil should return 0")
	}
}
```

- [ ] **Step 3: Run** — `go test -race ./indicators/...` → expect PASS for existing behavior (zscore + imbalance unchanged; tests document current semantics).

---

### Task 5: internal tests + multicall ABI fix

**Files:**
- Create: `internal/atomicx/float64_test.go`
- Create: `internal/buffer/ring_buffer_test.go`
- Create: `internal/multicall/multicall3_test.go`
- Modify: `internal/multicall/multicall3.go` (log + return ABI Unpack errors)

- [ ] **Step 1: Write `internal/atomicx/float64_test.go`**

```go
package atomicx

import (
	"sync"
	"testing"
)

func TestFloat64StoreLoad(t *testing.T) {
	var f Float64
	f.Store(3.14)
	if got := f.Load(); got != 3.14 {
		t.Fatalf("got %v", got)
	}
}

func TestFloat64ConcurrentRace(t *testing.T) {
	var f Float64
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(v float64) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				f.Store(v)
				_ = f.Load()
			}
		}(float64(i))
	}
	wg.Wait()
}
```

- [ ] **Step 2: Write `internal/buffer/ring_buffer_test.go`**

```go
package buffer

import (
	"sync"
	"testing"
)

func TestRingBufferAddAndValues(t *testing.T) {
	r := NewRingBuffer(3)
	r.Add(1)
	r.Add(2)
	r.Add(3)
	got := r.Values()
	if len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Fatalf("got %v", got)
	}
	r.Add(4)
	got = r.Values()
	if len(got) != 3 || got[0] != 2 || got[2] != 4 {
		t.Fatalf("after overflow got %v", got)
	}
}

func TestRingBufferLast(t *testing.T) {
	r := NewRingBuffer(5)
	for i := 1; i <= 5; i++ {
		r.Add(float64(i))
	}
	got := r.Last(3)
	if len(got) != 3 || got[0] != 3 || got[2] != 5 {
		t.Fatalf("got %v", got)
	}
}

func TestRingBufferReset(t *testing.T) {
	r := NewRingBuffer(3)
	r.Add(1)
	r.Reset()
	if r.Len() != 0 {
		t.Fatalf("expected len=0 after reset")
	}
}

func TestRingBufferLatestEmpty(t *testing.T) {
	r := NewRingBuffer(3)
	if _, ok := r.Latest(); ok {
		t.Fatalf("empty should return ok=false")
	}
}

func TestRingBufferConcurrentRace(t *testing.T) {
	r := NewRingBuffer(100)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				r.Add(float64(i))
				_ = r.Last(10)
			}
		}()
	}
	wg.Wait()
}
```

- [ ] **Step 3: Write `internal/multicall/multicall3_test.go`**

```go
package utils

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestFloat_ScalesByDecimals(t *testing.T) {
	info := &ERC20Info{Balance: big.NewInt(1234567), Decimals: 6}
	got := info.Float()
	if got != 1.234567 {
		t.Fatalf("got %v", got)
	}
}

func TestFloat_ZeroBalance(t *testing.T) {
	info := &ERC20Info{Balance: big.NewInt(0), Decimals: 18}
	if info.Float() != 0 {
		t.Fatalf("expected 0")
	}
}

func TestMulticall_UnsupportedChain(t *testing.T) {
	// chainID 999 is not configured
	_, err := FetchERC20InfoMulticall3(context.Background(), nil, 999,
		common.HexToAddress("0xdeadbeef"), common.HexToAddress("0xcafebabe"))
	if err == nil {
		t.Fatalf("expected error for unsupported chain")
	}
}
```

- [ ] **Step 4: Modify `internal/multicall/multicall3.go`** — replace `_ = erc20Parsed.UnpackIntoInterface(...)` calls with explicit error checks. Pattern:

```go
if results[0].Success {
	var out *big.Int
	if err := erc20Parsed.UnpackIntoInterface(&out, "balanceOf", results[0].ReturnData); err != nil {
		return nil, fmt.Errorf("multicall3 unpack balanceOf: %w", err)
	}
	info.Balance = out
} else {
	info.Balance = big.NewInt(0)
}
```

Same pattern for `decimals` and `symbol`. Add `"fmt"` import if missing.

- [ ] **Step 5: Run** — `go test -race ./internal/...` → expect PASS.

---

## Phase 1: state/ package (reservations, fills, balance, pnl, reconcile, position-expiring)

### Task 6: state/types.go — new fields

**Files:**
- Modify: `state/types.go`

- [ ] **Step 1: Add fields to existing structs** in `state/types.go`:

```go
type OrderReservation struct {
	OrderID        string
	MarketID       string
	TokenID        string
	Side           orders.Side
	Price          float64
	RemainingSize  float64
	Reserved       float64
	ExternalOrigin bool // true when reconciled in from Polymarket without local intent
}

type TokenPosition struct {
	Available    float64
	Reserved     float64
	AvgCost      float64
	AvgCostKnown bool    // false → exclude from UnrealizedPnL
	TotalBought  float64 // cumulative BUY size (for weighted average)
}

type Snapshot struct {
	Position       Position
	Balance        Balance
	Orders         map[string]OrderReservation
	DailyPnL       float64
	DailyPnLDate   string // "2006-01-02" UTC
	OpenOrderCount int
}
```

Add to `State` struct (in `state/state.go`):

```go
type State struct {
	mu       sync.RWMutex
	position Position
	balance  Balance

	orderReservations       map[string]OrderReservation
	provisionalReservations map[string]ProvisionalReservation

	dailyPnL     float64
	dailyPnLDate string

	balanceSync    BalanceSyncConfig
	balanceSyncRun sync.Once
	restoreClient  ExchangeStateClient

	// reconcile signal channel (capacity 1, deduplicated)
	reconcileTrigger chan struct{}
}
```

- [ ] **Step 2: Update `Snapshot()` method** in `state/state.go` to populate new fields:

```go
func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return Snapshot{
		Position:       Position{Tokens: cloneTokenPositions(s.position.Tokens)},
		Balance:        s.balance,
		Orders:         cloneOrderReservations(s.orderReservations),
		DailyPnL:       s.dailyPnL,
		DailyPnLDate:   s.dailyPnLDate,
		OpenOrderCount: len(s.orderReservations),
	}
}
```

- [ ] **Step 3: Update `NewStateWithBalanceSync`** to initialize `reconcileTrigger`:

```go
return &State{
	position:                Position{Tokens: make(map[string]TokenPosition)},
	balance:                 Balance{Available: 0, Reserved: 0, MinBalance: minBalance},
	orderReservations:       make(map[string]OrderReservation),
	provisionalReservations: make(map[string]ProvisionalReservation),
	balanceSync:             normalizeBalanceSyncConfig(balanceSync),
	restoreClient:           restoreClient,
	reconcileTrigger:        make(chan struct{}, 1),
}
```

- [ ] **Step 4: Run** — `go build ./...` → expect PASS. Existing tests may need new fields; if they break, leave new fields zero-value.

---

### Task 7: state/reservation.go — Provisional state machine + AttachOrder + AttachExternalOrder

**Files:**
- Create: `state/reservation.go`
- Create: `state/reservation_test.go`
- Modify: `state/state.go` (delete old `ConfirmProvisional`/`ReserveOrder` if fully replaced, or keep as thin wrappers)

- [ ] **Step 1: Write `state/reservation_test.go`** covering:

```go
package state

import (
	"errors"
	"testing"
	"time"

	"github.com/xiangxn/polypilot/core"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

func newStateWithBalance(t *testing.T, available float64) *State {
	t.Helper()
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, nil)
	s.Restore(Snapshot{Balance: Balance{Available: available}})
	return s
}

func TestAttachOrder_ConfirmsProvisional(t *testing.T) {
	s := newStateWithBalance(t, 100)
	now := time.Now()
	if err := s.TryReserveProvisional("i1", "m1", "tk1", orders.BUY, 0.5, 10, now, 5*time.Second); err != nil {
		t.Fatalf("provisional: %v", err)
	}
	if err := s.AttachOrder("i1", "o1", "m1", "tk1", orders.BUY, 0.5, 10); err != nil {
		t.Fatalf("attach: %v", err)
	}
	snap := s.Snapshot()
	if snap.Balance.Available != 95 || snap.Balance.Reserved != 5 {
		t.Fatalf("balance: %+v", snap.Balance)
	}
	if _, ok := snap.Orders["o1"]; !ok {
		t.Fatalf("expected o1 reservation")
	}
}

func TestAttachOrder_NoProvisional_CreatesFresh(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if err := s.AttachOrder("", "o1", "m1", "tk1", orders.BUY, 0.5, 10); err != nil {
		t.Fatalf("attach: %v", err)
	}
	snap := s.Snapshot()
	if snap.Balance.Reserved != 5 {
		t.Fatalf("expected reserved=5, got %v", snap.Balance.Reserved)
	}
	if snap.Orders["o1"].ExternalOrigin {
		t.Fatalf("AttachOrder should not set ExternalOrigin")
	}
}

func TestAttachOrder_Idempotent(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if err := s.AttachOrder("", "o1", "m1", "tk1", orders.BUY, 0.5, 10); err != nil {
		t.Fatalf("first: %v", err)
	}
	err := s.AttachOrder("", "o1", "m1", "tk1", orders.BUY, 0.5, 10)
	if err != nil && !errors.Is(err, core.ErrOrderAlreadyReserved) {
		t.Fatalf("expected ErrOrderAlreadyReserved or nil, got %v", err)
	}
	snap := s.Snapshot()
	if snap.Balance.Reserved != 5 {
		t.Fatalf("idempotent attach must not double-reserve, got %v", snap.Balance.Reserved)
	}
}

func TestAttachExternalOrder_SetsExternalOrigin(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if err := s.AttachExternalOrder("ext1", "m1", "tk1", orders.BUY, 0.5, 10); err != nil {
		t.Fatalf("external: %v", err)
	}
	snap := s.Snapshot()
	r := snap.Orders["ext1"]
	if !r.ExternalOrigin {
		t.Fatalf("expected ExternalOrigin=true")
	}
	if snap.Balance.Reserved != 5 {
		t.Fatalf("expected reserved=5, got %v", snap.Balance.Reserved)
	}
}
```

- [ ] **Step 2: Run** — `go test -run 'TestAttach' ./state/...` → expect FAIL (functions not defined).

- [ ] **Step 3: Create `state/reservation.go`**

```go
package state

import (
	"errors"
	"time"

	"github.com/xiangxn/polypilot/core"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

// AttachOrder unifies the two paths that produce an OrderReservation:
//   1. ConfirmProvisional (a local intent confirmed by exchange ACK)
//   2. Direct ReserveOrder (WS LIVE arrives before HTTP ACK, no intentID)
// If orderID already exists, returns ErrOrderAlreadyReserved without modifying state.
func (s *State) AttachOrder(intentID, orderID, marketID, tokenID string,
	side orders.Side, price, requestedSize float64) error {
	if err := validateOrderArgs(orderID, marketID, tokenID, side, price, requestedSize); err != nil {
		return err
	}
	reserved := core.RequiredCollateral(side, price, requestedSize)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.orderReservations[orderID]; exists {
		// idempotent: if intentID matches an existing provisional, release it
		if intentID != "" {
			if p, ok := s.provisionalReservations[intentID]; ok {
				delete(s.provisionalReservations, intentID)
				s.ensureTokenPositions()
				s.releaseReservedLocked(p.Side, p.TokenID, p.Reserved)
			}
		}
		return core.ErrOrderAlreadyReserved
	}

	s.ensureTokenPositions()

	if intentID != "" {
		if p, ok := s.provisionalReservations[intentID]; ok {
			delete(s.provisionalReservations, intentID)
			s.orderReservations[orderID] = OrderReservation{
				OrderID:       orderID,
				MarketID:      p.MarketID,
				TokenID:       p.TokenID,
				Side:          p.Side,
				Price:         p.Price,
				RemainingSize: p.RemainingSize,
				Reserved:      p.Reserved,
			}
			return nil
		}
	}

	// fresh reservation (no provisional)
	if side == orders.BUY {
		if s.balance.Available+core.FloatEpsilon < reserved {
			return core.ErrInsufficientBalance
		}
		s.balance.Available -= reserved
		s.balance.Reserved += reserved
	} else {
		k := tokenKey(tokenID)
		tp := s.position.Tokens[k]
		if tp.Available+core.FloatEpsilon < requestedSize {
			return core.ErrInsufficientPosition
		}
		tp.Available -= requestedSize
		tp.Reserved += requestedSize
		if tp.Available < 0 {
			tp.Available = 0
		}
		s.position.Tokens[k] = tp
	}

	s.orderReservations[orderID] = OrderReservation{
		OrderID:       orderID,
		MarketID:      marketID,
		TokenID:       tokenID,
		Side:          side,
		Price:         price,
		RemainingSize: requestedSize,
		Reserved:      reserved,
	}
	return nil
}

// AttachExternalOrder is called by reconcile when an order is found on
// Polymarket but not present locally. It marks ExternalOrigin=true and reserves
// the corresponding USDC/token. Idempotent.
func (s *State) AttachExternalOrder(orderID, marketID, tokenID string,
	side orders.Side, price, remainingSize float64) error {
	if err := validateOrderArgs(orderID, marketID, tokenID, side, price, remainingSize); err != nil {
		return err
	}
	reserved := core.RequiredCollateral(side, price, remainingSize)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.orderReservations[orderID]; exists {
		return nil // idempotent
	}
	s.ensureTokenPositions()

	if side == orders.BUY {
		if s.balance.Available+core.FloatEpsilon < reserved {
			return core.ErrInsufficientBalance
		}
		s.balance.Available -= reserved
		s.balance.Reserved += reserved
	} else {
		k := tokenKey(tokenID)
		tp := s.position.Tokens[k]
		if tp.Available+core.FloatEpsilon < remainingSize {
			return core.ErrInsufficientPosition
		}
		tp.Available -= remainingSize
		tp.Reserved += remainingSize
		s.position.Tokens[k] = tp
	}

	s.orderReservations[orderID] = OrderReservation{
		OrderID:        orderID,
		MarketID:       marketID,
		TokenID:        tokenID,
		Side:           side,
		Price:          price,
		RemainingSize:  remainingSize,
		Reserved:       reserved,
		ExternalOrigin: true,
	}
	return nil
}

func validateOrderArgs(orderID, marketID, tokenID string, side orders.Side, price, size float64) error {
	switch {
	case orderID == "":
		return errors.New("empty order id")
	case marketID == "":
		return core.ErrInvalidMarket
	case tokenID == "":
		return core.ErrInvalidToken
	case size <= 0:
		return core.ErrInvalidSize
	case price <= 0 || price >= 1:
		return core.ErrInvalidPrice
	case side != orders.BUY && side != orders.SELL:
		return core.ErrInvalidSide
	}
	return nil
}
```

- [ ] **Step 4: Update existing `ReserveOrder`** in `state/state.go` to wrap `AttachOrder("", orderID, ...)` — preserves existing callers and old behavior.

```go
func (s *State) ReserveOrder(orderID, marketID, tokenID string, side orders.Side, price, requestedSize float64) error {
	return s.AttachOrder("", orderID, marketID, tokenID, side, price, requestedSize)
}
```

Similarly leave `ConfirmProvisional` as-is (existing callers); it can later be replaced by `AttachOrder(intentID, orderID, ...)`.

- [ ] **Step 5: Run** — `go test -race ./state/...` → expect PASS for new tests + existing tests still green.

---

### Task 8: state/fill.go — ApplyFill + AvgCost + dailyPnL UTC

**Files:**
- Create: `state/fill.go`
- Create: `state/fill_test.go`
- Modify: `state/state.go` (move existing `ApplyFill` body into new file; keep method receiver)

- [ ] **Step 1: Write `state/fill_test.go`**

```go
package state

import (
	"testing"
	"time"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

func TestApplyFill_BUY_UpdatesAvgCost(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if err := s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.4, 10); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := s.ApplyFill("o1", "m1", "tk1", orders.BUY, 6, 0.4); err != nil {
		t.Fatalf("fill: %v", err)
	}
	snap := s.Snapshot()
	tp := snap.Position.Tokens["tk1"]
	if tp.Available != 6 || tp.AvgCost != 0.4 || !tp.AvgCostKnown {
		t.Fatalf("avg cost wrong: %+v", tp)
	}
}

func TestApplyFill_BUY_WeightedAvg(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if err := s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.4, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyFill("o1", "m1", "tk1", orders.BUY, 5, 0.4); err != nil {
		t.Fatal(err)
	}
	if err := s.ReserveOrder("o2", "m1", "tk1", orders.BUY, 0.6, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyFill("o2", "m1", "tk1", orders.BUY, 5, 0.6); err != nil {
		t.Fatal(err)
	}
	tp := s.Snapshot().Position.Tokens["tk1"]
	// weighted: (0.4*5 + 0.6*5)/10 = 0.5
	if tp.AvgCost != 0.5 {
		t.Fatalf("avg=%v", tp.AvgCost)
	}
}

func TestApplyFill_SELL_AccumulatesDailyPnL(t *testing.T) {
	s := newStateWithBalance(t, 100)
	// buy 10 @ 0.4 first
	if err := s.ReserveOrder("buy1", "m1", "tk1", orders.BUY, 0.4, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyFill("buy1", "m1", "tk1", orders.BUY, 10, 0.4); err != nil {
		t.Fatal(err)
	}
	// now sell 5 @ 0.6 → realized PnL = (0.6-0.4)*5 = 1.0
	if err := s.ReserveOrder("sell1", "m1", "tk1", orders.SELL, 0.6, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyFill("sell1", "m1", "tk1", orders.SELL, 5, 0.6); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if snap.DailyPnL < 0.99 || snap.DailyPnL > 1.01 {
		t.Fatalf("expected dailyPnL≈1.0, got %v", snap.DailyPnL)
	}
	if snap.DailyPnLDate != time.Now().UTC().Format("2006-01-02") {
		t.Fatalf("date mismatch: %v", snap.DailyPnLDate)
	}
}

func TestApplyFill_SELL_NoAvgCostKnown_NoRealizedPnL(t *testing.T) {
	s := newStateWithBalance(t, 100)
	// inject external position without buy history (simulate reconcile)
	s.Restore(Snapshot{
		Balance:  Balance{Available: 100},
		Position: Position{Tokens: map[string]TokenPosition{"tk1": {Available: 10, AvgCostKnown: false}}},
	})
	if err := s.ReserveOrder("sell1", "m1", "tk1", orders.SELL, 0.6, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyFill("sell1", "m1", "tk1", orders.SELL, 5, 0.6); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if snap.DailyPnL != 0 {
		t.Fatalf("expected zero PnL when AvgCostKnown=false, got %v", snap.DailyPnL)
	}
}
```

- [ ] **Step 2: Run** — expect FAIL on AvgCost / DailyPnL fields not populated.

- [ ] **Step 3: Create `state/fill.go`**

Move the existing `ApplyFill` from `state/state.go` into `state/fill.go` and modify per spec § 7.1. Pseudo (use real `orders.BUY` constants):

```go
package state

import (
	"time"

	"github.com/xiangxn/polypilot/core"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

func (s *State) ApplyFill(orderID, marketID, tokenID string, side orders.Side,
	filledSize, fillPrice float64) error {
	if orderID == "" {
		return core.ErrReservationNotFound
	}
	if filledSize <= 0 {
		return core.ErrInvalidSize
	}
	if side != orders.BUY && side != orders.SELL {
		return core.ErrInvalidSide
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	res, exists := s.orderReservations[orderID]
	if !exists {
		return core.ErrReservationNotFound
	}
	if res.MarketID != marketID || res.TokenID != tokenID {
		return core.ErrFillMarketTokenMismatch
	}
	if res.Side != side {
		return core.ErrFillSideMismatch
	}
	if filledSize > res.RemainingSize+core.FloatEpsilon {
		return core.ErrFillExceedsRemaining
	}
	if fillPrice <= 0 {
		fillPrice = res.Price
	}

	consumed := core.RequiredCollateral(side, fillPrice, filledSize)
	if consumed > res.Reserved {
		consumed = res.Reserved
	}

	res.RemainingSize -= filledSize
	if res.RemainingSize < 0 {
		res.RemainingSize = 0
	}
	res.Reserved -= consumed
	if res.Reserved < 0 {
		res.Reserved = 0
	}

	s.ensureTokenPositions()
	k := tokenKey(res.TokenID)
	tp := s.position.Tokens[k]

	switch side {
	case orders.BUY:
		s.balance.Reserved -= consumed
		if s.balance.Reserved < 0 {
			s.balance.Reserved = 0
		}
		// weighted average
		newTotal := tp.TotalBought + filledSize
		if newTotal > 0 {
			tp.AvgCost = (tp.AvgCost*tp.TotalBought + fillPrice*filledSize) / newTotal
		}
		tp.TotalBought = newTotal
		tp.AvgCostKnown = true
		tp.Available += filledSize
	case orders.SELL:
		tp.Reserved -= consumed
		if tp.Reserved < 0 {
			tp.Reserved = 0
		}
		// realized PnL only when AvgCostKnown
		if tp.AvgCostKnown {
			realized := (fillPrice - tp.AvgCost) * filledSize
			s.addDailyPnLLocked(realized)
		}
		proceeds := fillPrice * filledSize
		s.balance.Available += proceeds
	}
	s.position.Tokens[k] = tp

	if res.RemainingSize <= core.FloatEpsilon {
		delete(s.orderReservations, orderID)
	} else {
		s.orderReservations[orderID] = res
	}
	return nil
}

// addDailyPnLLocked maintains a UTC date stamp and resets on day rollover.
// Caller must hold s.mu.Lock.
func (s *State) addDailyPnLLocked(delta float64) {
	today := time.Now().UTC().Format("2006-01-02")
	if s.dailyPnLDate != today {
		s.dailyPnL = 0
		s.dailyPnLDate = today
	}
	s.dailyPnL += delta
}
```

- [ ] **Step 4: Delete old `ApplyFill`** from `state/state.go`.

- [ ] **Step 5: Run** — `go test -race ./state/...` → expect PASS.

---

### Task 9: state/balance.go — OpenOrders is `len(orderReservations)`

The existing `state/balance_sync.go` already handles balance reconciliation. The Snapshot in Task 6 already exposes `OpenOrderCount = len(s.orderReservations)`. **No new file needed for this task** — verify behavior with a test.

**Files:**
- Create: `state/openorders_test.go`

- [ ] **Step 1: Write test**

```go
package state

import (
	"testing"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

func TestSnapshot_OpenOrderCount(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if got := s.Snapshot().OpenOrderCount; got != 0 {
		t.Fatalf("expected 0 got %d", got)
	}
	_ = s.ReserveOrder("o1", "m1", "tk1", orders.BUY, 0.5, 10)
	_ = s.ReserveOrder("o2", "m1", "tk2", orders.BUY, 0.5, 5)
	if got := s.Snapshot().OpenOrderCount; got != 2 {
		t.Fatalf("expected 2 got %d", got)
	}
}

func TestSnapshot_OpenOrderCount_IncludesExternal(t *testing.T) {
	s := newStateWithBalance(t, 100)
	_ = s.AttachExternalOrder("ext1", "m1", "tk1", orders.BUY, 0.5, 5)
	if got := s.Snapshot().OpenOrderCount; got != 1 {
		t.Fatalf("external must count, got %d", got)
	}
}
```

- [ ] **Step 2: Run** — `go test -race ./state/...` → expect PASS.

---

### Task 10: state/pnl.go — UnrealizedPnL

**Files:**
- Create: `state/pnl.go`
- Create: `state/pnl_test.go`

- [ ] **Step 1: Write `state/pnl_test.go`**

```go
package state

import "testing"

func TestUnrealizedPnL_SkipsUnknownCost(t *testing.T) {
	s := newStateWithBalance(t, 100)
	s.Restore(Snapshot{
		Balance: Balance{Available: 100},
		Position: Position{Tokens: map[string]TokenPosition{
			"tk1": {Available: 10, AvgCost: 0.4, AvgCostKnown: true},
			"tk2": {Available: 10, AvgCost: 0, AvgCostKnown: false},
		}},
	})
	mid := map[string]float64{"tk1": 0.5, "tk2": 0.5}
	got := s.UnrealizedPnL(mid)
	// only tk1 counts: (0.5-0.4)*10 = 1.0
	if got < 0.99 || got > 1.01 {
		t.Fatalf("got %v want ~1.0", got)
	}
}

func TestUnrealizedPnL_EmptyPosition(t *testing.T) {
	s := newStateWithBalance(t, 100)
	if got := s.UnrealizedPnL(nil); got != 0 {
		t.Fatalf("expected 0 got %v", got)
	}
}
```

- [ ] **Step 2: Run** — expect FAIL.

- [ ] **Step 3: Create `state/pnl.go`**

```go
package state

func (s *State) UnrealizedPnL(midPrices map[string]float64) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var pnl float64
	for tokenID, tp := range s.position.Tokens {
		if !tp.AvgCostKnown {
			continue
		}
		mid, ok := midPrices[tokenID]
		if !ok || tp.Available <= 0 {
			continue
		}
		pnl += (mid - tp.AvgCost) * tp.Available
	}
	return pnl
}
```

- [ ] **Step 4: Run** — expect PASS.

---

### Task 11: state/reconcile.go — Polymarket authoritative reconciliation

**Files:**
- Create: `state/reconcile.go`
- Create: `state/reconcile_test.go`
- Modify: `state/types.go` (add `ReconcileReport`, `ReconcileConfig`)

This is the largest task in the plan. Covers spec § 5.4.

- [ ] **Step 1: Add types to `state/types.go`**

```go
type ReconcileReport struct {
	OrdersAdded      int
	OrdersRemoved    int
	OrdersUpdated    int
	PositionsAdded   int
	PositionsRemoved int
	PositionsUpdated int
	DurationMs       int64
	Err              error
}

type ReconcileConfig struct {
	Enabled  bool
	Interval time.Duration
	// RetryBackoff lists wait durations before each retry. Empty → no retry.
	RetryBackoff []time.Duration
	// OnReport is invoked after each reconcile (success or failure). Non-blocking.
	OnReport func(ReconcileReport)
}
```

- [ ] **Step 2: Write `state/reconcile_test.go`**

```go
package state

import (
	"context"
	"errors"
	"testing"
	"time"

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
	fake := &fakeExchangeClient{} // remote has nothing
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

func TestReconcile_FailureRetriesThenReports(t *testing.T) {
	fake := &fakeExchangeClient{err: errors.New("boom")}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)
	rep := s.ReconcileWithExchange(context.Background())
	if rep.Err == nil {
		t.Fatal("expected error report")
	}
}

func TestReconcile_TriggerDeduplicates(t *testing.T) {
	fake := &fakeExchangeClient{}
	s := NewStateWithBalanceSync(BalanceSyncConfig{}, fake)

	// Multiple triggers within tight window → only one reconcile actually runs
	for i := 0; i < 5; i++ {
		s.TriggerReconcile()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	// Drain trigger channel — should have at most 1 pending
	count := 0
drain:
	for {
		select {
		case <-s.reconcileTrigger:
			count++
		case <-ctx.Done():
			break drain
		default:
			break drain
		}
	}
	if count > 1 {
		t.Fatalf("trigger channel should dedupe, got %d", count)
	}
}
```

- [ ] **Step 3: Run** — `go test -run TestReconcile ./state/...` → expect FAIL.

- [ ] **Step 4: Create `state/reconcile.go`**

```go
package state

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

// TriggerReconcile signals a non-periodic reconcile pass. Non-blocking; if
// a trigger is already pending it's coalesced.
func (s *State) TriggerReconcile() {
	select {
	case s.reconcileTrigger <- struct{}{}:
	default:
	}
}

// StartReconcileLoop starts a goroutine that runs ReconcileWithExchange every
// cfg.Interval, plus whenever TriggerReconcile fires. Must be called at most
// once; subsequent calls are no-ops.
func (s *State) StartReconcileLoop(ctx context.Context, cfg ReconcileConfig) {
	if !cfg.Enabled || s.restoreClient == nil {
		return
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runReconcileWithRetry(ctx, cfg)
			case <-s.reconcileTrigger:
				s.runReconcileWithRetry(ctx, cfg)
			}
		}
	}()
}

func (s *State) runReconcileWithRetry(ctx context.Context, cfg ReconcileConfig) {
	rep := s.ReconcileWithExchange(ctx)
	if rep.Err != nil && len(cfg.RetryBackoff) > 0 {
		for _, wait := range cfg.RetryBackoff {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			rep = s.ReconcileWithExchange(ctx)
			if rep.Err == nil {
				break
			}
		}
	}
	if cfg.OnReport != nil {
		cfg.OnReport(rep)
	}
}

// ReconcileWithExchange runs one reconcile pass. Treats Polymarket as the
// authoritative source: local-only orders are released, remote-only orders are
// attached with ExternalOrigin=true, mismatched orders are updated to remote
// values.
func (s *State) ReconcileWithExchange(ctx context.Context) ReconcileReport {
	start := time.Now()
	rep := ReconcileReport{}

	if s == nil || s.restoreClient == nil {
		rep.Err = errReconcileNoClient
		return rep
	}

	openOrders, err := s.restoreClient.GetOpenOrders()
	if err != nil {
		rep.Err = err
		return rep
	}
	positions, err := s.restoreClient.GetPositions()
	if err != nil {
		rep.Err = err
		return rep
	}

	rep.OrdersAdded, rep.OrdersRemoved, rep.OrdersUpdated = s.reconcileOrders(openOrders)
	rep.PositionsAdded, rep.PositionsRemoved, rep.PositionsUpdated = s.reconcilePositions(positions)
	rep.DurationMs = time.Since(start).Milliseconds()
	return rep
}

var errReconcileNoClient = errReconcile("no exchange client")

type errReconcile string

func (e errReconcile) Error() string { return string(e) }

func (s *State) reconcileOrders(remote []orders.OpenOrder) (added, removed, updated int) {
	remoteByID := make(map[string]orders.OpenOrder, len(remote))
	for _, o := range remote {
		id := strings.TrimSpace(o.Id)
		if id == "" {
			continue
		}
		remoteByID[id] = o
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureTokenPositions()

	// 1. local-only → release
	for orderID, local := range s.orderReservations {
		if _, ok := remoteByID[orderID]; !ok {
			s.releaseOrderLocked(orderID, local)
			removed++
		}
	}

	// 2. remote-only or remote-mismatch → upsert
	for orderID, ro := range remoteByID {
		remaining := math.Max(0, ro.OriginalSize-ro.SizeMatched)
		if remaining <= 0 {
			continue
		}
		local, exists := s.orderReservations[orderID]
		if !exists {
			// add as external
			if err := s.attachExternalLocked(orderID, ro.Market, ro.AssetId,
				orders.Side(ro.Side), ro.Price, remaining); err == nil {
				added++
			}
			continue
		}
		if math.Abs(local.RemainingSize-remaining) > 1e-9 || math.Abs(local.Price-ro.Price) > 1e-9 {
			local.RemainingSize = remaining
			local.Price = ro.Price
			s.orderReservations[orderID] = local
			updated++
		}
	}
	return
}

func (s *State) releaseOrderLocked(orderID string, r OrderReservation) {
	switch r.Side {
	case orders.BUY:
		s.balance.Reserved -= r.Reserved
		s.balance.Available += r.Reserved
		if s.balance.Reserved < 0 {
			s.balance.Reserved = 0
		}
	case orders.SELL:
		k := tokenKey(r.TokenID)
		tp := s.position.Tokens[k]
		tp.Reserved -= r.Reserved
		tp.Available += r.Reserved
		if tp.Reserved < 0 {
			tp.Reserved = 0
		}
		s.position.Tokens[k] = tp
	}
	delete(s.orderReservations, orderID)
}

func (s *State) attachExternalLocked(orderID, marketID, tokenID string,
	side orders.Side, price, remainingSize float64) error {
	// Inlined essentials of AttachExternalOrder (already holding mu).
	if orderID == "" || marketID == "" || tokenID == "" || remainingSize <= 0 || price <= 0 || price >= 1 {
		return errReconcile("invalid external order")
	}
	if _, exists := s.orderReservations[orderID]; exists {
		return nil
	}
	// no balance check on external orders: they're already on-chain
	switch side {
	case orders.BUY:
		reserved := price * remainingSize
		s.balance.Available -= reserved
		s.balance.Reserved += reserved
		s.orderReservations[orderID] = OrderReservation{
			OrderID: orderID, MarketID: marketID, TokenID: tokenID,
			Side: side, Price: price, RemainingSize: remainingSize,
			Reserved: reserved, ExternalOrigin: true,
		}
	case orders.SELL:
		k := tokenKey(tokenID)
		tp := s.position.Tokens[k]
		tp.Available -= remainingSize
		tp.Reserved += remainingSize
		s.position.Tokens[k] = tp
		s.orderReservations[orderID] = OrderReservation{
			OrderID: orderID, MarketID: marketID, TokenID: tokenID,
			Side: side, Price: price, RemainingSize: remainingSize,
			Reserved: remainingSize, ExternalOrigin: true,
		}
	}
	return nil
}
```

- [ ] **Step 5: Implement `reconcilePositions`** — analogous to orders but on `s.position.Tokens`:

```go
func (s *State) reconcilePositions(remote *gjson.Result) (added, removed, updated int) {
	if remote == nil {
		return
	}
	remoteByToken := make(map[string]float64)
	items := remote.Array()
	if len(items) == 0 {
		items = remote.Get("data").Array()
	}
	for _, item := range items {
		tokenID := firstNonEmptyJSON(item, "asset", "assetId", "asset_id", "tokenId")
		if tokenID == "" {
			continue
		}
		sz := item.Get("size").Float()
		if sz <= 0 {
			continue
		}
		remoteByToken[tokenID] += sz
	}

	// already inside Lock from caller? No — reconcilePositions is called
	// outside reconcileOrders's lock. Acquire here:
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureTokenPositions()

	// 1. local-only → clear (likely redeemed externally)
	for tk := range s.position.Tokens {
		if _, ok := remoteByToken[tk]; !ok {
			delete(s.position.Tokens, tk)
			removed++
		}
	}
	// 2. remote → add or update
	for tk, sz := range remoteByToken {
		tp, exists := s.position.Tokens[tk]
		if !exists {
			s.position.Tokens[tk] = TokenPosition{Available: sz, AvgCostKnown: false}
			added++
			continue
		}
		// compare available+reserved with remote total
		if math.Abs(tp.Available+tp.Reserved-sz) > 1e-9 {
			// remote is authoritative
			diff := sz - (tp.Available + tp.Reserved)
			tp.Available += diff
			if tp.Available < 0 {
				tp.Available = 0
			}
			s.position.Tokens[tk] = tp
			updated++
		}
	}
	return
}

func firstNonEmptyJSON(item gjson.Result, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(item.Get(k).String()); v != "" {
			return v
		}
	}
	return ""
}
```

> **Lock ordering note**: `reconcileOrders` and `reconcilePositions` are called sequentially; each acquires/releases `s.mu` independently. There is a brief window between them where state is half-updated — acceptable because both writes converge to remote-authoritative.

- [ ] **Step 6: Run** — `go test -race ./state/...` → expect PASS.

---

### Task 12: state/position_expiring.go — ticker + EventPositionExpiring

**Files:**
- Create: `state/position_expiring.go`
- Create: `state/position_expiring_test.go`
- Modify: `core/event.go` (add `PositionExpiringEvent` + `EventPositionExpiring` const)

- [ ] **Step 1: Add to `core/constants.go`**

```go
const EventPositionExpiring EventType = "POSITION_EXPIRING"
```

- [ ] **Step 2: Add to `core/event.go`**

```go
type PositionExpiringEvent struct {
	MarketID  string
	EndTime   int64
	TokenIDs  []string
	Available map[string]float64
}
```

- [ ] **Step 3: Write `state/position_expiring_test.go`**

```go
package state

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/xiangxn/polypilot/core"
)

func TestPositionExpiring_FiresOnce(t *testing.T) {
	s := newStateWithBalance(t, 100)
	bus := core.NewEventBus()
	t.Cleanup(bus.Close)

	endsAt := time.Now().Add(20 * time.Second).UnixMilli()
	s.RegisterMarketExpiry("m1", endsAt, []string{"tk1"})

	var got core.PositionExpiringEvent
	var fired sync.WaitGroup
	fired.Add(1)
	ch, cancel := bus.SubscribeWithCancel()
	t.Cleanup(cancel)

	ctx, ctxCancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(ctxCancel)

	go s.StartPositionExpiringLoop(ctx, bus, 200*time.Millisecond, 30*time.Second)
	go func() {
		defer fired.Done()
		for ev := range ch {
			if ev.Type == core.EventPositionExpiring {
				got = ev.Data.(core.PositionExpiringEvent)
				return
			}
		}
	}()

	fired.Wait()
	if got.MarketID != "m1" {
		t.Fatalf("expected m1, got %+v", got)
	}
}
```

- [ ] **Step 4: Run** — expect FAIL.

- [ ] **Step 5: Create `state/position_expiring.go`**

```go
package state

import (
	"context"
	"sync"
	"time"

	"github.com/xiangxn/polypilot/core"
)

type expiringMarket struct {
	endTime  int64
	tokenIDs []string
	fired    bool
}

type marketExpiryRegistry struct {
	mu      sync.Mutex
	markets map[string]*expiringMarket
}

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

// StartPositionExpiringLoop polls every `tick` interval and publishes
// EventPositionExpiring when an end-time is within `warnBefore`.
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
```

- [ ] **Step 6: Add fields to `State` struct in `state/state.go`**

```go
type State struct {
	// ... existing fields ...
	expiryMu      sync.Mutex
	expiryMarkets map[string]*expiringMarket
}
```

- [ ] **Step 7: Run** — `go test -race ./state/...` → expect PASS.

---

## Phase 2: risk/ package

### Task 13: risk — RejectionType + new Check signature with all hard walls

**Files:**
- Create: `risk/rejection.go`
- Create: `risk/rejection_test.go`
- Modify: `risk/engine.go` (new struct fields + new Check signature)
- Create: `risk/engine_extra_test.go` (slippage, exposure, cooldown, daily-loss, max-open)
- Modify: `runtime/types.go` (`RiskManager.Check` signature)

- [ ] **Step 1: Create `risk/rejection.go`**

```go
package risk

import "fmt"

type RejectionType string

const (
	RejectInsufficientBalance  RejectionType = "INSUFFICIENT_BALANCE"
	RejectBelowMinReserve      RejectionType = "BELOW_MIN_RESERVE"
	RejectInsufficientPosition RejectionType = "INSUFFICIENT_POSITION"
	RejectExposureCap          RejectionType = "EXPOSURE_CAP"
	RejectSlippage             RejectionType = "SLIPPAGE"
	RejectCooldown             RejectionType = "COOLDOWN"
	RejectDailyLoss            RejectionType = "DAILY_LOSS"
	RejectMaxOpenOrders        RejectionType = "MAX_OPEN_ORDERS"
	RejectInvalidIntent        RejectionType = "INVALID_INTENT"
)

type Rejection struct {
	Type   RejectionType
	Detail string
}

func (r *Rejection) Error() string {
	return fmt.Sprintf("%s: %s", r.Type, r.Detail)
}

func reject(t RejectionType, format string, args ...any) *Rejection {
	return &Rejection{Type: t, Detail: fmt.Sprintf(format, args...)}
}
```

- [ ] **Step 2: Modify `runtime/types.go`** — `RiskManager` interface gains `midPrices`:

```go
type RiskManager interface {
	Check(orders []OrderIntent, snapshot state.Snapshot, midPrices map[string]float64) error
}
```

- [ ] **Step 3: Modify `risk/engine.go`** — full rewrite of `Engine`:

```go
package risk

import (
	"sync"
	"time"

	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/runtime"
	"github.com/xiangxn/polypilot/state"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

type Engine struct {
	MaxDailyLoss         float64
	MaxExposurePerMarket float64
	MaxSlippageBps       int
	MaxOpenOrders        int
	MarketCooldown       time.Duration

	mu                  sync.Mutex
	lastIntentPerMarket map[string]time.Time
}

func (r *Engine) Check(intents []runtime.OrderIntent, s state.Snapshot, midPrices map[string]float64) error {
	if len(intents) == 0 {
		return nil
	}

	// Pre-check: daily loss circuit breaker
	if r.MaxDailyLoss > 0 && -s.DailyPnL >= r.MaxDailyLoss {
		// allow CANCEL through, block everything else
		for _, in := range intents {
			if in.Action != runtime.OrderIntentActionCancel {
				return reject(RejectDailyLoss, "daily loss %.2f exceeds %.2f", -s.DailyPnL, r.MaxDailyLoss)
			}
		}
	}

	now := time.Now()
	var buyRequired float64
	sellRequiredByToken := make(map[string]float64)
	exposurePerMarket := make(map[string]float64)
	placeCount := 0

	for _, o := range intents {
		action := o.Action
		if action == "" {
			action = runtime.OrderIntentActionPlace
		}

		if action == runtime.OrderIntentActionCancel {
			if o.OrderID == "" {
				return reject(RejectInvalidIntent, "empty cancel order id")
			}
			continue
		}

		// PLACE / SPLIT / MERGE share validation
		if o.MarketID == "" {
			return reject(RejectInvalidIntent, "empty market id")
		}
		if o.Size <= 0 {
			return reject(RejectInvalidIntent, "invalid size %v", o.Size)
		}

		switch action {
		case runtime.OrderIntentActionPlace:
			placeCount++
			if o.TokenID == "" {
				return reject(RejectInvalidIntent, "empty token id")
			}
			if o.Price <= 0 || o.Price >= 1 {
				return reject(RejectInvalidIntent, "invalid price %v", o.Price)
			}

			// slippage check: only if midPrices has a value
			if r.MaxSlippageBps > 0 {
				if mid, ok := midPrices[o.TokenID]; ok && mid > 0 {
					deviationBps := int(((o.Price - mid) / mid) * 10000)
					if deviationBps < 0 {
						deviationBps = -deviationBps
					}
					if deviationBps > r.MaxSlippageBps {
						return reject(RejectSlippage, "price %v deviates from mid %v by %d bps (cap %d)",
							o.Price, mid, deviationBps, r.MaxSlippageBps)
					}
				}
			}

			switch o.Side {
			case orders.BUY:
				buyRequired += core.RequiredCollateral(o.Side, o.Price, o.Size)
			case orders.SELL:
				sellRequiredByToken[o.TokenID] += core.RequiredCollateral(o.Side, o.Price, o.Size)
			default:
				return reject(RejectInvalidIntent, "invalid side %v", o.Side)
			}
			exposurePerMarket[o.MarketID] += core.RequiredCollateral(o.Side, o.Price, o.Size)

		case runtime.OrderIntentActionSplit, runtime.OrderIntentActionMerge:
			if len(o.Tokens) != 2 {
				return reject(RejectInvalidIntent, "split/merge needs exactly 2 tokens")
			}
			if action == runtime.OrderIntentActionSplit {
				buyRequired += o.Size
			} else {
				for _, t := range o.Tokens {
					sellRequiredByToken[t] += o.Size
				}
			}
		default:
			return reject(RejectInvalidIntent, "unsupported action %v", action)
		}
	}

	// cooldown (per-market, only on local PLACE)
	if r.MarketCooldown > 0 && placeCount > 0 {
		r.mu.Lock()
		if r.lastIntentPerMarket == nil {
			r.lastIntentPerMarket = make(map[string]time.Time)
		}
		for _, o := range intents {
			if o.Action != "" && o.Action != runtime.OrderIntentActionPlace {
				continue
			}
			if last, ok := r.lastIntentPerMarket[o.MarketID]; ok {
				if now.Sub(last) < r.MarketCooldown {
					r.mu.Unlock()
					return reject(RejectCooldown, "market %s within cooldown %v", o.MarketID, r.MarketCooldown)
				}
			}
		}
		// passed: record now (after all checks succeed; revisited at end below)
		r.mu.Unlock()
	}

	// max open orders (includes ExternalOrigin via Snapshot)
	if r.MaxOpenOrders > 0 && s.OpenOrderCount+placeCount > r.MaxOpenOrders {
		return reject(RejectMaxOpenOrders, "would exceed max open orders %d (have %d, adding %d)",
			r.MaxOpenOrders, s.OpenOrderCount, placeCount)
	}

	// per-market exposure (existing + new)
	if r.MaxExposurePerMarket > 0 {
		existing := make(map[string]float64)
		for _, ord := range s.Orders {
			existing[ord.MarketID] += ord.Reserved
		}
		for mkt, add := range exposurePerMarket {
			if existing[mkt]+add > r.MaxExposurePerMarket+core.FloatEpsilon {
				return reject(RejectExposureCap, "market %s exposure %.2f exceeds cap %.2f",
					mkt, existing[mkt]+add, r.MaxExposurePerMarket)
			}
		}
	}

	// balance + min reserve
	if buyRequired > 0 {
		if s.Balance.Available <= s.Balance.MinBalance+core.FloatEpsilon {
			return reject(RejectBelowMinReserve, "min %.2f have %.2f", s.Balance.MinBalance, s.Balance.Available)
		}
		if s.Balance.Available+core.FloatEpsilon < buyRequired {
			return reject(RejectInsufficientBalance, "need %.2f have %.2f", buyRequired, s.Balance.Available)
		}
		if s.Balance.Available-buyRequired <= s.Balance.MinBalance+core.FloatEpsilon {
			return reject(RejectBelowMinReserve, "post-order would drop below min %.2f", s.Balance.MinBalance)
		}
	}

	// per-token position
	for tokenID, requiredSize := range sellRequiredByToken {
		avail := s.Position.Tokens[tokenID].Available
		if avail < requiredSize {
			return reject(RejectInsufficientPosition, "token %s need %.4f have %.4f", tokenID, requiredSize, avail)
		}
	}

	// success: record cooldown timestamps
	if r.MarketCooldown > 0 {
		r.mu.Lock()
		for _, o := range intents {
			if o.Action != "" && o.Action != runtime.OrderIntentActionPlace {
				continue
			}
			r.lastIntentPerMarket[o.MarketID] = now
		}
		r.mu.Unlock()
	}

	return nil
}
```

- [ ] **Step 4: Update existing `risk/engine_test.go`** — replace `r.Check(intents, snap)` with `r.Check(intents, snap, nil)`. Existing assertions should still pass.

- [ ] **Step 5: Write new `risk/engine_extra_test.go`** with one test per RejectionType:

```go
package risk

import (
	"errors"
	"testing"
	"time"

	"github.com/xiangxn/polypilot/runtime"
	"github.com/xiangxn/polypilot/state"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

func mustReject(t *testing.T, err error, want RejectionType) {
	t.Helper()
	var rej *Rejection
	if !errors.As(err, &rej) {
		t.Fatalf("expected *Rejection, got %v", err)
	}
	if rej.Type != want {
		t.Fatalf("got %s want %s", rej.Type, want)
	}
}

func TestReject_Slippage(t *testing.T) {
	r := &Engine{MaxSlippageBps: 100}
	mids := map[string]float64{"tk1": 0.5}
	err := r.Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk1", Price: 0.6, Size: 1, Side: orders.BUY,
	}}, state.Snapshot{Balance: state.Balance{Available: 100}}, mids)
	mustReject(t, err, RejectSlippage)
}

func TestReject_ExposureCap(t *testing.T) {
	r := &Engine{MaxExposurePerMarket: 10}
	snap := state.Snapshot{Balance: state.Balance{Available: 100}}
	err := r.Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk1", Price: 0.5, Size: 30, Side: orders.BUY,
	}}, snap, nil)
	mustReject(t, err, RejectExposureCap)
}

func TestReject_MaxOpenOrders(t *testing.T) {
	r := &Engine{MaxOpenOrders: 1}
	snap := state.Snapshot{
		Balance:        state.Balance{Available: 100},
		OpenOrderCount: 1,
	}
	err := r.Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk1", Price: 0.5, Size: 5, Side: orders.BUY,
	}}, snap, nil)
	mustReject(t, err, RejectMaxOpenOrders)
}

func TestReject_DailyLoss(t *testing.T) {
	r := &Engine{MaxDailyLoss: 5}
	snap := state.Snapshot{
		Balance:  state.Balance{Available: 100},
		DailyPnL: -10,
	}
	err := r.Check([]runtime.OrderIntent{{
		MarketID: "m", TokenID: "tk1", Price: 0.5, Size: 1, Side: orders.BUY,
	}}, snap, nil)
	mustReject(t, err, RejectDailyLoss)
}

func TestReject_Cooldown(t *testing.T) {
	r := &Engine{MarketCooldown: 100 * time.Millisecond}
	snap := state.Snapshot{Balance: state.Balance{Available: 100}}
	in := []runtime.OrderIntent{{MarketID: "m", TokenID: "tk1", Price: 0.5, Size: 1, Side: orders.BUY}}
	if err := r.Check(in, snap, nil); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := r.Check(in, snap, nil); err == nil {
		t.Fatalf("second within cooldown should reject")
	} else {
		mustReject(t, err, RejectCooldown)
	}
}

func TestCancel_StillAllowedDuringDailyLoss(t *testing.T) {
	r := &Engine{MaxDailyLoss: 5}
	snap := state.Snapshot{Balance: state.Balance{Available: 100}, DailyPnL: -10}
	err := r.Check([]runtime.OrderIntent{{
		Action: runtime.OrderIntentActionCancel, OrderID: "o1",
	}}, snap, nil)
	if err != nil {
		t.Fatalf("cancel should be allowed during daily-loss lock, got %v", err)
	}
}
```

- [ ] **Step 6: Run** — `go test -race ./risk/...` → expect PASS.

---

## Phase 3: probability/ package

### Task 14: probability split + NewEngine(client) + fix log module name

**Files:**
- Modify: `probability/engine.go` (already mu-locked from race fix; now: NewEngine constructor, fix log name, slim down by moving methods)
- Create: `probability/market_state.go` (move `resetForNewMarketLocked` + RPC-out-of-lock refactor)
- Create: `probability/features.go` (move `fillFeaturesLocked`)
- Create: `probability/book_store.go` (move `GetOrderBook`/`getBook`/`updateOrderBook`)
- Create: `probability/market_state_test.go`
- Create: `probability/features_test.go`
- Create: `probability/book_store_test.go`

- [ ] **Step 1: Fix log module name** in `probability/engine.go`:

```go
var log = logx.Module("probability")
```

- [ ] **Step 2: Add `NewEngine` constructor**

```go
// NewEngine constructs a probability Engine using the provided Polymarket SDK
// client. The client is used during market resets to fetch order books and
// open prices instead of constructing a new client each time.
func NewEngine(client *sdk.PolymarketClient) *Engine {
	return &Engine{client: client}
}
```

Add `client *sdk.PolymarketClient` field to `Engine` struct.

- [ ] **Step 3: Move `resetForNewMarketLocked` → `probability/market_state.go`** with RPC-out-of-lock refactor:

```go
package probability

import (
	"sync/atomic"
	"time"

	"github.com/xiangxn/polypilot/runtime"

	"github.com/tidwall/gjson"
	"github.com/xiangxn/go-polymarket-sdk/orders"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
	"github.com/xiangxn/go-polymarket-sdk/utils"
)

// resetForNewMarketLocked rebuilds market/token state for a new market.
// Caller must hold e.mu.Lock. RPC calls are made BEFORE the lock is acquired
// by the caller via the prepareReset helper.
func (e *Engine) resetForNewMarketLocked(obj gjson.Result, prep *resetPrep) (runtime.Observation, bool) {
	if prep == nil {
		return runtime.Observation{}, false
	}
	e.signal.latestZ.Store(0)
	e.token.items = make(map[string]runtime.Token, 2)

	e.market.endTime = prep.endTime
	e.market.tokenIDs = prep.tokenIDs
	e.market.openPrice = prep.openPrice
	e.market.raw = &obj
	e.generation.Add(1)

	for _, o := range prep.books {
		ap, bp := 0.0, 0.0
		if len(o.Asks) > 0 {
			ap = o.Asks[len(o.Asks)-1].Price
		}
		if len(o.Bids) > 0 {
			bp = o.Bids[len(o.Bids)-1].Price
		}
		e.token.items[o.AssetId] = runtime.Token{Id: o.AssetId, AskPrice: ap, BidPrice: bp}
		e.updateOrderBook(o.AssetId, func(old *sdk.OrderBook) *sdk.OrderBook {
			return &sdk.OrderBook{
				AssetId:   o.AssetId,
				Market:    o.Market,
				Timestamp: o.Timestamp,
				Asks:      append([]orders.Book(nil), o.Asks...),
				Bids:      append([]orders.Book(nil), o.Bids...),
			}
		})
	}

	if e.signal.zWindows != nil {
		e.signal.zWindows.Reset()
	}

	obs := runtime.Observation{
		At:          time.Now().Unix(),
		MarketID:    obj.Get("conditionId").String(),
		Tokens:      CopyMap(e.token.items),
		TokenIds:    append([]string(nil), prep.tokenIDs...),
		TimeLeftSec: prep.endTime/1000 - time.Now().Unix(),
	}
	return obs, true
}

type resetPrep struct {
	endTime   int64
	openPrice float64
	tokenIDs  []string
	books     []sdk.OrderBook
}

// prepareReset performs all RPC calls outside the engine lock. Returns nil
// if the market is invalid (caller should leave state unchanged).
func (e *Engine) prepareReset(obj gjson.Result) *resetPrep {
	tokenIDs := utils.GetStringArray(&obj, "clobTokenIds")
	if len(tokenIDs) < 2 {
		return nil
	}
	endTime, err := utils.ToTimestamp(obj.Get("endDate").String())
	if err != nil {
		endTime = 0
	}
	client := e.client
	if client == nil {
		// fallback for tests / legacy callers
		client = sdk.NewClient(sdk.DefaultConfig())
	}
	books, err := client.GetOrderBooks([]sdk.BookParams{
		{TokenId: tokenIDs[0]}, {TokenId: tokenIDs[1]},
	})
	if err != nil {
		return nil
	}
	cpm := sdk.NewCryptoPriceMonitor(client, sdk.MonitorChainlink, "btc")
	openPrice := cpm.FetchOpenPrice(&obj)
	if openPrice == 0 {
		return nil
	}
	return &resetPrep{endTime: endTime, openPrice: openPrice, tokenIDs: tokenIDs, books: books}
}

// generation counter used to detect concurrent resets
var _ atomic.Uint64 = atomic.Uint64{}
```

Add `generation atomic.Uint64` to `Engine` struct; the OnUpdate EventMarket path becomes:

```go
case core.EventMarket:
	obj, ok := ev.Data.(gjson.Result)
	if !ok {
		return runtime.Observation{}, false
	}
	conditionID := obj.Get("conditionId").String()

	e.mu.RLock()
	needReset := e.market.raw == nil || conditionID != e.market.raw.Get("conditionId").String() || e.market.openPrice == 0
	gen := e.generation.Load()
	e.mu.RUnlock()

	if !needReset {
		return runtime.Observation{}, false
	}
	prep := e.prepareReset(obj) // RPC outside lock
	if prep == nil {
		return runtime.Observation{}, false
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.generation.Load() != gen {
		// another reset happened while we were doing RPC — let the next event win
		return runtime.Observation{}, false
	}
	return e.resetForNewMarketLocked(obj, prep)
```

- [ ] **Step 4: Move `fillFeaturesLocked` → `probability/features.go`** (verbatim, no logic change).

- [ ] **Step 5: Move `GetOrderBook`/`getBook`/`updateOrderBook` → `probability/book_store.go`** (verbatim).

- [ ] **Step 6: Write `probability/features_test.go`**

```go
package probability

import (
	"testing"

	"github.com/xiangxn/polypilot/runtime"

	"github.com/tidwall/gjson"
)

func TestFillFeatures_PopulatesStandardKeys(t *testing.T) {
	e := &Engine{}
	raw := gjson.Parse(`{"conditionId":"c1"}`)
	e.market.raw = &raw
	e.market.openPrice = 100
	e.market.tokenIDs = []string{"tk1", "tk2"}

	var obs runtime.Observation
	e.fillFeaturesLocked(&obs)
	for _, key := range []string{"latestZ", "openPrice", "latestPrice", "endTime", "diffPrice", "imBalance"} {
		if _, ok := obs.Features[key]; !ok {
			t.Fatalf("missing key %s", key)
		}
	}
}
```

- [ ] **Step 7: Write `probability/book_store_test.go`**

```go
package probability

import (
	"sync"
	"testing"

	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

func TestUpdateOrderBook_ConcurrentRace(t *testing.T) {
	e := &Engine{}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				e.updateOrderBook("tk1", func(old *sdk.OrderBook) *sdk.OrderBook {
					return &sdk.OrderBook{AssetId: "tk1"}
				})
				_ = e.GetOrderBook("tk1")
			}
		}(i)
	}
	wg.Wait()
}

func TestGetOrderBook_StaleReturnsNil(t *testing.T) {
	e := &Engine{}
	e.updateOrderBook("tk1", func(old *sdk.OrderBook) *sdk.OrderBook {
		return &sdk.OrderBook{AssetId: "tk1", Latency: 9999}
	})
	if e.GetOrderBook("tk1") != nil {
		t.Fatal("stale book should return nil")
	}
}
```

- [ ] **Step 8: Run** — `go test -race ./probability/...` → expect PASS (including existing race test from prior commit).

---

## Phase 4: execution/ package

### Task 15: execution split + RelayClient field + unknown-orderID reconcile trigger

**Files:**
- Modify: `execution/executor.go` (move methods out, keep struct + Init/Execute)
- Create: `execution/placements.go` (submitPlacements + handlePostOrdersResults)
- Create: `execution/splits_merges.go` (submitSplits + submitMerges; use cached relayClient)
- Create: `execution/trade_events.go` (onOrderEvent + onTradeEvent + unknown-orderID → reconcile trigger)
- Modify: `execution/executor.go` (add `Reconcile func()` callback field)
- Create: `execution/trade_events_test.go`

- [ ] **Step 1: Add `RelayClient` + `Reconcile` fields to `Executor`** in `execution/executor.go`:

```go
type Executor struct {
	Bus *core.EventBus

	Client       *sdk.PolymarketClient
	TradeMonitor *sdk.TradeMonitor
	Config       *sdk.Config
	OrderType    orders.OrderType
	DeferExec    bool
	DryRun       bool

	// Reconcile is invoked when the executor detects an unknown order
	// (an event with an orderID not previously tracked, e.g. a manual order
	// placed on Polymarket). State will then trigger a reconcile pass.
	Reconcile func()

	relayClient        sdk.RelayerClient
	ExecutionQueueSize int

	startOnce  sync.Once
	workerOnce sync.Once
	mu         sync.Mutex
	tracked    map[string]*trackedOrder
	queue      chan []runtime.OrderIntent
}
```

> Define `sdk.RelayerClient` interface in `execution/executor.go` (small interface, accept-interface principle):

```go
type sdkRelayClient interface {
	SplitTokens(marketID, amount string, defer_ bool) (*relayer.RelayResult, error)
	MergeTokens(marketID, amount string, defer_ bool) (*relayer.RelayResult, error)
}
```

(Adjust to actual `relayer.NewRelayClient` return type.)

- [ ] **Step 2: Build relayClient once in `Executor.Init`**

```go
e.startOnce.Do(func() {
	cfg := e.Config
	if cfg == nil {
		cfg = sdk.DefaultConfig()
	}
	if e.Client == nil {
		e.Client = sdk.NewClient(cfg)
	}
	if e.relayClient == nil {
		p := cfg.Polymarket
		e.relayClient = relayer.NewRelayClient(p.RelayerBaseURL, p.OwnerKey, p.ChainID, p.BuilderCreds, nil, p.RelayerKey)
	}
	// ... existing TradeMonitor init ...
})
```

- [ ] **Step 3: Move `submitPlacements` + `handlePostOrdersResults` → `execution/placements.go`** (verbatim move, just relocate methods).

- [ ] **Step 4: Move `submitSplits` + `submitMerges` → `execution/splits_merges.go`** and replace per-iteration `relayer.NewRelayClient` with `e.relayClient`:

```go
// inside submitSplits loop:
result, err := e.relayClient.SplitTokens(intent.MarketID, strconv.FormatFloat(size, 'f', constants.CollateralTokenDecimals, 64), false)
// inside submitMerges loop:
result, err := e.relayClient.MergeTokens(intent.MarketID, strconv.FormatFloat(size, 'f', constants.CollateralTokenDecimals, 64), false)
```

Delete the `pcfg := e.Config.Polymarket` and `relayer.NewRelayClient(...)` calls inside the loops.

- [ ] **Step 5: Move `consumeTradeEvents`/`handleTradeEvent`/`onOrderEvent`/`onTradeEvent` → `execution/trade_events.go`**, plus add unknown-orderID reconcile trigger:

```go
func (e *Executor) onOrderEvent(o *model.WSOrder) {
	if o == nil || strings.TrimSpace(o.Id) == "" || !e.isOwnOwner(o.Owner) {
		return
	}
	// Detect unknown orderID → trigger reconcile
	e.mu.Lock()
	_, known := e.tracked[o.Id]
	e.mu.Unlock()
	if !known && e.Reconcile != nil {
		e.Reconcile()
	}
	// ... existing handling ...
}
```

Apply the same `if !known { e.Reconcile() }` pattern at the start of `onTradeEvent` for each fill where the orderID is not in `e.tracked`.

- [ ] **Step 6: Write `execution/trade_events_test.go`**

```go
package execution

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/xiangxn/polypilot/core"

	sdkmodel "github.com/xiangxn/go-polymarket-sdk/model"
)

func TestOnOrderEvent_UnknownOrderTriggersReconcile(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	var fired atomic.Int32
	exec := &Executor{
		Bus: bus, tracked: make(map[string]*trackedOrder),
		Reconcile: func() { fired.Add(1) },
	}
	exec.onOrderEvent(&sdkmodel.WSOrder{
		Id: "external-1", Market: "m", AssetId: "tk1", Side: "BUY", Price: 0.5,
		OriginalSize: 5, Status: "LIVE", Timestamp: time.Now().Unix(),
		Owner: "", // bypass owner check via override below
	})
	// because ownKey is empty when Config is nil, isOwnOwner returns true
	if fired.Load() != 1 {
		t.Fatalf("expected reconcile fired once, got %d", fired.Load())
	}
}
```

- [ ] **Step 7: Run** — `go test -race ./execution/...` → expect PASS.

---

### Task 16: execution — DryRun + shutdown drain

**Files:**
- Modify: `execution/executor.go` (Execute branches on DryRun; consumeExecuteQueue drains on ctx)
- Create: `execution/dryrun_test.go`
- Create: `execution/shutdown_test.go`

- [ ] **Step 1: Modify `Execute`** to short-circuit when `DryRun=true`:

```go
func (e *Executor) Execute(intents []runtime.OrderIntent) {
	if len(intents) == 0 {
		return
	}
	if e.DryRun {
		now := time.Now()
		for _, in := range intents {
			if in.Action == runtime.OrderIntentActionCancel {
				continue
			}
			orderID := fmt.Sprintf("dryrun-%d-%s", time.Now().UnixNano(), in.TokenID)
			e.publish(core.ExecutionEvent{
				ParentOrderID: in.IntentID, OrderID: orderID,
				MarketID: in.MarketID, TokenID: in.TokenID,
				Price: in.Price, Side: in.Side, RequestedSize: in.Size,
				Status: core.ExecutionStatusAccepted, At: now,
			})
			e.publish(core.ExecutionEvent{
				ParentOrderID: in.IntentID, OrderID: orderID,
				MarketID: in.MarketID, TokenID: in.TokenID,
				Price: in.Price, Side: in.Side, RequestedSize: in.Size,
				FilledSize: in.Size, Status: core.ExecutionStatusFilled, At: now,
			})
		}
		return
	}
	// ... existing validation + queue submission ...
}
```

- [ ] **Step 2: Modify `consumeExecuteQueue`** to drain queue on ctx.Done():

```go
func (e *Executor) consumeExecuteQueue(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			// drain
			for {
				select {
				case batch := <-e.queue:
					e.rejectBatch(batch, "shutting down")
				default:
					return
				}
			}
		case batch := <-e.queue:
			if len(batch) == 0 {
				continue
			}
			// ... existing dispatch ...
		}
	}
}
```

- [ ] **Step 3: Write `execution/dryrun_test.go`**

```go
package execution

import (
	"context"
	"testing"
	"time"

	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/runtime"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

func TestExecute_DryRun_PublishesAcceptedThenFilled(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()
	exec := &Executor{Bus: bus, DryRun: true}
	exec.Init(bus, context.Background())

	exec.Execute([]runtime.OrderIntent{{
		MarketID: "m1", TokenID: "tk1", Price: 0.4, Size: 5, Side: orders.BUY,
	}})

	got := []core.ExecutionStatus{}
	for i := 0; i < 2; i++ {
		select {
		case ev := <-ch:
			got = append(got, ev.Data.(core.ExecutionEvent).Status)
		case <-time.After(time.Second):
			t.Fatalf("timeout: got %v", got)
		}
	}
	if got[0] != core.ExecutionStatusAccepted || got[1] != core.ExecutionStatusFilled {
		t.Fatalf("got %v", got)
	}
}
```

- [ ] **Step 4: Write `execution/shutdown_test.go`**

```go
package execution

import (
	"context"
	"testing"
	"time"

	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/runtime"

	"github.com/xiangxn/go-polymarket-sdk/orders"
	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
)

func TestShutdown_DrainsQueueAndRejects(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	ch := bus.Subscribe()

	ctx, cancel := context.WithCancel(context.Background())
	exec := &Executor{Bus: bus, Client: &sdk.PolymarketClient{}, ExecutionQueueSize: 4}
	exec.Init(bus, ctx)

	// stuff queue with intents that would block on a real client; we cancel immediately
	for i := 0; i < 4; i++ {
		exec.queue <- []runtime.OrderIntent{{
			Action: runtime.OrderIntentActionPlace,
			MarketID: "m", TokenID: "tk1", Price: 0.5, Size: 1, Side: orders.BUY,
		}}
	}
	cancel()

	deadline := time.After(2 * time.Second)
	rejected := 0
	for rejected < 4 {
		select {
		case ev := <-ch:
			if ev.Data.(core.ExecutionEvent).Status == core.ExecutionStatusRejected {
				rejected++
			}
		case <-deadline:
			t.Fatalf("expected 4 rejects, got %d", rejected)
		}
	}
}
```

- [ ] **Step 5: Run** — `go test -race ./execution/...` → expect PASS.

---

## Phase 5: strategy/ + market/

### Task 17: strategy — Features safety + viper.Sub nil + MarketQueue value + OnPositionExpiring

**Files:**
- Modify: `strategy/strategy.go`
- Modify: `strategy/market_queue.go`
- Create: `strategy/event_position_expiring.go`
- Create: `strategy/strategy_test.go`
- Create: `strategy/market_queue_test.go`
- Create: `strategy/event_position_expiring_test.go`

- [ ] **Step 1: Replace MarketQueue to store value (not *gjson.Result)**

```go
// strategy/market_queue.go

package strategy

import (
	"sync"

	"github.com/xiangxn/polypilot/market"
)

type MarketQueue struct {
	mu    sync.RWMutex
	m     map[string]market.SlugMarket
	queue []string
	max   int
}

func NewMarketQueue(max int) *MarketQueue {
	if max <= 0 {
		max = 3
	}
	return &MarketQueue{
		m:     make(map[string]market.SlugMarket, max),
		queue: make([]string, 0, max),
		max:   max,
	}
}

func (c *MarketQueue) Add(marketID string, info market.SlugMarket) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.m[marketID]; ok {
		c.m[marketID] = info
		return
	}
	if len(c.queue) >= c.max {
		oldest := c.queue[0]
		c.queue = c.queue[1:]
		delete(c.m, oldest)
	}
	c.queue = append(c.queue, marketID)
	c.m[marketID] = info
}

func (c *MarketQueue) Get(marketID string) (market.SlugMarket, bool) {
	c.mu.RLock()
	info, ok := c.m[marketID]
	c.mu.RUnlock()
	return info, ok
}
```

> **Caller breakage**: `Strategy.OnUpdate` previously called `s.markets.Add(o.MarketID, &obj)` with a gjson pointer. Now the strategy needs a `market.SlugMarket` value. Since `OnUpdate` does have access to the raw `gjson.Result`, parse it into `SlugMarket` inline or pull from `Observation.Features["market"]`. For minimal disruption, **add a helper in `market/` to convert `gjson.Result → SlugMarket`** named `market.ParseSlugMarket(obj gjson.Result) (market.SlugMarket, error)`, then call it from Strategy.

- [ ] **Step 2: Update `strategy/strategy.go`**

Fix `Init` viper.Sub nil check:

```go
func (s *Strategy) Init(bus *core.EventBus, ctx context.Context, cfg *viper.Viper) {
	s.Bus = bus
	sc := DefaultStrategyConfig()
	if cfg != nil {
		if sub := cfg.Sub("strategies.strategy"); sub != nil {
			if err := sub.Unmarshal(&sc); err != nil {
				log.Warn().Err(err).Msg("strategy config unmarshal failed; using defaults")
			}
		}
	}
	s.config = sc
	cap := sc.MarketQueueCap
	if cap <= 0 {
		cap = 3
	}
	s.markets = NewMarketQueue(cap)
}
```

Add `MarketQueueCap int` to `StrategyConfig`; default to 3.

Delete the unused `PlacePrice = 0.35` constant.

Add type-assertion safety to `OnUpdate` EventOrderBook branch — replace each unchecked `.(float64)` with:

```go
openPrice, ok := o.Features["openPrice"].(float64)
if !ok || openPrice <= 0 {
	return nil
}
latestPrice, _ := o.Features["latestPrice"].(float64)
latestZ, _ := o.Features["latestZ"].(float64)
zWindows, _ := o.Features["zWindows"].([]float64)
```

Replace `tokenKeys := utils.GetStringArray(market, "clobTokenIds")` in `OnExecution` with `tokenKeys := info.TokenIDs` (using `SlugMarket.TokenIDs` field, since `markets.Get` now returns value not pointer).

- [ ] **Step 3: Create `strategy/event_position_expiring.go`** (handler for the new event):

```go
package strategy

import (
	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/runtime"
	"github.com/xiangxn/polypilot/state"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

// OnPositionExpiring implements runtime.ExecutionAwareStrategy-style hook (loose
// coupling; runtime.Engine should dispatch EventPositionExpiring through this).
//
// Behavior: for every token with Available > 0 in the expiring market, emit a
// MARKET_FAK SELL intent and cancel any standing PLACE orders on those tokens.
func (s *Strategy) OnPositionExpiring(ev core.PositionExpiringEvent, snap state.Snapshot) []runtime.OrderIntent {
	out := make([]runtime.OrderIntent, 0, len(ev.TokenIDs)*2)
	for _, tk := range ev.TokenIDs {
		avail, _ := ev.Available[tk]
		if avail > 0 {
			out = append(out, runtime.OrderIntent{
				MarketID: ev.MarketID,
				TokenID:  tk,
				Price:    s.config.InPrice,
				Side:     orders.SELL,
				Size:     avail,
			})
		}
		for _, oid := range BuildCancelIntent(tk, snap.Orders) {
			out = append(out, runtime.OrderIntent{
				Action:  runtime.OrderIntentActionCancel,
				OrderID: oid,
			})
		}
	}
	return out
}
```

- [ ] **Step 4: Write tests** — covering MarketQueue LRU, OnUpdate safety, OnPositionExpiring with both available and no-available cases. Minimum 1 case per behavior. Skeleton:

```go
// strategy/market_queue_test.go
package strategy

import (
	"testing"

	"github.com/xiangxn/polypilot/market"
)

func TestMarketQueue_LRU(t *testing.T) {
	q := NewMarketQueue(2)
	q.Add("m1", market.SlugMarket{Slug: "1"})
	q.Add("m2", market.SlugMarket{Slug: "2"})
	q.Add("m3", market.SlugMarket{Slug: "3"})
	if _, ok := q.Get("m1"); ok {
		t.Fatal("m1 should be evicted")
	}
	if _, ok := q.Get("m3"); !ok {
		t.Fatal("m3 should exist")
	}
}
```

```go
// strategy/strategy_test.go
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
```

```go
// strategy/event_position_expiring_test.go
package strategy

import (
	"testing"

	"github.com/xiangxn/polypilot/core"
	"github.com/xiangxn/polypilot/runtime"
	"github.com/xiangxn/polypilot/state"

	"github.com/xiangxn/go-polymarket-sdk/orders"
)

func TestOnPositionExpiring_SellsAvailableAndCancelsOrders(t *testing.T) {
	s := &Strategy{config: DefaultStrategyConfig()}
	ev := core.PositionExpiringEvent{
		MarketID:  "m1",
		TokenIDs:  []string{"tk1"},
		Available: map[string]float64{"tk1": 5},
	}
	snap := state.Snapshot{
		Orders: map[string]state.OrderReservation{
			"o1": {OrderID: "o1", TokenID: "tk1", Side: orders.BUY},
		},
	}
	got := s.OnPositionExpiring(ev, snap)
	if len(got) != 2 {
		t.Fatalf("expected sell + cancel, got %d", len(got))
	}
	if got[0].Side != orders.SELL || got[0].Size != 5 {
		t.Fatalf("first intent wrong: %+v", got[0])
	}
	if got[1].Action != runtime.OrderIntentActionCancel {
		t.Fatalf("second should be cancel: %+v", got[1])
	}
}
```

- [ ] **Step 5: Run** — `go test -race ./strategy/...` → expect PASS.

---

### Task 18: market/PolymarketSlugFeed — retry + ParseSlugMarket helper + tests

**Files:**
- Modify: `market/polymarket_slug_feed.go`
- Create: `market/polymarket_slug_feed_retry_test.go`

- [ ] **Step 1: Add `ParseSlugMarket` helper**

```go
// market/polymarket_slug_feed.go (append)
func ParseSlugMarket(result gjson.Result) (SlugMarket, error) {
	tokenIDs := utils.GetStringArray(&result, "clobTokenIds")
	if len(tokenIDs) == 0 {
		return SlugMarket{}, fmt.Errorf("no clobTokenIds")
	}
	endDate, _ := utils.ToTimestamp(result.Get("endDate").String())
	startDate, _ := utils.ToTimestamp(result.Get("startDate").String())
	return SlugMarket{
		Slug:             "",
		MarketID:         result.Get("conditionId").String(),
		TokenIDs:         tokenIDs,
		Prices:           utils.GetFloatArray(&result, "outcomePrices"),
		EndDate:          endDate,
		ResolutionSource: result.Get("resolutionSource").String(),
		TickSize:         result.Get("orderPriceMinTickSize").Float(),
		NegRisk:          result.Get("negRisk").Bool(),
		StartDate:        startDate,
		Closed:           result.Get("closed").Bool(),
		Outcomes:         utils.GetStringArray(&result, "outcomes"),
	}, nil
}
```

- [ ] **Step 2: Modify `Start` retry behavior** (don't return on fetch failure; sleep + retry, after N failures publish RISK):

```go
go func() {
	const maxConsecutiveFailures = 6 // 30s window of retries
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		slug := f.slugFor(time.Now())
		_, rawMarket, err := f.FetchMarketBySlug(slug)
		if err != nil {
			failures++
			if failures >= maxConsecutiveFailures {
				f.Bus.Publish(core.Event{
					Type: core.EventRisk,
					Data: core.RiskEvent{
						Reason: fmt.Sprintf("polymarket feed fetch failed %d times slug=%s err=%v", failures, slug, err),
						At:     time.Now(),
					},
				})
				failures = 0
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}
		failures = 0
		f.Bus.Publish(core.Event{Type: core.EventMarket, Data: *rawMarket})
		// ... existing subscribe/loop logic ...
	}
}()
```

- [ ] **Step 3: Write retry test**

```go
// market/polymarket_slug_feed_retry_test.go
package market

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestParseSlugMarket_Minimal(t *testing.T) {
	res := gjson.Parse(`{"conditionId":"c1","clobTokenIds":"[\"tk1\",\"tk2\"]","endDate":"2099-01-01T00:00:00Z"}`)
	sm, err := ParseSlugMarket(res)
	if err != nil {
		t.Fatal(err)
	}
	if sm.MarketID != "c1" || len(sm.TokenIDs) != 2 {
		t.Fatalf("got %+v", sm)
	}
}
```

(The retry control-flow is exercised at integration level. Manual scenario verification covered in spec § 13.)

- [ ] **Step 4: Run** — `go test -race ./market/...` → expect PASS.

---

## Phase 6: runtime/ package

### Task 19: runtime split + errors.Is + AttachOrder integration

**Files:**
- Create: `runtime/event_handler.go`
- Create: `runtime/order_tracking.go`
- Create: `runtime/metrics.go`
- Modify: `runtime/engine.go` (slim down to Start/Close + struct + initConfig)
- Modify: `runtime/types.go` (no change beyond Task 13)

- [ ] **Step 1: Move methods**

Move from `runtime/engine.go`:
- `handleInputUpdate`, `handleExecutionAwareStrategy`, `handleStrategyTick`, `currentObservation`, `submitIntents` → `runtime/event_handler.go`
- `initOrderTracking`, `isFinalized`, `markAccepted`, `hasAccepted`, `bufferExecution`, `replayPending`, `cleanupTracking`, `cleanupExpiredPending`, `cleanupExpiredFinalized`, `finalizeOrder`, `pendingOrderCount`, `restoreOpenOrdersTrackingByIDs`, `cleanupExpiredProvisional`, `nextIntentID` → `runtime/order_tracking.go`
- `publishMetrics`, `publishRisk` → `runtime/metrics.go`

Keep `Start`, `Close`, `initConfig`, `hasTickStrategy`, `handleExecutionEvent` in `runtime/engine.go`.

- [ ] **Step 2: Replace string-error compare with errors.Is**

In `runtime/engine.go` (now in `handleExecutionEvent` after the move), find the line:

```go
if err := e.State.ReserveOrder(...); err != nil && err.Error() != "order already reserved" {
```

Replace with:

```go
if err := e.State.AttachOrder("", data.OrderID, data.MarketID, data.TokenID,
	data.Side, data.Price, data.RequestedSize); err != nil &&
	!errors.Is(err, core.ErrOrderAlreadyReserved) {
	e.publishRisk(fmt.Sprintf("attach failed order=%s reason=%s", data.OrderID, err.Error()))
}
```

Add `"errors"` and `"github.com/xiangxn/polypilot/core"` imports if missing.

Then merge the two Accepted paths (ParentOrderID confirm + fresh reserve) into a single `AttachOrder` call (intentID may be empty for path-B):

```go
case core.ExecutionStatusAccepted:
	e.executionAccepted.Add(1)
	e.markAccepted(data.OrderID)
	err := e.State.AttachOrder(data.ParentOrderID, data.OrderID, data.MarketID, data.TokenID,
		data.Side, data.Price, data.RequestedSize)
	if err != nil && !errors.Is(err, core.ErrOrderAlreadyReserved) {
		e.publishRisk(fmt.Sprintf("attach failed order=%s reason=%s", data.OrderID, err.Error()))
	}
	e.replayPending(data.OrderID)
```

- [ ] **Step 3: Write `runtime/event_handler_test.go`** — copy the existing `engine_provisional_test.go` body but verify the new AttachOrder-based path. The existing tests already cover the equivalent flows; ensure they still pass after the refactor.

- [ ] **Step 4: Run** — `go test -race ./runtime/...` → expect PASS.

---

### Task 20: runtime.submitIntents — midPrices map + new Risk signature

**Files:**
- Modify: `runtime/event_handler.go` (submitIntents constructs midPrices)
- Modify: `runtime/metrics.go` (publishMetrics adds UnrealizedPnL/DailyPnL/ReconcileRuns)
- Modify: `core/event.go` (MetricsEvent new fields)

- [ ] **Step 1: Update `submitIntents` signature and body**

```go
func (e *Engine) submitIntents(intents []OrderIntent, snap state.Snapshot, midPrices map[string]float64) bool {
	if len(intents) == 0 {
		return true
	}
	if err := e.Risk.Check(intents, snap, midPrices); err != nil {
		e.riskRejected.Add(1)
		e.publishRisk(err.Error())
		return false
	}
	// ... rest unchanged ...
}
```

- [ ] **Step 2: Build `midPrices` at the call site (`handleInputUpdate`, `handleExecutionAwareStrategy`, `handleStrategyTick`)**

```go
mids := make(map[string]float64, len(obs.Tokens))
for _, tk := range obs.Tokens {
	if tk.AskPrice > 0 && tk.BidPrice > 0 {
		mids[tk.Id] = (tk.AskPrice + tk.BidPrice) / 2
	}
}
// then pass mids to submitIntents(...)
```

Apply in all three handlers.

- [ ] **Step 3: Add MetricsEvent fields** in `core/event.go`:

```go
type MetricsEvent struct {
	// ... existing ...
	UnrealizedPnL  float64
	DailyPnL       float64
	ReconcileRuns  uint64
	ReconcileDiffs uint64
	At             time.Time
}
```

- [ ] **Step 4: Populate in `publishMetrics`** in `runtime/metrics.go`:

```go
// build mids snapshot from latest probability observation if available
mids := map[string]float64{}
if obs, ok := e.currentObservation(); ok {
	for _, tk := range obs.Tokens {
		if tk.AskPrice > 0 && tk.BidPrice > 0 {
			mids[tk.Id] = (tk.AskPrice + tk.BidPrice) / 2
		}
	}
}
unreal := e.State.UnrealizedPnL(mids)
e.Bus.Publish(core.Event{
	Type: core.EventMetrics,
	Data: core.MetricsEvent{
		// ... existing ...
		UnrealizedPnL:  unreal,
		DailyPnL:       snapshot.DailyPnL,
		ReconcileRuns:  e.reconcileRuns.Load(),
		ReconcileDiffs: e.reconcileDiffs.Load(),
		At:             time.Now().UTC(),
	},
})
```

Add `reconcileRuns`, `reconcileDiffs atomic.Uint64` fields to `runtime.Engine`. Wire these from `state.ReconcileConfig.OnReport` callback in main.

- [ ] **Step 5: Run** — `go test -race ./runtime/...` → expect PASS.

---

## Phase 7: observer/ + config/

### Task 21: observer — type assertion safety + Reconcile/PositionExpiring cases

**Files:**
- Modify: `observer/logger.go`
- Create: `observer/logger_test.go`

- [ ] **Step 1: Modify `observer/logger.go` logEvent**

Replace all unchecked type assertions:

```go
func (l *Logger) logEvent(e core.Event) {
	switch e.Type {
	case core.EventMarket:
		data, ok := e.Data.(gjson.Result)
		if !ok {
			return
		}
		log.Info().Str("event", string(e.Type)).
			Str("question", data.Get("question").String()).
			Str("end_date", data.Get("endDate").String()).
			Msg("observer event")
	case core.EventExecution:
		data, ok := e.Data.(core.ExecutionEvent)
		if !ok {
			return
		}
		// ... existing fields ...
	case core.EventRisk:
		data, ok := e.Data.(core.RiskEvent)
		if !ok {
			return
		}
		// ... existing fields ...
	case core.EventMetrics:
		data, ok := e.Data.(core.MetricsEvent)
		if !ok {
			return
		}
		log.Info().
			// ... existing fields ...
			Float64("unrealized_pnl", data.UnrealizedPnL).
			Float64("daily_pnl", data.DailyPnL).
			Uint64("reconcile_runs", data.ReconcileRuns).
			Uint64("reconcile_diffs", data.ReconcileDiffs).
			Msg("observer event")
	case core.EventPositionExpiring:
		data, ok := e.Data.(core.PositionExpiringEvent)
		if !ok {
			return
		}
		log.Info().Str("event", string(e.Type)).
			Str("market_id", data.MarketID).
			Int64("end_time", data.EndTime).
			Int("tokens", len(data.TokenIDs)).
			Msg("position expiring")
	}
}
```

- [ ] **Step 2: Write `observer/logger_test.go`**

```go
package observer

import (
	"context"
	"testing"

	"github.com/xiangxn/polypilot/core"
)

func TestLogger_NoCrashOnWrongEventPayload(t *testing.T) {
	bus := core.NewEventBus()
	defer bus.Close()
	l := &Logger{}
	l.Init(bus)
	go l.Start(context.Background())

	bus.Publish(core.Event{Type: core.EventMarket, Data: 42}) // wrong type
	bus.Publish(core.Event{Type: core.EventExecution, Data: "not an event"})
	bus.Publish(core.Event{Type: core.EventRisk, Data: nil})
	// no panic = pass
}
```

- [ ] **Step 3: Run** — `go test -race ./observer/...` → expect PASS.

---

### Task 22: config — startup validation + risk/reconcile/redeem schema

**Files:**
- Modify: `config/config.go`
- Create: `config/validation_test.go`

- [ ] **Step 1: Extend `Config` struct**

```go
type Config struct {
	ChainRPCURL string             `mapstructure:"chain_rpc_url"`
	BalanceSync BalanceSyncConfig  `mapstructure:"balance_sync"`
	Logging     logx.LoggingConfig `mapstructure:"logging"`
	SDKConfig   sdk.Config         `mapstructure:"sdk_config"`
	Risk        RiskConfig         `mapstructure:"risk"`
	Reconcile   ReconcileConfig    `mapstructure:"reconcile"`
	Redeem      RedeemConfig       `mapstructure:"redeem"`
}

type RiskConfig struct {
	MaxDailyLoss         float64       `mapstructure:"max_daily_loss"`
	MaxExposurePerMarket float64       `mapstructure:"max_exposure_per_market"`
	MaxSlippageBps       int           `mapstructure:"max_slippage_bps"`
	MaxOpenOrders        int           `mapstructure:"max_open_orders"`
	MarketCooldown       time.Duration `mapstructure:"market_cooldown"`
}

type ReconcileConfig struct {
	Interval     time.Duration   `mapstructure:"interval"`
	RetryBackoff []time.Duration `mapstructure:"retry_backoff"`
}

type RedeemConfig struct {
	Enabled bool `mapstructure:"enabled"`
}
```

- [ ] **Step 2: Apply conservative defaults in `Load`**

```go
cfg := Config{
	ChainRPCURL: "https://polygon.drpc.org",
	Logging:     logx.DefaultConfig(),
	Risk: RiskConfig{
		MaxDailyLoss:         20.0,
		MaxExposurePerMarket: 100.0,
		MaxSlippageBps:       200,
		MaxOpenOrders:        20,
		MarketCooldown:       2 * time.Second,
	},
	Reconcile: ReconcileConfig{
		Interval:     30 * time.Second,
		RetryBackoff: []time.Duration{time.Second, 2 * time.Second, 4 * time.Second},
	},
	Redeem: RedeemConfig{Enabled: false},
}
```

- [ ] **Step 3: Add startup validation** after `decryptSensitiveFields`:

```go
if cfg.SDKConfig.Polymarket.FunderAddress == "" {
	return Config{}, nil, fmt.Errorf("config: sdk_config.polymarket.funder_address is required")
}
if cfg.SDKConfig.Polymarket.OwnerKey == "" {
	return Config{}, nil, fmt.Errorf("config: sdk_config.polymarket.owner_key is required (encrypted or env)")
}
if cfg.SDKConfig.Polymarket.ChainID == 0 {
	return Config{}, nil, fmt.Errorf("config: sdk_config.polymarket.chain_id is required")
}
```

- [ ] **Step 4: Write `config/validation_test.go`**

```go
package config

import (
	"strings"
	"testing"
)

func TestLoad_MissingFunderAddress_Rejected(t *testing.T) {
	tmp := t.TempDir()
	writeConfig(t, tmp, `chain_rpc_url: "https://test"`)
	t.Setenv("PM_CONFIG_DECRYPT_PASSWORD", "x")

	_, _, err := loadFromDirRawNoFatal(t, tmp)
	if err == nil || !strings.Contains(err.Error(), "funder_address") {
		t.Fatalf("expected funder_address required, got %v", err)
	}
}

// helper duplicates loadFromDirRaw but returns error instead of Fatal
func loadFromDirRawNoFatal(t *testing.T, dir string) (Config, any, error) {
	t.Helper()
	cwd, _ := osGetwd()
	_ = osChdir(dir)
	t.Cleanup(func() { _ = osChdir(cwd) })
	cfg, v, err := Load()
	return cfg, v, err
}
```

> Provide `osGetwd` / `osChdir` shims if needed, or inline `os.Getwd`/`os.Chdir`.

- [ ] **Step 5: Existing tests still pass** — existing `config_test.go` writes minimal configs with `owner_key` set; they'll need `funder_address` too. Update each test fixture to include `funder_address: "0xtest-funder"` and `chain_id: 137`.

- [ ] **Step 6: Run** — `go test -race ./config/...` → expect PASS.

---

## Phase 8: main wiring + cleanup + finalize

### Task 23: main.go — wire new dependencies

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Update Engine construction**

```go
engine := &runtime.Engine{
	Config: viper,
	State:  st,
	Risk: &risk.Engine{
		MaxDailyLoss:         cfg.Risk.MaxDailyLoss,
		MaxExposurePerMarket: cfg.Risk.MaxExposurePerMarket,
		MaxSlippageBps:       cfg.Risk.MaxSlippageBps,
		MaxOpenOrders:        cfg.Risk.MaxOpenOrders,
		MarketCooldown:       cfg.Risk.MarketCooldown,
	},
	Exec: &execution.Executor{
		Client:    sharedClient,
		Config:    &cfg.SDKConfig,
		Reconcile: st.TriggerReconcile,
	},
	Feeds: []runtime.Feed{
		&market.PolymarketSlugFeed{
			SlugPrefix:    "btc-updown-5m",
			Config:        &cfg.SDKConfig,
			WindowMinutes: 5,
		},
		&market.CryptoPriceFeed{MonitoSymble: "btc", MonitorType: sdk.MonitorChainlink},
	},
	Observers:   []runtime.Observer{&observer.Logger{}},
	Probability: probability.NewEngine(sharedClient),
	Strategies:  []runtime.Strategy{&strategy.Strategy{}},
}

// start reconcile loop
st.StartReconcileLoop(ctx, state.ReconcileConfig{
	Enabled:      true,
	Interval:     cfg.Reconcile.Interval,
	RetryBackoff: cfg.Reconcile.RetryBackoff,
	OnReport: func(rep state.ReconcileReport) {
		engine.Bus.Publish(core.Event{Type: core.EventReconcile, Data: core.ReconcileEvent{
			Type:       "BOTH",
			Added:      rep.OrdersAdded + rep.PositionsAdded,
			Removed:    rep.OrdersRemoved + rep.PositionsRemoved,
			Updated:    rep.OrdersUpdated + rep.PositionsUpdated,
			DurationMs: rep.DurationMs,
			Err:        rep.Err,
			At:         time.Now().UTC(),
		}})
	},
})

engine.Start(ctx)
```

> Add `EventReconcile EventType = "RECONCILE"` and `ReconcileEvent` to `core/constants.go` + `core/event.go`. Add observer case.

- [ ] **Step 2: Run** — `go build ./...` → expect PASS.

---

### Task 24: cleanup

**Files:**
- Modify: multiple

- [ ] **Step 1: Delete commented `log.Printf` lines**

In `strategy/strategy.go`: delete lines 105, 157, 169–175, 177.
In `market/polymarket_slug_feed.go`: delete line 93 (`// log.Printf("orderBook: ...")`).
In `market/crypto_price_feed.go`: delete line 47 (`// log.Printf("data: %+v", data)`).
In `internal/multicall/multicall3.go`: delete debug `log.Printf` comments.

- [ ] **Step 2: Delete `PlacePrice` constant** in `strategy/strategy.go` (line 18).

- [ ] **Step 3: Add `Redeem` enable flag in `state/state_restore_pm.go`**

Add `Enabled bool` field to `PolymarketStateClient`:

```go
func NewPolymarketStateClient(client *sdk.PolymarketClient, config *sdk.PolymarketConfig, positionLimits int, redeemEnabled bool) *PolymarketStateClient {
	return &PolymarketStateClient{
		Client:         client,
		PositionLimits: positionLimits,
		SDKConfig:      config,
		RedeemEnabled:  redeemEnabled,
	}
}
```

Modify `Redeem` to short-circuit when disabled:

```go
func (p *PolymarketStateClient) Redeem(ctx context.Context, onRedeemSuccess func(tokenIDs []string)) {
	if p == nil || !p.RedeemEnabled {
		log.Debug().Msg("redeem disabled by config")
		return
	}
	// ... existing body ...
}
```

Update `main.go` to pass `cfg.Redeem.Enabled`.

- [ ] **Step 4: Run** — `go build ./...` and `go test -race ./...` → expect PASS.

---

### Task 25: lint + test -race -cover + README record + single commit

**Files:**
- Create: `.golangci.yml`
- Modify: `README.md` (append refactor record)

- [ ] **Step 1: Create `.golangci.yml`** (spec § 4.3):

```yaml
run:
  timeout: 5m

linters:
  enable:
    - errcheck
    - gosimple
    - govet
    - ineffassign
    - staticcheck
    - unused
    - gofmt
    - goimports
    - misspell
    - unconvert
    - unparam

issues:
  exclude-rules:
    - path: _test\.go
      linters:
        - errcheck
        - unparam
```

- [ ] **Step 2: Run lint** (skip if not installed):

```bash
which golangci-lint && golangci-lint run ./... || echo "golangci-lint not installed; skipping"
```

If any warning surfaces, fix it before continuing.

- [ ] **Step 3: Final verification**

```bash
go build ./...
go vet ./...
go test -race -cover ./... | tee /tmp/coverage.txt
```

Expected:
- Build OK
- vet zero output
- All test packages green
- Coverage:
  - `state/` ≥ 90%
  - `risk/` ≥ 90%
  - `runtime/` ≥ 90%
  - `execution/` ≥ 90%
  - `probability/` ≥ 80%
  - `strategy/` ≥ 80%
  - `market/` ≥ 80%
  - `indicators/` ≥ 80%
  - `internal/...` ≥ 80%
  - `core/` ≥ 80%

If any module is below target, **add tests until target is met**. The plan author leaves "fill-in" testing to the executor — use spec § 10.3 as the checklist.

- [ ] **Step 4: Append refactor record to `README.md`**

Append a new section after the existing review:

```markdown
## 重构记录

### Refactor @ 2026-05-18 → 完成

**Spec**: docs/superpowers/specs/2026-05-18-refactor-b-level.md
**Plan**: docs/superpowers/plans/2026-05-18-refactor-b-level.md
**Scope**: B 级（包内重构，对外形态不变）+ 22 条 review 问题 + 10 条策略优化 + Polymarket 权威对账 + 完整测试

#### 改动一览

| 类别 | 改动 |
|---|---|
| 工程基础 | `core/errors.go` 集中 sentinel error；`core/pricing.go` 抽 `RequiredCollateral`/`FloatEpsilon`；`.golangci.yml` |
| 错误处理 | `errors.Is` 替换字符串比较；自定义 `risk.Rejection` 类型 |
| 依赖注入 | `probability.NewEngine(client)`；`Executor.relayClient` 字段一次构造 |
| 文件拆分 | `runtime/{event_handler,order_tracking,metrics}.go`；`state/{reservation,fill,balance,pnl,reconcile,position_expiring}.go`；`execution/{placements,splits_merges,trade_events}.go`；`probability/{market_state,features,book_store}.go` |
| 风控硬墙 | `MaxDailyLoss` / `MaxExposurePerMarket` / `MaxSlippageBps` / `MaxOpenOrders` / `MarketCooldown` + 9 类 `RejectionType` |
| 持仓增强 | `TokenPosition.AvgCost` / `AvgCostKnown`；`State.UnrealizedPnL`；`EventPositionExpiring` |
| 状态机收敛 | `state.AttachOrder` / `AttachExternalOrder` 单入口 |
| 可观测 | `Executor.DryRun`；`EventBus.DropThreshold`；`EventReconcile` |
| Polymarket 权威对账 | `state.ReconcileWithExchange` (30s 定时 + WS 即时触发)；以远端为准；外部订单 `ExternalOrigin=true` 计入风控 |
| 配置 | `risk` / `reconcile` / `redeem` 三个 section，保守默认开启；启动校验 `funder_address` / `owner_key` |
| 韧性 | Feed 失败重试不退出；Executor shutdown drain；probability reset RPC 移出锁外 |
| 测试 | 全包覆盖率达标（critical ≥ 90%，其他 ≥ 80%）；race test 覆盖所有 mutex 边界 |

#### 收益

| 维度 | 之前 | 现在 |
|---|---|---|
| 风险 | 余额够就下单 | 5 道硬墙：daily PnL / exposure / slippage / open orders / cooldown |
| 一致性 | 本地账本 vs 远端可脱节 | 30s + WS 双驱动对账，远端为权威源 |
| 可观测 | 5 分钟一次 metrics log | + `UnrealizedPnL` + `DailyPnL` + `ReconcileRuns/Diffs` + `RejectionType` 枚举可聚合 |
| 可测性 | 仅 state/risk/runtime 部分覆盖 | 全包均达目标覆盖率；mock 接口分离；race test 系统化 |
| 可维护性 | 单文件 500–900 行 | 单文件 < 400 行；职责单一 |

#### 用户使用说明

**手动在 Polymarket 操作的兼容性**：
- 手动挂单后 ≤ 30s 本地账本自动同步（标记 `ExternalOrigin=true`），并计入 `max_open_orders` / `max_exposure_per_market` 风控限额
- 手动取消订单 ≤ 30s 本地 release
- 手动卖出仓位 ≤ 30s 本地 position 减少；本地 `AvgCost` 按比例保留；新出现的 token 标记 `AvgCostKnown=false`，不参与 `UnrealizedPnL` 计算

**新增配置项（`config.yaml`）**：
```yaml
risk:
  max_daily_loss: 20.0
  max_exposure_per_market: 100
  max_slippage_bps: 200
  max_open_orders: 20
  market_cooldown: 2s

reconcile:
  interval: 30s
  retry_backoff: [1s, 2s, 4s]

redeem:
  enabled: false
```

所有时间字段统一使用 **UTC+0**。
```

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
refactor: B-level refactor + strategy hard walls + Polymarket reconciliation

- core: sentinel errors, RequiredCollateral, EventBus DropThreshold
- state: AttachOrder/AttachExternalOrder, AvgCost/AvgCostKnown, dailyPnL UTC,
  UnrealizedPnL, periodic ReconcileWithExchange (30s + WS trigger),
  PositionExpiring ticker
- risk: RejectionType enum, midPrices slippage, exposure cap, cooldown,
  daily loss, max open orders (incl. ExternalOrigin)
- probability: NewEngine(client) DI, fix log module name, split files,
  RPC out of lock
- execution: split files, RelayClient one-time init, unknown-orderID reconcile
  trigger, DryRun mode, shutdown drain
- strategy: Features type-safe, viper.Sub nil check, MarketQueue value cache,
  OnPositionExpiring
- market: PolymarketSlugFeed retry on fetch failure
- runtime: split files, errors.Is for sentinel, midPrices, new metrics
- observer: type-safe assertions, EventReconcile/EventPositionExpiring cases
- config: validate funder_address/owner_key at startup, risk/reconcile/redeem
  schema with conservative defaults
- tests: 90% coverage for state/risk/runtime/execution; 80% for the rest;
  race tests on all mutex boundaries

Spec: docs/superpowers/specs/2026-05-18-refactor-b-level.md
Plan: docs/superpowers/plans/2026-05-18-refactor-b-level.md
EOF
)"
```

- [ ] **Step 6: Push**

```bash
git push origin benj
```

---

## Self-Review Checklist (executor verifies before final commit)

- [ ] All 22 README review issues are resolved (mapped in plan task 5.3 table)
- [ ] All 10 strategy optimizations are implemented (mapped to Tasks 13, 11, 12, 15, 16, 19, 20)
- [ ] No `requiredCollateral` outside of `core/`
- [ ] No `floatEpsilon` outside of `core/`
- [ ] No string compare on error messages anywhere (`grep -rn 'err.Error() == \|err.Error() !=' --include='*.go'` returns nothing)
- [ ] `logx.Module("observer")` only in `observer/logger.go`
- [ ] `go test -race ./...` PASS
- [ ] Coverage targets met (see Task 25 Step 3)
- [ ] No commented-out `log.Printf` in production files
- [ ] `PlacePrice` const removed
- [ ] `README.md` refactor record added
- [ ] Single commit on `benj`
