package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/PatrickSteil/gtfs-transfers/internal/config"
	"github.com/PatrickSteil/gtfs-transfers/internal/osm"
	"github.com/PatrickSteil/gtfs-transfers/internal/transfers"
	gtfsparser "github.com/patrickbr/gtfsparser"
	gtfswriter "github.com/patrickbr/gtfswriter"
)

func main() {
	cfg := config.Default()

	flag.Float64Var(&cfg.MaxWalkingTime, "max-walk", cfg.MaxWalkingTime,
		"maximum walking time budget in seconds")
	flag.Float64Var(&cfg.FlatSpeed, "flat-speed", cfg.FlatSpeed,
		"average walking speed on flat footways (m/s)")
	flag.Float64Var(&cfg.StairSpeedUp, "stair-up", cfg.StairSpeedUp,
		"effective walking speed ascending stairs (m/s)")
	flag.Float64Var(&cfg.StairSpeedDown, "stair-down", cfg.StairSpeedDown,
		"effective walking speed descending stairs (m/s)")
	flag.BoolVar(&cfg.WheelchairAccessible, "wheelchair", cfg.WheelchairAccessible,
		"exclude stairs and wheelchair=no/limited paths")
	flag.Float64Var(&cfg.TransferPenalty, "penalty", cfg.TransferPenalty,
		"fixed penalty added to every transfer (seconds)")

	var dimacsOut string
	var dimacsModes string
	var bikeSpeed float64
	flag.StringVar(&dimacsOut, "dimacs-out", "",
		"if set, export DIMACS graph(s) to this directory plus a stations.csv "+
			"stop-to-node mapping")
	flag.StringVar(&dimacsModes, "dimacs-modes", "foot,bike",
		"comma-separated modes to export when -dimacs-out is set: foot, bike, or foot,bike")
	flag.Float64Var(&bikeSpeed, "bike-speed", config.DefaultBikeSpeedMPS,
		"constant bicycle speed used for the bike DIMACS export (m/s)")

	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	if len(args) != 3 {
		usage()
		os.Exit(1)
	}
	gtfsIn := args[0]
	osmIn := args[1]
	gtfsOut := args[2]

	var exportModes []osm.Mode
	if dimacsOut != "" {
		var err error
		exportModes, err = parseModes(dimacsModes)
		if err != nil {
			fatalf("%v", err)
		}
	}

	fmt.Println("═══════════════════════════════════════════")
	fmt.Println("gtfs-transfers  –  pedestrian transfers")
	fmt.Println("═══════════════════════════════════════════")
	fmt.Printf("GTFS input  : %s\n", gtfsIn)
	fmt.Printf("OSM input   : %s\n", osmIn)
	fmt.Printf("GTFS output : %s\n", gtfsOut)
	fmt.Printf("Max walk    : %.0f s (%.1f min)\n", cfg.MaxWalkingTime, cfg.MaxWalkingTime/60)
	fmt.Printf("Flat speed  : %.2f m/s\n", cfg.FlatSpeed)
	fmt.Printf("Stair ↑/↓   : %.2f / %.2f m/s\n", cfg.StairSpeedUp, cfg.StairSpeedDown)
	fmt.Printf("Wheelchair  : %v\n", cfg.WheelchairAccessible)
	fmt.Printf("Penalty     : %.0f s\n", cfg.TransferPenalty)
	if dimacsOut != "" {
		fmt.Printf("DIMACS out  : %s (modes: %s)\n", dimacsOut, dimacsModes)
	}
	fmt.Println("───────────────────────────────────────────")

	t0 := time.Now()
	fmt.Println("[1/5] Parsing GTFS feed …")
	feed := gtfsparser.NewFeed()
	opts := gtfsparser.ParseOptions{
		UseDefValueOnError: true,
		DropErroneous:      false,
	}
	feed.SetParseOpts(opts)
	if err := feed.Parse(gtfsIn); err != nil {
		fatalf("GTFS parse error: %v", err)
	}
	fmt.Printf("Stops: %d  Transfers (existing): %d  (%.1fs)\n",
		len(feed.Stops), len(feed.Transfers), time.Since(t0).Seconds())

	// Always parse the foot graph (needed for GTFS transfer generation).
	// If a bike DIMACS export was requested, parse it in the same pass —
	// ParseMulti decodes the OSM XML exactly once and builds both graphs
	// from that single decode, rather than re-reading/re-parsing the file.
	t1 := time.Now()
	fmt.Println("[2/5] Parsing OSM graph(s) …")
	parseModesList := []osm.Mode{osm.ModeFoot}
	for _, m := range exportModes {
		if m != osm.ModeFoot {
			parseModesList = append(parseModesList, m)
		}
	}
	graphs, err := osm.ParseMulti(osmIn, parseModesList, cfg.WheelchairAccessible)
	if err != nil {
		fatalf("OSM parse error: %v", err)
	}
	graph := graphs[osm.ModeFoot]
	for _, m := range parseModesList {
		g := graphs[m]
		fmt.Printf("  %-5s  nodes: %d  edge lists: %d\n", m, len(g.Nodes), len(g.Edges))
	}
	fmt.Printf("(%.1fs)\n", time.Since(t1).Seconds())

	t2 := time.Now()
	fmt.Println("[3/5] Snapping stops to OSM nodes …")
	snapped := transfers.SnapStops(feed, graph)
	fmt.Printf("Snapped %d of %d stops to OSM nodes  (%.1fs)\n",
		len(snapped), len(feed.Stops), time.Since(t2).Seconds())

	t3 := time.Now()
	fmt.Println("[4/5] Generating transfers …")
	transfers.GenerateTransfers(feed, graph, cfg, snapped)
	fmt.Printf("Total transfers in feed: %d  (%.1fs)\n",
		len(feed.Transfers), time.Since(t3).Seconds())

	if dimacsOut != "" {
		if err := exportDIMACS(dimacsOut, graphs, exportModes, cfg, bikeSpeed, feed, snapped); err != nil {
			fatalf("DIMACS export error: %v", err)
		}
	}

	t4 := time.Now()
	fmt.Println("\n[5/5] Writing GTFS feed …")
	writer := gtfswriter.Writer{
		Sorted:           true,
		ExplicitCalendar: false,
	}
	err = writer.Write(feed, gtfsOut)
	if err != nil {
		fatalf("GTFS write error: %v", err)
	}
	fmt.Printf("Written to %s  (%.1fs)\n", gtfsOut, time.Since(t4).Seconds())

	fmt.Printf("Done in %.2fs total\n", time.Since(t0).Seconds())
}

// parseModes parses a comma-separated list like "foot,bike" into []osm.Mode,
// deduplicated and order-preserving.
func parseModes(s string) ([]osm.Mode, error) {
	seen := make(map[osm.Mode]bool)
	var modes []osm.Mode
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if part == "" {
			continue
		}
		var m osm.Mode
		switch part {
		case "foot", "pedestrian", "walk":
			m = osm.ModeFoot
		case "bike", "bicycle", "cycling":
			m = osm.ModeBike
		default:
			return nil, fmt.Errorf("unknown -dimacs-modes entry %q (expected foot or bike)", part)
		}
		if !seen[m] {
			seen[m] = true
			modes = append(modes, m)
		}
	}
	if len(modes) == 0 {
		return nil, fmt.Errorf("-dimacs-modes must name at least one of: foot, bike")
	}
	return modes, nil
}

// exportDIMACS writes one DIMACS ".gr"/".co" pair per requested mode, plus
// one combined stations.csv mapping GTFS stop IDs to each mode's OSM/DIMACS
// node IDs, all into dir (created if it doesn't exist).
//
// footSnapped is the stop-snapping result already computed for the foot
// graph in the main pipeline; it's reused here instead of re-snapping, so
// only genuinely extra modes (e.g. bike) pay the snapping cost again.
func exportDIMACS(
	dir string,
	graphs map[osm.Mode]*osm.Graph,
	modes []osm.Mode,
	cfg config.WalkConfig,
	bikeSpeed float64,
	feed *gtfsparser.Feed,
	footSnapped []transfers.StopNode,
) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	// Stable column order regardless of how -dimacs-modes was written.
	sorted := append([]osm.Mode(nil), modes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var modeSnaps []transfers.ModeSnap
	for _, mode := range sorted {
		g, ok := graphs[mode]
		if !ok {
			return fmt.Errorf("internal error: no graph parsed for mode %s", mode)
		}
		index := g.NodeIndex()

		var speed osm.EdgeSpeedFunc
		if mode == osm.ModeBike {
			speed = osm.ConstantSpeedFunc(bikeSpeed)
		} else {
			speed = osm.FootSpeedFunc(cfg)
		}

		grPath := filepath.Join(dir, "graph_"+mode.String()+".gr")
		grFile, err := os.Create(grPath)
		if err != nil {
			return fmt.Errorf("create %s: %w", grPath, err)
		}
		if err := g.WriteDIMACSGraph(grFile, index, speed); err != nil {
			grFile.Close()
			return fmt.Errorf("write %s: %w", grPath, err)
		}
		if err := grFile.Close(); err != nil {
			return fmt.Errorf("close %s: %w", grPath, err)
		}

		coPath := filepath.Join(dir, "graph_"+mode.String()+".co")
		coFile, err := os.Create(coPath)
		if err != nil {
			return fmt.Errorf("create %s: %w", coPath, err)
		}
		if err := g.WriteDIMACSCoords(coFile, index); err != nil {
			coFile.Close()
			return fmt.Errorf("write %s: %w", coPath, err)
		}
		if err := coFile.Close(); err != nil {
			return fmt.Errorf("close %s: %w", coPath, err)
		}

		var snapped []transfers.StopNode
		if mode == osm.ModeFoot {
			snapped = footSnapped // reuse — already computed in the main pipeline
		} else {
			snapped = transfers.SnapStops(feed, g)
		}

		fmt.Printf("  %-5s  %s, %s (%d nodes, %d stops snapped)\n",
			mode, grPath, coPath, len(index), len(snapped))

		modeSnaps = append(modeSnaps, transfers.ModeSnap{
			Mode:    mode.String(),
			Snapped: snapped,
			Index:   index,
		})
	}

	stationsPath := filepath.Join(dir, "stations.csv")
	stationsFile, err := os.Create(stationsPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", stationsPath, err)
	}
	if err := transfers.WriteStationMapping(stationsFile, modeSnaps); err != nil {
		stationsFile.Close()
		return fmt.Errorf("write %s: %w", stationsPath, err)
	}
	if err := stationsFile.Close(); err != nil {
		return fmt.Errorf("close %s: %w", stationsPath, err)
	}

	fmt.Printf("  %s\n", stationsPath)
	return nil
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

func usage() {
	fmt.Fprintf(os.Stderr, `gtfs-transfers – generate pedestrian transfer entries for a GTFS feed

Usage:
  gtfs-transfers [flags] <gtfs-input> <osm-input.osm> <gtfs-output>

Arguments:
  gtfs-input    Path to GTFS ZIP file or directory (input)
  osm-input     Path to OSM XML file (.osm) covering the same area
  gtfs-output   Path to write the enriched GTFS ZIP or directory

Flags:
`)
	flag.PrintDefaults()
	fmt.Fprintf(os.Stderr, `
Examples:
  # Basic run with defaults (5-min walking budget)
  gtfs-transfers feed.zip city.osm output/

  # Wheelchair-accessible transfers only, 3-minute budget
  gtfs-transfers -wheelchair -max-walk 180 feed.zip city.osm output/

  # Slower walkers (elderly), with 60 s station entry penalty
  gtfs-transfers -flat-speed 0.9 -penalty 60 feed.zip city.osm output/

  # Also export foot AND bike DIMACS graphs (graph_foot.gr/.co,
  # graph_bike.gr/.co) plus one combined stations.csv, into dimacs/
  gtfs-transfers -dimacs-out dimacs/ feed.zip city.osm output/

  # Only the bike graph, with a faster assumed cycling speed
  gtfs-transfers -dimacs-out dimacs/ -dimacs-modes bike -bike-speed 5.5 \
    feed.zip city.osm output/
`)
}
