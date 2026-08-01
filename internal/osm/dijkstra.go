package osm

import (
	"container/heap"

	"github.com/PatrickSteil/gtfs-transfers/internal/config"
)

type ReachedNode struct {
	NodeID  NodeID
	Seconds float64
}

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

type WorkBuf struct {
	dist    map[NodeID]float64
	touched []NodeID
	pq      priorityQueue
	pool    []*pqItem
}

func NewWorkBuf(nodeCount int) *WorkBuf {
	return &WorkBuf{
		dist:    make(map[NodeID]float64, nodeCount),
		touched: make([]NodeID, 0, 256),
		pq:      make(priorityQueue, 0, 256),
		pool:    make([]*pqItem, 0, 256),
	}
}

func (b *WorkBuf) reset() {
	for _, id := range b.touched {
		delete(b.dist, id)
	}
	b.touched = b.touched[:0]
	b.pq = b.pq[:0]
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

func DijkstraWithBuf(g *Graph, src NodeID, mode config.Mode, maxSeconds float64, buf *WorkBuf) []ReachedNode {
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

		if best, ok := buf.dist[curID]; ok && curCost > best {
			continue // stale entry — a shorter path was already settled
		}

		if curCost <= maxSeconds {
			reached = append(reached, ReachedNode{NodeID: curID, Seconds: curCost})
		}

		for _, edge := range g.Edges[curID] {
			newCost := curCost + edge.TravelTimeSeconds(mode)
			if newCost > maxSeconds {
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
