package stops

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// csvColumns maps the recognized header names (matched case-insensitively,
// whitespace-trimmed) to their meaning. Only id/lat/lon are required; name
// and min-change-time are optional. This accepts both the GTFS-style
// StopId,StopName,Latitude,Longitude,MinChangeTime header and common
// snake_case variants.
var (
	idNames   = []string{"stopid", "stop_id", "id"}
	nameNames = []string{"stopname", "stop_name", "name"}
	latNames  = []string{"latitude", "lat"}
	lonNames  = []string{"longitude", "lon", "lng"}
	mctNames  = []string{"minchangetime", "min_change_time", "min_transfer_time"}
)

// FromCSV builds a Source from a plain stops CSV file, e.g.:
//
//	StopId,StopName,Latitude,Longitude,MinChangeTime
//	0,"Bad Herrenalb Bahnhof",48.8023,8.4391,0
//
// The header is matched case-insensitively; StopId/Latitude/Longitude (or
// their snake_case equivalents) are required, StopName and MinChangeTime
// are optional. Rows with an empty ID or unparsable coordinates are
// skipped with a returned error naming the offending row. There is no
// existing-transfers equivalent for a CSV basis, so Source.Existing is
// always empty.
func FromCSV(path string) (*Source, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open stops csv %s: %w", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true

	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read stops csv header %s: %w", path, err)
	}

	idCol := findColumn(header, idNames)
	nameCol := findColumn(header, nameNames)
	latCol := findColumn(header, latNames)
	lonCol := findColumn(header, lonNames)
	mctCol := findColumn(header, mctNames)

	if idCol < 0 || latCol < 0 || lonCol < 0 {
		return nil, fmt.Errorf(
			"stops csv %s: header must include a stop id, latitude and longitude column "+
				"(e.g. StopId,StopName,Latitude,Longitude); got %v", path, header)
	}

	var list []*Stop
	rowNum := 1 // header was row 1
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read stops csv %s, row %d: %w", path, rowNum+1, err)
		}
		rowNum++

		id := field(rec, idCol)
		if id == "" {
			continue
		}
		lat, err := strconv.ParseFloat(field(rec, latCol), 64)
		if err != nil {
			return nil, fmt.Errorf("stops csv %s, row %d (stop %q): invalid latitude: %w", path, rowNum, id, err)
		}
		lon, err := strconv.ParseFloat(field(rec, lonCol), 64)
		if err != nil {
			return nil, fmt.Errorf("stops csv %s, row %d (stop %q): invalid longitude: %w", path, rowNum, id, err)
		}

		mct := 0
		if v := field(rec, mctCol); v != "" {
			if parsed, err := strconv.Atoi(v); err == nil {
				mct = parsed
			}
		}

		list = append(list, &Stop{
			ID:            id,
			Name:          field(rec, nameCol),
			Lat:           lat,
			Lon:           lon,
			MinChangeTime: mct,
		})
	}

	if len(list) == 0 {
		return nil, fmt.Errorf("stops csv %s: no usable stop rows found", path)
	}

	return newSource(list), nil
}

func findColumn(header []string, names []string) int {
	for i, h := range header {
		hn := strings.ToLower(strings.TrimSpace(h))
		for _, n := range names {
			if hn == n {
				return i
			}
		}
	}
	return -1
}

// field safely reads rec[col], trimmed, returning "" if col is -1 or out
// of range (short/ragged rows).
func field(rec []string, col int) string {
	if col < 0 || col >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[col])
}
