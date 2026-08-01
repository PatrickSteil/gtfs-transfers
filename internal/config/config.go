package config

import "math"

type Mode struct {
	Name     string
	SpeedKmH float64
}

var (
	ModeWalking = Mode{Name: "walking", SpeedKmH: 4.5}
	ModeBike    = Mode{Name: "bike", SpeedKmH: 15.0}
)

const DefaultMaxSpeedKmH = 140.0

func KmHToMS(kmh float64) float64 { return kmh / 3.6 }

type PrepareConfig struct {
	IdentifyDistM float64

	ConnectDistM float64

	BBox *BoundingBox

	BBoxPadM float64
}

func DefaultPrepareConfig() PrepareConfig {
	return PrepareConfig{
		IdentifyDistM: 5.0,
		ConnectDistM:  100.0,
		BBoxPadM:      2000.0,
	}
}

type BoundingBox struct {
	MinLat, MinLon, MaxLat, MaxLon float64
}

func (b *BoundingBox) Contains(lat, lon float64) bool {
	if b == nil {
		return true
	}
	return lat >= b.MinLat && lat <= b.MaxLat && lon >= b.MinLon && lon <= b.MaxLon
}

func (b *BoundingBox) PadMetres(padM float64) *BoundingBox {
	if b == nil || padM <= 0 {
		return b
	}
	const earthRadius = 6_371_000.0
	dLat := padM / earthRadius * (180 / math.Pi)
	cosLat := math.Cos((b.MinLat + b.MaxLat) / 2 * math.Pi / 180)
	if cosLat < 1e-6 {
		cosLat = 1e-6
	}
	dLon := padM / (earthRadius * cosLat) * (180 / math.Pi)
	return &BoundingBox{
		MinLat: b.MinLat - dLat,
		MaxLat: b.MaxLat + dLat,
		MinLon: b.MinLon - dLon,
		MaxLon: b.MaxLon + dLon,
	}
}
