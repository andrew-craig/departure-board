package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config types

type Config struct {
	Trips []TripConfig `yaml:"trips"`
}

type TripConfig struct {
	Name             string          `yaml:"name"`
	DepartureStops   []StopConfig    `yaml:"departure_stops"`
	Transfer         *TransferConfig `yaml:"transfer,omitempty"`
	FinalArrivalStop FinalStopConfig `yaml:"final_arrival_stop"`
}

type StopConfig struct {
	StopID   string `yaml:"stop_id"`
	StopName string `yaml:"stop_name"`
}

type TransferConfig struct {
	ArrivalStopID   string `yaml:"arrival_stop_id"`
	TransferTime    int    `yaml:"transfer_time"`
	DepartureStopID string `yaml:"departure_stop_id"`
}

type FinalStopConfig struct {
	StopID   string `yaml:"stop_id"`
	WalkTime int    `yaml:"walk_time"`
}

// API types

type Departure struct {
	TripID             string          `json:"trip_id"`
	RouteShortName     string          `json:"route_short_name"`
	RouteLongName      string          `json:"route_long_name"`
	Headsign           string          `json:"headsign"`
	ScheduledDeparture time.Time       `json:"scheduled_departure"`
	RealtimeDeparture  *time.Time      `json:"realtime_departure"`
	DelaySeconds       *int            `json:"delay_seconds"`
	Arrivals           []ArrivalDetail `json:"arrivals,omitempty"`
}

type ArrivalDetail struct {
	StopID           string     `json:"stop_id"`
	StopName         string     `json:"stop_name"`
	ScheduledArrival time.Time  `json:"scheduled_arrival"`
	RealtimeArrival  *time.Time `json:"realtime_arrival"`
}

// View types

type PageData struct {
	Trips []TripView
	Now   time.Time
	Error string
}

type TripView struct {
	Name  string
	Stops []StopView
}

type StopView struct {
	StopID     string
	StopName   string
	Departures []DepartureView
}

type DepartureView struct {
	RouteShortName   string
	Headsign         string
	DepartureTime    string
	MinutesAway      string
	IsRealtime       bool
	IsDelayed        bool
	DelayMinutes     int
	FinalArrivalTime string
	FinalArrivalMins string
	HasConnection    bool
}

var sydneyTZ *time.Location

func init() {
	var err error
	sydneyTZ, err = time.LoadLocation("Australia/Sydney")
	if err != nil {
		log.Fatal("failed to load timezone: ", err)
	}
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	apiURL := os.Getenv("GTFS_API_URL")
	if apiURL == "" {
		apiURL = "http://localhost:8080"
	}
	configPath := "config.yaml"

	cfg, err := loadConfig(configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	tmpl := parseTemplate()
	http.HandleFunc("/", buildHandler(tmpl, apiURL, cfg))

	log.Printf("departure board listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("reading config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing config: %w", err)
	}
	if len(cfg.Trips) == 0 {
		return Config{}, fmt.Errorf("no trips defined in config")
	}
	return cfg, nil
}

func parseTemplate() *template.Template {
	return template.Must(template.New("board").Parse(boardTemplate))
}

func buildHandler(tmpl *template.Template, apiURL string, cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		now := time.Now().In(sydneyTZ)
		data := PageData{Now: now}

		for _, trip := range cfg.Trips {
			tv, err := buildTripView(r.Context(), apiURL, trip, now)
			if err != nil {
				data.Error = fmt.Sprintf("Failed to load trip %q: %v", trip.Name, err)
				break
			}
			data.Trips = append(data.Trips, tv)
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		tmpl.Execute(w, data)
	}
}

func buildTripView(ctx context.Context, apiURL string, trip TripConfig, now time.Time) (TripView, error) {
	tv := TripView{Name: trip.Name}

	var arrivalStopID string
	if trip.Transfer != nil {
		arrivalStopID = trip.Transfer.ArrivalStopID
	} else {
		arrivalStopID = trip.FinalArrivalStop.StopID
	}

	var transferDepartures []Departure
	if trip.Transfer != nil {
		var err error
		transferDepartures, err = fetchDepartures(ctx, apiURL, trip.Transfer.DepartureStopID, trip.FinalArrivalStop.StopID)
		if err != nil {
			return tv, fmt.Errorf("fetching transfer departures: %w", err)
		}
	}

	for _, stop := range trip.DepartureStops {
		departures, err := fetchDepartures(ctx, apiURL, stop.StopID, arrivalStopID)
		if err != nil {
			return tv, fmt.Errorf("fetching departures for stop %s: %w", stop.StopID, err)
		}

		sv := StopView{
			StopID:   stop.StopID,
			StopName: stop.StopName,
		}

		for _, d := range departures {
			depTime := effectiveDeparture(d)
			if depTime.Before(now) || depTime.After(now.Add(20*time.Minute)) {
				continue
			}

			dv := toDepartureView(d, now)

			if trip.Transfer != nil {
				calcTransferArrival(&dv, d, trip, transferDepartures, now)
			} else {
				calcDirectArrival(&dv, d, trip, now)
			}

			sv.Departures = append(sv.Departures, dv)
		}

		tv.Stops = append(tv.Stops, sv)
	}

	return tv, nil
}

func calcTransferArrival(dv *DepartureView, d Departure, trip TripConfig, transferDepartures []Departure, now time.Time) {
	transferArrival := findArrival(d, trip.Transfer.ArrivalStopID)
	if transferArrival == nil {
		dv.HasConnection = false
		dv.FinalArrivalMins = "No connection"
		return
	}

	arrTime := effectiveArrival(*transferArrival)
	earliestTransferDept := arrTime.Add(time.Duration(trip.Transfer.TransferTime) * time.Second)

	connection := findConnection(transferDepartures, earliestTransferDept, trip.FinalArrivalStop.StopID)
	if connection == nil {
		dv.HasConnection = false
		dv.FinalArrivalMins = "No connection"
		return
	}

	finalArr := connection.Add(time.Duration(trip.FinalArrivalStop.WalkTime) * time.Second)
	dv.HasConnection = true
	dv.FinalArrivalTime = finalArr.In(sydneyTZ).Format("15:04")
	dv.FinalArrivalMins = formatMinsAway(finalArr, now)
}

func calcDirectArrival(dv *DepartureView, d Departure, trip TripConfig, now time.Time) {
	finalArrival := findArrival(d, trip.FinalArrivalStop.StopID)
	if finalArrival == nil {
		dv.HasConnection = false
		dv.FinalArrivalMins = "No connection"
		return
	}

	arrTime := effectiveArrival(*finalArrival)
	finalArr := arrTime.Add(time.Duration(trip.FinalArrivalStop.WalkTime) * time.Second)
	dv.HasConnection = true
	dv.FinalArrivalTime = finalArr.In(sydneyTZ).Format("15:04")
	dv.FinalArrivalMins = formatMinsAway(finalArr, now)
}

func formatMinsAway(t time.Time, now time.Time) string {
	mins := int(t.Sub(now).Minutes())
	switch {
	case mins <= 0:
		return "Now"
	case mins == 1:
		return "1 min"
	default:
		return fmt.Sprintf("%d min", mins)
	}
}

func effectiveDeparture(d Departure) time.Time {
	if d.RealtimeDeparture != nil {
		return *d.RealtimeDeparture
	}
	return d.ScheduledDeparture
}

func effectiveArrival(a ArrivalDetail) time.Time {
	if a.RealtimeArrival != nil {
		return *a.RealtimeArrival
	}
	return a.ScheduledArrival
}

func findArrival(d Departure, stopID string) *ArrivalDetail {
	for i := range d.Arrivals {
		if d.Arrivals[i].StopID == stopID {
			return &d.Arrivals[i]
		}
	}
	return nil
}

func findConnection(transferDepartures []Departure, earliestDept time.Time, finalStopID string) *time.Time {
	for _, td := range transferDepartures {
		tdTime := effectiveDeparture(td)
		if tdTime.Before(earliestDept) {
			continue
		}
		arr := findArrival(td, finalStopID)
		if arr != nil {
			t := effectiveArrival(*arr)
			return &t
		}
	}
	return nil
}

func toDepartureView(d Departure, now time.Time) DepartureView {
	depTime := d.ScheduledDeparture
	isRealtime := false
	if d.RealtimeDeparture != nil {
		depTime = *d.RealtimeDeparture
		isRealtime = true
	}

	isDelayed := false
	delayMins := 0
	if d.DelaySeconds != nil && *d.DelaySeconds > 60 {
		isDelayed = true
		delayMins = *d.DelaySeconds / 60
	}

	return DepartureView{
		RouteShortName: d.RouteShortName,
		Headsign:       d.Headsign,
		DepartureTime:  depTime.In(sydneyTZ).Format("15:04"),
		MinutesAway:    formatMinsAway(depTime, now),
		IsRealtime:     isRealtime,
		IsDelayed:      isDelayed,
		DelayMinutes:   delayMins,
	}
}

func fetchDepartures(ctx context.Context, apiURL, stopID, arrivalStops string) ([]Departure, error) {
	url := fmt.Sprintf("%s/departures/arrivals?stop_id=%s&arrival_stops=%s", apiURL, stopID, arrivalStops)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&apiErr)
		if apiErr.Error != "" {
			return nil, fmt.Errorf("API error: %s", apiErr.Error)
		}
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	var departures []Departure
	if err := json.NewDecoder(resp.Body).Decode(&departures); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return departures, nil
}

var boardTemplate = strings.TrimSpace(`
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="30">
<title>Departure Board</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;background:#1a1a2e;color:#eee;min-height:100vh}
.hdr{background:#16213e;padding:12px 16px;display:flex;justify-content:space-between;align-items:center}
.hdr h1{font-size:16px;font-weight:600}
.hdr .time{font-size:13px;opacity:.7}
.tabs{display:flex;background:#16213e;border-bottom:2px solid #0f3460;overflow-x:auto;-webkit-overflow-scrolling:touch}
.tab{padding:10px 20px;font-size:14px;font-weight:500;color:#aaa;cursor:pointer;border-bottom:2px solid transparent;margin-bottom:-2px;white-space:nowrap;user-select:none}
.tab.active{color:#e94560;border-bottom-color:#e94560}
.trip{display:none}
.trip.active{display:block}
.stop-hdr{background:#16213e;padding:8px 16px;font-size:13px;font-weight:600;color:#4ecca3;border-bottom:1px solid rgba(255,255,255,.05)}
.dep{border-bottom:1px solid rgba(255,255,255,.08)}
.dep-row{display:flex;align-items:center;padding:12px 16px;gap:12px}
.route{background:#0f3460;color:#e94560;font-weight:700;font-size:14px;padding:4px 8px;border-radius:4px;min-width:44px;text-align:center;flex-shrink:0}
.info{flex:1;min-width:0}
.headsign{font-size:15px;font-weight:500;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.sched{font-size:12px;opacity:.6;margin-top:2px}
.sched .delay{color:#ff6b6b;opacity:1}
.times{text-align:right;flex-shrink:0}
.times .depart{font-size:18px;font-weight:700;color:#e94560}
.times .depart.rt{color:#4ecca3}
.times .arrive{font-size:13px;color:#4ecca3;margin-top:2px}
.times .arrive.no-conn{color:#ff6b6b}
.times .lbl{font-size:10px;opacity:.5}
.empty{padding:48px 16px;text-align:center;opacity:.5;font-size:14px}
.err{padding:24px 16px;text-align:center;color:#ff6b6b;font-size:14px}
</style>
</head>
<body>
<div class="hdr">
  <h1>Departure Board</h1>
  <span class="time">{{.Now.Format "15:04"}}</span>
</div>
{{if .Error}}
  <div class="err">{{.Error}}</div>
{{else}}
<div class="tabs">
  {{range $i, $t := .Trips}}
  <div class="tab{{if eq $i 0}} active{{end}}" onclick="switchTab({{$i}})">{{$t.Name}}</div>
  {{end}}
</div>
{{range $i, $t := .Trips}}
<div class="trip{{if eq $i 0}} active{{end}}" id="trip-{{$i}}">
  {{range $t.Stops}}
  <div class="stop-hdr">{{.StopName}}</div>
  {{if not .Departures}}
    <div class="empty">No departures in next 20 min</div>
  {{else}}
    {{range .Departures}}
    <div class="dep">
      <div class="dep-row">
        <div class="route">{{.RouteShortName}}</div>
        <div class="info">
          <div class="headsign">{{.Headsign}}</div>
          <div class="sched">
            Departs {{.DepartureTime}}{{if .IsDelayed}} <span class="delay">+{{.DelayMinutes}}m late</span>{{end}}
          </div>
        </div>
        <div class="times">
          <div class="lbl">departs</div>
          <div class="depart{{if .IsRealtime}} rt{{end}}">{{.MinutesAway}}</div>
          {{if .HasConnection}}
          <div class="lbl">arrives</div>
          <div class="arrive">{{.FinalArrivalTime}} ({{.FinalArrivalMins}})</div>
          {{else}}
          <div class="arrive no-conn">{{.FinalArrivalMins}}</div>
          {{end}}
        </div>
      </div>
    </div>
    {{end}}
  {{end}}
  {{end}}
</div>
{{end}}
<script>
function switchTab(idx){
  document.querySelectorAll('.tab').forEach(function(t,i){t.classList.toggle('active',i===idx)});
  document.querySelectorAll('.trip').forEach(function(t,i){t.classList.toggle('active',i===idx)});
  try{localStorage.setItem('activeTab',idx)}catch(e){}
}
(function(){
  try{var s=localStorage.getItem('activeTab');if(s!==null)switchTab(parseInt(s))}catch(e){}
})();
</script>
{{end}}
</body>
</html>
`)
