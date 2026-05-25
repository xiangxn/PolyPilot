package market

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sdk "github.com/xiangxn/go-polymarket-sdk/polymarket"
	"github.com/xiangxn/polypilot/core"
)

type syncBus struct {
	bus *core.EventBus
}

func newSyncBus(t *testing.T) *syncBus {
	t.Helper()
	b := core.NewEventBus()
	t.Cleanup(b.Close)
	return &syncBus{bus: b}
}

// newFakeGammaServer returns an httptest.Server that responds to
// `/markets/slug/{slug}` with the given status and body. handlerExtras can be
// nil; if non-nil it is consulted before the slug-route fallback.
func newFakeGammaServer(t *testing.T, status int, body string) (*httptest.Server, *sdk.PolymarketClient) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	cfg := sdk.DefaultConfig()
	cfg.Polymarket.GammaBaseURL = srv.URL
	cfg.Polymarket.ClobBaseURL = srv.URL
	client := sdk.NewClient(cfg)
	return srv, client
}

// buildFeedWithFakeClient wires a PolymarketSlugFeed with a fake gamma server
// and a real MarketMonitor that uses the fake client.
func buildFeedWithFakeClient(t *testing.T, status int, body string) (*PolymarketSlugFeed, *httptest.Server) {
	t.Helper()
	srv, client := newFakeGammaServer(t, status, body)
	// We must provide a non-nil MarketMonitor so f.MarketMonitor.GetClient()
	// returns our fake-wired client.
	mm := sdk.NewMarketMonitor("ws://localhost:0", false, client)
	f := &PolymarketSlugFeed{
		MarketMonitor: mm,
	}
	f.ensureDefaults()
	return f, srv
}

func TestFetchMarketBySlug_HTTP404Error(t *testing.T) {
	f, srv := buildFeedWithFakeClient(t, 404, `{"error":"not found"}`)
	defer srv.Close()

	_, _, err := f.FetchMarketBySlug("some-slug")
	if err == nil {
		t.Fatal("expected error from 404")
	}
}

func TestFetchMarketBySlug_MissingClobTokenIds(t *testing.T) {
	body := `{"conditionId":"c1","endDate":"2099-01-01T00:00:00Z","startDate":"2024-01-01T00:00:00Z"}`
	f, srv := buildFeedWithFakeClient(t, 200, body)
	defer srv.Close()

	_, _, err := f.FetchMarketBySlug("missing-tokens")
	if err == nil {
		t.Fatal("expected error for missing clobTokenIds")
	}
}

func TestFetchMarketBySlug_InvalidEndDate(t *testing.T) {
	body := `{"conditionId":"c1","clobTokenIds":"[\"tk1\"]","endDate":"not-a-date","startDate":"2024-01-01T00:00:00Z"}`
	f, srv := buildFeedWithFakeClient(t, 200, body)
	defer srv.Close()

	_, _, err := f.FetchMarketBySlug("bad-end")
	if err == nil {
		t.Fatal("expected error for invalid endDate")
	}
}

func TestFetchMarketBySlug_InvalidStartDate(t *testing.T) {
	body := `{"conditionId":"c1","clobTokenIds":"[\"tk1\"]","endDate":"2099-01-01T00:00:00Z","startDate":"not-a-date"}`
	f, srv := buildFeedWithFakeClient(t, 200, body)
	defer srv.Close()

	_, _, err := f.FetchMarketBySlug("bad-start")
	if err == nil {
		t.Fatal("expected error for invalid startDate")
	}
}

func TestFetchMarketBySlug_HappyPath_FeesDisabled(t *testing.T) {
	// feesEnabled=false → no GetFeeRateBps network call, exercises the
	// SetFeeRateBps(0) branch and full success path.
	body := `{
		"conditionId":"c1",
		"clobTokenIds":"[\"tk1\",\"tk2\"]",
		"outcomePrices":"[\"0.42\",\"0.58\"]",
		"endDate":"2099-01-01T00:00:00Z",
		"startDate":"2024-01-01T00:00:00Z",
		"resolutionSource":"src",
		"orderPriceMinTickSize":0.01,
		"negRisk":true,
		"feesEnabled":false,
		"closed":false,
		"outcomes":"[\"YES\",\"NO\"]"
	}`
	f, srv := buildFeedWithFakeClient(t, 200, body)
	defer srv.Close()

	sm, raw, err := f.FetchMarketBySlug("happy-slug")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sm == nil || raw == nil {
		t.Fatalf("expected non-nil results, got sm=%v raw=%v", sm, raw)
	}
	if sm.MarketID != "c1" {
		t.Fatalf("MarketID mismatch: %q", sm.MarketID)
	}
	if len(sm.TokenIDs) != 2 {
		t.Fatalf("TokenIDs len mismatch: %d", len(sm.TokenIDs))
	}
	if !sm.NegRisk {
		t.Fatal("NegRisk should be true")
	}
	if sm.ResolutionSource != "src" {
		t.Fatalf("ResolutionSource: %q", sm.ResolutionSource)
	}
	if sm.TickSize != 0.01 {
		t.Fatalf("TickSize: %v", sm.TickSize)
	}
	if len(sm.Outcomes) != 2 || sm.Outcomes[0] != "YES" {
		t.Fatalf("Outcomes: %+v", sm.Outcomes)
	}
	if len(sm.Prices) != 2 || sm.Prices[0] != 0.42 {
		t.Fatalf("Prices: %+v", sm.Prices)
	}
	if sm.Closed {
		t.Fatal("Closed should be false")
	}
}

func TestPolymarketSlugFeed_Start_FetchError_CtxCancel(t *testing.T) {
	// Fake gamma returns 404 → FetchMarketBySlug fails → goroutine enters
	// the failure backoff branch. We cancel the context to exercise the
	// `case <-ctx.Done(): return` branch inside that select.
	bus := newSyncBus(t)
	srv, client := newFakeGammaServer(t, 404, `{}`)
	defer srv.Close()
	mm := sdk.NewMarketMonitor("ws://127.0.0.1:1", false, client)
	f := &PolymarketSlugFeed{
		Bus:           bus.bus,
		MarketMonitor: mm,
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.Start(ctx)
	// Allow the goroutine to issue one FetchMarketBySlug call and reach the
	// backoff select before we cancel.
	time.Sleep(150 * time.Millisecond)
	cancel()
	// Give the goroutine time to observe ctx.Done() and return.
	time.Sleep(150 * time.Millisecond)
}

// NOTE: a Start happy-path test that lets the inner loop run + fires the
// timer branch is not safe under -race because the SDK MarketMonitor has a
// known data race between its internal Disconnect() (called from Run) and
// Reset() (called from our goroutine when the per-market deadline expires).
// See go-polymarket-sdk@v0.6.8 market_monitor.go lines 78 / 141 / 151.
// We intentionally do not cover the happy-path inner loop here to keep the
// test suite clean under -race; the inner-loop logic is small (~12 lines)
// and would require an SDK fix.

func TestPolymarketSlugFeed_Start_PreCancelledContext(t *testing.T) {
	// Pre-cancel the context so the goroutine exits via the first ctx.Done()
	// check before performing any HTTP work. This exercises the construction
	// branch of Start (Bus != nil, MarketMonitor == nil → build it).
	bus := newSyncBus(t)
	srv, client := newFakeGammaServer(t, 404, `{}`)
	defer srv.Close()
	cfg := sdk.DefaultConfig()
	cfg.Polymarket.GammaBaseURL = srv.URL
	cfg.Polymarket.ClobBaseURL = srv.URL
	cfg.Polymarket.ClobWSBaseURL = "ws://127.0.0.1:0" // unreachable but harmless
	_ = client

	f := &PolymarketSlugFeed{
		Bus:    bus.bus,
		Config: cfg,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	f.Start(ctx)
	// Allow the goroutine to observe ctx.Done() and return.
	time.Sleep(100 * time.Millisecond)

	if f.MarketMonitor == nil {
		t.Fatal("expected Start to construct MarketMonitor before the cancel branch")
	}
}

func TestFetchMarketBySlug_HappyPath_FeesEnabled(t *testing.T) {
	// feesEnabled=true → branch invokes GetFeeRateBps which hits the same
	// fake server (it'll return our canned response — content is irrelevant
	// for coverage). The handler captures the request to ensure we are
	// reachable.
	gotFee := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/fee-rate":
			gotFee = true
			_, _ = w.Write([]byte(`{"base_fee":0.001}`))
		default:
			_, _ = fmt.Fprint(w, `{
				"conditionId":"cX",
				"clobTokenIds":"[\"tkA\"]",
				"endDate":"2099-01-01T00:00:00Z",
				"startDate":"2024-01-01T00:00:00Z",
				"orderPriceMinTickSize":0.01,
				"negRisk":false,
				"feesEnabled":true
			}`)
		}
	}))
	defer srv.Close()

	cfg := sdk.DefaultConfig()
	cfg.Polymarket.GammaBaseURL = srv.URL
	cfg.Polymarket.ClobBaseURL = srv.URL
	client := sdk.NewClient(cfg)
	mm := sdk.NewMarketMonitor("ws://localhost:0", false, client)
	f := &PolymarketSlugFeed{MarketMonitor: mm}
	f.ensureDefaults()

	sm, _, err := f.FetchMarketBySlug("fees-enabled")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sm.MarketID != "cX" {
		t.Fatalf("MarketID mismatch: %q", sm.MarketID)
	}
	if !gotFee {
		t.Fatal("expected /fee-rate to be called when feesEnabled=true")
	}
}
