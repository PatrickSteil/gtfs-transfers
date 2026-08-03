package prepare

import (
	"math"

	"github.com/PatrickSteil/gtfs-transfers/internal/config"
	osmgraph "github.com/PatrickSteil/gtfs-transfers/internal/osm"
	"github.com/PatrickSteil/gtfs-transfers/internal/stops"
)

// ConnectStops identifies/connects every stop in src with the OSM graph
// (see the paper-style procedure in the package doc of pipeline.go),
// retaining any existing transfer times supplied by src.Existing.
func ConnectStops(src *stops.Source, g *osmgraph.Graph, cfg config.PrepareConfig) map[string]osmgraph.NodeID {
	stopList := src.Stops

	idToStop := make([]*stops.Stop, len(stopList))
	pts := make([]osmgraph.IndexPoint, len(stopList))
	for i, s := range stopList {
		idToStop[i] = s
		pts[i] = osmgraph.IndexPoint{ID: int64(i), Lat: s.Lat, Lon: s.Lon}
	}
	stopIndex := osmgraph.NewPointIndex(pts)

	result := make(map[string]osmgraph.NodeID, len(stopList))

	for _, v := range stopList {
		vLat, vLon := v.Lat, v.Lon

		w, distVW, ok := g.NearestNode(vLat, vLon, math.Inf(1))
		if !ok {
			result[v.ID] = addStopVertex(g, v)
			continue
		}

		mutual := false
		if wNode := g.Nodes[w]; wNode != nil {
			if nearestStopIdx, _, ok2 := stopIndex.Nearest(wNode.Lat, wNode.Lon, math.Inf(1)); ok2 {
				mutual = idToStop[nearestStopIdx].ID == v.ID
			}
		}

		if distVW < cfg.IdentifyDistM && mutual {
			g.Nodes[w].StopIDs = append(g.Nodes[w].StopIDs, v.ID)
			result[v.ID] = w
			continue
		}

		nid := addStopVertex(g, v)
		result[v.ID] = nid

		if distVW < cfg.ConnectDistM {
			g.AddEdge(nid, osmgraph.Edge{To: w, Kind: osmgraph.EdgeConnector, DistM: distVW})
			g.AddEdge(w, osmgraph.Edge{To: nid, Kind: osmgraph.EdgeConnector, DistM: distVW})
		}
	}

	g.RebuildIndex()

	retainExistingTransfers(src, g, result)
	g.RebuildIndex()

	return result
}

func addStopVertex(g *osmgraph.Graph, v *stops.Stop) osmgraph.NodeID {
	nid := g.NewSyntheticNodeID()
	g.Nodes[nid] = &osmgraph.Node{
		ID:      nid,
		Lat:     v.Lat,
		Lon:     v.Lon,
		StopIDs: []string{v.ID},
	}
	return nid
}

// retainExistingTransfers adds src.Existing as graph edges (e.g. footpaths
// already present in a GTFS feed's transfers.txt) so their original fixed
// time is preserved rather than recomputed from OSM geometry. A CSV stop
// basis has no equivalent source, so src.Existing is simply empty there.
func retainExistingTransfers(src *stops.Source, g *osmgraph.Graph, stopNode map[string]osmgraph.NodeID) {
	for _, t := range src.Existing {
		fromID, ok1 := stopNode[t.FromID]
		toID, ok2 := stopNode[t.ToID]
		if !ok1 || !ok2 || fromID == toID {
			continue
		}
		g.AddEdge(fromID, osmgraph.Edge{
			To:           toID,
			Kind:         osmgraph.EdgeRetained,
			FixedSeconds: float64(t.Seconds),
		})
	}
}
