package transfers

import (
	"encoding/csv"
	"io"
	"sort"
	"strconv"

	osmgraph "github.com/PatrickSteil/gtfs-transfers/internal/osm"
	gtfsparser "github.com/patrickbr/gtfsparser"
)

func WriteStationMapping(w io.Writer, feed *gtfsparser.Feed, stopNode map[string]osmgraph.NodeID, index map[osmgraph.NodeID]int) error {
	stopByID := make(map[string]*struct {
		Name     string
		Lat, Lon float32
	}, len(feed.Stops))
	for _, s := range feed.Stops {
		stopByID[s.Id] = &struct {
			Name     string
			Lat, Lon float32
		}{Name: s.Name, Lat: s.Lat, Lon: s.Lon}
	}

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
		s := stopByID[id]
		if s == nil {
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
			strconv.FormatFloat(float64(s.Lat), 'f', 6, 64),
			strconv.FormatFloat(float64(s.Lon), 'f', 6, 64),
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
