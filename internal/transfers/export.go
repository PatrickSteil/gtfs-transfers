package transfers

import (
	"encoding/csv"
	"io"
	"sort"
	"strconv"

	osmgraph "github.com/PatrickSteil/gtfs-transfers/internal/osm"
	gtfs "github.com/patrickbr/gtfsparser/gtfs"
)

// ModeSnap bundles one mode's snapped-stop results for WriteStationMapping.
// Mode is used as the column-name suffix (osm_node_id_<Mode>,
// dimacs_node_id_<Mode>) — typically osmgraph.ModeFoot.String() or
// osmgraph.ModeBike.String().
type ModeSnap struct {
	Mode    string
	Snapped []StopNode

	// Index should be the result of Graph.NodeIndex() for the same graph
	// Snapped was produced from. May be nil to omit dimacs_node_id_<Mode>
	// values (the column is still written, left blank).
	Index map[osmgraph.NodeID]int
}

// WriteStationMapping writes one CSV row per GTFS stop that was snapped in
// at least one mode, with per-mode columns:
//
//	stop_id, stop_name, lat, lon,
//	osm_node_id_<mode1>, dimacs_node_id_<mode1>,
//	osm_node_id_<mode2>, dimacs_node_id_<mode2>, ...
//
// This wide, one-row-per-station format (rather than one file per mode) is
// the point: a downstream multi-modal router can look up a single stop_id
// once and get the right start node for every mode's DIMACS graph in the
// same row, instead of joining several files on stop_id itself. A cell is
// left blank if that stop didn't snap in that particular mode (e.g. no
// bike-legal way within the snap radius).
//
// modes determines both the column order and which modes are included;
// pass them in the order you want columns to appear (foot before bike is
// the conventional order used elsewhere in this codebase, but any order is
// fine). Rows are sorted by stop_id for deterministic output.
func WriteStationMapping(w io.Writer, modes []ModeSnap) error {
	type row struct {
		stop *gtfs.Stop
		// cols[modeName] = [osm_node_id, dimacs_node_id], both possibly ""
		cols map[string][2]string
	}

	byID := make(map[string]*row)
	for _, ms := range modes {
		for _, sn := range ms.Snapped {
			r, ok := byID[sn.Stop.Id]
			if !ok {
				r = &row{stop: sn.Stop, cols: make(map[string][2]string, len(modes))}
				byID[sn.Stop.Id] = r
			}
			dimacsID := ""
			if ms.Index != nil {
				if idx, ok := ms.Index[sn.NodeID]; ok {
					dimacsID = strconv.Itoa(idx)
				}
			}
			r.cols[ms.Mode] = [2]string{strconv.FormatInt(sn.NodeID, 10), dimacsID}
		}
	}

	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	cw := csv.NewWriter(w)

	header := []string{"stop_id", "stop_name", "lat", "lon"}
	for _, ms := range modes {
		header = append(header, "osm_node_id_"+ms.Mode, "dimacs_node_id_"+ms.Mode)
	}
	if err := cw.Write(header); err != nil {
		return err
	}

	for _, id := range ids {
		r := byID[id]
		row := []string{
			r.stop.Id,
			r.stop.Name,
			strconv.FormatFloat(float64(r.stop.Lat), 'f', 6, 64),
			strconv.FormatFloat(float64(r.stop.Lon), 'f', 6, 64),
		}
		for _, ms := range modes {
			c := r.cols[ms.Mode] // zero value ["", ""] if this mode never snapped this stop
			row = append(row, c[0], c[1])
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}

	cw.Flush()
	return cw.Error()
}
