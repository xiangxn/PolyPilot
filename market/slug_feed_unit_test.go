package market

import (
	"context"
	"testing"
	"time"

	"github.com/xiangxn/polypilot/core"
)

func TestPolymarketSlugFeed_Init(t *testing.T) {
	bus := core.NewEventBus()
	t.Cleanup(bus.Close)
	f := &PolymarketSlugFeed{}
	f.Init(bus)
	if f.Bus != bus {
		t.Fatal("Init should set Bus")
	}
}

func TestEnsureDefaults_AppliesWhenEmpty(t *testing.T) {
	f := &PolymarketSlugFeed{}
	f.ensureDefaults()
	if f.SlugPrefix == "" {
		t.Fatalf("default SlugPrefix not set: %+v", f)
	}
	if f.WindowMinutes <= 0 {
		t.Fatalf("default WindowMinutes not set: %+v", f)
	}
	// Locked-down defaults: must match the package constants.
	if f.SlugPrefix != defaultSlugPrefix {
		t.Fatalf("SlugPrefix default mismatch: got %q want %q", f.SlugPrefix, defaultSlugPrefix)
	}
	if f.WindowMinutes != defaultWindowMinutes {
		t.Fatalf("WindowMinutes default mismatch: got %d want %d", f.WindowMinutes, defaultWindowMinutes)
	}
}

func TestEnsureDefaults_PreservesProvided(t *testing.T) {
	f := &PolymarketSlugFeed{SlugPrefix: "custom", WindowMinutes: 10}
	f.ensureDefaults()
	if f.SlugPrefix != "custom" || f.WindowMinutes != 10 {
		t.Fatalf("provided values clobbered: %+v", f)
	}
}

func TestEnsureDefaults_NegativeWindowResets(t *testing.T) {
	f := &PolymarketSlugFeed{SlugPrefix: "x", WindowMinutes: -2}
	f.ensureDefaults()
	if f.WindowMinutes != defaultWindowMinutes {
		t.Fatalf("non-positive WindowMinutes should be reset to default, got %d", f.WindowMinutes)
	}
	if f.SlugPrefix != "x" {
		t.Fatalf("SlugPrefix should not be touched if already set: %q", f.SlugPrefix)
	}
}

func TestSlugFor_DefaultWindow(t *testing.T) {
	f := &PolymarketSlugFeed{SlugPrefix: "btc-updown-5m"}
	f.ensureDefaults()
	if got := f.slugFor(time.Unix(1718106299, 0)); got == "" {
		t.Fatal("slug should not be empty")
	}
}

func TestSlugFor_NonPositiveWindowFallsBackInternally(t *testing.T) {
	// slugFor has its own internal fallback to defaultWindowMinutes when the
	// configured window is non-positive. Exercise that branch directly without
	// calling ensureDefaults first.
	f := &PolymarketSlugFeed{SlugPrefix: "p"}
	got := f.slugFor(time.Unix(1718106299, 0))
	// With default 5m window: 1718106299 / 300 * 300 = 1718106000
	want := "p-1718106000"
	if got != want {
		t.Fatalf("slugFor fallback window mismatch: got %q want %q", got, want)
	}
}

func TestSlugFor_10mWindow(t *testing.T) {
	f := &PolymarketSlugFeed{SlugPrefix: "10m", WindowMinutes: 10}
	// 10m window = 600s; 1718106299 / 600 * 600 = 1718106000
	got := f.slugFor(time.Unix(1718106299, 0))
	if want := "10m-1718106000"; got != want {
		t.Fatalf("slugFor mismatch: got %q want %q", got, want)
	}
}

func TestPolymarketSlugFeed_Start_NilBusIsNoOp(t *testing.T) {
	// Without a Bus the Start function should bail out before reaching SDK code.
	f := &PolymarketSlugFeed{}
	// Sanity: do not panic, do not block.
	done := make(chan struct{})
	go func() {
		f.Start(context.Background())
		close(done)
	}()
	select {
	case <-done:
		// ok — returned synchronously
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Start with nil Bus should return immediately")
	}
}

func TestFetchMarketBySlug_EmptySlug(t *testing.T) {
	// The empty-slug guard runs before any SDK access, so this should fail
	// fast without dereferencing MarketMonitor.
	f := &PolymarketSlugFeed{}
	_, _, err := f.FetchMarketBySlug("")
	if err == nil {
		t.Fatal("expected error for empty slug")
	}

	_, _, err = f.FetchMarketBySlug("   ")
	if err == nil {
		t.Fatal("expected error for whitespace-only slug")
	}
}
