// Package stops provides a stop-basis abstraction that the rest of the
// pipeline (prepare, transfers, DIMACS export) works against, independent
// of where the stops came from. Two sources currently implement it:
//
//   - gtfs.go — top-level stops (+ existing transfers.txt) from a parsed
//     GTFS feed
//   - csv.go  — stops from a plain CSV file (no existing-transfer data)
//
// Only one is ever used per run; see cmd/gtfs-transfers/main.go.
package stops

// Stop is one stop/station, independent of its origin format.
type Stop struct {
	ID   string
	Name string
	Lat  float64
	Lon  float64

	// MinChangeTime is an optional, source-provided minimum change/
	// transfer time in seconds for this stop (GTFS has no direct
	// equivalent per-stop; a stops.csv may supply one via a
	// MinChangeTime column). It is not currently applied by the
	// pipeline — it's carried through for callers/extensions that want
	// it (e.g. a future per-stop floor on generated transfer times).
	MinChangeTime int
}

// ExistingTransfer is a pre-existing (from, to) transfer time, in seconds,
// that should be retained as a graph edge rather than recomputed from OSM
// geometry. Only a GTFS source currently populates this (from
// transfers.txt); a CSV stop basis has no equivalent file, so its Existing
// slice is always empty.
type ExistingTransfer struct {
	FromID  string
	ToID    string
	Seconds int
}

// Source is a complete stop basis for one run: every top-level stop plus
// any existing transfer times to retain.
type Source struct {
	Stops    []*Stop
	ByID     map[string]*Stop
	Existing []ExistingTransfer
}

// Get looks up a stop by ID.
func (s *Source) Get(id string) (*Stop, bool) {
	st, ok := s.ByID[id]
	return st, ok
}

func newSource(list []*Stop) *Source {
	byID := make(map[string]*Stop, len(list))
	for _, s := range list {
		byID[s.ID] = s
	}
	return &Source{Stops: list, ByID: byID}
}
