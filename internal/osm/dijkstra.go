// Package osm – Dijkstra search on a pedestrian graph.
package osm

import (
	"container/heap"

	"github.com/PatrickSteil/gtfs-transfers/internal/config"
)

type pqItem struct {
	node NodeID
	cost float64 // seconds so far
	idx  int
}

type priorityQueue []*pqItem

func (pq priorityQueue) Len() int           { return len(pq) }
func (pq priorityQueue) Less(i, j int) bool { return pq[i].cost < pq[j].cost }
func (pq priorityQueue) Swap(i, j int)      { pq[i], pq[j] = pq[j], pq[i]; pq[i].idx = i; pq[j].idx = j }
func (pq *priorityQueue) Push(x interface{}) {
	item := x.(*pqItem)
	item.idx = len(*pq)
	*pq = append(*pq, item)
}
func (pq *priorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	*pq = old[:n-1]
	return item
}

type ReachResult struct {
	NodeID  NodeID
	Seconds float64
}

func Dijkstra(g *Graph, startNode NodeID, cfg config.WalkConfig) []ReachResult {
	dist := make(map[NodeID]float64)
	dist[startNode] = 0

	pq := &priorityQueue{{node: startNode, cost: 0}}
	heap.Init(pq)

	for pq.Len() > 0 {
		cur := heap.Pop(pq).(*pqItem)
		if cur.cost > dist[cur.node] {
			continue // stale entry
		}
		if cur.cost > cfg.MaxWalkingTime {
			break // everything left in the queue is over budget
		}

		for _, e := range g.Edges[cur.node] {
			speed := edgeSpeed(e, cfg)
			if speed <= 0 {
				continue
			}
			newCost := cur.cost + e.Cost/speed
			if prev, seen := dist[e.To]; !seen || newCost < prev {
				dist[e.To] = newCost
				heap.Push(pq, &pqItem{node: e.To, cost: newCost})
			}
		}
	}

	results := make([]ReachResult, 0, len(dist))
	for nid, sec := range dist {
		if nid == startNode {
			continue
		}
		if sec <= cfg.MaxWalkingTime {
			results = append(results, ReachResult{NodeID: nid, Seconds: sec})
		}
	}
	return results
}

func edgeSpeed(e Edge, cfg config.WalkConfig) float64 {
	if e.IsStairs {
		if cfg.WheelchairAccessible {
			return 0 // skip stairs entirely
		}
		return (cfg.StairSpeedUp + cfg.StairSpeedDown) / 2
	}
	return cfg.FlatSpeed
}
