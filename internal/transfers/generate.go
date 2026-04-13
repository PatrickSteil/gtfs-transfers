// Package transfers generates GTFS transfer entries by routing pedestrian
// paths through an OSM graph.
package transfers

import (
	"fmt"
	"math"

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

// SnapStops maps every stop in the GTFS feed to the nearest OSM node.
// Stops that cannot be snapped within snapRadius are silently skipped and
// logged to stderr.
func SnapStops(feed *gtfsparser.Feed, g *osmgraph.Graph) []StopNode {
	result := make([]StopNode, 0, len(feed.Stops))
	for _, stop := range feed.Stops {
		if !stop.HasLatLon() {
			continue
		}
		nid, ok := g.NearestNode(float64(stop.Lat), float64(stop.Lon), snapRadius)
		if !ok {
			fmt.Printf("  [warn] stop %s (%s) could not be snapped to OSM within %.0fm\n",
				stop.Id, stop.Name, snapRadius)
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

// GenerateTransfers runs a Dijkstra search from every snapped stop and adds
// a GTFS transfer entry for each reachable stop within the walking budget.
//
// transfer_type=2 ("requires minimum transfer time") is used so that trip
// planners respect the computed pedestrian duration.
func GenerateTransfers(
	feed *gtfsparser.Feed,
	g *osmgraph.Graph,
	cfg config.WalkConfig,
) {
	snapped := SnapStops(feed, g)
	n2s := nodeToStops(snapped)

	fmt.Printf("  Snapped %d of %d stops to OSM nodes\n", len(snapped), len(feed.Stops))

	generated := 0
	skipped := 0

	for _, src := range snapped {
		reached := osmgraph.Dijkstra(g, src.NodeID, cfg)

		for _, r := range reached {
			dstStops, ok := n2s[r.NodeID]
			if !ok {
				continue
			}
			for _, dst := range dstStops {
				if dst.Id == src.Stop.Id {
					continue
				}
				totalSec := r.Seconds + cfg.TransferPenalty
				if totalSec > cfg.MaxWalkingTime+cfg.TransferPenalty {
					skipped++
					continue
				}
				addTransfer(feed, src.Stop, dst, int(math.Ceil(totalSec)))
				generated++
			}
		}
	}

	fmt.Printf("  Generated %d transfers (%d skipped over budget)\n", generated, skipped)
}

// addTransfer upserts a transfer into feed.Transfers. If a transfer for the
// same (from, to) stop pair already exists, we keep the shorter time.
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
