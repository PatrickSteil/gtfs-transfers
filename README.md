# gtfs-transfers

Builds a combined GTFS + OSM transfer graph and writes realistic multi-modal
transfer times back into a GTFS feed's `transfers.txt`.

Rather than modeling each transfer mode (walking, bike, e-scooter, ...) as
its own routing graph, this tool follows the approach used in the paper it's
based on: build **one** transfer graph from OSM, connect it to the GTFS
stops, clean it up, and then reuse that single graph for every mode —
letting only the assumed travel **speed** differ between modes. Every mode
is assumed able to traverse every edge; the accuracy loss from that
simplification is the tool's explicit trade-off, not an oversight (see
"Design notes" below).

---

## Features

| Feature | Detail |
|---|---|
| Real street routing | Uses the actual OSM street/footway graph, not straight-line estimates |
| OSM XML *and* PBF | Reads `.osm`/`.xml` and binary `.osm.pbf` directly — no pre-conversion needed |
| Single shared graph, multiple modes | One graph is built and cleaned up once; each mode (`-modes`) only changes the assumed speed used to convert distance into time |
| Speed-limit-aware travel time | Travel time is distance ÷ min(mode speed, OSM `maxspeed`); roads with no `maxspeed` tag default to 140 km/h |
| Paper-style stop connection | Stops are identified with an existing vertex (≤5 m, mutual nearest neighbor) or added as a new vertex with connector edges (≤100 m) |
| Existing transfer retention | Footpaths already present in the feed's `transfers.txt` are kept as graph edges, not discarded |
| Degree-1/2 contraction | Superfluous non-stop vertices are contracted into single edges after stops are connected, before cropping |
| Bounding box + largest component | The graph is cropped to the GTFS stops' extent (padded) and reduced to its largest connected component, run *after* contraction |
| Idempotent upsert | Pre-existing shorter transfer times in the feed are never overwritten |
| Standard output | Writes `transfer_type=2` (minimum transfer time) per the GTFS spec |
| DIMACS export | Optional `.gr`/`.co` export — one graph per mode, sharing one node numbering and one `stations.csv` mapping |

---

## Installation

```bash
git clone https://github.com/PatrickSteil/gtfs-transfers
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
| `gtfs-output` | Output path – ZIP file or directory (must already exist if it's a directory) |

### Flags

| Flag | Default | Description |
|---|---|---|
| `-modes` | `walking:4.5,bike:15` | Comma-separated `name:speed_kmh` list. The first mode is the one used to generate `transfers.txt`; all listed modes are exported if `-dimacs-out` is set. Paper defaults: walking = 4.5 km/h, bike/e-scooter = 15 km/h |
| `-max-walk` | `300` | Time budget in seconds for `transfers.txt` generation (first listed mode only) |
| `-identify-dist` | `5` | Max distance (m) at which a GTFS stop is identified with (merged into) an existing transfer-graph vertex, provided it's also that vertex's nearest stop |
| `-connect-dist` | `100` | Max distance (m) at which a non-identified stop is linked to the transfer graph with new connector edges |
| `-bbox` | *(auto)* | Explicit `"minLat,minLon,maxLat,maxLon"` bounding box; if unset, derived automatically from the GTFS stops' extent plus `-bbox-pad` |
| `-bbox-pad` | `2000` | Padding (m) applied around the GTFS stops' extent for the automatic bounding box |
| `-no-transfers` | `false` | Skip generating `transfers.txt` entries (useful with `-dimacs-out` alone) |
| `-dimacs-out` | *(off)* | Directory to export the prepared graph into: one shared `graph_coords.co`, one `graph_<mode>.gr` per mode in `-modes`, and a `stations.csv` |
| `-dimacs-scale` | `100` | Seconds → integer-weight scale for the DIMACS export (100 = centiseconds) |

### Examples

```bash
# Default: walking transfers.txt, 5-minute budget
gtfs-transfers feed.zip city.osm.pbf output/

# Slower walkers, 3-minute budget
gtfs-transfers -modes "walking:3.5" -max-walk 180 feed.zip city.osm.pbf output/

# Also export the prepared graph (walking + bike weights) in DIMACS format
gtfs-transfers -dimacs-out dimacs/ feed.zip city.osm.pbf output/

# Only export DIMACS graphs for three modes, skip transfers.txt generation
gtfs-transfers -no-transfers -dimacs-out dimacs/ \
  -modes "walking:4.5,bike:15,car:140" feed.zip city.osm.pbf output/

# Explicit bounding box instead of the automatic stop-extent + padding one
gtfs-transfers -bbox "49.3,8.6,49.5,8.8" feed.zip city.osm.pbf output/
```

---

## Getting OSM data

The easiest source is [Geofabrik](https://download.geofabrik.de/) — download the `.osm.pbf` region covering your GTFS feed and pass it straight to `gtfs-transfers`, no conversion required.

For very large regions you may still want to cut out a bounding box first, e.g. with **osmium** or **osmconvert**, to keep the file (and memory use) small — the tool applies its own bounding box afterward regardless, but starting from a smaller file speeds up parsing:

```bash
# osmium (recommended, handles large files well)
osmium extract -b 8.6,49.3,8.8,49.5 region.osm.pbf -o city.osm.pbf

# or osmconvert
osmconvert region.osm.pbf -b=8.6,49.3,8.8,49.5 --complete-ways -o=city.osm.pbf
```

---

## How it works

### 1. GTFS parsing
The feed is read with [gtfsparser](https://github.com/patrickbr/gtfsparser). Only top-level stops (no `parent_station`) with valid coordinates are used for connecting to the transfer graph — child stops (platforms, entrances) are skipped, since transfers are computed at the station level. Any pre-existing `transfer_type=2` entries between two top-level stops are read and retained as graph edges (step 3).

### 2. OSM graph construction
The OSM file (XML or PBF) is parsed once into a single directed graph. A way is included if it's routable at all (`isRoutable` — excludes proposed/construction/abandoned ways and `access=no|private`); there is **no per-mode filtering** — every mode is assumed able to traverse every edge, per the paper's simplifying assumption. Each way becomes directed edges (respecting `oneway=yes/-1`), each carrying its geographic length in metres and its OSM speed limit:

- `maxspeed` is parsed from km/h, mph, or `walk` (6 km/h); an unset, `none`, `unlimited`, `signals`, or `variable` value defaults to **140 km/h**.

Edge cost is stored purely as distance + speed limit — no mode-specific time is baked in at parse time, so the same graph is reused for every mode's Dijkstra run and DIMACS export.

### 3. Connecting GTFS stops to the OSM graph
For each top-level stop *v*, its nearest OSM vertex *w* is found. If *v* and *w* are less than `-identify-dist` (default 5 m) apart, **and** *v* is also *w*'s nearest stop (mutual nearest neighbor), *v* is identified with *w* — no new vertex is added. Otherwise a new vertex is added for *v*; if it's still within `-connect-dist` (default 100 m) of *w*, a pair of connector edges is inserted between them. Existing `transfers.txt` footpath entries between two stops (read in step 1) are added as additional graph edges at this point, preserving their original fixed time rather than recomputing it from OSM geometry.

### 4. Contraction
After the two networks are connected, every vertex of degree ≤ 2 that is *not* a GTFS stop is contracted away — chains of such vertices are collapsed into a single edge. Each contracted edge keeps its original per-segment distance and speed-limit data internally, so travel time is still computed correctly (segment by segment) after contraction, not from a single pre-collapsed number. These low-degree vertices exist mainly for OSM's own visualization/geometry purposes and are otherwise superfluous for routing.

### 5. Bounding box + largest component
Once contracted, the graph is cropped to a bounding box (explicit via `-bbox`, or automatically derived from the GTFS stops' extent plus `-bbox-pad` metres of padding), then reduced to its largest connected component, discarding remote or isolated fragments. This runs *after* contraction, matching the paper's ordering.

### 6. Transfer-time computation (per mode)
For each surviving stop, a bounded Dijkstra search runs outward with an early cutoff once the frontier exceeds `-max-walk` seconds. Each edge's travel time is:

```
cost_seconds = distance_metres / (min(mode_speed_kmh, edge_speed_limit_kmh) / 3.6)
```

Connector and retained-transfer edges use the mode's speed and their fixed time respectively, rather than a speed limit (there isn't one to apply).

### 7. Transfer writing
For every (source stop, reached stop) pair within budget, using the **first** mode listed in `-modes`, the computed time is written as:

```
transfer_type      = 2   (minimum transfer time required)
min_transfer_time  = ceil(walk_seconds)
```

If a transfer for the same stop pair already exists in the feed with a shorter time, the existing value is kept (idempotent upsert).

### 8. GTFS output
The enriched feed is written back with [gtfswriter](https://github.com/patrickbr/gtfswriter). All original feed data is preserved; only `transfers.txt` is augmented.

### 9. Optional DIMACS export
When `-dimacs-out` is set, the prepared graph gets one dense 1-based node numbering, shared across every mode (`graph_coords.co`), and one `graph_<mode>.gr` per mode listed in `-modes`, each in the [DIMACS shortest-path challenge format](http://www.diag.uniroma1.it/~challenge9/) with edge weights = travel time × `-dimacs-scale`. Because all modes share the same graph and node numbering, a downstream multi-modal router can look a station up once (via `stations.csv`) and get a valid start node in every mode's `.gr` file.

---

## Design notes

- **Why one graph for every mode instead of per-mode filtering.** Restricting a mode's usable edges (e.g. cars can't enter pedestrian zones) tends to *reduce* the number of intermediate transfers that mode can use, which in turn reduces the number of shortcuts a routing algorithm needs. Since the goal here is to stress-test that shortcut behavior, the tool deliberately keeps every edge traversable by every mode rather than filtering, even though that's not perfectly realistic.
- **Why travel time isn't finalized until Dijkstra/export time.** Storing raw distance + speed limit per edge (rather than a precomputed per-mode time) lets the exact same graph — including the exact same contraction and bounding-box/component result — be reused unmodified for every mode, instead of rebuilding or re-cropping the graph per mode.

---

## Project structure

```
gtfs-transfers/
├── cmd/
│   └── gtfs-transfers/
│       └── main.go             # CLI entry point, flag parsing, DIMACS export orchestration
└── internal/
    ├── config/
    │   └── config.go           # Mode, PrepareConfig, BoundingBox — all tunable parameters
    ├── osm/
    │   ├── graph.go            # Graph, Node, Edge/segment, XML/PBF parsing entry point, travel-time math
    │   ├── speed.go            # Way routability + maxspeed parsing
    │   ├── pbf.go               # Binary OSM PBF decoding (gosmparse)
    │   ├── kdtree.go           # k-d tree nearest-neighbor index (local metres projection)
    │   ├── contract.go         # Degree-1/2 vertex contraction, skipping GTFS stops
    │   ├── filter.go           # Bounding-box crop + largest-connected-component filter
    │   ├── dijkstra.go         # Bounded single-source Dijkstra, reusable per-worker buffer
    │   └── dimacs.go           # DIMACS .gr/.co export
    ├── prepare/
    │   ├── connect.go          # Stop identification/connection, existing-transfer retention
    │   └── pipeline.go         # Orchestrates connect → contract → bbox → largest component
    └── transfers/
        ├── generate.go         # Per-mode Dijkstra fan-out + transfers.txt writing
        └── export.go           # stop_id -> OSM/DIMACS node CSV export
```

---

## Extending the speed model

- **Elevation-aware speed**: adjust travel speed using SRTM elevation data for slopes/stairs.
- **Surface penalty**: slow down on cobblestones, gravel, etc. using OSM `surface=` tags.
- **Age/mobility profiles**: expose named presets (`-profile elderly`, `-profile child`) that set a bundle of speeds at once.
- **Per-mode routability**: an optional, non-default flag to restrict specific modes to specific way types, for comparison against the paper's all-edges-traversable assumption.
- **Indoor routing**: OSM `indoor=` and `level=` tags could be used for complex stations once paired with GTFS Pathways data.

---

## License

GPL-2.0 – matching the upstream gtfsparser/gtfswriter libraries.
