package prepare

import (
	"math"

	"github.com/PatrickSteil/gtfs-transfers/internal/config"
	osmgraph "github.com/PatrickSteil/gtfs-transfers/internal/osm"
	gtfsparser "github.com/patrickbr/gtfsparser"
	gtfs "github.com/patrickbr/gtfsparser/gtfs"
)

func isChildStop(stop *gtfs.Stop) bool {
	return stop.Parent_station != nil
}

func topLevelStops(feed *gtfsparser.Feed) []*gtfs.Stop {
	out := make([]*gtfs.Stop, 0, len(feed.Stops))
	for _, s := range feed.Stops {
		if isChildStop(s) || !s.HasLatLon() {
			continue
		}
		out = append(out, s)
	}
	return out
}

func ConnectStops(feed *gtfsparser.Feed, g *osmgraph.Graph, cfg config.PrepareConfig) map[string]osmgraph.NodeID {
	stops := topLevelStops(feed)

	idToStop := make([]*gtfs.Stop, len(stops))
	pts := make([]osmgraph.IndexPoint, len(stops))
	for i, s := range stops {
		idToStop[i] = s
		pts[i] = osmgraph.IndexPoint{ID: int64(i), Lat: float64(s.Lat), Lon: float64(s.Lon)}
	}
	stopIndex := osmgraph.NewPointIndex(pts)

	result := make(map[string]osmgraph.NodeID, len(stops))

	for _, v := range stops {
		vLat, vLon := float64(v.Lat), float64(v.Lon)

		w, distVW, ok := g.NearestNode(vLat, vLon, math.Inf(1))
		if !ok {
			result[v.Id] = addStopVertex(g, v)
			continue
		}

		mutual := false
		if wNode := g.Nodes[w]; wNode != nil {
			if nearestStopIdx, _, ok2 := stopIndex.Nearest(wNode.Lat, wNode.Lon, math.Inf(1)); ok2 {
				mutual = idToStop[nearestStopIdx].Id == v.Id
			}
		}

		if distVW < cfg.IdentifyDistM && mutual {
			g.Nodes[w].StopIDs = append(g.Nodes[w].StopIDs, v.Id)
			result[v.Id] = w
			continue
		}

		nid := addStopVertex(g, v)
		result[v.Id] = nid

		if distVW < cfg.ConnectDistM {
			g.AddEdge(nid, osmgraph.Edge{To: w, Kind: osmgraph.EdgeConnector, DistM: distVW})
			g.AddEdge(w, osmgraph.Edge{To: nid, Kind: osmgraph.EdgeConnector, DistM: distVW})
		}
	}

	g.RebuildIndex()

	retainExistingTransfers(feed, g, result)
	g.RebuildIndex()

	return result
}

func addStopVertex(g *osmgraph.Graph, v *gtfs.Stop) osmgraph.NodeID {
	nid := g.NewSyntheticNodeID()
	g.Nodes[nid] = &osmgraph.Node{
		ID:      nid,
		Lat:     float64(v.Lat),
		Lon:     float64(v.Lon),
		StopIDs: []string{v.Id},
	}
	return nid
}

func retainExistingTransfers(feed *gtfsparser.Feed, g *osmgraph.Graph, stopNode map[string]osmgraph.NodeID) {
	for key, val := range feed.Transfers {
		if val.Transfer_type != 2 {
			continue
		}
		if key.From_stop == nil || key.To_stop == nil {
			continue
		}
		fromID, ok1 := stopNode[key.From_stop.Id]
		toID, ok2 := stopNode[key.To_stop.Id]
		if !ok1 || !ok2 || fromID == toID {
			continue
		}
		g.AddEdge(fromID, osmgraph.Edge{
			To:           toID,
			Kind:         osmgraph.EdgeRetained,
			FixedSeconds: float64(val.Min_transfer_time),
		})
	}
}
