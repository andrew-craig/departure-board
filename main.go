package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config types

type Config struct {
	GtfsAPIURL string       `yaml:"gtfs_api_url"`
	Port       string       `yaml:"port"`
	Trips      []TripConfig `yaml:"trips"`
}

type TripConfig struct {
	Name   string        `yaml:"name"`
	Routes []RouteConfig `yaml:"routes"`
}

type RouteConfig struct {
	RouteName               string   `yaml:"route_name"`
	DepartureStopID         string   `yaml:"departure_stop_id"`
	DepartureName           string   `yaml:"departure_name"`
	Leg1Services            []string `yaml:"leg_1_services,omitempty"`
	TransferArrivalStopID   string   `yaml:"transfer_arrival_stop_id,omitempty"`
	TransferTime            int      `yaml:"transfer_time,omitempty"`
	TransferDepartureStopID string   `yaml:"transfer_departure_stop_id,omitempty"`
	TransferName            string   `yaml:"transfer_name,omitempty"`
	Leg2Services            []string `yaml:"leg_2_services,omitempty"`
	FinalArrivalStop        string   `yaml:"final_arrival_stop"`
	FinalWalkTime           int      `yaml:"final_walk_time"`
	ArrivalName             string   `yaml:"arrival_name"`
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

const departureWindowMinutes = 60

type PageData struct {
	Trips         []TripView
	Now           time.Time
	Error         string
	WindowMinutes int
}

type TripView struct {
	Name       string
	Departures []DepartureView
}

type DepartureView struct {
	RouteShortName      string
	RouteColor          string
	Headsign            string
	DepartureTime       string
	MinutesAway         string
	MinutesAwayLabel    string
	IsRealtime          bool
	IsDelayed           bool
	DelayMinutes        int
	FinalArrivalTime    string
	FinalArrivalMins    string
	HasConnection       bool
	SecondLegRouteShort string
	SecondLegRouteColor string
	SecondLegHeadsign   string
	TransferWaitMins    int
	DepartureName       string
	TransferName        string
	ArrivalName         string
	finalArrivalSort    time.Time
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
	configPath := "config.yaml"

	cfg, err := loadConfig(configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	port := cfg.Port
	if port == "" {
		port = os.Getenv("PORT")
		if port == "" {
			port = "3000"
		}
	}

	apiURL := cfg.GtfsAPIURL
	if apiURL == "" {
		apiURL = os.Getenv("GTFS_API_URL")
		if apiURL == "" {
			apiURL = "http://localhost:8080"
		}
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
		data := PageData{Now: now, WindowMinutes: departureWindowMinutes}

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

	for _, route := range trip.Routes {
		deps, err := buildRouteDepartures(ctx, apiURL, route, now)
		if err != nil {
			return tv, fmt.Errorf("building route %q: %w", route.RouteName, err)
		}
		tv.Departures = append(tv.Departures, deps...)
	}

	sort.Slice(tv.Departures, func(i, j int) bool {
		return tv.Departures[i].finalArrivalSort.Before(tv.Departures[j].finalArrivalSort)
	})

	return tv, nil
}

func buildRouteDepartures(ctx context.Context, apiURL string, route RouteConfig, now time.Time) ([]DepartureView, error) {
	hasTransfer := route.TransferArrivalStopID != ""

	// Determine the arrival stop for the first-leg query
	var firstLegArrivalStop string
	if hasTransfer {
		firstLegArrivalStop = route.TransferArrivalStopID
	} else {
		firstLegArrivalStop = route.FinalArrivalStop
	}

	departures, err := fetchDepartures(ctx, apiURL, route.DepartureStopID, firstLegArrivalStop)
	if err != nil {
		return nil, fmt.Errorf("fetching departures for stop %s: %w", route.DepartureStopID, err)
	}

	// Filter first-leg departures by allowed services
	if len(route.Leg1Services) > 0 {
		filtered := departures[:0]
		for _, d := range departures {
			if matchesServices(d.RouteShortName, route.Leg1Services) {
				filtered = append(filtered, d)
			}
		}
		departures = filtered
	}

	// If there's a transfer and the second leg requires transit (different stops),
	// fetch departures for the connecting service
	var transferDepartures []Departure
	needsSecondLeg := hasTransfer && route.TransferDepartureStopID != route.FinalArrivalStop
	if needsSecondLeg {
		transferDepartures, err = fetchDepartures(ctx, apiURL, route.TransferDepartureStopID, route.FinalArrivalStop)
		if err != nil {
			return nil, fmt.Errorf("fetching transfer departures: %w", err)
		}

		// Filter second-leg departures by allowed services
		if len(route.Leg2Services) > 0 {
			filtered := transferDepartures[:0]
			for _, d := range transferDepartures {
				if matchesServices(d.RouteShortName, route.Leg2Services) {
					filtered = append(filtered, d)
				}
			}
			transferDepartures = filtered
		}
	}

	var result []DepartureView
	for _, d := range departures {
		depTime := effectiveDeparture(d)
		if depTime.Before(now) || depTime.After(now.Add(departureWindowMinutes*time.Minute)) {
			continue
		}

		dv := toDepartureView(d, route, now)

		if hasTransfer {
			calcTransferArrival(&dv, d, route, transferDepartures, needsSecondLeg, now)
		} else {
			calcDirectArrival(&dv, d, route, now)
		}

		// Only show departures with valid connections
		if dv.HasConnection {
			result = append(result, dv)
		}
	}

	return result, nil
}

func calcTransferArrival(dv *DepartureView, d Departure, route RouteConfig, transferDepartures []Departure, needsSecondLeg bool, now time.Time) {
	transferArrival := findArrival(d, route.TransferArrivalStopID)
	if transferArrival == nil {
		dv.HasConnection = false
		dv.FinalArrivalMins = "No connection"
		return
	}

	arrTime := effectiveArrival(*transferArrival)

	if needsSecondLeg {
		// Need a connecting service from transfer departure stop to final stop
		earliestTransferDept := arrTime.Add(time.Duration(route.TransferTime) * time.Second)
		connection := findConnection(transferDepartures, earliestTransferDept, route.FinalArrivalStop)
		if connection == nil {
			dv.HasConnection = false
			dv.FinalArrivalMins = "No connection"
			return
		}
		finalArr := connection.ArrivalTime.Add(time.Duration(route.FinalWalkTime) * time.Second)
		dv.HasConnection = true
		dv.FinalArrivalTime = finalArr.In(sydneyTZ).Format("15:04")
		dv.FinalArrivalMins = formatMinsAway(finalArr, now)
		dv.finalArrivalSort = finalArr
		dv.SecondLegRouteShort = connection.RouteShortName
		dv.SecondLegRouteColor = routeColor(connection.RouteShortName)
		dv.SecondLegHeadsign = connection.Headsign
		dv.TransferWaitMins = int(connection.DepartureTime.Sub(arrTime).Minutes())
	} else {
		// Walk-only transfer: arrival at transfer stop + transfer walk + final walk
		finalArr := arrTime.Add(time.Duration(route.TransferTime+route.FinalWalkTime) * time.Second)
		dv.HasConnection = true
		dv.FinalArrivalTime = finalArr.In(sydneyTZ).Format("15:04")
		dv.FinalArrivalMins = formatMinsAway(finalArr, now)
		dv.finalArrivalSort = finalArr
	}
}

func calcDirectArrival(dv *DepartureView, d Departure, route RouteConfig, now time.Time) {
	finalArrival := findArrival(d, route.FinalArrivalStop)
	if finalArrival == nil {
		dv.HasConnection = false
		dv.FinalArrivalMins = "No connection"
		return
	}

	arrTime := effectiveArrival(*finalArrival)
	finalArr := arrTime.Add(time.Duration(route.FinalWalkTime) * time.Second)
	dv.HasConnection = true
	dv.FinalArrivalTime = finalArr.In(sydneyTZ).Format("15:04")
	dv.FinalArrivalMins = formatMinsAway(finalArr, now)
	dv.finalArrivalSort = finalArr
}

func formatMinsAway(t time.Time, now time.Time) string {
	mins := int(t.Sub(now).Minutes())
	switch {
	case mins <= 0:
		return "0"
	case mins == 1:
		return "1"
	default:
		return fmt.Sprintf("%d", mins)
	}
}

func formatMinsAwayLabel(t time.Time, now time.Time) string {
	mins := int(t.Sub(now).Minutes())
	switch {
	case mins <= 0:
		return "mins"
	case mins == 1:
		return "min"
	default:
		return "mins"
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

type ConnectionResult struct {
	DepartureTime  time.Time
	ArrivalTime    time.Time
	RouteShortName string
	Headsign       string
}

func findConnection(transferDepartures []Departure, earliestDept time.Time, finalStopID string) *ConnectionResult {
	for _, td := range transferDepartures {
		tdTime := effectiveDeparture(td)
		if tdTime.Before(earliestDept) {
			continue
		}
		arr := findArrival(td, finalStopID)
		if arr != nil {
			return &ConnectionResult{
				DepartureTime:  tdTime,
				ArrivalTime:    effectiveArrival(*arr),
				RouteShortName: td.RouteShortName,
				Headsign:       td.Headsign,
			}
		}
	}
	return nil
}

func matchesServices(routeShortName string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if a == routeShortName {
			return true
		}
	}
	return false
}

func routeColor(routeShortName string) string {
	if strings.HasPrefix(routeShortName, "M") {
		return "#168388"
	}
	if strings.HasPrefix(routeShortName, "L") {
		return "#E4022D"
	}
	return "#009ED7"
}

func toDepartureView(d Departure, route RouteConfig, now time.Time) DepartureView {
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
		RouteShortName:   d.RouteShortName,
		RouteColor:       routeColor(d.RouteShortName),
		Headsign:         d.Headsign,
		DepartureTime:    depTime.In(sydneyTZ).Format("15:04"),
		MinutesAway:      formatMinsAway(depTime, now),
		MinutesAwayLabel: formatMinsAwayLabel(depTime, now),
		IsRealtime:       isRealtime,
		IsDelayed:        isDelayed,
		DelayMinutes:     delayMins,
		DepartureName:    route.DepartureName,
		TransferName:     route.TransferName,
		ArrivalName:      route.ArrivalName,
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
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=IBM+Plex+Sans:ital,wght@0,100..700;1,100..700&display=swap" rel="stylesheet">
<style>
:root{--accent-color: #ea580c;--bg-color: #fafafa;--header-bg-color: #e4e4e4; --text-color: #1a1a1a; --secondary-text-color: #555}
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:"IBM Plex Sans",system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;background:var(--bg-color);color:var(--text-color);min-height:100vh}
.topbar{background:var(--header-bg-color);padding-left:16px;padding-right:16px;display:flex;align-items:center;}
.hdr{justify-content:space-between;padding-top:16px;padding-bottom:16px}
.hdr h1{font-size:16px;font-weight:600}
.hdr .time{font-size:13px;color:var(--secondary-text-color)}
.tabs{gap:16px;justify-content:flex-start;overflow-x:auto;padding-top:0;padding-bottom:2px}
.tab{padding:10px 0px;font-size:14px;font-weight:400;cursor:pointer;border-bottom:2px solid transparent;margin-bottom:-2px;white-space:nowrap;user-select:none}
.tab.active{font-weight:700;border-bottom-color:var(--accent-color)}
.trip{display:none}
.trip.active{display:block}
.dep{border-bottom:1px solid var(--header-bg-color)}
.dep-row{display:flex;align-items:flex-start;padding:12px 16px;gap:16px}
.route{color:var(--bg-color);font-weight:700;font-size:14px;padding:4px 8px;border-radius:4px;min-width:44px;text-align:center;flex-shrink:0}
.info{flex-grow:3;flex-basis:70%;flex-shrink:1;display:flex;flex-direction:column;align-items:center;gap:8px;min-width:0}
.info-top{display:flex;gap:8px;align-items:center;width:100%}
.info-bottom{display:flex;gap:8px;align-items:center;width:100%}
.route-details{font-size:13px;font-weight:400;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.sched{font-size:12px;opacity:.6;margin-top:2px}
.sched .delay{color:#ff6b6b;opacity:1}
.deptime{display:flex;flex-direction:row;align-items:center;gap:8px;width:50px;flex-shrink:0}
.depindicator{width:8px;height:8px;border-radius:50%;background:var(--secondary-text-color)}
.rt{background:#4ecca3}
.delay{background:#ff6b6b}
.mindep{display:flex;flex-direction:column;align-items:center}
.minval{font-size:24px;font-weight:700}
.minlabel{font-size:12px;color:var(--secondary-text-color)}
.times{text-align:right;flex-grow:1;flex-basis:15%;flex-shrink:0;min-width:60px}
.times .time{font-size:20px;font-weight:500}
.times .lbl{font-size:12px;color:var(--secondary-text-color)}
.transfer-wait{font-size:12px;color:var(--secondary-text-color);font-weight:500}
.empty{padding:48px 16px;text-align:center;opacity:.5;font-size:14px}
.err{padding:24px 16px;text-align:center;color:#ff6b6b;font-size:14px}
@media (max-width: 540px) {
	.departs{display:none}
}
</style>
</head>
<body>
  <div class="topbar hdr">
    <h1>Departure Board</h1>
  	<span class="time">{{.Now.Format "15:04"}}</span>
  </div>

  {{if .Error}} 
  <div class="err">
    {{.Error}}
  </div>
  {{else}}

  <div class="topbar tabs">
  	{{range $i, $t := .Trips}}
  	<div class="tab{{if eq $i 0}} active{{end}}" onclick="switchTab({{$i}})">{{$t.Name}}</div>
  	{{end}}
  </div>
  

{{range $i, $t := .Trips}}
<div class="trip{{if eq $i 0}} active{{end}}" id="trip-{{$i}}">
  {{if not $t.Departures}}
    <div class="empty">No departures in next {{$.WindowMinutes}} min</div>
  {{else}}
    {{range $t.Departures}}
    <div class="dep">
    	<div class="dep-row">
			<div class="deptime">
				<div class="depindicator{{if .IsRealtime}} rt{{end}} {{if .IsDelayed}} delay{{end}}"></div>
				<div class="mindep">
					<span class="minval">{{.MinutesAway}}</span>
					<span class="minlabel">{{.MinutesAwayLabel}}</span>
					</div>
			</div>
    		<div class="info">
				<div class="info-top">
					<div class="route" style="background:{{.RouteColor}}">{{.RouteShortName}}</div>
					{{if .SecondLegRouteShort}}<span class="transfer-wait">{{.TransferWaitMins}}m</span><div class="route" style="background:{{.SecondLegRouteColor}}">{{.SecondLegRouteShort}}</div>{{end}}
				</div>
				<div class="info-bottom">
	        		<div class="route-details">{{.DepartureName}} →
					{{if .TransferName}}{{.TransferName}} →{{end}}
					{{.ArrivalName}}
					</div>
				</div>
        	</div>
        	<div class="times departs">
          		<div class="lbl">Departs</div>
		  		<div class="time">{{.DepartureTime}}</div>
        	</div>
        	<div class="times">
          		<div class="lbl">Arrives</div>
          		<div class="time">{{.FinalArrivalTime}}</div>
        	</div>
    	</div>
    </div>
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
