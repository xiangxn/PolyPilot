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
		Bus:       bus,
		tracked:   make(map[string]*trackedOrder),
		Reconcile: func() { fired.Add(1) },
	}
	exec.onOrderEvent(&sdkmodel.WSOrder{
		Id:           "external-1",
		Market:       "m",
		AssetId:      "tk1",
		Side:         "BUY",
		Price:        0.5,
		OriginalSize: 5,
		Status:       "LIVE",
		Timestamp:    time.Now().Unix(),
		Owner:        "", // bypass owner check via empty ownKey
	})
	// Reconcile fires in a goroutine; give it time
	deadline := time.After(2 * time.Second)
	for fired.Load() < 1 {
		select {
		case <-deadline:
			t.Fatalf("expected reconcile fired, got %d", fired.Load())
		case <-time.After(20 * time.Millisecond):
		}
	}
}
