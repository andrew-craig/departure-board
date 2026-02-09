package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	yaml := `
trips:
  - name: "Test Trip"
    departure_stops:
      - stop_id: "100"
        stop_name: "Stop A"
    transfer:
      arrival_stop_id: "200"
      transfer_time: 120
      departure_stop_id: "201"
    final_arrival_stop:
      stop_id: "300"
      walk_time: 60
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte(yaml), 0644)

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Trips) != 1 {
		t.Fatalf("expected 1 trip, got %d", len(cfg.Trips))
	}
	if cfg.Trips[0].Name != "Test Trip" {
		t.Errorf("expected trip name 'Test Trip', got %q", cfg.Trips[0].Name)
	}
	if cfg.Trips[0].Transfer == nil {
		t.Fatal("expected transfer to be set")
	}
	if cfg.Trips[0].Transfer.TransferTime != 120 {
		t.Errorf("expected transfer time 120, got %d", cfg.Trips[0].Transfer.TransferTime)
	}
}

func TestLoadConfig_NoTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("trips: []\n"), 0644)

	_, err := loadConfig(path)
	if err == nil {
		t.Fatal("expected error for empty trips")
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	_, err := loadConfig("/nonexistent/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestToDepartureView(t *testing.T) {
	now := time.Now().In(sydneyTZ)
	future := now.Add(12 * time.Minute)
	delay := 120

	d := Departure{
		TripID:             "trip1",
		RouteShortName:     "T1",
		RouteLongName:      "North Shore Line",
		Headsign:           "Hornsby",
		ScheduledDeparture: future,
		RealtimeDeparture:  &future,
		DelaySeconds:       &delay,
	}

	view := toDepartureView(d, now)

	if view.RouteShortName != "T1" {
		t.Errorf("expected route T1, got %s", view.RouteShortName)
	}
	if view.Headsign != "Hornsby" {
		t.Errorf("expected headsign Hornsby, got %s", view.Headsign)
	}
	if !view.IsRealtime {
		t.Error("expected IsRealtime true")
	}
	if !view.IsDelayed {
		t.Error("expected IsDelayed true for 120s delay")
	}
	if view.DelayMinutes != 2 {
		t.Errorf("expected 2 delay minutes, got %d", view.DelayMinutes)
	}
	if !strings.Contains(view.MinutesAway, "min") {
		t.Errorf("expected minutes away string, got %s", view.MinutesAway)
	}
}

func TestToDepartureView_Now(t *testing.T) {
	now := time.Now().In(sydneyTZ)
	past := now.Add(-1 * time.Minute)

	d := Departure{
		RouteShortName:     "333",
		Headsign:           "Bondi Beach",
		ScheduledDeparture: past,
	}

	view := toDepartureView(d, now)
	if view.MinutesAway != "Now" {
		t.Errorf("expected 'Now', got %s", view.MinutesAway)
	}
	if view.IsRealtime {
		t.Error("expected IsRealtime false when no realtime data")
	}
}

func TestFindArrival(t *testing.T) {
	d := Departure{
		Arrivals: []ArrivalDetail{
			{StopID: "100", StopName: "Stop A"},
			{StopID: "200", StopName: "Stop B"},
		},
	}

	a := findArrival(d, "200")
	if a == nil {
		t.Fatal("expected to find arrival for stop 200")
	}
	if a.StopName != "Stop B" {
		t.Errorf("expected Stop B, got %s", a.StopName)
	}

	a = findArrival(d, "999")
	if a != nil {
		t.Error("expected nil for non-existent stop")
	}
}

func TestFindConnection(t *testing.T) {
	now := time.Now().In(sydneyTZ)

	transferDeps := []Departure{
		{
			ScheduledDeparture: now.Add(10 * time.Minute),
			Arrivals: []ArrivalDetail{
				{StopID: "300", ScheduledArrival: now.Add(25 * time.Minute)},
			},
		},
		{
			ScheduledDeparture: now.Add(20 * time.Minute),
			Arrivals: []ArrivalDetail{
				{StopID: "300", ScheduledArrival: now.Add(35 * time.Minute)},
			},
		},
	}

	// Should find second departure (first is too early)
	earliest := now.Add(15 * time.Minute)
	conn := findConnection(transferDeps, earliest, "300")
	if conn == nil {
		t.Fatal("expected to find connection")
	}
	expected := now.Add(35 * time.Minute)
	if !conn.Truncate(time.Second).Equal(expected.Truncate(time.Second)) {
		t.Errorf("expected arrival at %v, got %v", expected, *conn)
	}

	// No connection available
	conn = findConnection(transferDeps, now.Add(60*time.Minute), "300")
	if conn != nil {
		t.Error("expected no connection")
	}

	// Wrong stop
	conn = findConnection(transferDeps, now, "999")
	if conn != nil {
		t.Error("expected no connection for wrong stop")
	}
}

func TestEffectiveDeparture(t *testing.T) {
	now := time.Now()
	rt := now.Add(5 * time.Minute)

	d := Departure{ScheduledDeparture: now, RealtimeDeparture: &rt}
	if !effectiveDeparture(d).Equal(rt) {
		t.Error("expected realtime departure")
	}

	d2 := Departure{ScheduledDeparture: now}
	if !effectiveDeparture(d2).Equal(now) {
		t.Error("expected scheduled departure")
	}
}

func TestEffectiveArrival(t *testing.T) {
	now := time.Now()
	rt := now.Add(5 * time.Minute)

	a := ArrivalDetail{ScheduledArrival: now, RealtimeArrival: &rt}
	if !effectiveArrival(a).Equal(rt) {
		t.Error("expected realtime arrival")
	}

	a2 := ArrivalDetail{ScheduledArrival: now}
	if !effectiveArrival(a2).Equal(now) {
		t.Error("expected scheduled arrival")
	}
}

func TestFormatMinsAway(t *testing.T) {
	now := time.Now()

	tests := []struct {
		offset   time.Duration
		expected string
	}{
		{-1 * time.Minute, "Now"},
		{0, "Now"},
		{90 * time.Second, "1 min"},
		{5 * time.Minute, "5 min"},
		{20 * time.Minute, "20 min"},
	}

	for _, tc := range tests {
		got := formatMinsAway(now.Add(tc.offset), now)
		if got != tc.expected {
			t.Errorf("offset %v: expected %q, got %q", tc.offset, tc.expected, got)
		}
	}
}

// Mock API that responds based on stop_id
func newMockAPI(t *testing.T, responses map[string][]Departure) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stopID := r.URL.Query().Get("stop_id")
		deps, ok := responses[stopID]
		if !ok {
			deps = []Departure{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(deps)
	}))
}

func TestHandler_DirectTrip(t *testing.T) {
	now := time.Now().In(sydneyTZ)

	responses := map[string][]Departure{
		"100": {
			{
				TripID:             "trip1",
				RouteShortName:     "T1",
				Headsign:           "City",
				ScheduledDeparture: now.Add(5 * time.Minute),
				Arrivals: []ArrivalDetail{
					{StopID: "300", StopName: "Final Stop", ScheduledArrival: now.Add(30 * time.Minute)},
				},
			},
		},
	}

	mock := newMockAPI(t, responses)
	defer mock.Close()

	cfg := Config{
		Trips: []TripConfig{
			{
				Name:             "Direct",
				DepartureStops:   []StopConfig{{StopID: "100", StopName: "Start"}},
				FinalArrivalStop: FinalStopConfig{StopID: "300", WalkTime: 120},
			},
		},
	}

	tmpl := parseTemplate()
	handler := buildHandler(tmpl, mock.URL, cfg)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(body, "T1") {
		t.Error("expected T1 in response")
	}
	if !strings.Contains(body, "City") {
		t.Error("expected headsign City in response")
	}
	if !strings.Contains(body, "Direct") {
		t.Error("expected trip name Direct in response")
	}
	if !strings.Contains(body, "Start") {
		t.Error("expected stop name Start in response")
	}
	// Should have a connection (direct arrival + 120s walk)
	if strings.Contains(body, "No connection") {
		t.Error("expected a viable connection for direct trip")
	}
}

func TestHandler_TransferTrip(t *testing.T) {
	now := time.Now().In(sydneyTZ)

	responses := map[string][]Departure{
		// Departures from origin
		"100": {
			{
				TripID:             "trip1",
				RouteShortName:     "T1",
				Headsign:           "Transfer Hub",
				ScheduledDeparture: now.Add(5 * time.Minute),
				Arrivals: []ArrivalDetail{
					{StopID: "200", StopName: "Transfer Arrival", ScheduledArrival: now.Add(15 * time.Minute)},
				},
			},
		},
		// Departures from transfer departure stop
		"201": {
			{
				TripID:             "trip2",
				RouteShortName:     "T2",
				Headsign:           "Final Dest",
				ScheduledDeparture: now.Add(20 * time.Minute),
				Arrivals: []ArrivalDetail{
					{StopID: "300", StopName: "Final Stop", ScheduledArrival: now.Add(35 * time.Minute)},
				},
			},
		},
	}

	mock := newMockAPI(t, responses)
	defer mock.Close()

	cfg := Config{
		Trips: []TripConfig{
			{
				Name:           "With Transfer",
				DepartureStops: []StopConfig{{StopID: "100", StopName: "Origin"}},
				Transfer: &TransferConfig{
					ArrivalStopID:   "200",
					TransferTime:    300, // 5 minutes
					DepartureStopID: "201",
				},
				FinalArrivalStop: FinalStopConfig{StopID: "300", WalkTime: 60},
			},
		},
	}

	tmpl := parseTemplate()
	handler := buildHandler(tmpl, mock.URL, cfg)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(body, "T1") {
		t.Error("expected T1 in response")
	}
	if !strings.Contains(body, "With Transfer") {
		t.Error("expected trip name in response")
	}
	// Transfer arrives at 15min, +5min transfer = 20min, T2 departs at 20min, arrives at 35min + 1min walk = 36min
	if strings.Contains(body, "No connection") {
		t.Error("expected viable connection")
	}
}

func TestHandler_NoConnection(t *testing.T) {
	now := time.Now().In(sydneyTZ)

	responses := map[string][]Departure{
		"100": {
			{
				TripID:             "trip1",
				RouteShortName:     "T1",
				Headsign:           "Somewhere",
				ScheduledDeparture: now.Add(5 * time.Minute),
				// No arrivals at the final stop
				Arrivals: []ArrivalDetail{},
			},
		},
	}

	mock := newMockAPI(t, responses)
	defer mock.Close()

	cfg := Config{
		Trips: []TripConfig{
			{
				Name:             "No Conn",
				DepartureStops:   []StopConfig{{StopID: "100", StopName: "Start"}},
				FinalArrivalStop: FinalStopConfig{StopID: "300", WalkTime: 60},
			},
		},
	}

	tmpl := parseTemplate()
	handler := buildHandler(tmpl, mock.URL, cfg)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "No connection") {
		t.Error("expected 'No connection' in response")
	}
}

func TestHandler_APIError(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": "db down"})
	}))
	defer mock.Close()

	cfg := Config{
		Trips: []TripConfig{
			{
				Name:             "Err Trip",
				DepartureStops:   []StopConfig{{StopID: "100", StopName: "Start"}},
				FinalArrivalStop: FinalStopConfig{StopID: "300", WalkTime: 60},
			},
		},
	}

	tmpl := parseTemplate()
	handler := buildHandler(tmpl, mock.URL, cfg)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "db down") {
		t.Error("expected error message in response")
	}
}

func TestHandler_NotFound(t *testing.T) {
	cfg := Config{
		Trips: []TripConfig{
			{
				Name:             "Test",
				DepartureStops:   []StopConfig{{StopID: "100", StopName: "Start"}},
				FinalArrivalStop: FinalStopConfig{StopID: "300", WalkTime: 60},
			},
		},
	}

	tmpl := parseTemplate()
	handler := buildHandler(tmpl, "http://localhost:9999", cfg)

	req := httptest.NewRequest("GET", "/other", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandler_MultipleTabs(t *testing.T) {
	now := time.Now().In(sydneyTZ)

	responses := map[string][]Departure{
		"100": {
			{
				RouteShortName:     "T1",
				Headsign:           "City",
				ScheduledDeparture: now.Add(5 * time.Minute),
				Arrivals: []ArrivalDetail{
					{StopID: "300", StopName: "Final", ScheduledArrival: now.Add(30 * time.Minute)},
				},
			},
		},
		"200": {
			{
				RouteShortName:     "T2",
				Headsign:           "Home",
				ScheduledDeparture: now.Add(8 * time.Minute),
				Arrivals: []ArrivalDetail{
					{StopID: "100", StopName: "Start", ScheduledArrival: now.Add(40 * time.Minute)},
				},
			},
		},
	}

	mock := newMockAPI(t, responses)
	defer mock.Close()

	cfg := Config{
		Trips: []TripConfig{
			{
				Name:             "To Work",
				DepartureStops:   []StopConfig{{StopID: "100", StopName: "Home"}},
				FinalArrivalStop: FinalStopConfig{StopID: "300", WalkTime: 60},
			},
			{
				Name:             "To Home",
				DepartureStops:   []StopConfig{{StopID: "200", StopName: "Work"}},
				FinalArrivalStop: FinalStopConfig{StopID: "100", WalkTime: 120},
			},
		},
	}

	tmpl := parseTemplate()
	handler := buildHandler(tmpl, mock.URL, cfg)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "To Work") {
		t.Error("expected 'To Work' tab")
	}
	if !strings.Contains(body, "To Home") {
		t.Error("expected 'To Home' tab")
	}
	if !strings.Contains(body, "T1") {
		t.Error("expected T1 route")
	}
	if !strings.Contains(body, "T2") {
		t.Error("expected T2 route")
	}
}
