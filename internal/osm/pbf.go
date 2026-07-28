package osm

import (
	"fmt"
	"io"
	"sync"

	"github.com/thomersch/gosmparse"
)

// decodePBF decodes the binary OSM PBF format using gosmparse (pure Go, no
// cgo, streaming). PBF is the format used by Geofabrik/BBBike extracts and
// planet.osm.org dumps — it's roughly 4-6x smaller than the equivalent OSM
// XML and much faster to decode, which matters for anything larger than a
// small city extract.
//
// Relations are ignored: pedestrian/bicycle routing here only uses nodes
// and ways (see buildGraph), never relation membership.
func decodePBF(r io.Reader) (*parsedData, error) {
	h := &pbfHandler{data: &parsedData{}}
	dec := gosmparse.NewDecoder(r)
	// dec.Workers defaults to runtime.GOMAXPROCS(0) when left at its zero
	// value (see gosmparse's decoder.go) — i.e. ReadNode/ReadWay/
	// ReadRelation are called concurrently from multiple goroutines by
	// default, not just once Workers is set explicitly. pbfHandler's
	// mutex below is required correctness, not an optional speed/safety
	// trade-off.
	if err := dec.Parse(h); err != nil {
		return nil, fmt.Errorf("osm: pbf decode: %w", err)
	}
	return h.data, nil
}

// pbfHandler implements gosmparse.OSMReader. gosmparse calls ReadNode/
// ReadWay/ReadRelation from multiple worker goroutines concurrently, so all
// three must synchronize their writes into the shared parsedData.
type pbfHandler struct {
	mu   sync.Mutex
	data *parsedData
}

func (h *pbfHandler) ReadNode(n gosmparse.Node) {
	h.mu.Lock()
	h.data.Nodes = append(h.data.Nodes, parsedNode{ID: n.ID, Lat: n.Lat, Lon: n.Lon})
	h.mu.Unlock()
}

func (h *pbfHandler) ReadWay(w gosmparse.Way) {
	h.mu.Lock()
	h.data.Ways = append(h.data.Ways, parsedWay{ID: w.ID, NodeIDs: w.NodeIDs, Tags: w.Tags})
	h.mu.Unlock()
}

func (h *pbfHandler) ReadRelation(gosmparse.Relation) {
	// Not needed: routing here only follows way geometry, never relation
	// membership (e.g. we don't assemble turn restrictions or route
	// relations).
}
