# Departure Board

Mobile-optimised departure board with server-side rendering.

## Architecture

- **Language**: Go (stdlib only, no frameworks)
- **Rendering**: Server-side HTML via `html/template`
- **Styling**: Inline CSS optimised for mobile viewports
- **Data source**: Local GTFS Departure Service API (see below)

## How it works

1. User visits `/?stop_id=200060` (optionally `&arrival_stops=200010,200020`)
2. Server fetches departures from the GTFS API (`http://localhost:8080/departures` or `/departures/arrivals`)
3. Server renders an HTML departure board and returns it to the browser
4. Page auto-refreshes every 30 seconds

## GTFS Departure Service API

Base URL: `http://localhost:8080` (configurable via `GTFS_API_URL` env var)

### `GET /departures?stop_id={stop_id}`

Returns departures from a stop within the next 60 minutes.

Response fields per departure:
- `trip_id` - GTFS trip identifier
- `route_short_name` - e.g. "T1", "333"
- `route_long_name` - e.g. "North Shore Line"
- `headsign` - destination displayed on vehicle
- `scheduled_departure` - RFC 3339 timestamp
- `realtime_departure` - RFC 3339 timestamp (nullable)
- `delay_seconds` - integer (nullable)

### `GET /departures/arrivals?stop_id={stop_id}&arrival_stops={stop1,stop2}`

Same as above, plus an `arrivals` array per departure with:
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
