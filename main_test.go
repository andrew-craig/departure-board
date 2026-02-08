package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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

func TestHandler_MissingStopID(t *testing.T) {
	tmpl := parseTemplate()
	handler := buildHandler(tmpl, "http://localhost:9999")

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "stop_id") {
		t.Error("expected prompt for stop_id parameter")
	}
}

func TestHandler_WithMockAPI(t *testing.T) {
	now := time.Now().In(sydneyTZ).Add(5 * time.Minute)
	departures := []Departure{
		{
			TripID:             "trip1",
			RouteShortName:     "T1",
			RouteLongName:      "Test Line",
			Headsign:           "Test Station",
			ScheduledDeparture: now,
		},
	}

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("stop_id") != "123" {
			t.Errorf("expected stop_id=123, got %s", r.URL.Query().Get("stop_id"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(departures)
	}))
	defer mock.Close()

	tmpl := parseTemplate()
	handler := buildHandler(tmpl, mock.URL)

	req := httptest.NewRequest("GET", "/?stop_id=123", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "T1") {
		t.Error("expected T1 in response")
	}
	if !strings.Contains(body, "Test Station") {
		t.Error("expected Test Station in response")
	}
}

func TestHandler_APIError(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": "db down"})
	}))
	defer mock.Close()

	tmpl := parseTemplate()
	handler := buildHandler(tmpl, mock.URL)

	req := httptest.NewRequest("GET", "/?stop_id=123", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "db down") {
		t.Error("expected error message in response")
	}
}
