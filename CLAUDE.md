# Departure Board

Mobile-optimised departure board with server-side rendering. Trips are defined in `config.yaml` and displayed as tabs.

## Architecture

- **Language**: Go + `gopkg.in/yaml.v3`
- **Rendering**: Server-side HTML via `html/template`
- **Styling**: Inline CSS optimised for mobile viewports
- **Data source**: Local GTFS Departure Service API (see below)

## How it works

1. On startup the server reads `config.yaml` which defines predefined trips
2. User visits `/` — each trip is rendered as a tab
3. For each trip, the server fetches departures (next 20 min) from each departure stop
4. For each departure, the server calculates the earliest final arrival time (including transfers and walk time)
5. Page auto-refreshes every 30 seconds; active tab is persisted via localStorage

## Trip configuration (`config.yaml`)

```yaml
trips:
  - name: "To Work"                    # Tab label
    departure_stops:                    # Array of boarding points
      - stop_id: "200060"
        stop_name: "Home Station"
    transfer:                           # Optional (omit for direct trips)
      arrival_stop_id: "200010"         # Alight here
      transfer_time: 300                # Walk time to next stop (seconds)
      departure_stop_id: "200015"       # Board here
    final_arrival_stop:
      stop_id: "200020"                 # Final destination stop
      walk_time: 600                    # Walk from stop to destination (seconds)
```

### Final arrival time calculation

**Direct trip** (no transfer):
1. Fetch departures from departure stop with arrivals at final stop
2. Final arrival = arrival at final stop + walk_time

**Trip with transfer**:
1. Fetch departures from departure stop with arrivals at transfer arrival stop
2. Fetch departures from transfer departure stop with arrivals at final stop
3. For each departure: arrival at transfer stop + transfer_time = earliest transfer departure
4. Find first connecting departure from transfer stop after that time
5. Final arrival = connecting service arrival at final stop + walk_time
6. If no connection exists, display "No connection"

## GTFS Departure Service API

Base URL: `http://localhost:8080` (configurable via `GTFS_API_URL` env var)

### `GET /departures/arrivals?stop_id={stop_id}&arrival_stops={stop1,stop2}`

Returns departures from a stop within the next 60 minutes, plus arrival times at specified stops.

Response fields per departure:
- `trip_id` - GTFS trip identifier
- `route_short_name` - e.g. "T1", "333"
- `route_long_name` - e.g. "North Shore Line"
- `headsign` - destination displayed on vehicle
- `scheduled_departure` - RFC 3339 timestamp
- `realtime_departure` - RFC 3339 timestamp (nullable)
- `delay_seconds` - integer (nullable)
- `arrivals` - array with:
  - `stop_id`, `stop_name`
  - `scheduled_arrival` - RFC 3339 timestamp
  - `realtime_arrival` - RFC 3339 timestamp (nullable)

## Configuration

| Env var | Default | Description |
|---------|---------|-------------|
| `PORT` | `3000` | Port the departure board listens on |
| `GTFS_API_URL` | `http://localhost:8080` | Base URL of the GTFS departure service |

## Build & Run

```sh
go build -o departure-board .
./departure-board
```

## Lint & Test

```sh
go vet ./...
go test ./...
```
