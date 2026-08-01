package prepare

import (
	"fmt"

	"github.com/PatrickSteil/gtfs-transfers/internal/config"
	osmgraph "github.com/PatrickSteil/gtfs-transfers/internal/osm"
	gtfsparser "github.com/patrickbr/gtfsparser"
	gtfs "github.com/patrickbr/gtfsparser/gtfs"
)

// Result bundles the outputs of Prepare: the fully-prepared transfer graph
// and the stop_id -> NodeID mapping for every top-level stop that survived
// the whole pipeline (a stop can vanish if it — or the graph fragment it
// connected to — fell outside the bounding box, or wasn't part of the
// largest connected component).
type Result struct {
	Graph     *osmgraph.Graph
	StopNode  map[string]osmgraph.NodeID
	TotalTop  int // top-level stops considered
	Connected int // of those, how many survived into StopNode
}

// Prepare runs the paper's full graph-preparation procedure:
//
//  1. ConnectStops — identify/connect every GTFS stop with the OSM graph,
//     retaining the feed's existing transfer edges.
//  2. g.ContractDegreeOneTwo() — remove superfluous degree-1/2 vertices.
//  3. g.ApplyBoundingBox(...) — crop to the study area (derived from the
//     feed's stop extent, padded by cfg.BBoxPadM, if cfg.BBox is nil).
//  4. g.KeepLargestComponent() — discard every other fragment.
//
// Note the order: bounding-box/largest-component filtering runs *after*
// contraction, exactly as in the paper ("After the two networks have been
// connected, we contract ... Finally, we remove remote and isolated
// parts..."), and edge travel times are never pre-collapsed to a single
// number before contraction — see Edge.Chain in osm/graph.go.
func Prepare(feed *gtfsparser.Feed, g *osmgraph.Graph, cfg config.PrepareConfig) Result {
	stops := topLevelStops(feed)

	bbox := cfg.BBox
	if bbox == nil {
		bbox = boundingBoxOfStops(stops).PadMetres(cfg.BBoxPadM)
	}

	stopNode := ConnectStops(feed, g, cfg)

	g.ContractDegreeOneTwo()
	g.ApplyBoundingBox(bbox)
	g.KeepLargestComponent()

	// Some stops may have been dropped by the bounding-box/largest
	// -component filters; report only those that survived.
	survived := make(map[string]osmgraph.NodeID, len(stopNode))
	for id, nid := range stopNode {
		if _, ok := g.Nodes[nid]; ok {
			survived[id] = nid
		}
	}

	return Result{
		Graph:     g,
		StopNode:  survived,
		TotalTop:  len(stops),
		Connected: len(survived),
	}
}

func boundingBoxOfStops(stops []*gtfs.Stop) *config.BoundingBox {
	if len(stops) == 0 {
		return nil
	}
	b := &config.BoundingBox{
		MinLat: float64(stops[0].Lat), MaxLat: float64(stops[0].Lat),
		MinLon: float64(stops[0].Lon), MaxLon: float64(stops[0].Lon),
	}
	for _, s := range stops[1:] {
		lat, lon := float64(s.Lat), float64(s.Lon)
		if lat < b.MinLat {
			b.MinLat = lat
		}
		if lat > b.MaxLat {
			b.MaxLat = lat
		}
		if lon < b.MinLon {
			b.MinLon = lon
		}
		if lon > b.MaxLon {
			b.MaxLon = lon
		}
	}
	return b
}

// Summary returns a short human-readable report, useful for CLI logging.
func (r Result) Summary() string {
	return fmt.Sprintf(
		"stops connected: %d/%d  |  graph: %d vertices, %d edge-lists",
		r.Connected, r.TotalTop, len(r.Graph.Nodes), len(r.Graph.Edges),
	)
}
