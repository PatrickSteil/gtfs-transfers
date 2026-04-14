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
	"sort"
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
	kd    *kdNode           // spatial index, built lazily via BuildIndex
}

// ---------------------------------------------------------------------------
// K-D tree
// ---------------------------------------------------------------------------

// kdNode is one node in a 2-D tree that indexes graph Nodes by (Lat, Lon).
// axis 0 = split on Lat, axis 1 = split on Lon.
type kdNode struct {
	node        *Node
	left, right *kdNode
	axis        int
}

// BuildIndex constructs the k-d tree over all nodes currently in the graph.
// Call this once after Parse/ParseReader returns, before any NearestNode
// queries. It is safe to call multiple times (rebuilds the index).
func (g *Graph) BuildIndex() {
	pts := make([]*Node, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		pts = append(pts, n)
	}
	g.kd = buildKDTree(pts, 0)
}

// buildKDTree recursively partitions pts into a balanced k-d tree.
func buildKDTree(pts []*Node, depth int) *kdNode {
	if len(pts) == 0 {
		return nil
	}
	axis := depth % 2
	// Sort along the current axis so we can pick the median.
	sort.Slice(pts, func(i, j int) bool {
		if axis == 0 {
			return pts[i].Lat < pts[j].Lat
		}
		return pts[i].Lon < pts[j].Lon
	})
	mid := len(pts) / 2
	return &kdNode{
		node:  pts[mid],
		axis:  axis,
		left:  buildKDTree(pts[:mid], depth+1),
		right: buildKDTree(pts[mid+1:], depth+1),
	}
}

// nearestSearch performs branch-and-bound nearest-neighbour search.
// bestDist is the squared-degree distance to the current best candidate
// (we compare in degree-space for speed; the final winner is verified with
// HaversineMetres).
func nearestSearch(kd *kdNode, lat, lon float64, best **Node, bestDist *float64) {
	if kd == nil {
		return
	}
	// Degree-space distance to this node (cheap proxy, no trig).
	dLat := kd.node.Lat - lat
	dLon := kd.node.Lon - lon
	d2 := dLat*dLat + dLon*dLon
	if d2 < *bestDist {
		*bestDist = d2
		*best = kd.node
	}

	// Decide which child to descend first (the side containing the query).
	var first, second *kdNode
	var diff float64
	if kd.axis == 0 {
		diff = lat - kd.node.Lat
	} else {
		diff = lon - kd.node.Lon
	}
	if diff <= 0 {
		first, second = kd.left, kd.right
	} else {
		first, second = kd.right, kd.left
	}

	nearestSearch(first, lat, lon, best, bestDist)

	// Only descend the far side if the splitting hyperplane is closer than
	// our current best — this is the key pruning step.
	if diff*diff < *bestDist {
		nearestSearch(second, lat, lon, best, bestDist)
	}
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
// The spatial index is built automatically before returning.
func Parse(path string, wheelchairOnly bool) (*Graph, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("osm: open %s: %w", path, err)
	}
	defer f.Close()
	return ParseReader(f, wheelchairOnly)
}

// ParseReader reads OSM XML from an io.Reader.
// The spatial index is built automatically before returning.
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

	// Build the spatial index so NearestNode is O(log n) from the start.
	g.BuildIndex()

	return g, nil
}

// NearestNode finds the graph node closest to (lat, lon) within maxDistMetres.
// Returns (nodeID, found).
//
// If the k-d tree index has been built (it is built automatically by Parse and
// ParseReader), the search runs in O(log n) average time. If the index is
// absent for any reason it falls back to the O(n) linear scan.
func (g *Graph) NearestNode(lat, lon, maxDistMetres float64) (NodeID, bool) {
	if g.kd != nil {
		return g.nearestKD(lat, lon, maxDistMetres)
	}
	return g.nearestLinear(lat, lon, maxDistMetres)
}

// nearestKD uses the k-d tree for O(log n) average-case lookup.
func (g *Graph) nearestKD(lat, lon, maxDistMetres float64) (NodeID, bool) {
	var best *Node
	// Seed bestDist with a value corresponding to maxDistMetres in
	// degree-space so the search prunes nodes that are already out of range.
	// 1 degree ≈ 111 km; we use a conservative conversion to avoid false
	// pruning due to the Lat/Lon asymmetry near the equator.
	maxDeg := maxDistMetres / 111_000.0
	bestDist := maxDeg * maxDeg

	nearestSearch(g.kd, lat, lon, &best, &bestDist)
	if best == nil {
		return 0, false
	}
	// Verify the winner with the accurate haversine distance.
	if HaversineMetres(lat, lon, best.Lat, best.Lon) > maxDistMetres {
		return 0, false
	}
	return best.ID, true
}

// nearestLinear is the original O(n) fallback.
func (g *Graph) nearestLinear(lat, lon, maxDistMetres float64) (NodeID, bool) {
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
