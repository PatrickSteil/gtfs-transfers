package osm

import "github.com/PatrickSteil/gtfs-transfers/internal/config"

func (g *Graph) ApplyBoundingBox(bbox *config.BoundingBox) {
	if bbox == nil {
		return
	}
	keep := make(map[NodeID]bool, len(g.Nodes))
	for id, n := range g.Nodes {
		if bbox.Contains(n.Lat, n.Lon) {
			keep[id] = true
		}
	}
	g.keepOnly(keep)
	g.RebuildIndex()
}

// KeepLargestComponent discards every vertex not in the graph's largest
// connected component. Connectivity is evaluated on the *undirected*
// version of the graph (an edge in either direction counts as a
// connection) — a one-way street shouldn't fragment what is physically one
// connected network.
func (g *Graph) KeepLargestComponent() {
	adj := make(map[NodeID][]NodeID, len(g.Nodes))
	for from, edges := range g.Edges {
		for _, e := range edges {
			adj[from] = append(adj[from], e.To)
			adj[e.To] = append(adj[e.To], from)
		}
	}

	visited := make(map[NodeID]bool, len(g.Nodes))
	var best []NodeID
	for id := range g.Nodes {
		if visited[id] {
			continue
		}
		comp := bfsComponent(id, adj, visited)
		if len(comp) > len(best) {
			best = comp
		}
	}

	keep := make(map[NodeID]bool, len(best))
	for _, id := range best {
		keep[id] = true
	}
	g.keepOnly(keep)
	g.RebuildIndex()
}

func bfsComponent(start NodeID, adj map[NodeID][]NodeID, visited map[NodeID]bool) []NodeID {
	visited[start] = true
	comp := []NodeID{start}
	queue := []NodeID{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, nb := range adj[cur] {
			if !visited[nb] {
				visited[nb] = true
				comp = append(comp, nb)
				queue = append(queue, nb)
			}
		}
	}
	return comp
}

// keepOnly rebuilds g.Nodes/g.Edges to contain only the vertices in keep
// (and only edges whose endpoints are both kept).
func (g *Graph) keepOnly(keep map[NodeID]bool) {
	newNodes := make(map[NodeID]*Node, len(keep))
	for id := range keep {
		if n, ok := g.Nodes[id]; ok {
			newNodes[id] = n
		}
	}

	newEdges := make(map[NodeID][]Edge, len(keep))
	for from, edges := range g.Edges {
		if !keep[from] {
			continue
		}
		var kept []Edge
		for _, e := range edges {
			if keep[e.To] {
				kept = append(kept, e)
			}
		}
		if len(kept) > 0 {
			newEdges[from] = kept
		}
	}

	g.Nodes = newNodes
	g.Edges = newEdges
}
