package osm

func (g *Graph) ContractDegreeOneTwo() {
	inAdj := make(map[NodeID][]NodeID, len(g.Nodes))
	for from, edges := range g.Edges {
		for _, e := range edges {
			inAdj[e.To] = append(inAdj[e.To], from)
		}
	}

	queue := make([]NodeID, 0, len(g.Nodes))
	queued := make(map[NodeID]bool, len(g.Nodes))
	enqueue := func(id NodeID) {
		if _, ok := g.Nodes[id]; !ok {
			return
		}
		if !queued[id] {
			queued[id] = true
			queue = append(queue, id)
		}
	}
	for id, n := range g.Nodes {
		if !n.IsStop() {
			enqueue(id)
		}
	}

	neighborsOf := func(v NodeID) []NodeID {
		seen := make(map[NodeID]bool, 4)
		var out []NodeID
		for _, e := range g.Edges[v] {
			if !seen[e.To] {
				seen[e.To] = true
				out = append(out, e.To)
			}
		}
		for _, from := range inAdj[v] {
			if !seen[from] {
				seen[from] = true
				out = append(out, from)
			}
		}
		return out
	}

	dropDeadEnd := func(v NodeID, neighbors []NodeID) {
		for _, nb := range neighbors {
			removeEdgeBetween(g, v, nb)
			removeEdgeBetween(g, nb, v)
			removeFromInAdj(inAdj, nb, v)
			removeFromInAdj(inAdj, v, nb)
			enqueue(nb)
		}
		delete(g.Edges, v)
		delete(g.Nodes, v)
		delete(inAdj, v)
	}

	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		queued[v] = false

		n, ok := g.Nodes[v]
		if !ok || n.IsStop() {
			continue
		}

		neighbors := neighborsOf(v)
		switch {
		case len(neighbors) <= 1:
			dropDeadEnd(v, neighbors)

		case len(neighbors) == 2 && neighbors[0] == neighbors[1]:
			dropDeadEnd(v, neighbors[:1])

		case len(neighbors) == 2:
			a, b := neighbors[0], neighbors[1]
			if tryChainContract(g, inAdj, v, a, b) {
				delete(g.Nodes, v)
				delete(g.Edges, v)
				delete(inAdj, v)
				enqueue(a)
				enqueue(b)
			}
		}
	}

	g.RebuildIndex()
}

func tryChainContract(g *Graph, inAdj map[NodeID][]NodeID, v, a, b NodeID) bool {
	outA := edgesBetween(g, v, a) // v -> a
	outB := edgesBetween(g, v, b) // v -> b
	inA := edgesBetween(g, a, v)  // a -> v
	inB := edgesBetween(g, b, v)  // b -> v

	if len(outA) > 1 || len(outB) > 1 || len(inA) > 1 || len(inB) > 1 {
		return false // parallel edges: ambiguous, leave alone
	}

	type newChain struct {
		from, to NodeID
		chain    []segment
	}
	var chains []newChain
	consumed := 0

	if len(inA) == 1 && len(outB) == 1 {
		chains = append(chains, newChain{from: a, to: b, chain: concatSegments(inA[0], outB[0])})
		consumed += 2
	}
	if len(inB) == 1 && len(outA) == 1 {
		chains = append(chains, newChain{from: b, to: a, chain: concatSegments(inB[0], outA[0])})
		consumed += 2
	}

	total := len(outA) + len(outB) + len(inA) + len(inB)
	if consumed != total {
		// Some incident edge couldn't be paired into a through-chain
		// (e.g. v is a genuine one-way fork/merge point, with two edges
		// pointing the same logical direction). Leave v untouched.
		return false
	}
	if len(chains) == 0 {
		return false // v had no edges at all; handled by the degree<=1 path
	}

	// Remove v's original edges and the corresponding inAdj bookkeeping.
	removeEdgeBetween(g, v, a)
	removeEdgeBetween(g, v, b)
	removeEdgeBetween(g, a, v)
	removeEdgeBetween(g, b, v)
	removeFromInAdj(inAdj, a, v)
	removeFromInAdj(inAdj, b, v)
	removeFromInAdj(inAdj, v, a)
	removeFromInAdj(inAdj, v, b)

	for _, c := range chains {
		var total float64
		for _, s := range c.chain {
			total += s.DistM
		}
		g.Edges[c.from] = append(g.Edges[c.from], Edge{To: c.to, DistM: total, Chain: c.chain})
		inAdj[c.to] = append(inAdj[c.to], c.from)
	}
	return true
}

func concatSegments(in, out Edge) []segment {
	segs := make([]segment, 0, 2)
	segs = append(segs, in.segments()...)
	segs = append(segs, out.segments()...)
	return segs
}

func (e Edge) segments() []segment {
	if e.Chain != nil {
		return e.Chain
	}
	return []segment{e.self()}
}

// edgesBetween returns every edge from -> to currently in the graph.
func edgesBetween(g *Graph, from, to NodeID) []Edge {
	var out []Edge
	for _, e := range g.Edges[from] {
		if e.To == to {
			out = append(out, e)
		}
	}
	return out
}

// removeEdgeBetween deletes every from->to edge (there's normally at most
// one, but this is defensive).
func removeEdgeBetween(g *Graph, from, to NodeID) {
	edges := g.Edges[from]
	if len(edges) == 0 {
		return
	}
	kept := edges[:0]
	for _, e := range edges {
		if e.To != to {
			kept = append(kept, e)
		}
	}
	g.Edges[from] = kept
}

// removeFromInAdj removes one occurrence of `from` from inAdj[to].
func removeFromInAdj(inAdj map[NodeID][]NodeID, to, from NodeID) {
	lst := inAdj[to]
	for i, f := range lst {
		if f == from {
			inAdj[to] = append(lst[:i], lst[i+1:]...)
			return
		}
	}
}
