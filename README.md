# gtfs-transfers

Generates pedestrian **transfer entries** for a GTFS feed by routing through a real OpenStreetMap street graph. Each GTFS stop is snapped to its nearest OSM node; a bounded Dijkstra search is then run from that node and every reachable stop within the walking budget is written back as a `transfers.txt` entry with a realistic travel time.

---

## Features

| Feature | Detail |
|---|---|
| Real street routing | Uses the actual OSM street/footway graph, not straight-line estimates |
| OSM XML *and* PBF | Reads `.osm`/`.xml` and binary `.osm.pbf` directly — no pre-conversion needed |
| Foot and bike graphs | Independent way-filtering and directionality rules per mode (see below) |
| Speed model | Separate speeds for flat footways, stair ascent, and stair descent |
| Wheelchair mode | Drops `highway=steps` and `wheelchair=no/limited` edges entirely |
| Transfer penalty | Fixed per-transfer offset for station entry/exit overhead |
| Idempotent upsert | Pre-existing shorter transfer times are never overwritten |
| Standard output | Writes `transfer_type=2` (minimum transfer time) per the GTFS spec |
| DIMACS export | Optional `.gr`/`.co` export per mode, plus a combined stop→node mapping |

---

## Installation

```bash
git clone https://github.com/user/gtfs-transfers
cd gtfs-transfers
go mod tidy   # fetches dependencies, including the PBF decoder
go build -o gtfs-transfers ./cmd/gtfs-transfers
```

Requires **Go 1.25+**.

---

## Usage

```
gtfs-transfers [flags] <gtfs-input> <osm-input> <gtfs-output>
```

### Arguments

| Argument | Description |
|---|---|
| `gtfs-input` | GTFS ZIP file or directory (read-only) |
| `osm-input` | OSM data covering the same geographic area — `.osm`/`.xml` (XML) or `.osm.pbf` (binary PBF), auto-detected from the extension |
| `gtfs-output` | Output path – ZIP file or directory |

### Flags

| Flag | Default | Description |
|---|---|---|
| `-max-walk` | `300` | Walking time budget in seconds (5 min) |
| `-flat-speed` | `1.39` | Speed on level footways, m/s (~5 km/h) |
| `-stair-up` | `0.50` | Effective speed ascending stairs, m/s |
| `-stair-down` | `0.70` | Effective speed descending stairs, m/s |
| `-wheelchair` | `false` | Exclude stairs and inaccessible paths |
| `-penalty` | `0` | Fixed seconds added to every transfer |
| `-dimacs-out` | *(off)* | Directory to export DIMACS graph(s) + `stations.csv` into |
| `-dimacs-modes` | `foot,bike` | Which mode graph(s) to export when `-dimacs-out` is set |
| `-dimacs-scale` | `100` | Seconds→integer-weight scale for the DIMACS export (100 = centiseconds) |
| `-bike-speed` | `4.17` | Constant bicycle speed for the bike DIMACS export, m/s (~15 km/h) |

### Examples

```bash
# Default: 5-minute walking budget
gtfs-transfers feed.zip city.osm.pbf output/

# Wheelchair-accessible transfers, 3-minute budget
gtfs-transfers -wheelchair -max-walk 180 feed.zip city.osm.pbf output/

# Slower walkers with a 60-second station-entry penalty
gtfs-transfers -flat-speed 0.9 -penalty 60 feed.zip city.osm.pbf output/

# Also export foot AND bike DIMACS graphs + a combined stations.csv
gtfs-transfers -dimacs-out dimacs/ feed.zip city.osm.pbf output/

# Only the bike graph, with a faster assumed cycling speed
gtfs-transfers -dimacs-out dimacs/ -dimacs-modes bike -bike-speed 5.5 \
  feed.zip city.osm.pbf output/
```

---

## Getting OSM data

The easiest source is [Geofabrik](https://download.geofabrik.de/) — download the `.osm.pbf` region covering your GTFS feed and pass it straight to `gtfs-transfers`, no conversion required.

For very large regions you may still want to cut out a bounding box first, e.g. with **osmium** or **osmconvert**, to keep the file (and memory use) small:

```bash
# osmium (recommended, handles large files well)
osmium extract -b 8.6,49.3,8.8,49.5 region.osm.pbf -o city.osm.pbf

# or osmconvert
osmconvert region.osm.pbf -b=8.6,49.3,8.8,49.5 --complete-ways -o=city.osm.pbf
```

---

## How it works

### 1. GTFS parsing
The feed is read with [gtfsparser](https://github.com/patrickbr/gtfsparser). All stops with valid coordinates are extracted; only top-level stops (no `parent_station`) are used for snapping — child stops (platforms, entrances) are intentionally skipped, since transfers are computed at the station level.

### 2. OSM graph construction
The OSM file (XML or PBF — decoded once regardless of how many mode graphs are requested) is scanned for ways usable by the requested mode(s):

- **Foot**: motorways/trunks excluded, `foot=no` excluded, everything else allowed by default.
- **Bike**: motorways excluded, footway/path/steps only if explicitly `bicycle=yes`/`designated`, `bicycle=no` always excluded. Contraflow cycling on one-way streets is respected via `oneway:bicycle=no` and `cycleway=opposite*`.

Each way becomes a set of directed edges, correctly one-way in either the forward or reverse node order (`oneway=yes/1/-1`). Edge cost is stored as **metres**; speed conversion happens at Dijkstra/export time, so the same graph can be reused across different speed configurations. Stair edges (`highway=steps`) are tagged separately for the foot graph; in wheelchair mode they're dropped entirely, otherwise they use the averaged stair speed. The bike graph never carries stair edges unless a way is explicitly tagged rideable.

Only nodes with at least one incident edge in a given mode's graph are ever returned by nearest-node lookups — a GTFS stop can't accidentally snap to an isolated OSM node (e.g. an unrelated POI, or a footway-only junction in the bike graph) that has no actual route out of it.

### 3. Stop snapping
Each GTFS stop is matched to its nearest routable OSM node within a 200 m radius using a k-d tree built over a local metres-projection of the graph (not raw lat/lon degrees, which would distort distances away from the equator). Stops outside this radius are skipped.

### 4. Dijkstra search
For each snapped stop a standard min-heap Dijkstra is run with early termination once the frontier exceeds `MaxWalkingTime`. Edge cost in seconds is computed as:

```
cost_seconds = distance_metres / speed_m_s
```

where `speed_m_s` is chosen based on whether the edge is stairs (averaged ascent/descent) or a flat footway.

### 5. Transfer writing
For every (source stop, reached stop) pair the computed time plus the optional penalty is written as:

```
transfer_type  = 2   (minimum transfer time required)
min_transfer_time = ceil(walk_seconds + penalty)
```

If a transfer for the same stop pair already exists with a shorter time, the existing value is kept.

### 6. GTFS output
The enriched feed is written back with [gtfswriter](https://github.com/patrickbr/gtfswriter). All original feed data is preserved; only `transfers.txt` is augmented.

### 7. Optional DIMACS export
When `-dimacs-out` is set, each requested mode gets its own dense 1-based node numbering (`Graph.NodeIndex()`) and is written as `graph_<mode>.gr`/`graph_<mode>.co` in the [DIMACS shortest-path challenge format](http://www.diag.uniroma1.it/~challenge9/). Foot and bike graphs have independent node sets and independent numbering — they are not expected to agree, since what's routable differs by mode. A single `stations.csv` maps every GTFS `stop_id` to `osm_node_id_<mode>`/`dimacs_node_id_<mode>` for each exported mode, so a downstream multi-modal router can look up one station once and get the right start node in every mode's graph.

---

## Project structure

```
gtfs-transfers/
├── cmd/
│   └── gtfs-transfers/
│       └── main.go          # CLI entry point, flag parsing, DIMACS export orchestration
└── internal/
    ├── config/
    │   └── config.go        # WalkConfig – all tunable parameters
    ├── osm/
    │   ├── graph.go         # Graph, Mode, k-d tree index, way filtering, oneway handling
    │   ├── pbf.go            # Binary OSM PBF decoding (gosmparse)
    │   ├── dijkstra.go      # Bounded single-source Dijkstra
    │   └── dimacs.go        # DIMACS .gr/.co export
    └── transfers/
        ├── generate.go      # Stop snapping + transfer generation
        └── export.go        # Multi-mode station→node CSV export
```

---

## Extending the speed model

All foot-mode speed parameters live in `internal/config/config.go`. Future additions you might consider:

- **Elevation-aware stair direction**: track whether each step edge goes uphill or downhill using SRTM elevation data, then apply `StairSpeedUp` vs `StairSpeedDown` precisely.
- **Surface penalty**: slow down on cobblestones, gravel, etc. using OSM `surface=` tags — for both foot and bike edges.
- **Age/mobility profiles**: expose named presets (`-profile elderly`, `-profile child`) that set a bundle of speeds and penalties at once.
- **Incline speed reduction**: model walking/cycling slowdown on steep slopes using `incline=` or derived DEM gradients — this matters more for bikes than for the current flat-speed model.
- **Indoor routing**: OSM `indoor=` and `level=` tags can be used for complex stations once paired with GTFS Pathways data.

---

## License

GPL-2.0 – matching the upstream gtfsparser/gtfswriter libraries.
