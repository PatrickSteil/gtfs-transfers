package prepare

import (
	"fmt"

	"github.com/PatrickSteil/gtfs-transfers/internal/config"
	osmgraph "github.com/PatrickSteil/gtfs-transfers/internal/osm"
	"github.com/PatrickSteil/gtfs-transfers/internal/stops"
)

// Result bundles the outputs of Prepare: the fully-prepared transfer graph
// and the stop_id -> NodeID mapping for every stop that survived the whole
// pipeline (a stop can vanish if it — or the graph fragment it connected
// to — fell outside the bounding box, or wasn't part of the largest
// connected component).
type Result struct {
	Graph     *osmgraph.Graph
	StopNode  map[string]osmgraph.NodeID
	TotalTop  int // stops considered
	Connected int // of those, how many survived into StopNode
}

// Prepare runs the paper's full graph-preparation procedure against a
// stop basis (either GTFS- or CSV-derived — see internal/stops):
//
//  1. ConnectStops — identify/connect every stop with the OSM graph,
//     retaining any existing transfer edges the source supplies.
//  2. g.ContractDegreeOneTwo() — remove superfluous degree-1/2 vertices.
//  3. g.ApplyBoundingBox(...) — crop to the study area (derived from the
//     stops' extent, padded by cfg.BBoxPadM, if cfg.BBox is nil).
//  4. g.KeepLargestComponent() — discard every other fragment.
//
// Note the order: bounding-box/largest-component filtering runs *after*
// contraction, exactly as in the paper ("After the two networks have been
// connected, we contract ... Finally, we remove remote and isolated
// parts..."), and edge travel times are never pre-collapsed to a single
// number before contraction — see Edge.Chain in osm/graph.go.
func Prepare(src *stops.Source, g *osmgraph.Graph, cfg config.PrepareConfig) Result {
	bbox := cfg.BBox
	if bbox == nil {
		bbox = boundingBoxOfStops(src.Stops).PadMetres(cfg.BBoxPadM)
	}

	stopNode := ConnectStops(src, g, cfg)

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
		TotalTop:  len(src.Stops),
		Connected: len(survived),
	}
}

func boundingBoxOfStops(stopList []*stops.Stop) *config.BoundingBox {
	if len(stopList) == 0 {
		return nil
	}
	b := &config.BoundingBox{
		MinLat: stopList[0].Lat, MaxLat: stopList[0].Lat,
		MinLon: stopList[0].Lon, MaxLon: stopList[0].Lon,
	}
	for _, s := range stopList[1:] {
		if s.Lat < b.MinLat {
			b.MinLat = s.Lat
		}
		if s.Lat > b.MaxLat {
			b.MaxLat = s.Lat
		}
		if s.Lon < b.MinLon {
			b.MinLon = s.Lon
		}
		if s.Lon > b.MaxLon {
			b.MaxLon = s.Lon
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
