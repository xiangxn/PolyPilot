package market

import (
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

func TestSlugFor5m(t *testing.T) {
	feed := &PolymarketSlugFeed{SlugPrefix: "btc-updown-5m", WindowMinutes: 5}
	now := time.Unix(1718106299, 0) // 2024-06-11 10:24:59 UTC
	if got, want := feed.slugFor(now), "btc-updown-5m-1718106000"; got != want {
		t.Fatalf("slugFor() = %s, want %s", got, want)
	}
}

func TestParseStringArray(t *testing.T) {
	arr := parseStringArray(gjson.Parse(`["a","b"]`))
	if len(arr) != 2 || arr[0] != "a" || arr[1] != "b" {
		t.Fatalf("parse array failed: %+v", arr)
	}

	strArr := parseStringArray(gjson.Parse(`"[\"x\",\"y\"]"`))
	if len(strArr) != 2 || strArr[0] != "x" || strArr[1] != "y" {
		t.Fatalf("parse string-array failed: %+v", strArr)
	}
}

func TestParseEndTime(t *testing.T) {
	end, err := parseEndTime("2026-04-15T12:35:00Z")
	if err != nil {
		t.Fatalf("parseEndTime failed: %v", err)
	}
	if end.Unix() != 1776256500 {
		t.Fatalf("unexpected end unix: %d", end.Unix())
	}
}
