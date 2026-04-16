package market

import (
	"testing"
	"time"
)

func TestSlugFor5m(t *testing.T) {
	feed := &PolymarketSlugFeed{SlugPrefix: "btc-updown-5m", WindowMinutes: 5}
	now := time.Unix(1718106299, 0) // 2024-06-11 10:24:59 UTC
	if got, want := feed.slugFor(now), "btc-updown-5m-1718106000"; got != want {
		t.Fatalf("slugFor() = %s, want %s", got, want)
	}
}
