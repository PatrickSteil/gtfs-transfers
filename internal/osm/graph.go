package osm

import (
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/PatrickSteil/gtfs-transfers/internal/config"
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

func tagsMap(tags []tag) map[string]string {
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		m[t.K] = t.V
	}
	return m
}

type NodeID = int64

type Node struct {
	ID  NodeID
	Lat float64
	Lon float64

	StopIDs []string
}

func (n *Node) IsStop() bool { return len(n.StopIDs) > 0 }

type EdgeKind int

const (
	EdgeOSM EdgeKind = iota

	EdgeConnector

	EdgeRetained
)

type segment struct {
	Kind          EdgeKind
	DistM         float64
	SpeedLimitKmH float64 // only meaningful for EdgeOSM
	FixedSeconds  float64 // only meaningful for EdgeRetained
}

func (s segment) travelTimeSeconds(mode config.Mode) float64 {
	switch s.Kind {
	case EdgeRetained:
		return s.FixedSeconds
	case EdgeConnector:
		return s.DistM / config.KmHToMS(mode.SpeedKmH)
	default: // EdgeOSM
		speed := math.Min(mode.SpeedKmH, s.SpeedLimitKmH)
		if speed <= 0 {
			speed = mode.SpeedKmH
		}
		return s.DistM / config.KmHToMS(speed)
	}
}

type Edge struct {
	To NodeID

	// The edge's own segment, used directly when Chain is nil.
	Kind          EdgeKind
	DistM         float64
	SpeedLimitKmH float64
	FixedSeconds  float64

	Chain []segment
}

func (e Edge) self() segment {
	return segment{Kind: e.Kind, DistM: e.DistM, SpeedLimitKmH: e.SpeedLimitKmH, FixedSeconds: e.FixedSeconds}
}

func (e Edge) TravelTimeSeconds(mode config.Mode) float64 {
	if e.Chain != nil {
		var total float64
		for _, s := range e.Chain {
			total += s.travelTimeSeconds(mode)
		}
		return total
	}
	return e.self().travelTimeSeconds(mode)
}

func (e Edge) DistanceMetres() float64 {
	if e.Chain != nil {
		var total float64
		for _, s := range e.Chain {
			total += s.DistM
		}
		return total
	}
	return e.DistM
}

type Graph struct {
	Nodes map[NodeID]*Node
	Edges map[NodeID][]Edge

	index *PointIndex

	connected map[NodeID]bool

	nextSyntheticID NodeID
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

func Parse(path string) (*Graph, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("osm: open %s: %w", path, err)
	}
	defer f.Close()
	return ParseReader(f, detectFormat(path))
}

func ParseReader(r io.Reader, format Format) (*Graph, error) {
	var data *parsedData
	var err error
	switch format {
	case FormatPBF:
		data, err = decodePBF(r)
	default:
		data, err = decodeXML(r)
	}
	if err != nil {
		return nil, err
	}
	return buildGraph(data), nil
}

func buildGraph(data *parsedData) *Graph {
	g := &Graph{
		Nodes: make(map[NodeID]*Node, len(data.Nodes)),
		Edges: make(map[NodeID][]Edge, len(data.Nodes)),
	}

	for i := range data.Nodes {
		n := &data.Nodes[i]
		g.Nodes[n.ID] = &Node{ID: n.ID, Lat: n.Lat, Lon: n.Lon}
	}

	for _, way := range data.Ways {
		if !isRoutable(way.Tags) {
			continue
		}
		speedLimit := parseSpeedLimitKmH(way.Tags)
		oneway, reversed := isOneway(way.Tags)

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
			g.Edges[from] = append(g.Edges[from], Edge{To: to, Kind: EdgeOSM, DistM: dist, SpeedLimitKmH: speedLimit})
			if !oneway {
				g.Edges[to] = append(g.Edges[to], Edge{To: from, Kind: EdgeOSM, DistM: dist, SpeedLimitKmH: speedLimit})
			}
		}
	}

	g.RebuildIndex()
	return g
}

func (g *Graph) RebuildIndex() {
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
		g.index = nil
		return
	}

	pts := make([]IndexPoint, 0, len(connected))
	for id := range connected {
		n := g.Nodes[id]
		pts = append(pts, IndexPoint{ID: id, Lat: n.Lat, Lon: n.Lon})
	}
	g.index = NewPointIndex(pts)
}

func (g *Graph) NearestNode(lat, lon, maxDistMetres float64) (NodeID, float64, bool) {
	return g.index.Nearest(lat, lon, maxDistMetres)
}

func (g *Graph) Connected(id NodeID) bool { return g.connected[id] }

func (g *Graph) NewSyntheticNodeID() NodeID {
	g.nextSyntheticID--
	return g.nextSyntheticID
}

func (g *Graph) AddEdge(from NodeID, e Edge) {
	g.Edges[from] = append(g.Edges[from], e)
}

func (g *Graph) RemoveNode(id NodeID) {
	delete(g.Nodes, id)
	delete(g.Edges, id)
	for from, edges := range g.Edges {
		kept := edges[:0]
		for _, e := range edges {
			if e.To != id {
				kept = append(kept, e)
			}
		}
		g.Edges[from] = kept
	}
}
