package transfers

import (
	"encoding/csv"
	"io"
	"sort"
	"strconv"
)

// WriteTransfersCSV writes computed entries as a plain CSV with columns
// from_stop_id,to_stop_id,min_transfer_time, sorted by (from, to). This is
// the transfers.txt equivalent for a plain stops.csv basis, which has no
// GTFS feed to write a transfers.txt into.
func WriteTransfersCSV(w io.Writer, entries []Entry) error {
	sorted := make([]Entry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].FromID != sorted[j].FromID {
			return sorted[i].FromID < sorted[j].FromID
		}
		return sorted[i].ToID < sorted[j].ToID
	})

	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"from_stop_id", "to_stop_id", "min_transfer_time"}); err != nil {
		return err
	}
	for _, e := range sorted {
		row := []string{e.FromID, e.ToID, strconv.Itoa(e.Seconds)}
		if err := cw.Write(row); err != nil {
			return err
		}
	}

	cw.Flush()
	return cw.Error()
}
