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
