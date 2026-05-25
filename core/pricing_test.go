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
