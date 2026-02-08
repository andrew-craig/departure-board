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
)

type Departure struct {
	TripID             string           `json:"trip_id"`
	RouteShortName     string           `json:"route_short_name"`
	RouteLongName      string           `json:"route_long_name"`
	Headsign           string           `json:"headsign"`
	ScheduledDeparture time.Time        `json:"scheduled_departure"`
	RealtimeDeparture  *time.Time       `json:"realtime_departure"`
	DelaySeconds       *int             `json:"delay_seconds"`
	Arrivals           []ArrivalDetail  `json:"arrivals,omitempty"`
}

type ArrivalDetail struct {
	StopID           string     `json:"stop_id"`
	StopName         string     `json:"stop_name"`
	ScheduledArrival time.Time  `json:"scheduled_arrival"`
	RealtimeArrival  *time.Time `json:"realtime_arrival"`
}

type PageData struct {
	StopID       string
	ArrivalStops string
	Departures   []DepartureView
	Error        string
	Now          time.Time
}

type DepartureView struct {
	RouteShortName string
	Headsign       string
	DepartureTime  string
	MinutesAway    string
	IsRealtime     bool
	IsDelayed      bool
	DelayMinutes   int
	Arrivals       []ArrivalView
}

type ArrivalView struct {
	StopName    string
	ArrivalTime string
	IsRealtime  bool
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

	tmpl := parseTemplate()
	http.HandleFunc("/", buildHandler(tmpl, apiURL))

	log.Printf("departure board listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func parseTemplate() *template.Template {
	return template.Must(template.New("board").Parse(boardTemplate))
}

func buildHandler(tmpl *template.Template, apiURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		stopID := r.URL.Query().Get("stop_id")
		arrivalStops := r.URL.Query().Get("arrival_stops")

		data := PageData{
			StopID:       stopID,
			ArrivalStops: arrivalStops,
			Now:          time.Now().In(sydneyTZ),
		}

		if stopID == "" {
			data.Error = "Provide a stop_id query parameter, e.g. /?stop_id=200060"
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			tmpl.Execute(w, data)
			return
		}

		departures, err := fetchDepartures(r.Context(), apiURL, stopID, arrivalStops)
		if err != nil {
			data.Error = fmt.Sprintf("Failed to fetch departures: %v", err)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			tmpl.Execute(w, data)
			return
		}

		now := time.Now().In(sydneyTZ)
		data.Now = now
		for _, d := range departures {
			dv := toDepartureView(d, now)
			data.Departures = append(data.Departures, dv)
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		tmpl.Execute(w, data)
	}
}

func fetchDepartures(ctx context.Context, apiURL, stopID, arrivalStops string) ([]Departure, error) {
	var url string
	if arrivalStops != "" {
		url = fmt.Sprintf("%s/departures/arrivals?stop_id=%s&arrival_stops=%s", apiURL, stopID, arrivalStops)
	} else {
		url = fmt.Sprintf("%s/departures?stop_id=%s", apiURL, stopID)
	}

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
		var apiErr struct{ Error string `json:"error"` }
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

func toDepartureView(d Departure, now time.Time) DepartureView {
	depTime := d.ScheduledDeparture
	isRealtime := false
	if d.RealtimeDeparture != nil {
		depTime = *d.RealtimeDeparture
		isRealtime = true
	}

	mins := int(time.Until(depTime).Minutes())
	var minsAway string
	switch {
	case mins <= 0:
		minsAway = "Now"
	case mins == 1:
		minsAway = "1 min"
	default:
		minsAway = fmt.Sprintf("%d min", mins)
	}

	isDelayed := false
	delayMins := 0
	if d.DelaySeconds != nil && *d.DelaySeconds > 60 {
		isDelayed = true
		delayMins = *d.DelaySeconds / 60
	}

	var arrivals []ArrivalView
	for _, a := range d.Arrivals {
		at := a.ScheduledArrival
		rt := false
		if a.RealtimeArrival != nil {
			at = *a.RealtimeArrival
			rt = true
		}
		arrivals = append(arrivals, ArrivalView{
			StopName:    a.StopName,
			ArrivalTime: at.In(sydneyTZ).Format("15:04"),
			IsRealtime:  rt,
		})
	}

	return DepartureView{
		RouteShortName: d.RouteShortName,
		Headsign:       d.Headsign,
		DepartureTime:  depTime.In(sydneyTZ).Format("15:04"),
		MinutesAway:    minsAway,
		IsRealtime:     isRealtime,
		IsDelayed:      isDelayed,
		DelayMinutes:   delayMins,
		Arrivals:       arrivals,
	}
}

var boardTemplate = strings.TrimSpace(`
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="30">
<title>Departures{{if .StopID}} — Stop {{.StopID}}{{end}}</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;background:#1a1a2e;color:#eee;min-height:100vh}
.hdr{background:#16213e;padding:12px 16px;display:flex;justify-content:space-between;align-items:center}
.hdr h1{font-size:16px;font-weight:600}
.hdr .time{font-size:13px;opacity:.7}
.err{padding:24px 16px;text-align:center;color:#ff6b6b;font-size:14px}
.dep{border-bottom:1px solid rgba(255,255,255,.08)}
.dep-row{display:flex;align-items:center;padding:12px 16px;gap:12px}
.route{background:#0f3460;color:#e94560;font-weight:700;font-size:14px;padding:4px 8px;border-radius:4px;min-width:44px;text-align:center;flex-shrink:0}
.info{flex:1;min-width:0}
.headsign{font-size:15px;font-weight:500;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.sched{font-size:12px;opacity:.6;margin-top:2px}
.sched .delay{color:#ff6b6b;opacity:1}
.eta{text-align:right;flex-shrink:0}
.eta .mins{font-size:18px;font-weight:700;color:#e94560}
.eta .rt{color:#4ecca3}
.eta .time{font-size:11px;opacity:.6}
.arrivals{padding:0 16px 10px 72px;display:flex;flex-wrap:wrap;gap:4px 16px}
.arr{font-size:12px;opacity:.7}
.arr .arr-rt{color:#4ecca3;opacity:1}
.empty{padding:48px 16px;text-align:center;opacity:.5;font-size:14px}
</style>
</head>
<body>
<div class="hdr">
  <h1>Departures{{if .StopID}} — Stop {{.StopID}}{{end}}</h1>
  <span class="time">{{.Now.Format "15:04"}}</span>
</div>
{{if .Error}}
  <div class="err">{{.Error}}</div>
{{else if not .Departures}}
  <div class="empty">No upcoming departures</div>
{{else}}
  {{range .Departures}}
  <div class="dep">
    <div class="dep-row">
      <div class="route">{{.RouteShortName}}</div>
      <div class="info">
        <div class="headsign">{{.Headsign}}</div>
        <div class="sched">
          Sched {{.DepartureTime}}{{if .IsDelayed}} <span class="delay">+{{.DelayMinutes}}m late</span>{{end}}
        </div>
      </div>
      <div class="eta">
        <div class="mins{{if .IsRealtime}} rt{{end}}">{{.MinutesAway}}</div>
        <div class="time">{{.DepartureTime}}</div>
      </div>
    </div>
    {{if .Arrivals}}
    <div class="arrivals">
      {{range .Arrivals}}
      <span class="arr">{{.StopName}} <strong{{if .IsRealtime}} class="arr-rt"{{end}}>{{.ArrivalTime}}</strong></span>
      {{end}}
    </div>
    {{end}}
  </div>
  {{end}}
{{end}}
</body>
</html>
`)
