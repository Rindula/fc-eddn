package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

const eddnBase = "https://eddn.edcd.io"

var callsignPattern = regexp.MustCompile(`^[A-Z0-9]{3}-[A-Z0-9]{3}$`)

type carrier struct {
	Name              struct{ Callsign string }
	CurrentStarSystem string
	Market            struct {
		ID          uint64
		Name        string
		OutpostType string
		Services    map[string]string
		Commodities []capiCommodity
	}
}

type commodity struct {
	Name          string          `json:"name"`
	MeanPrice     *int64          `json:"meanPrice"`
	BuyPrice      *int64          `json:"buyPrice"`
	Stock         *int64          `json:"stock"`
	StockBracket  json.RawMessage `json:"stockBracket"`
	SellPrice     *int64          `json:"sellPrice"`
	Demand        *int64          `json:"demand"`
	DemandBracket json.RawMessage `json:"demandBracket"`
	StatusFlags   []string        `json:"statusFlags,omitempty"`
}

type capiCommodity struct {
	commodity
	CategoryName string
	Legality     string
	StatusFlasg  []string
}

type marketMessage struct {
	System      string      `json:"systemName"`
	Station     string      `json:"stationName"`
	MarketID    uint64      `json:"marketId"`
	Timestamp   string      `json:"timestamp"`
	Commodities []commodity `json:"commodities"`
}

func bracketValid(raw json.RawMessage) bool {
	switch string(raw) {
	case "0", "1", "2", "3", `""`:
		return true
	default:
		return false
	}
}

func marketFromCarrier(c carrier) (*marketMessage, error) {
	if c.Market.Services["commodities"] != "ok" || c.Market.Commodities == nil {
		return nil, nil
	}
	items := make([]commodity, 0, len(c.Market.Commodities))
	seen := map[string]bool{}
	for _, entry := range c.Market.Commodities {
		if strings.EqualFold(entry.CategoryName, "NonMarketable") || entry.Legality != "" {
			continue
		}
		item := entry.commodity
		item.Name = strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(item.Name), "$"), "_name;"))
		if item.Name == "" || seen[item.Name] || item.MeanPrice == nil || item.BuyPrice == nil || item.SellPrice == nil ||
			item.Stock == nil || item.Demand == nil || !bracketValid(item.StockBracket) || !bracketValid(item.DemandBracket) {
			return nil, errors.New("incomplete or duplicate CAPI commodity")
		}
		seen[item.Name] = true
		if len(item.StatusFlags) == 0 {
			item.StatusFlags = entry.StatusFlasg
		}
		flags := map[string]bool{}
		item.StatusFlags = append([]string(nil), item.StatusFlags...)
		normalized := item.StatusFlags[:0]
		for _, flag := range item.StatusFlags {
			if flag != "" && !flags[flag] {
				normalized = append(normalized, flag)
				flags[flag] = true
			}
		}
		sort.Strings(normalized)
		item.StatusFlags = normalized
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return &marketMessage{System: c.CurrentStarSystem, Station: c.Name.Callsign, MarketID: c.Market.ID, Commodities: items}, nil
}

type position struct {
	Timestamp   time.Time `json:"timestamp"`
	Event       string    `json:"event"`
	System      string    `json:"StarSystem"`
	Address     uint64    `json:"SystemAddress"`
	Coordinates []float64 `json:"StarPos"`
	Station     string    `json:"StationName"`
	StationType string    `json:"StationType"`
	MarketID    uint64    `json:"MarketID"`
	Horizons    *bool     `json:"horizons,omitempty"`
	Odyssey     *bool     `json:"odyssey,omitempty"`
}

type journalEntry struct {
	position
	Commander   string
	GameVersion string
	Build       string
	Docked      bool
}

func inspectJournal(data []byte, c carrier, commander string, now time.Time) (*position, string, string, bool, error) {
	var dockedAtCarrier bool
	var best *position
	var current journalEntry
	var system journalEntry
	var gameVersion, build string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 65536), 4<<20)
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var e journalEntry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			return nil, "", "", false, errors.New("invalid CAPI journal entry")
		}
		switch e.Event {
		case "Fileheader", "Shutdown":
			current, system = journalEntry{}, journalEntry{}
		case "LoadGame":
			current, system = e, journalEntry{}
			dockedAtCarrier = false
		}
		if !strings.EqualFold(current.Commander, commander) || !strings.HasPrefix(current.GameVersion, "4.") {
			continue
		}
		if (e.Event == "Location" || e.Event == "FSDJump" || e.Event == "CarrierJump") && len(e.Coordinates) == 3 {
			system = e
		}
		switch e.Event {
		case "Undocked", "FSDJump", "SupercruiseEntry":
			dockedAtCarrier = false
		case "Location", "Docked", "CarrierJump":
			dockedAtCarrier = (e.Event != "Location" || e.Docked) && e.MarketID == c.Market.ID &&
				strings.EqualFold(e.Station, c.Name.Callsign) && e.StationType == "FleetCarrier" &&
				strings.EqualFold(e.System, c.CurrentStarSystem) && !e.Timestamp.Before(now.Add(-24*time.Hour)) && !e.Timestamp.After(now.Add(time.Minute))
		}
		if e.Event != "CarrierJump" && e.Event != "Docked" && !(e.Event == "Location" && e.Docked) {
			continue
		}
		if e.MarketID != c.Market.ID || !strings.EqualFold(e.Station, c.Name.Callsign) || e.StationType != "FleetCarrier" {
			continue
		}
		if best != nil && e.Timestamp.Before(best.Timestamp) {
			continue
		}
		best, gameVersion, build = nil, "", ""
		if !strings.EqualFold(e.System, c.CurrentStarSystem) || e.Address == 0 || e.Timestamp.After(now.Add(time.Minute)) ||
			e.Timestamp.Before(now.Add(-24*time.Hour)) {
			continue
		}
		if len(e.Coordinates) == 0 && e.Event == "Docked" && e.Address == system.Address &&
			strings.EqualFold(e.System, system.System) {
			e.Coordinates = system.Coordinates
		}
		if len(e.Coordinates) != 3 {
			continue
		}
		e.Horizons, e.Odyssey = current.Horizons, current.Odyssey
		best, gameVersion, build = &e.position, current.GameVersion, current.Build
	}
	return best, gameVersion, build, dockedAtCarrier, scanner.Err()
}

type syncer struct {
	config config
	token  token
	sent   map[string]string
}

type eddnHeader struct {
	Uploader    string `json:"uploaderID"`
	Software    string `json:"softwareName"`
	Version     string `json:"softwareVersion"`
	GameVersion string `json:"gameversion"`
	GameBuild   string `json:"gamebuild"`
}

type envelope struct {
	Schema  string     `json:"$schemaRef"`
	Header  eddnHeader `json:"header"`
	Message any        `json:"message"`
}

func (s *syncer) publish(ctx context.Context, schema, digest, gameVersion, build string, message any) error {
	if s.sent[schema] == digest || s.sent[schema] == "rejected:"+version+":"+digest {
		return nil
	}
	if s.config.mode == "dry-run" {
		log.Printf("dry-run: %s ready for upload", schema)
		s.sent[schema] = digest
		return nil
	}
	ref := eddnBase + "/schemas/" + schema
	if s.config.mode == "test" {
		ref += "/test"
	}
	payload, err := json.Marshal(envelope{ref, eddnHeader{s.config.commander, "fc-eddn", version, gameVersion, build}, message})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://eddn.edcd.io:4430/upload/", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := request(req, 4096, nil)
	var status statusError
	if errors.As(err, &status) && (status.code == 400 || status.code == 426) {
		return errors.Join(fmt.Errorf("EDDN permanently rejected %s: %w", schema, err), s.record(schema, "rejected:"+version+":"+digest))
	}
	if err != nil {
		return fmt.Errorf("EDDN %s: %w", schema, err)
	}
	if strings.TrimSpace(string(response)) != "OK" {
		return fmt.Errorf("unexpected EDDN acknowledgement for %s", schema)
	}
	log.Printf("EDDN accepted %s", schema)
	return s.record(schema, digest)
}

func (s *syncer) record(schema, digest string) error {
	s.sent[schema] = digest
	return save(s.config.path("sent.json"), s.sent)
}

func (s *syncer) sync(ctx context.Context) error {
	var c carrier
	if _, err := s.capi(ctx, "/fleetcarrier", 16<<20, &c); err != nil {
		var status statusError
		if errors.As(err, &status) && status.code == 204 {
			return errors.New("Frontier reports no personal Fleet Carrier")
		}
		return fmt.Errorf("CAPI fleetcarrier: %w", err)
	}
	if !callsignPattern.MatchString(c.Name.Callsign) || strings.TrimSpace(c.CurrentStarSystem) == "" {
		return errors.New("CAPI did not return a valid carrier callsign and system")
	}
	log.Printf("Carrier %s in %s", c.Name.Callsign, c.CurrentStarSystem)
	if c.Market.ID == 0 || !strings.EqualFold(c.Market.Name, c.Name.Callsign) || c.Market.OutpostType != "fleetcarrier" {
		return errors.New("CAPI did not return matching Fleet Carrier market identification")
	}
	data, err := s.capi(ctx, "/journal", 32<<20, nil)
	if err != nil {
		var status statusError
		if errors.As(err, &status) && (status.code == 204 || status.code == 404 || status.code == 206) {
			log.Print("No complete Frontier journal available today; upload skipped")
			return nil
		}
		return fmt.Errorf("CAPI journal: %w", err)
	}
	now := time.Now().UTC()
	pos, gameVersion, build, docked, err := inspectJournal(data, c, s.config.commander, now)
	if err != nil {
		return err
	}
	var positionErr error
	if pos == nil {
		log.Print("No matching Fleet Carrier position in today's CAPI journal")
	} else {
		digest := fmt.Sprintf("%d:%s:%s", pos.MarketID, pos.Event, pos.Timestamp.Format(time.RFC3339Nano))
		positionErr = s.publish(ctx, "journal/1", digest, gameVersion, build, pos)
	}
	return errors.Join(positionErr, s.syncMarket(ctx, c, docked, now))
}

func (s *syncer) syncMarket(ctx context.Context, c carrier, docked bool, observed time.Time) error {
	market, err := marketFromCarrier(c)
	if err != nil || market == nil {
		return err
	}
	if !docked {
		log.Print("Market upload skipped: journal does not confirm docking at this carrier")
		return nil
	}
	var profile struct {
		Commander struct {
			Name   string
			Docked bool
		}
		LastStarport struct {
			Name string
			ID   uint64
		}
		LastSystem struct{ Name string }
	}
	if _, err := s.capi(ctx, "/profile", 16<<20, &profile); err != nil {
		return fmt.Errorf("CAPI profile: %w", err)
	}
	if !profile.Commander.Docked || !strings.EqualFold(profile.Commander.Name, s.config.commander) ||
		!strings.EqualFold(profile.LastSystem.Name, c.CurrentStarSystem) ||
		!strings.EqualFold(profile.LastStarport.Name, c.Name.Callsign) ||
		(profile.LastStarport.ID != 0 && profile.LastStarport.ID != c.Market.ID) {
		log.Print("Market upload skipped: Frontier profile and carrier journal disagree")
		return nil
	}
	data, err := json.Marshal(market)
	if err != nil {
		return err
	}
	market.Timestamp = observed.Format(time.RFC3339)
	return s.publish(ctx, "commodity/3", fmt.Sprintf("%x", sha256.Sum256(data)), "CAPI-Live-fleetcarrier", "", market)
}
