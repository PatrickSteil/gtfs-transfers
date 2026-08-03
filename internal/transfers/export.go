package transfers

import (
	"encoding/csv"
	"io"
	"sort"
	"strconv"

	osmgraph "github.com/PatrickSteil/gtfs-transfers/internal/osm"
	"github.com/PatrickSteil/gtfs-transfers/internal/stops"
)

// WriteStationMapping writes stop_id -> OSM/DIMACS node CSV rows for every
// stop in src that survived into stopNode, sorted by stop_id.
func WriteStationMapping(w io.Writer, src *stops.Source, stopNode map[string]osmgraph.NodeID, index map[osmgraph.NodeID]int) error {
	ids := make([]string, 0, len(stopNode))
	for id := range stopNode {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"stop_id", "stop_name", "lat", "lon", "node_id", "dimacs_node_id"}); err != nil {
		return err
	}

	for _, id := range ids {
		nid := stopNode[id]
		s, ok := src.Get(id)
		if !ok {
			continue
		}
		dimacsID := ""
		if index != nil {
			if idx, ok := index[nid]; ok {
				dimacsID = strconv.Itoa(idx)
			}
		}
		row := []string{
			id,
			s.Name,
			strconv.FormatFloat(s.Lat, 'f', 6, 64),
			strconv.FormatFloat(s.Lon, 'f', 6, 64),
			strconv.FormatInt(nid, 10),
			dimacsID,
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}

	cw.Flush()
	return cw.Error()
}
