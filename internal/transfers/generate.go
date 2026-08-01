package transfers

import (
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/PatrickSteil/gtfs-transfers/internal/config"
	osmgraph "github.com/PatrickSteil/gtfs-transfers/internal/osm"
	gtfsparser "github.com/patrickbr/gtfsparser"
	gtfs "github.com/patrickbr/gtfsparser/gtfs"
)

type transferEntry struct {
	fromID  string
	toID    string
	seconds int
}

func GenerateTransfers(
	feed *gtfsparser.Feed,
	g *osmgraph.Graph,
	mode config.Mode,
	maxSeconds float64,
	stopNode map[string]osmgraph.NodeID,
) {
	nodeToStops := make(map[osmgraph.NodeID][]string, len(stopNode))
	for id, nid := range stopNode {
		nodeToStops[nid] = append(nodeToStops[nid], id)
	}

	type job struct {
		stopID string
		nodeID osmgraph.NodeID
	}
	jobs := make(chan job, runtime.NumCPU()*2)
	results := make(chan []transferEntry, runtime.NumCPU()*2)

	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := osmgraph.NewWorkBuf(len(g.Nodes))

			for j := range jobs {
				reached := osmgraph.DijkstraWithBuf(g, j.nodeID, mode, maxSeconds, buf)

				var entries []transferEntry
				for _, r := range reached {
					dstIDs, ok := nodeToStops[r.NodeID]
					if !ok {
						continue
					}
					for _, dstID := range dstIDs {
						if dstID == j.stopID {
							continue
						}
						entries = append(entries, transferEntry{
							fromID:  j.stopID,
							toID:    dstID,
							seconds: int(math.Ceil(r.Seconds)),
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

	stopByID := make(map[string]*gtfs.Stop, len(feed.Stops))
	for _, s := range feed.Stops {
		stopByID[s.Id] = s
	}

	var generated int64
	for entries := range results {
		for _, e := range entries {
			from, to := stopByID[e.fromID], stopByID[e.toID]
			if from == nil || to == nil {
				continue
			}
			addTransfer(feed, from, to, e.seconds)
			atomic.AddInt64(&generated, 1)
		}
	}

	fmt.Printf("  Generated %d transfers (%s)\n", generated, mode.Name)
}

func addTransfer(feed *gtfsparser.Feed, from, to *gtfs.Stop, seconds int) {
	key := gtfs.TransferKey{
		From_stop: from,
		To_stop:   to,
	}
	if existing, ok := feed.Transfers[key]; ok {
		if existing.Min_transfer_time <= seconds {
			return // keep the shorter existing value
		}
	}
	feed.Transfers[key] = gtfs.TransferVal{
		Transfer_type:     2, // requires minimum transfer time
		Min_transfer_time: seconds,
	}
}
