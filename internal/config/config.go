// Package config holds all tunable parameters for transfer generation.
package config

// WalkConfig controls how pedestrian travel times are computed during
// the Dijkstra search on the OSM street graph.
type WalkConfig struct {
	// MaxWalkingTime is the Dijkstra cutoff in seconds. Any station
	// reachable within this budget is added as a transfer.
	MaxWalkingTime float64

	// FlatSpeed is the average walking speed on level footways, in m/s.
	// Default: 1.39 m/s  (~5 km/h)
	FlatSpeed float64

	// StairSpeedUp is the effective vertical-progress speed (m/s of
	// horizontal path) when ascending stairs.
	// Default: 0.5 m/s
	StairSpeedUp float64

	// StairSpeedDown is the effective speed when descending stairs.
	// Default: 0.7 m/s
	StairSpeedDown float64

	// WheelchairAccessible, when true, excludes OSM edges that are
	// tagged with highway=steps or wheelchair=no/limited.
	WheelchairAccessible bool

	// TransferPenalty is a fixed number of seconds added to every
	// generated transfer to account for e.g. station entry/exit time.
	TransferPenalty float64
}

// Default returns a WalkConfig with sensible defaults.
func Default() WalkConfig {
	return WalkConfig{
		MaxWalkingTime:       300, // 5 minutes
		FlatSpeed:            1.39,
		StairSpeedUp:         0.50,
		StairSpeedDown:       0.70,
		WheelchairAccessible: false,
		TransferPenalty:      0,
	}
}
