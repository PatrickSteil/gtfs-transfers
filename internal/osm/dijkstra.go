// This file implements a bounded single-source Dijkstra search over the
// pedestrian Graph. DijkstraWithBuf takes a reusable WorkBuf so that
// callers running many searches (one per GTFS stop, in the transfers
// package) can amortise heap/map allocations across the whole run instead
// of paying them on every call. For a single one-off search, use:
//
//   buf := osm.NewWorkBuf(len(g.Nodes))
//   reached := osm.DijkstraWithBuf(g, src, cfg, buf)

package osm

import (
	"container/heap"

	"github.com/PatrickSteil/gtfs-transfers/internal/config"
)

// ReachedNode is one entry in the Dijkstra result set.
type ReachedNode struct {
	NodeID  NodeID
	Seconds float64
}

// ---------------------------------------------------------------------------
// Priority queue (min-heap on cost)
// ---------------------------------------------------------------------------

type pqItem struct {
	nodeID NodeID
	cost   float64
	index  int // maintained by heap.Interface
}

type priorityQueue []*pqItem

func (pq priorityQueue) Len() int           { return len(pq) }
func (pq priorityQueue) Less(i, j int) bool { return pq[i].cost < pq[j].cost }
func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}
func (pq *priorityQueue) Push(x any) {
	item := x.(*pqItem)
	item.index = len(*pq)
	*pq = append(*pq, item)
}
func (pq *priorityQueue) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*pq = old[:n-1]
	return item
}

// ---------------------------------------------------------------------------
// WorkBuf — reusable allocations owned by one worker goroutine
// ---------------------------------------------------------------------------

// WorkBuf holds the heap and distance map that Dijkstra needs on every call.
// Allocate one per worker with NewWorkBuf and pass it to DijkstraWithBuf;
// the buffer is reset at the start of each call so it can be reused safely.
type WorkBuf struct {
	// dist maps NodeID → best known cost so far.
	// We reuse the map by deleting only the keys we touched rather than
	// allocating a fresh map each call — O(reached) deletions vs O(n) GC.
	dist map[NodeID]float64

	// touched records every key written to dist this call so we can scrub
	// them at the end without iterating the whole map.
	touched []NodeID

	// pq is the priority queue, reset to length 0 between calls.
	pq priorityQueue

	// pool is a free-list of pqItems so we avoid per-item allocations.
	pool []*pqItem
}

// NewWorkBuf allocates a WorkBuf sized for a graph with nodeCount nodes.
// nodeCount is a hint; the buffer will grow if needed.
func NewWorkBuf(nodeCount int) *WorkBuf {
	return &WorkBuf{
		dist:    make(map[NodeID]float64, nodeCount),
		touched: make([]NodeID, 0, 256),
		pq:      make(priorityQueue, 0, 256),
		pool:    make([]*pqItem, 0, 256),
	}
}

// reset prepares the buffer for a new Dijkstra call by clearing only the
// entries written during the previous call.
func (b *WorkBuf) reset() {
	for _, id := range b.touched {
		delete(b.dist, id)
	}
	b.touched = b.touched[:0]
	b.pq = b.pq[:0]
	// Return pooled items; we keep the backing array.
	b.pool = b.pool[:0]
}

func (b *WorkBuf) acquire(id NodeID, cost float64) *pqItem {
	if len(b.pool) > 0 {
		item := b.pool[len(b.pool)-1]
		b.pool = b.pool[:len(b.pool)-1]
		item.nodeID = id
		item.cost = cost
		return item
	}
	return &pqItem{nodeID: id, cost: cost}
}

func (b *WorkBuf) release(item *pqItem) {
	b.pool = append(b.pool, item)
}

// ---------------------------------------------------------------------------
// DijkstraWithBuf
// ---------------------------------------------------------------------------

// DijkstraWithBuf runs Dijkstra from src, reusing buf's allocations.
// buf must not be shared between goroutines.
//
// Edges whose IsStairs flag is set are penalised by cfg.StairPenalty (a
// multiplier on their cost); set it to 1.0 to treat stairs as flat.
//
// Adapt the speed / penalty arithmetic below to match your existing Dijkstra.
func DijkstraWithBuf(g *Graph, src NodeID, cfg config.WalkConfig, buf *WorkBuf) []ReachedNode {
	buf.reset()

	startItem := buf.acquire(src, 0)
	heap.Push(&buf.pq, startItem)
	buf.dist[src] = 0
	buf.touched = append(buf.touched, src)

	var reached []ReachedNode

	for buf.pq.Len() > 0 {
		cur := heap.Pop(&buf.pq).(*pqItem)
		curID := cur.nodeID
		curCost := cur.cost
		buf.release(cur)

		// Stale entry — a shorter path was already settled.
		if best, ok := buf.dist[curID]; ok && curCost > best {
			continue
		}

		// Convert settled cost (seconds) to a ReachedNode only if within
		// the walking budget.
		if curCost <= cfg.MaxWalkingTime {
			reached = append(reached, ReachedNode{NodeID: curID, Seconds: curCost})
		}

		for _, edge := range g.Edges[curID] {
			// Convert metres → seconds using configured walk speed.
			// Stair edges are undirected so we can't know if the
			// pedestrian is ascending or descending; average the two
			// stair speeds as a reasonable approximation.
			var speed float64
			if edge.IsStairs {
				speed = (cfg.StairSpeedUp + cfg.StairSpeedDown) / 2.0
			} else {
				speed = cfg.FlatSpeed
			}
			edgeSec := edge.Cost / speed

			newCost := curCost + edgeSec
			if newCost > cfg.MaxWalkingTime {
				continue // prune: can't reach anything useful via this edge
			}

			if prev, ok := buf.dist[edge.To]; !ok || newCost < prev {
				buf.dist[edge.To] = newCost
				if !ok {
					buf.touched = append(buf.touched, edge.To)
				}
				item := buf.acquire(edge.To, newCost)
				heap.Push(&buf.pq, item)
			}
		}
	}

	return reached
}
