package stops

import (
	gtfsparser "github.com/patrickbr/gtfsparser"
	gtfs "github.com/patrickbr/gtfsparser/gtfs"
)

// FromGTFS builds a Source from a parsed GTFS feed: every top-level stop
// (no parent_station) with valid coordinates, plus any existing
// transfer_type=2 entries between two such stops (read from the feed's
// transfers.txt, if present) as ExistingTransfer entries to retain as
// graph edges.
func FromGTFS(feed *gtfsparser.Feed) *Source {
	var list []*Stop
	for _, s := range feed.Stops {
		if s.Parent_station != nil || !s.HasLatLon() {
			continue
		}
		list = append(list, &Stop{
			ID:   s.Id,
			Name: s.Name,
			Lat:  float64(s.Lat),
			Lon:  float64(s.Lon),
		})
	}

	src := newSource(list)

	for key, val := range feed.Transfers {
		if val.Transfer_type != 2 || key.From_stop == nil || key.To_stop == nil {
			continue
		}
		if _, ok := src.ByID[key.From_stop.Id]; !ok {
			continue
		}
		if _, ok := src.ByID[key.To_stop.Id]; !ok {
			continue
		}
		src.Existing = append(src.Existing, ExistingTransfer{
			FromID:  key.From_stop.Id,
			ToID:    key.To_stop.Id,
			Seconds: val.Min_transfer_time,
		})
	}

	return src
}

// StopByID indexes a feed's stops (including child stops) by ID — used
// where the caller needs the full *gtfs.Stop, not just the generic Stop
// (e.g. writing transfers.txt back into the feed).
func StopByID(feed *gtfsparser.Feed) map[string]*gtfs.Stop {
	m := make(map[string]*gtfs.Stop, len(feed.Stops))
	for _, s := range feed.Stops {
		m[s.Id] = s
	}
	return m
}
