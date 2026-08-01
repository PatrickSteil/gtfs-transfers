package osm

import (
	"math"
	"sort"
)

type IndexPoint struct {
	ID       int64
	Lat, Lon float64
}

type PointIndex struct {
	root      *kdNode
	refCosLat float64
}

type kdPoint struct {
	id   int64
	lat  float64
	lon  float64
	x, y float64 // local equirectangular projection, metres
}

type kdNode struct {
	pt          kdPoint
	left, right *kdNode
}

func degToRad(d float64) float64 { return d * math.Pi / 180 }

func NewPointIndex(pts []IndexPoint) *PointIndex {
	if len(pts) == 0 {
		return nil
	}

	var sumLat float64
	for _, p := range pts {
		sumLat += p.Lat
	}
	meanLat := sumLat / float64(len(pts))
	refCosLat := math.Cos(degToRad(meanLat))
	if refCosLat < 1e-6 {
		refCosLat = 1e-6 // guard against point sets sitting exactly on a pole
	}

	project := func(lat, lon float64) (x, y float64) {
		x = degToRad(lon) * earthRadius * refCosLat
		y = degToRad(lat) * earthRadius
		return x, y
	}

	kdPts := make([]kdPoint, len(pts))
	for i, p := range pts {
		x, y := project(p.Lat, p.Lon)
		kdPts[i] = kdPoint{id: p.ID, lat: p.Lat, lon: p.Lon, x: x, y: y}
	}

	return &PointIndex{
		root:      buildKDTree(kdPts, 0),
		refCosLat: refCosLat,
	}
}

func buildKDTree(pts []kdPoint, depth int) *kdNode {
	if len(pts) == 0 {
		return nil
	}
	axis := depth % 2
	sort.Slice(pts, func(i, j int) bool {
		if axis == 0 {
			return pts[i].x < pts[j].x
		}
		return pts[i].y < pts[j].y
	})
	mid := len(pts) / 2
	return &kdNode{
		pt:    pts[mid],
		left:  buildKDTree(pts[:mid], depth+1),
		right: buildKDTree(pts[mid+1:], depth+1),
	}
}

func (p *PointIndex) project(lat, lon float64) (x, y float64) {
	x = degToRad(lon) * earthRadius * p.refCosLat
	y = degToRad(lat) * earthRadius
	return x, y
}

func (p *PointIndex) Nearest(lat, lon, maxDistMetres float64) (id int64, distMetres float64, ok bool) {
	if p == nil || p.root == nil {
		return 0, 0, false
	}
	x, y := p.project(lat, lon)
	var best *kdPoint
	bestD2 := maxDistMetres * maxDistMetres
	nearestSearch(p.root, x, y, 0, &best, &bestD2)
	if best == nil {
		return 0, 0, false
	}
	d := HaversineMetres(lat, lon, best.lat, best.lon)
	if d > maxDistMetres {
		return 0, 0, false
	}
	return best.id, d, true
}

func nearestSearch(node *kdNode, x, y float64, depth int, best **kdPoint, bestD2 *float64) {
	if node == nil {
		return
	}
	dx := node.pt.x - x
	dy := node.pt.y - y
	d2 := dx*dx + dy*dy
	if d2 < *bestD2 {
		*bestD2 = d2
		*best = &node.pt
	}

	axis := depth % 2
	var diff float64
	if axis == 0 {
		diff = x - node.pt.x
	} else {
		diff = y - node.pt.y
	}

	first, second := node.left, node.right
	if diff > 0 {
		first, second = node.right, node.left
	}
	nearestSearch(first, x, y, depth+1, best, bestD2)
	if diff*diff < *bestD2 {
		nearestSearch(second, x, y, depth+1, best, bestD2)
	}
}
