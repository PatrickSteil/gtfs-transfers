// Package transfers generates GTFS transfer entries by routing pedestrian
// paths through an OSM graph.
package transfers

import (
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/PatrickSteil/gtfs-transfers/internal/config"
	osmgraph "github.com/PatrickSteil/gtfs-transfers/internal/osm"
	gtfsparser "github.com/patrickbr/gtfsparser"
	gtfs "github.com/patrickbr/gtfsparser/gtfs"
)

// snapRadius is the maximum distance (metres) used when snapping a GTFS
// stop to its nearest OSM node.
const snapRadius = 200.0

// StopNode is the mapping of a GTFS stop to an OSM graph node.
type StopNode struct {
	Stop   *gtfs.Stop
	NodeID osmgraph.NodeID
}

// isChildStop returns true for stops that belong to a station hierarchy as
// a child entry (platform, entrance, generic node). Transfers are only
// meaningful between top-level stops, i.e. those with no Parent_station.
func isChildStop(stop *gtfs.Stop) bool {
	return stop.Parent_station != nil
}

// SnapStops maps every top-level (non-child) stop in the GTFS feed to the
// nearest OSM node. Child stops (those with a Parent_station) and stops
// that cannot be snapped within snapRadius are silently skipped.
func SnapStops(feed *gtfsparser.Feed, g *osmgraph.Graph) []StopNode {
	result := make([]StopNode, 0, len(feed.Stops))
	for _, stop := range feed.Stops {
		// BUG FIX: this condition was inverted (`!isChildStop`), which
		// skipped every top-level stop and generated transfers only
		// between child stops (platforms/entrances) — the opposite of
		// what the doc-comment above promises and of what GTFS consumers
		// expect. We want to skip child stops, not keep them.
		if isChildStop(stop) {
			continue
		}
		if !stop.HasLatLon() {
			continue
		}
		nid, ok := g.NearestNode(float64(stop.Lat), float64(stop.Lon), snapRadius)
		if !ok {
			continue
		}
		result = append(result, StopNode{Stop: stop, NodeID: nid})
	}
	return result
}

// nodeToStops is an inverted index: OSM node → list of snapped GTFS stops.
func nodeToStops(snapped []StopNode) map[osmgraph.NodeID][]*gtfs.Stop {
	m := make(map[osmgraph.NodeID][]*gtfs.Stop, len(snapped))
	for _, sn := range snapped {
		m[sn.NodeID] = append(m[sn.NodeID], sn.Stop)
	}
	return m
}

// transferEntry is a candidate transfer produced by one worker.
type transferEntry struct {
	from    *gtfs.Stop
	to      *gtfs.Stop
	seconds int
}

// GenerateTransfers runs a Dijkstra search from every snapped stop and adds
// a GTFS transfer entry for each reachable stop within the walking budget.
//
// transfer_type=2 ("requires minimum transfer time") is used so that trip
// planners respect the computed pedestrian duration.
//
// snapped is normally the result of a prior call to SnapStops; it is taken
// as a parameter (rather than computed internally) so callers that also
// need the stop→node mapping — e.g. to export it alongside a DIMACS graph —
// can snap once and reuse the result.
//
// Work is parallelised over all source stops using a pool of
// runtime.NumCPU() workers. Each worker owns its own Dijkstra WorkBuf so
// heap allocations are amortised across the full run rather than paid on
// every Dijkstra call.
func GenerateTransfers(
	feed *gtfsparser.Feed,
	g *osmgraph.Graph,
	cfg config.WalkConfig,
	snapped []StopNode,
) {
	n2s := nodeToStops(snapped)

	workers := runtime.NumCPU()
	jobs := make(chan StopNode, workers*2)
	results := make(chan []transferEntry, workers*2)

	var wg sync.WaitGroup

	// -----------------------------------------------------------------------
	// Worker goroutines — each owns one WorkBuf for the lifetime of the pool.
	// -----------------------------------------------------------------------
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// WorkBuf holds the reusable dist map and priority queue that
			// Dijkstra would otherwise allocate on every call.
			//
			// NOTE: adapt this to match your osmgraph.NewWorkBuf() / WorkBuf
			// type. The key contract is that each worker's buf is never shared
			// with another goroutine, so no locking is required inside Dijkstra.
			buf := osmgraph.NewWorkBuf(len(g.Nodes))

			for src := range jobs {
				reached := osmgraph.DijkstraWithBuf(g, src.NodeID, cfg, buf)

				// Collect candidate transfers locally before sending.
				//
				// NOTE: no extra "over budget" filter is needed here.
				// DijkstraWithBuf already only returns nodes whose cost is
				// <= cfg.MaxWalkingTime, so r.Seconds+cfg.TransferPenalty
				// can never exceed cfg.MaxWalkingTime+cfg.TransferPenalty —
				// the previous version of this code had a check for that
				// which could never be true, and a "skipped" counter that
				// was always zero because nothing ever incremented it.
				var entries []transferEntry
				for _, r := range reached {
					dstStops, ok := n2s[r.NodeID]
					if !ok {
						continue
					}
					totalSec := r.Seconds + cfg.TransferPenalty
					for _, dst := range dstStops {
						if dst.Id == src.Stop.Id {
							continue
						}
						entries = append(entries, transferEntry{
							from:    src.Stop,
							to:      dst,
							seconds: int(math.Ceil(totalSec)),
						})
					}
				}
				if len(entries) > 0 {
					results <- entries
				}
			}
		}()
	}

	// -----------------------------------------------------------------------
	// Closer: once all workers are done, close the results channel so the
	// collector below exits cleanly.
	// -----------------------------------------------------------------------
	go func() {
		wg.Wait()
		close(results)
	}()

	// -----------------------------------------------------------------------
	// Producer: fan out source stops to the worker pool.
	// -----------------------------------------------------------------------
	go func() {
		for _, src := range snapped {
			jobs <- src
		}
		close(jobs)
	}()

	// -----------------------------------------------------------------------
	// Collector: merge results into feed.Transfers on the main goroutine.
	// feed.Transfers is not safe for concurrent writes, so we funnel all
	// mutations through here rather than locking.
	// -----------------------------------------------------------------------
	var generated int64
	for entries := range results {
		for _, e := range entries {
			addTransfer(feed, e.from, e.to, e.seconds)
			atomic.AddInt64(&generated, 1)
		}
	}

	fmt.Printf("  Generated %d transfers\n", generated)
}

// addTransfer upserts a transfer into feed.Transfers. If a transfer for the
// same (from, to) stop pair already exists, we keep the shorter time.
// Must only be called from a single goroutine (the collector).
func addTransfer(feed *gtfsparser.Feed, from, to *gtfs.Stop, seconds int) {
	key := gtfs.TransferKey{
		From_stop: from,
		To_stop:   to,
	}
	if existing, ok := feed.Transfers[key]; ok {
		if existing.Min_transfer_time <= seconds {
			return // keep the shorter existing value
		}
	}
	feed.Transfers[key] = gtfs.TransferVal{
		Transfer_type:     2, // requires minimum transfer time
		Min_transfer_time: seconds,
	}
}
