// Package osm parses OpenStreetMap data into a routable pedestrian graph.
// It supports the plain OSM XML format (.osm) only; for PBF you can
// pre-convert with osmconvert.
package osm

import (
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
)

// ---------------------------------------------------------------------------
// Raw OSM XML structures
// ---------------------------------------------------------------------------

type osmXML struct {
	Nodes []rawNode `xml:"node"`
	Ways  []rawWay  `xml:"way"`
}

type rawNode struct {
	ID   int64   `xml:"id,attr"`
	Lat  float64 `xml:"lat,attr"`
	Lon  float64 `xml:"lon,attr"`
	Tags []tag   `xml:"tag"`
}

type rawWay struct {
	ID   int64 `xml:"id,attr"`
	NDs  []nd  `xml:"nd"`
	Tags []tag `xml:"tag"`
}

type tag struct {
	K string `xml:"k,attr"`
	V string `xml:"v,attr"`
}

type nd struct {
	Ref int64 `xml:"ref,attr"`
}

// ---------------------------------------------------------------------------
// Graph types
// ---------------------------------------------------------------------------

// NodeID is an OSM node identifier.
type NodeID = int64

// Node is a point in the pedestrian graph.
type Node struct {
	ID  NodeID
	Lat float64
	Lon float64
}

// Edge is a directed pedestrian connection between two graph nodes.
type Edge struct {
	To       NodeID
	Cost     float64 // travel time in seconds
	IsStairs bool
}

// Graph is the in-memory pedestrian routing graph.
type Graph struct {
	Nodes map[NodeID]*Node
	Edges map[NodeID][]Edge // adjacency list
}

// ---------------------------------------------------------------------------
// Tag helpers
// ---------------------------------------------------------------------------

func tagsMap(tags []tag) map[string]string {
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		m[t.K] = t.V
	}
	return m
}

// isPedestrian returns true when the OSM way is usable by pedestrians.
func isPedestrian(tags map[string]string) bool {
	hw := tags["highway"]
	if hw == "" {
		return false
	}
	// Exclude motor-only roads unless sidewalk is tagged.
	switch hw {
	case "motorway", "motorway_link", "trunk", "trunk_link":
		return false
	case "footway", "pedestrian", "path", "living_street",
		"residential", "service", "unclassified", "tertiary",
		"secondary", "primary", "steps", "track", "corridor":
		return true
	}
	// For everything else, allow if foot≠no.
	if tags["foot"] == "no" {
		return false
	}
	return true
}

func isStairs(tags map[string]string) bool {
	return tags["highway"] == "steps"
}

func isWheelchairFriendly(tags map[string]string) bool {
	switch tags["wheelchair"] {
	case "no", "limited":
		return false
	}
	if isStairs(tags) {
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Geometry helpers
// ---------------------------------------------------------------------------

const earthRadius = 6_371_000.0 // metres

// HaversineMetres computes the great-circle distance between two WGS84 points.
func HaversineMetres(lat1, lon1, lat2, lon2 float64) float64 {
	φ1 := lat1 * math.Pi / 180
	φ2 := lat2 * math.Pi / 180
	Δφ := (lat2 - lat1) * math.Pi / 180
	Δλ := (lon2 - lon1) * math.Pi / 180

	a := math.Sin(Δφ/2)*math.Sin(Δφ/2) +
		math.Cos(φ1)*math.Cos(φ2)*math.Sin(Δλ/2)*math.Sin(Δλ/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadius * c
}

// ---------------------------------------------------------------------------
// Parser
// ---------------------------------------------------------------------------

// Parse reads an OSM XML file and returns a routable pedestrian Graph.
// wheelchairOnly, when true, drops steps and wheelchair=no ways.
func Parse(path string, wheelchairOnly bool) (*Graph, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("osm: open %s: %w", path, err)
	}
	defer f.Close()
	return ParseReader(f, wheelchairOnly)
}

// ParseReader reads OSM XML from an io.Reader.
func ParseReader(r io.Reader, wheelchairOnly bool) (*Graph, error) {
	var raw osmXML
	dec := xml.NewDecoder(r)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("osm: xml decode: %w", err)
	}

	g := &Graph{
		Nodes: make(map[NodeID]*Node, len(raw.Nodes)),
		Edges: make(map[NodeID][]Edge, len(raw.Nodes)),
	}

	// Index all nodes.
	for i := range raw.Nodes {
		n := &raw.Nodes[i]
		g.Nodes[n.ID] = &Node{ID: n.ID, Lat: n.Lat, Lon: n.Lon}
	}

	// Build adjacency list from ways.
	for _, way := range raw.Ways {
		tags := tagsMap(way.Tags)
		if !isPedestrian(tags) {
			continue
		}
		if wheelchairOnly && !isWheelchairFriendly(tags) {
			continue
		}
		stairs := isStairs(tags)
		oneWay := tags["oneway"] == "yes" || tags["oneway"] == "1"

		nds := way.NDs
		for i := 0; i < len(nds)-1; i++ {
			aID := nds[i].Ref
			bID := nds[i+1].Ref
			a, okA := g.Nodes[aID]
			b, okB := g.Nodes[bID]
			if !okA || !okB {
				continue
			}
			dist := HaversineMetres(a.Lat, a.Lon, b.Lat, b.Lon)

			// Cost will be filled in by the caller with the actual
			// walk config speeds; here we store raw distance and
			// let the Dijkstra layer apply the speed model.
			// We encode the edge cost as distance (metres) – the
			// dijkstra package will convert using config.
			g.Edges[aID] = append(g.Edges[aID], Edge{To: bID, Cost: dist, IsStairs: stairs})
			if !oneWay {
				g.Edges[bID] = append(g.Edges[bID], Edge{To: aID, Cost: dist, IsStairs: stairs})
			}
		}
	}

	return g, nil
}

// NearestNode finds the graph node closest to (lat, lon) within maxDistMetres.
// Returns (nodeID, found).
func (g *Graph) NearestNode(lat, lon, maxDistMetres float64) (NodeID, bool) {
	best := maxDistMetres + 1
	var bestID NodeID
	found := false
	for _, n := range g.Nodes {
		d := HaversineMetres(lat, lon, n.Lat, n.Lon)
		if d < best {
			best = d
			bestID = n.ID
			found = true
		}
	}
	return bestID, found
}
