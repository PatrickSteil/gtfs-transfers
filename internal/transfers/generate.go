package transfers

import (
	"fmt"
	"math"
	"runtime"
	"sync"

	"github.com/PatrickSteil/gtfs-transfers/internal/config"
	osmgraph "github.com/PatrickSteil/gtfs-transfers/internal/osm"
)

// Entry is one computed (from, to) transfer with its travel time in
// seconds, for one mode. It carries no assumption about where the stop
// IDs came from (GTFS or a plain stops.csv) or where the result will be
// written (transfers.txt vs. a plain CSV) — see gtfs.go and csv.go.
type Entry struct {
	FromID  string
	ToID    string
	Seconds int
}

// ComputeTransfers runs a bounded Dijkstra fan-out from every stop in
// stopNode over g and returns every (from, to) pair reached within
// maxSeconds for the given mode.
func ComputeTransfers(
	g *osmgraph.Graph,
	mode config.Mode,
	maxSeconds float64,
	stopNode map[string]osmgraph.NodeID,
) []Entry {
	nodeToStops := make(map[osmgraph.NodeID][]string, len(stopNode))
	for id, nid := range stopNode {
		nodeToStops[nid] = append(nodeToStops[nid], id)
	}

	type job struct {
		stopID string
		nodeID osmgraph.NodeID
	}
	jobs := make(chan job, runtime.NumCPU()*2)
	results := make(chan []Entry, runtime.NumCPU()*2)

	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := osmgraph.NewWorkBuf(len(g.Nodes))

			for j := range jobs {
				reached := osmgraph.DijkstraWithBuf(g, j.nodeID, mode, maxSeconds, buf)

				var entries []Entry
				for _, r := range reached {
					dstIDs, ok := nodeToStops[r.NodeID]
					if !ok {
						continue
					}
					for _, dstID := range dstIDs {
						if dstID == j.stopID {
							continue
						}
						entries = append(entries, Entry{
							FromID:  j.stopID,
							ToID:    dstID,
							Seconds: int(math.Ceil(r.Seconds)),
						})
					}
				}
				if len(entries) > 0 {
					results <- entries
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	go func() {
		for id, nid := range stopNode {
			jobs <- job{stopID: id, nodeID: nid}
		}
		close(jobs)
	}()

	var all []Entry
	for entries := range results {
		all = append(all, entries...)
	}

	fmt.Printf("  Computed %d transfers (%s)\n", len(all), mode.Name)
	return all
}
