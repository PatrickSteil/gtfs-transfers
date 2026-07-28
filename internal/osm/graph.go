package osm

import (
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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

type parsedData struct {
	Nodes []parsedNode
	Ways  []parsedWay
}

type parsedNode struct {
	ID       NodeID
	Lat, Lon float64
}

type parsedWay struct {
	ID      int64
	NodeIDs []int64
	Tags    map[string]string
}

func decodeXML(r io.Reader) (*parsedData, error) {
	var raw osmXML
	dec := xml.NewDecoder(r)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("osm: xml decode: %w", err)
	}

	data := &parsedData{
		Nodes: make([]parsedNode, len(raw.Nodes)),
		Ways:  make([]parsedWay, len(raw.Ways)),
	}
	for i, n := range raw.Nodes {
		data.Nodes[i] = parsedNode{ID: n.ID, Lat: n.Lat, Lon: n.Lon}
	}
	for i, w := range raw.Ways {
		nodeIDs := make([]int64, len(w.NDs))
		for j, ref := range w.NDs {
			nodeIDs[j] = ref.Ref
		}
		data.Ways[i] = parsedWay{ID: w.ID, NodeIDs: nodeIDs, Tags: tagsMap(w.Tags)}
	}
	return data, nil
}

type NodeID = int64

type Node struct {
	ID  NodeID
	Lat float64
	Lon float64
}

type Edge struct {
	To       NodeID
	Cost     float64 // travel time in seconds
	IsStairs bool
}

type Mode int

const (
	ModeFoot Mode = iota
	ModeBike
)

func (m Mode) String() string {
	switch m {
	case ModeFoot:
		return "foot"
	case ModeBike:
		return "bike"
	default:
		return "unknown"
	}
}

type Graph struct {
	Mode  Mode
	Nodes map[NodeID]*Node
	Edges map[NodeID][]Edge
	kd    *kdNode

	connected map[NodeID]bool

	refCosLat float64
}
type kdPoint struct {
	node *Node
	x, y float64 // local equirectangular projection, metres
}

type kdNode struct {
	pt          kdPoint
	left, right *kdNode
	axis        int
}

func degToRad(d float64) float64 { return d * math.Pi / 180 }

func (g *Graph) project(lat, lon float64) (x, y float64) {
	x = degToRad(lon) * earthRadius * g.refCosLat
	y = degToRad(lat) * earthRadius
	return x, y
}

func (g *Graph) BuildIndex() {
	connected := make(map[NodeID]bool, len(g.Edges)*2)
	for from, edges := range g.Edges {
		if len(edges) == 0 {
			continue
		}
		connected[from] = true
		for _, e := range edges {
			connected[e.To] = true
		}
	}
	g.connected = connected

	if len(connected) == 0 {
		g.kd = nil
		return
	}

	var sumLat float64
	for id := range connected {
		sumLat += g.Nodes[id].Lat
	}
	meanLat := sumLat / float64(len(connected))
	g.refCosLat = math.Cos(degToRad(meanLat))
	if g.refCosLat < 1e-6 {
		g.refCosLat = 1e-6 // guard against graphs sitting exactly on a pole
	}

	pts := make([]kdPoint, 0, len(connected))
	for id := range connected {
		n := g.Nodes[id]
		x, y := g.project(n.Lat, n.Lon)
		pts = append(pts, kdPoint{node: n, x: x, y: y})
	}
	g.kd = buildKDTree(pts, 0)
}

// buildKDTree recursively partitions pts into a balanced k-d tree.
func buildKDTree(pts []kdPoint, depth int) *kdNode {
	if len(pts) == 0 {
		return nil
	}
	axis := depth % 2
	// Sort along the current axis so we can pick the median.
	sort.Slice(pts, func(i, j int) bool {
		if axis == 0 {
			return pts[i].x < pts[j].x
		}
		return pts[i].y < pts[j].y
	})
	mid := len(pts) / 2
	return &kdNode{
		pt:    pts[mid],
		axis:  axis,
		left:  buildKDTree(pts[:mid], depth+1),
		right: buildKDTree(pts[mid+1:], depth+1),
	}
}

func nearestSearch(kd *kdNode, x, y float64, best **Node, bestDist *float64) {
	if kd == nil {
		return
	}
	dx := kd.pt.x - x
	dy := kd.pt.y - y
	d2 := dx*dx + dy*dy
	if d2 < *bestDist {
		*bestDist = d2
		*best = kd.pt.node
	}

	var first, second *kdNode
	var diff float64
	if kd.axis == 0 {
		diff = x - kd.pt.x
	} else {
		diff = y - kd.pt.y
	}
	if diff <= 0 {
		first, second = kd.left, kd.right
	} else {
		first, second = kd.right, kd.left
	}

	nearestSearch(first, x, y, best, bestDist)

	if diff*diff < *bestDist {
		nearestSearch(second, x, y, best, bestDist)
	}
}

func tagsMap(tags []tag) map[string]string {
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		m[t.K] = t.V
	}
	return m
}

func isPedestrian(tags map[string]string) bool {
	hw := tags["highway"]
	if hw == "" {
		return false
	}
	switch hw {
	case "motorway", "motorway_link", "trunk", "trunk_link":
		return false
	case "footway", "pedestrian", "path", "living_street",
		"residential", "service", "unclassified", "tertiary",
		"secondary", "primary", "steps", "track", "corridor":
		return true
	}
	if tags["foot"] == "no" {
		return false
	}
	return true
}

func isBicycle(tags map[string]string) bool {
	hw := tags["highway"]
	if hw == "" {
		return false
	}
	if tags["bicycle"] == "no" {
		return false
	}
	switch hw {
	case "motorway", "motorway_link":
		return false // bicycles are illegal on motorways essentially everywhere
	case "cycleway":
		return true
	case "footway", "pedestrian", "path", "steps", "corridor":
		return tags["bicycle"] == "yes" || tags["bicycle"] == "designated"
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

func isOnewayForMode(tags map[string]string, mode Mode) (oneway, reversed bool) {
	if mode == ModeBike {
		if v, ok := tags["oneway:bicycle"]; ok {
			switch v {
			case "no":
				return false, false
			case "yes", "1":
				return true, false
			case "-1":
				return true, true
			}
		}
		if tags["cycleway"] == "opposite" ||
			tags["cycleway:left"] == "opposite" ||
			tags["cycleway:right"] == "opposite" {
			return false, false
		}
	}

	switch tags["oneway"] {
	case "yes", "1":
		return true, false
	case "-1":
		return true, true
	}
	return false, false
}

const earthRadius = 6_371_000.0 // metres

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

type Format int

const (
	FormatAuto Format = iota
	FormatXML
	FormatPBF
)

func detectFormat(path string) Format {
	if strings.EqualFold(filepath.Ext(path), ".pbf") {
		return FormatPBF
	}
	return FormatXML
}

func Parse(path string, mode Mode, wheelchairOnly bool) (*Graph, error) {
	graphs, err := ParseMulti(path, []Mode{mode}, wheelchairOnly)
	if err != nil {
		return nil, err
	}
	return graphs[mode], nil
}

func ParseReader(r io.Reader, format Format, mode Mode, wheelchairOnly bool) (*Graph, error) {
	graphs, err := ParseReaderMulti(r, format, []Mode{mode}, wheelchairOnly)
	if err != nil {
		return nil, err
	}
	return graphs[mode], nil
}

func ParseMulti(path string, modes []Mode, wheelchairOnly bool) (map[Mode]*Graph, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("osm: open %s: %w", path, err)
	}
	defer f.Close()
	return ParseReaderMulti(f, detectFormat(path), modes, wheelchairOnly)
}

func ParseReaderMulti(r io.Reader, format Format, modes []Mode, wheelchairOnly bool) (map[Mode]*Graph, error) {
	var data *parsedData
	var err error
	switch format {
	case FormatPBF:
		data, err = decodePBF(r)
	default: // FormatAuto and FormatXML: a Reader has no filename to sniff
		data, err = decodeXML(r)
	}
	if err != nil {
		return nil, err
	}

	out := make(map[Mode]*Graph, len(modes))
	for _, mode := range modes {
		out[mode] = buildGraph(data, mode, wheelchairOnly)
	}
	return out, nil
}

func buildGraph(data *parsedData, mode Mode, wheelchairOnly bool) *Graph {
	g := &Graph{
		Mode:  mode,
		Nodes: make(map[NodeID]*Node, len(data.Nodes)),
		Edges: make(map[NodeID][]Edge, len(data.Nodes)),
	}

	for i := range data.Nodes {
		n := &data.Nodes[i]
		g.Nodes[n.ID] = &Node{ID: n.ID, Lat: n.Lat, Lon: n.Lon}
	}

	for _, way := range data.Ways {
		var usable bool
		switch mode {
		case ModeBike:
			usable = isBicycle(way.Tags)
		default:
			usable = isPedestrian(way.Tags)
		}
		if !usable {
			continue
		}
		if mode == ModeFoot && wheelchairOnly && !isWheelchairFriendly(way.Tags) {
			continue
		}
		stairs := mode == ModeFoot && isStairs(way.Tags)
		oneway, reversed := isOnewayForMode(way.Tags, mode)

		nds := way.NodeIDs
		for i := 0; i < len(nds)-1; i++ {
			aID := nds[i]
			bID := nds[i+1]
			a, okA := g.Nodes[aID]
			b, okB := g.Nodes[bID]
			if !okA || !okB {
				continue
			}
			dist := HaversineMetres(a.Lat, a.Lon, b.Lat, b.Lon)

			from, to := aID, bID
			if oneway && reversed {
				from, to = bID, aID
			}
			g.Edges[from] = append(g.Edges[from], Edge{To: to, Cost: dist, IsStairs: stairs})
			if !oneway {
				g.Edges[to] = append(g.Edges[to], Edge{To: from, Cost: dist, IsStairs: stairs})
			}
		}
	}

	g.BuildIndex()

	return g
}

func (g *Graph) NearestNode(lat, lon, maxDistMetres float64) (NodeID, bool) {
	if g.kd != nil {
		return g.nearestKD(lat, lon, maxDistMetres)
	}
	return g.nearestLinear(lat, lon, maxDistMetres)
}

// nearestKD uses the k-d tree for O(log n) average-case lookup.
func (g *Graph) nearestKD(lat, lon, maxDistMetres float64) (NodeID, bool) {
	var best *Node
	bestDist := maxDistMetres * maxDistMetres

	x, y := g.project(lat, lon)
	nearestSearch(g.kd, x, y, &best, &bestDist)
	if best == nil {
		return 0, false
	}
	if HaversineMetres(lat, lon, best.Lat, best.Lon) > maxDistMetres {
		return 0, false
	}
	return best.ID, true
}

func (g *Graph) nearestLinear(lat, lon, maxDistMetres float64) (NodeID, bool) {
	best := maxDistMetres + 1
	var bestID NodeID
	found := false
	for id := range g.connected {
		n := g.Nodes[id]
		d := HaversineMetres(lat, lon, n.Lat, n.Lon)
		if d < best {
			best = d
			bestID = n.ID
			found = true
		}
	}
	return bestID, found
}
