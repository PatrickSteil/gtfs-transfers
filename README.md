# gtfs-transfers

Generates pedestrian **transfer entries** for a GTFS feed by routing through a real OpenStreetMap street graph. Each GTFS stop is snapped to its nearest OSM node; a bounded Dijkstra search is then run from that node and every reachable stop within the walking budget is written back as a `transfers.txt` entry with a realistic travel time.

```
[GTFS feed]  +  [OSM .osm file]
        │               │
        └───────┬───────┘
                ▼
     snap stops → nearest OSM node
                ▼
     Dijkstra (per stop, bounded)
                ▼
   transfer_type=2 entries (min_transfer_time)
                ▼
        [enriched GTFS feed]
```

---

## Features

| Feature | Detail |
|---|---|
| Real street routing | Uses the actual OSM pedestrian graph, not straight-line estimates |
| Speed model | Separate speeds for flat footways, stair ascent, and stair descent |
| Wheelchair mode | Drops `highway=steps` and `wheelchair=no/limited` edges entirely |
| Transfer penalty | Fixed per-transfer offset for station entry/exit overhead |
| Idempotent upsert | Pre-existing shorter transfer times are never overwritten |
| Standard output | Writes `transfer_type=2` (minimum transfer time) per the GTFS spec |

---

## Installation

```bash
git clone https://github.com/user/gtfs-transfers
cd gtfs-transfers
go build -o gtfs-transfers ./cmd/gtfs-transfers
```

Requires **Go 1.22+**.

---

## Usage

```
gtfs-transfers [flags] <gtfs-input> <osm-input.osm> <gtfs-output>
```

### Arguments

| Argument | Description |
|---|---|
| `gtfs-input` | GTFS ZIP file or directory (read-only) |
| `osm-input` | OSM XML file (`.osm`) covering the same geographic area |
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

### Examples

```bash
# Default: 5-minute walking budget
gtfs-transfers feed.zip city.osm output/

# Wheelchair-accessible transfers, 3-minute budget
gtfs-transfers -wheelchair -max-walk 180 feed.zip city.osm output/

# Slower walkers with a 60-second station-entry penalty
gtfs-transfers -flat-speed 0.9 -penalty 60 feed.zip city.osm output/

# Faster walkers, generous 8-minute budget
gtfs-transfers -flat-speed 1.6 -max-walk 480 feed.zip city.osm output/
```

---

## Getting OSM data

The easiest source is [Geofabrik](https://download.geofabrik.de/) – download the region covering your GTFS feed. For large regions you can extract a bounding-box with **osmconvert** (also converts PBF to XML):

```bash
# Install osmconvert (Debian/Ubuntu)
sudo apt-get install osmctools

# Extract a bounding box and convert PBF → OSM XML
osmconvert region.osm.pbf \
  -b=8.6,49.3,8.8,49.5 \
  --complete-ways \
  -o=city.osm
```

The tool only needs pedestrian-relevant data. You can slim the file significantly:

```bash
osmfilter region.osm \
  --keep="highway=footway =path =pedestrian =steps =living_street \
          =residential =service =unclassified =tertiary =secondary \
          =primary =track =corridor" \
  -o=pedestrian.osm
```

---

## How it works

### 1. GTFS parsing
The feed is read with [gtfsparser](https://github.com/patrickbr/gtfsparser). All stops with valid coordinates are extracted.

### 2. OSM graph construction
The OSM XML file is scanned for ways whose `highway` tag indicates pedestrian usability. Motorways, trunks, and `foot=no` ways are excluded. Each way becomes a set of bidirectional directed edges. Edge cost is stored as **metres**; speed conversion happens at Dijkstra time so the graph can be reused across different speed configurations.

Stair edges (`highway=steps`) are tagged separately. In wheelchair mode they are dropped entirely; otherwise they use the averaged stair speed.

### 3. Stop snapping
Each GTFS stop is matched to its nearest OSM node within a 200 m radius using a linear scan. Stops outside this radius are warned about and skipped.

### 4. Dijkstra search
For each snapped stop a standard min-heap Dijkstra is run with early termination once the frontier exceeds `MaxWalkingTime`. Edge cost in seconds is computed as:

```
cost_seconds = distance_metres / speed_m_s
```

where `speed_m_s` is chosen from the config based on whether the edge is stairs (and direction, averaged) or a flat footway.

### 5. Transfer writing
For every (source stop, reached stop) pair the computed time plus the optional penalty is written as:

```
transfer_type  = 2   (minimum transfer time required)
min_transfer_time = ceil(walk_seconds + penalty)
```

If a transfer for the same stop pair already exists with a shorter time, the existing value is kept.

### 6. GTFS output
The enriched feed is written back with [gtfswriter](https://github.com/patrickbr/gtfswriter). All original feed data is preserved; only `transfers.txt` is augmented.

---

## Project structure

```
gtfs-transfers/
├── cmd/
│   └── gtfs-transfers/
│       └── main.go          # CLI entry point and flag parsing
└── internal/
    ├── config/
    │   └── config.go        # WalkConfig – all tunable parameters
    ├── osm/
    │   ├── graph.go         # OSM XML parser → pedestrian Graph
    │   ├── dijkstra.go      # Bounded single-source Dijkstra
    │   └── osm_test.go      # Unit tests (graph parsing + Dijkstra)
    └── transfers/
        ├── generate.go      # Stop snapping + transfer generation
        └── generate_test.go # Integration tests
```

---

## Extending the speed model

All speed parameters live in `internal/config/config.go`. Future additions you might consider:

- **Elevation-aware stair direction**: track whether each step edge goes uphill or downhill using SRTM elevation data, then apply `StairSpeedUp` vs `StairSpeedDown` precisely.
- **Surface penalty**: add a `SurfaceSpeeds map[string]float64` to slow down on cobblestones, gravel, etc. (using OSM `surface=` tags).
- **Age/mobility profiles**: expose named presets (`-profile elderly`, `-profile child`) that set a bundle of speeds and penalties at once.
- **Incline speed reduction**: model walking slowdown on steep slopes using `incline=` or derived DEM gradients.
- **Indoor routing**: OSM `indoor=` and `level=` tags can be used for complex stations once paired with GTFS Pathways data.

---

## License

GPL-2.0 – matching the upstream gtfsparser/gtfswriter libraries.
