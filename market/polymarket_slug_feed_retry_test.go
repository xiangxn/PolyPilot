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

func TestParseSlugMarket_MissingTokens(t *testing.T) {
	res := gjson.Parse(`{"conditionId":"c1"}`)
	_, err := ParseSlugMarket(res)
	if err == nil {
		t.Fatal("expected error on missing clobTokenIds")
	}
}
