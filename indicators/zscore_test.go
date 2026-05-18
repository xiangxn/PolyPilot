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
