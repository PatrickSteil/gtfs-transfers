package osm

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"sort"

	"github.com/PatrickSteil/gtfs-transfers/internal/config"
)

// DIMACS shortest-path format (as used by the 9th DIMACS Implementation
// Challenge) requires a dense, 1-based node numbering and integer arc
// weights. OSM node IDs are sparse 64-bit values, so we build an explicit
// mapping from OSM NodeID to a compact index before writing anything.

// NodeIndex builds a deterministic, dense 1-based numbering of every
// routable node in the graph (i.e. every node with at least one incident
// edge — see the Graph.connected doc comment). The same map must be passed
// to WriteDIMACSGraph, WriteDIMACSCoords, and to any station-mapping export
// so that all outputs agree on node numbers.
//
// Two graphs for different modes (foot vs. bike) built from the same OSM
// file will generally *not* agree on node numbers, because their connected
// sets differ — that's expected and is exactly why each mode gets its own
// .gr/.co pair rather than sharing one numbering.
func (g *Graph) NodeIndex() map[NodeID]int {
	// g.connected is nil only if BuildIndex was never called; fall back to
	// every node in that case rather than returning an empty map.
	src := g.connected
	if src == nil {
		src = make(map[NodeID]bool, len(g.Nodes))
		for id := range g.Nodes {
			src[id] = true
		}
	}

	ids := make([]NodeID, 0, len(src))
	for id := range src {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	idx := make(map[NodeID]int, len(ids))
	for i, id := range ids {
		idx[id] = i + 1 // DIMACS node numbers are 1-based
	}
	return idx
}

// EdgeSpeedFunc computes an edge's traversal speed in m/s, given the edge
// itself. Passed to WriteDIMACSGraph so that the same exporter works for
// any mode without needing to know its config type.
type EdgeSpeedFunc func(e Edge) float64

// FootSpeedFunc returns an EdgeSpeedFunc for a pedestrian (ModeFoot) graph,
// using the same speed model as DijkstraWithBuf: cfg.FlatSpeed for normal
// edges, the average of cfg.StairSpeedUp/Down for stair edges (edges don't
// carry ascend/descend direction, so we can't do better without a bearing).
func FootSpeedFunc(cfg config.WalkConfig) EdgeSpeedFunc {
	avgStair := (cfg.StairSpeedUp + cfg.StairSpeedDown) / 2.0
	return func(e Edge) float64 {
		if e.IsStairs {
			return avgStair
		}
		return cfg.FlatSpeed
	}
}

// ConstantSpeedFunc returns an EdgeSpeedFunc that applies the same speed to
// every edge. Suitable for bike graphs (ModeBike edges never carry a stairs
// flag — stairs are excluded from the bike graph unless explicitly tagged
// bicycle=yes/designated, in which case they're treated as a normal edge).
func ConstantSpeedFunc(speedMPS float64) EdgeSpeedFunc {
	return func(Edge) float64 { return speedMPS }
}

// dimacsEdgeWeight converts one Edge's stored distance (metres) into an
// integer DIMACS arc weight using speed, scaled by scale and rounded to the
// nearest integer. scale lets the caller trade off precision vs. weight
// magnitude — e.g. scale=1 gives whole seconds, scale=100 gives
// centiseconds. The weight is floored at 1 because DIMACS solvers generally
// assume strictly positive arc weights, and a real (non-loop) path segment
// should never be free.
func dimacsEdgeWeight(e Edge, speed EdgeSpeedFunc) int64 {
	s := speed(e)
	if s <= 0 {
		s = 1e-6
	}
	seconds := e.Cost / s
	w := int64(math.Round(seconds))
	if w < 1 {
		w = 1
	}
	return w
}

// WriteDIMACSGraph writes the graph's directed arcs in the DIMACS ".gr"
// shortest-path challenge format:
//
//	c <comment>
//	p sp <num_nodes> <num_arcs>
//	a <from> <to> <weight>
//
// index should come from NodeIndex(). Arc weights are travel time computed
// via speed (see FootSpeedFunc / ConstantSpeedFunc)
//
// Output is deterministic: nodes and their outgoing arcs are both written
// in sorted order, so re-running the export over the same graph/speed model
// byte-for-byte reproduces the same file.
func (g *Graph) WriteDIMACSGraph(w io.Writer, index map[NodeID]int, speed EdgeSpeedFunc) error {
	if speed == nil {
		speed = ConstantSpeedFunc(1.0)
	}

	// Only count/emit arcs whose endpoints are both present in index (index
	// may be a subset of g.Nodes if the caller built it that way; normally
	// it covers every connected node).
	numArcs := 0
	for from, edges := range g.Edges {
		if _, ok := index[from]; !ok {
			continue
		}
		for _, e := range edges {
			if _, ok := index[e.To]; ok {
				numArcs++
			}
		}
	}

	bw := bufio.NewWriter(w)

	fmt.Fprintf(bw, "c DIMACS shortest-path graph exported by gtfs-transfers\n")
	fmt.Fprintf(bw, "c mode: %s\n", g.Mode)
	fmt.Fprintf(bw, "c weight unit = travel time in seconds, rounded, floored at 1\n")
	fmt.Fprintf(bw, "p sp %d %d\n", len(index), numArcs)

	froms := make([]NodeID, 0, len(index))
	for id := range index {
		froms = append(froms, id)
	}
	sort.Slice(froms, func(i, j int) bool { return froms[i] < froms[j] })

	for _, from := range froms {
		edges := g.Edges[from]
		if len(edges) == 0 {
			continue
		}
		sorted := make([]Edge, len(edges))
		copy(sorted, edges)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].To < sorted[j].To })

		fu := index[from]
		for _, e := range sorted {
			tv, ok := index[e.To]
			if !ok {
				continue
			}
			fmt.Fprintf(bw, "a %d %d %d\n", fu, tv, dimacsEdgeWeight(e, speed))
		}
	}

	return bw.Flush()
}

// WriteDIMACSCoords writes node coordinates in the DIMACS ".co" auxiliary
// format used alongside ".gr" files:
//
//	c <comment>
//	p aux sp co <num_nodes>
//	v <node_id> <x> <y>
//
// The format requires integer coordinates, so lat/lon are scaled by 1e6
// (micro-degrees) and rounded. Following the convention used by the
// original DIMACS USA road-network instances, x is longitude and y is
// latitude.
func (g *Graph) WriteDIMACSCoords(w io.Writer, index map[NodeID]int) error {
	bw := bufio.NewWriter(w)

	fmt.Fprintf(bw, "c DIMACS coordinates exported by gtfs-transfers\n")
	fmt.Fprintf(bw, "c mode: %s\n", g.Mode)
	fmt.Fprintf(bw, "c x = lon * 1e6, y = lat * 1e6 (micro-degrees, rounded)\n")
	fmt.Fprintf(bw, "p aux sp co %d\n", len(index))

	ids := make([]NodeID, 0, len(index))
	for id := range index {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, id := range ids {
		n, ok := g.Nodes[id]
		if !ok {
			continue // shouldn't happen if index was built from this graph
		}
		x := int64(math.Round(n.Lon * 1e6))
		y := int64(math.Round(n.Lat * 1e6))
		fmt.Fprintf(bw, "v %d %d %d\n", index[id], x, y)
	}

	return bw.Flush()
}
