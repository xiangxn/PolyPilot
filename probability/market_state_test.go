package probability

import (
	"fmt"
	"testing"
	"time"
)

func slugFor(now time.Time) string {
	window := 5 * 60
	ts := now.Unix() / int64(window) * int64(window)
	return fmt.Sprintf("%s-%d", "btc-updown-5m", ts)
}

func TestCheckResolved(t *testing.T) {
	e := NewEngine("btc", nil)
	index, ok := e.checkResolved("btc-updown-5m-1780210500")
	fmt.Printf("1 === index: %d, ok: %v\n", index, ok)

	index, ok = e.checkResolved(slugFor(time.Now()))
	fmt.Printf("2 === index: %d, ok: %v\n", index, ok)

}
