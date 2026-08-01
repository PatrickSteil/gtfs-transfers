package osm

import (
	"strconv"
	"strings"

	"github.com/PatrickSteil/gtfs-transfers/internal/config"
)

func isRoutable(tags map[string]string) bool {
	hw := tags["highway"]
	if hw == "" {
		return false
	}
	switch hw {
	case "proposed", "construction", "razed", "abandoned", "planned",
		"platform", "elevator", "bus_stop", "rest_area", "services":
		return false
	}
	switch tags["access"] {
	case "no", "private":
		return false
	}
	return true
}

func isOneway(tags map[string]string) (oneway, reversed bool) {
	switch tags["oneway"] {
	case "yes", "1", "true":
		return true, false
	case "-1", "reverse":
		return true, true
	}
	return false, false
}

func parseSpeedLimitKmH(tags map[string]string) float64 {
	raw, ok := tags["maxspeed"]
	if !ok {
		return config.DefaultMaxSpeedKmH
	}
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "", "none", "unlimited", "signals", "variable":
		return config.DefaultMaxSpeedKmH
	case "walk":
		return 6.0
	}

	if strings.HasSuffix(v, "mph") {
		n, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(v, "mph")), 64)
		if err != nil || n <= 0 {
			return config.DefaultMaxSpeedKmH
		}
		return n * 1.60934
	}

	v = strings.TrimSuffix(v, "km/h")
	v = strings.TrimSuffix(v, "kmh")
	v = strings.TrimSpace(v)
	n, err := strconv.ParseFloat(v, 64)
	if err != nil || n <= 0 {
		return config.DefaultMaxSpeedKmH
	}
	return n
}
