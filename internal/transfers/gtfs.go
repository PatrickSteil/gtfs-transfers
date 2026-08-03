package transfers

import (
	gtfsparser "github.com/patrickbr/gtfsparser"
	gtfs "github.com/patrickbr/gtfsparser/gtfs"
)

// ApplyToGTFS upserts computed entries into feed.Transfers as
// transfer_type=2 (minimum transfer time). Idempotent: a pre-existing
// entry between the same stop pair is only overwritten if the computed
// time is strictly shorter. Returns the number of entries written or
// updated.
func ApplyToGTFS(feed *gtfsparser.Feed, entries []Entry) int {
	stopByID := make(map[string]*gtfs.Stop, len(feed.Stops))
	for _, s := range feed.Stops {
		stopByID[s.Id] = s
	}

	var n int
	for _, e := range entries {
		from, to := stopByID[e.FromID], stopByID[e.ToID]
		if from == nil || to == nil {
			continue
		}
		key := gtfs.TransferKey{From_stop: from, To_stop: to}
		if existing, ok := feed.Transfers[key]; ok && existing.Min_transfer_time <= e.Seconds {
			continue // keep the shorter existing value
		}
		feed.Transfers[key] = gtfs.TransferVal{
			Transfer_type:     2,
			Min_transfer_time: e.Seconds,
		}
		n++
	}
	return n
}
