package osm

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"sort"

	"github.com/PatrickSteil/gtfs-transfers/internal/config"
)

func (g *Graph) NodeIndex() map[NodeID]int {
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

func dimacsEdgeWeight(e Edge, mode config.Mode, scale float64) int64 {
	seconds := e.TravelTimeSeconds(mode)
	w := int64(math.Round(seconds * scale))
	if w < 1 {
		w = 1
	}
	return w
}

func (g *Graph) WriteDIMACSGraph(w io.Writer, index map[NodeID]int, mode config.Mode, scale float64) error {
	if scale <= 0 {
		scale = 1
	}

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
	fmt.Fprintf(bw, "c mode: %s (%.2f km/h)\n", mode.Name, mode.SpeedKmH)
	fmt.Fprintf(bw, "c weight unit = travel time in seconds * %g, rounded, floored at 1\n", scale)
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
			fmt.Fprintf(bw, "a %d %d %d\n", fu, tv, dimacsEdgeWeight(e, mode, scale))
		}
	}

	return bw.Flush()
}

func (g *Graph) WriteDIMACSCoords(w io.Writer, index map[NodeID]int) error {
	bw := bufio.NewWriter(w)

	fmt.Fprintf(bw, "c DIMACS coordinates exported by gtfs-transfers\n")
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
