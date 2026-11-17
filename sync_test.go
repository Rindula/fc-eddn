package main

import (
	"encoding/json"
	"testing"
	"time"
)

func fixture(t *testing.T) carrier {
	t.Helper()
	var c carrier
	data := `{
  "name":{"callsign":"ABC-123"},
  "currentStarSystem":"Sol",
  "market":{"name":"ABC-123","id":3706117376,"outpostType":"fleetcarrier",
   "services":{"commodities":"ok"},"commodities":[
    {"name":"Tritium","meanPrice":5,"buyPrice":20,"sellPrice":10,"stock":2,
     "stockBracket":1,"demand":0,"demandBracket":"","statusFlasg":["Consumer","Consumer"]},
    {"name":"drones","categoryname":"NonMarketable"},
    {"name":"x","legality":"Illegal"}
   ]}
 }`
	if err := json.Unmarshal([]byte(data), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMarketFiltersPrivateAndNonmarketableData(t *testing.T) {
	c := fixture(t)
	market, err := marketFromCarrier(c)
	if err != nil {
		t.Fatal(err)
	}
	if market == nil || len(market.Commodities) != 1 {
		t.Fatalf("unexpected market: %+v", market)
	}
	item := market.Commodities[0]
	if item.Name != "tritium" || len(item.StatusFlags) != 1 || *item.BuyPrice != 20 || *item.SellPrice != 10 {
		t.Fatalf("unexpected commodity: %+v", item)
	}
	c.Market.Services["commodities"] = "private"
	market, err = marketFromCarrier(c)
	if market != nil || err != nil {
		t.Fatal("private market should be skipped")
	}
}

func TestPositionRequiresMatchingMarketAndSystem(t *testing.T) {
	c := fixture(t)
	data := []byte(`{"timestamp":"2026-11-17T11:00:00Z","event":"LoadGame","Commander":"Rindula","gameversion":"4.1.0.0","build":"r123 ","Horizons":true}
{"timestamp":"2026-11-17T11:00:01Z","event":"Location","StarSystem":"Sol","SystemAddress":10477373803,"StarPos":[0,0,0]}
{"timestamp":"2026-11-17T11:02:00Z","event":"Docked","StarSystem":"Sol","SystemAddress":10477373803,"MarketID":3706117376,"StationName":"ABC-123","StationType":"FleetCarrier"}
`)
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	pos, game, build, docked, err := inspectJournal(data, c, "Rindula", now)
	if err != nil {
		t.Fatal(err)
	}
	if !docked || pos == nil || len(pos.Coordinates) != 3 || game != "4.1.0.0" || build != "r123 " || pos.Horizons == nil || !*pos.Horizons {
		t.Fatalf("unexpected position: %+v, %q, %q", pos, game, build)
	}
	c.CurrentStarSystem = "Achenar"
	pos, _, _, docked, err = inspectJournal(data, c, "Rindula", now)
	if pos != nil || docked || err != nil {
		t.Fatal("stale carrier position should be skipped")
	}
}
