package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/PatrickSteil/gtfs-transfers/internal/config"
	"github.com/PatrickSteil/gtfs-transfers/internal/osm"
	"github.com/PatrickSteil/gtfs-transfers/internal/prepare"
	"github.com/PatrickSteil/gtfs-transfers/internal/stops"
	"github.com/PatrickSteil/gtfs-transfers/internal/transfers"
	gtfsparser "github.com/patrickbr/gtfsparser"
	gtfswriter "github.com/patrickbr/gtfswriter"
)

func main() {
	pcfg := config.DefaultPrepareConfig()

	var (
		gtfsIn       string
		stopsCSV     string
		osmIn        string
		gtfsOut      string
		transfersOut string

		modesFlag   string
		maxWalk     float64
		bboxFlag    string
		dimacsOut   string
		dimacsScale float64
		noTransfers bool
	)

	flag.StringVar(&gtfsIn, "gtfs-in", "",
		"GTFS ZIP file or directory to use as the stop basis (mutually exclusive with -stops-csv)")
	flag.StringVar(&stopsCSV, "stops-csv", "",
		"plain stops CSV to use as the stop basis, e.g. \"StopId,StopName,Latitude,Longitude,MinChangeTime\" "+
			"(mutually exclusive with -gtfs-in)")
	flag.StringVar(&osmIn, "osm", "",
		"OSM data covering the same geographic area — .osm/.xml (XML) or .osm.pbf (binary), auto-detected from the extension (required)")
	flag.StringVar(&gtfsOut, "gtfs-out", "",
		"output path (ZIP file or existing directory) to write the GTFS feed with generated transfers.txt entries; "+
			"required together with -gtfs-in unless -no-transfers is set")
	flag.StringVar(&transfersOut, "transfers-out", "",
		"path to write computed transfers as a plain CSV (from_stop_id,to_stop_id,min_transfer_time); "+
			"required together with -stops-csv unless -no-transfers is set (there is no transfers.txt to write "+
			"into without a GTFS feed), optional otherwise")

	flag.Float64Var(&pcfg.IdentifyDistM, "identify-dist", pcfg.IdentifyDistM,
		"max distance (m) at which a stop is identified with an existing transfer-graph vertex")
	flag.Float64Var(&pcfg.ConnectDistM, "connect-dist", pcfg.ConnectDistM,
		"max distance (m) at which a stop is connected to the transfer graph with new edges")
	flag.Float64Var(&pcfg.BBoxPadM, "bbox-pad", pcfg.BBoxPadM,
		"padding (m) applied around the stops' extent for the automatic bounding box")
	flag.StringVar(&bboxFlag, "bbox", "",
		"explicit bounding box \"minLat,minLon,maxLat,maxLon\"; if unset, derived automatically from the stops")

	flag.StringVar(&modesFlag, "modes", "walking:4.5,bike:15",
		"comma-separated transfer modes as name:speed_kmh (paper defaults: walking=4.5, bike/e-scooter=15)")
	flag.Float64Var(&maxWalk, "max-walk", 300,
		"time budget in seconds for the transfers generated for the first listed mode")
	flag.BoolVar(&noTransfers, "no-transfers", false,
		"skip generating transfer entries (useful if you only want the -dimacs-out export)")

	flag.StringVar(&dimacsOut, "dimacs-out", "",
		"if set, export the prepared graph in DIMACS format to this directory: one shared graph_coords.co "+
			"file plus one graph_<mode>.gr file per mode in -modes, and a stations.csv stop mapping")
	flag.Float64Var(&dimacsScale, "dimacs-scale", 100,
		"seconds -> integer-weight scale for the DIMACS export (100 = centiseconds)")

	flag.Usage = usage
	flag.Parse()

	if len(flag.Args()) != 0 {
		fatalf("unexpected extra arguments: %v (all inputs/outputs are now flags — see -h)", flag.Args())
	}

	if err := validateFlags(gtfsIn, stopsCSV, osmIn, gtfsOut, transfersOut, dimacsOut, noTransfers); err != nil {
		usage()
		fatalf("%v", err)
	}

	modes, err := parseModes(modesFlag)
	if err != nil {
		fatalf("%v", err)
	}
	if bboxFlag != "" {
		bb, err := parseBBox(bboxFlag)
		if err != nil {
			fatalf("%v", err)
		}
		pcfg.BBox = bb
	}

	usingGTFS := gtfsIn != ""

	fmt.Println("═══════════════════════════════════════════")
	fmt.Println("gtfs-transfers  –  GTFS/CSV + OSM transfer graph preparation")
	fmt.Println("═══════════════════════════════════════════")
	if usingGTFS {
		fmt.Printf("Stop basis     : GTFS  (%s)\n", gtfsIn)
	} else {
		fmt.Printf("Stop basis     : CSV   (%s)\n", stopsCSV)
	}
	fmt.Printf("OSM input      : %s\n", osmIn)
	if gtfsOut != "" {
		fmt.Printf("GTFS output    : %s\n", gtfsOut)
	}
	if transfersOut != "" {
		fmt.Printf("Transfers CSV  : %s\n", transfersOut)
	}
	fmt.Printf("Modes          : %s\n", describeModes(modes))
	fmt.Printf("Identify dist  : %.0f m\n", pcfg.IdentifyDistM)
	fmt.Printf("Connect dist   : %.0f m\n", pcfg.ConnectDistM)
	if pcfg.BBox != nil {
		fmt.Printf("Bounding box   : %.5f,%.5f,%.5f,%.5f (explicit)\n", pcfg.BBox.MinLat, pcfg.BBox.MinLon, pcfg.BBox.MaxLat, pcfg.BBox.MaxLon)
	} else {
		fmt.Printf("Bounding box   : auto (stop extent + %.0f m padding)\n", pcfg.BBoxPadM)
	}
	if !noTransfers {
		fmt.Printf("Max walk       : %.0f s (mode: %s)\n", maxWalk, modes[0].Name)
	}
	if dimacsOut != "" {
		fmt.Printf("DIMACS out     : %s\n", dimacsOut)
	}
	fmt.Println("───────────────────────────────────────────")

	t0 := time.Now()

	var feed *gtfsparser.Feed
	var src *stops.Source

	if usingGTFS {
		fmt.Println("[1/5] Parsing GTFS feed …")
		feed = gtfsparser.NewFeed()
		opts := gtfsparser.ParseOptions{
			UseDefValueOnError: true,
			DropErroneous:      false,
		}
		feed.SetParseOpts(opts)
		if err := feed.Parse(gtfsIn); err != nil {
			fatalf("GTFS parse error: %v", err)
		}
		src = stops.FromGTFS(feed)
		fmt.Printf("Stops: %d  Existing transfers retained: %d  (%.1fs)\n",
			len(src.Stops), len(src.Existing), time.Since(t0).Seconds())
	} else {
		fmt.Println("[1/5] Reading stops CSV …")
		var err error
		src, err = stops.FromCSV(stopsCSV)
		if err != nil {
			fatalf("stops csv error: %v", err)
		}
		fmt.Printf("Stops: %d  (%.1fs)\n", len(src.Stops), time.Since(t0).Seconds())
	}

	t1 := time.Now()
	fmt.Println("[2/5] Parsing OSM transfer graph …")
	graph, err := osm.Parse(osmIn)
	if err != nil {
		fatalf("OSM parse error: %v", err)
	}
	fmt.Printf("  %d nodes, %d edge lists  (%.1fs)\n", len(graph.Nodes), len(graph.Edges), time.Since(t1).Seconds())

	t2 := time.Now()
	fmt.Println("[3/5] Preparing combined transfer graph …")
	fmt.Println("        connecting stops → contracting degree-1/2 vertices → bbox + largest component")
	result := prepare.Prepare(src, graph, pcfg)
	fmt.Printf("  %s  (%.1fs)\n", result.Summary(), time.Since(t2).Seconds())

	if !noTransfers {
		t3 := time.Now()
		fmt.Println("[4/5] Computing transfers …")
		entries := transfers.ComputeTransfers(result.Graph, modes[0], maxWalk, result.StopNode)

		if feed != nil {
			n := transfers.ApplyToGTFS(feed, entries)
			fmt.Printf("  Upserted %d transfers into the feed (now %d total)  (%.1fs)\n",
				n, len(feed.Transfers), time.Since(t3).Seconds())
		}
		if transfersOut != "" {
			if err := writeTransfersCSV(transfersOut, entries); err != nil {
				fatalf("transfers csv write error: %v", err)
			}
			fmt.Printf("  %s\n", transfersOut)
		}
	} else {
		fmt.Println("[4/5] Skipping transfer generation (-no-transfers)")
	}

	if dimacsOut != "" {
		if err := exportDIMACS(dimacsOut, result, modes, dimacsScale, src); err != nil {
			fatalf("DIMACS export error: %v", err)
		}
	}

	if feed != nil && gtfsOut != "" {
		t4 := time.Now()
		fmt.Println("\n[5/5] Writing GTFS feed …")
		writer := gtfswriter.Writer{
			Sorted:           true,
			ExplicitCalendar: false,
		}
		if err := writer.Write(feed, gtfsOut); err != nil {
			fatalf("GTFS write error: %v", err)
		}
		fmt.Printf("Written to %s  (%.1fs)\n", gtfsOut, time.Since(t4).Seconds())
	} else {
		fmt.Println("\n[5/5] No GTFS output requested")
	}

	fmt.Printf("Done in %.2fs total\n", time.Since(t0).Seconds())
}

// validateFlags enforces the mutually-exclusive/required combinations that
// flag.Parse alone can't express: exactly one stop basis, an OSM input,
// and (when transfers are being generated) an output that basis can
// actually be written to.
func validateFlags(gtfsIn, stopsCSV, osmIn, gtfsOut, transfersOut, dimacsOut string, noTransfers bool) error {
	if gtfsIn == "" && stopsCSV == "" {
		return fmt.Errorf("set a stop basis: -gtfs-in <path> or -stops-csv <path>")
	}
	if gtfsIn != "" && stopsCSV != "" {
		return fmt.Errorf("set only one of -gtfs-in or -stops-csv, not both")
	}
	if osmIn == "" {
		return fmt.Errorf("-osm <path> is required")
	}
	if gtfsOut != "" && gtfsIn == "" {
		return fmt.Errorf("-gtfs-out requires -gtfs-in (there's no GTFS feed to write without one)")
	}
	if noTransfers && dimacsOut == "" {
		return fmt.Errorf("nothing to do: -no-transfers is set but -dimacs-out isn't")
	}
	if !noTransfers {
		if gtfsIn != "" && gtfsOut == "" && transfersOut == "" {
			return fmt.Errorf("set -gtfs-out (to write transfers.txt) and/or -transfers-out (to write a CSV), " +
				"or pass -no-transfers")
		}
		if stopsCSV != "" && transfersOut == "" {
			return fmt.Errorf("-stops-csv has no transfers.txt to write into — set -transfers-out, " +
				"or pass -no-transfers")
		}
	}
	return nil
}

func writeTransfersCSV(path string, entries []transfers.Entry) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := transfers.WriteTransfersCSV(f, entries); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	return f.Close()
}

// parseModes parses a comma-separated "name:speed_kmh" list, e.g.
// "walking:4.5,bike:15,car:140". Order is preserved; the first mode is the
// one used for transfer generation.
func parseModes(s string) ([]config.Mode, error) {
	var modes []config.Mode
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		nameSpeed := strings.SplitN(part, ":", 2)
		if len(nameSpeed) != 2 {
			return nil, fmt.Errorf("invalid -modes entry %q (want name:speed_kmh)", part)
		}
		speed, err := strconv.ParseFloat(strings.TrimSpace(nameSpeed[1]), 64)
		if err != nil || speed <= 0 {
			return nil, fmt.Errorf("invalid speed in -modes entry %q", part)
		}
		modes = append(modes, config.Mode{Name: strings.TrimSpace(nameSpeed[0]), SpeedKmH: speed})
	}
	if len(modes) == 0 {
		return nil, fmt.Errorf("-modes must name at least one mode")
	}
	return modes, nil
}

func describeModes(modes []config.Mode) string {
	parts := make([]string, len(modes))
	for i, m := range modes {
		parts[i] = fmt.Sprintf("%s (%.1f km/h)", m.Name, m.SpeedKmH)
	}
	return strings.Join(parts, ", ")
}

// parseBBox parses "minLat,minLon,maxLat,maxLon".
func parseBBox(s string) (*config.BoundingBox, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return nil, fmt.Errorf("invalid -bbox %q (want minLat,minLon,maxLat,maxLon)", s)
	}
	vals := make([]float64, 4)
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid -bbox %q: %w", s, err)
		}
		vals[i] = v
	}
	return &config.BoundingBox{MinLat: vals[0], MinLon: vals[1], MaxLat: vals[2], MaxLon: vals[3]}, nil
}

// exportDIMACS writes one shared coordinates file, one .gr per mode (same
// node numbering throughout — see osm/dimacs.go), and one stations.csv.
func exportDIMACS(dir string, result prepare.Result, modes []config.Mode, scale float64, src *stops.Source) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	index := result.Graph.NodeIndex()

	coPath := filepath.Join(dir, "graph_coords.co")
	coFile, err := os.Create(coPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", coPath, err)
	}
	if err := result.Graph.WriteDIMACSCoords(coFile, index); err != nil {
		coFile.Close()
		return fmt.Errorf("write %s: %w", coPath, err)
	}
	if err := coFile.Close(); err != nil {
		return fmt.Errorf("close %s: %w", coPath, err)
	}
	fmt.Printf("  %s (%d nodes, shared across all modes)\n", coPath, len(index))

	for _, mode := range modes {
		grPath := filepath.Join(dir, "graph_"+sanitizeModeName(mode.Name)+".gr")
		grFile, err := os.Create(grPath)
		if err != nil {
			return fmt.Errorf("create %s: %w", grPath, err)
		}
		if err := result.Graph.WriteDIMACSGraph(grFile, index, mode, scale); err != nil {
			grFile.Close()
			return fmt.Errorf("write %s: %w", grPath, err)
		}
		if err := grFile.Close(); err != nil {
			return fmt.Errorf("close %s: %w", grPath, err)
		}
		fmt.Printf("  %s (mode: %s, %.1f km/h)\n", grPath, mode.Name, mode.SpeedKmH)
	}

	stationsPath := filepath.Join(dir, "stations.csv")
	stationsFile, err := os.Create(stationsPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", stationsPath, err)
	}
	if err := transfers.WriteStationMapping(stationsFile, src, result.StopNode, index); err != nil {
		stationsFile.Close()
		return fmt.Errorf("write %s: %w", stationsPath, err)
	}
	if err := stationsFile.Close(); err != nil {
		return fmt.Errorf("close %s: %w", stationsPath, err)
	}
	fmt.Printf("  %s\n", stationsPath)

	return nil
}

func sanitizeModeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "_")
	return name
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

func usage() {
	fmt.Fprintf(os.Stderr, `gtfs-transfers – prepare a combined GTFS/CSV + OSM transfer graph

Following the procedure described in the paper this tool implements: build
a single OSM transfer graph, connect stops to it geometrically, contract
superfluous degree-1/2 vertices, then crop to a bounding box and its
largest connected component. Every requested transfer mode reuses this
same graph — only the travel speed varies (see -modes).

The stop basis is either a GTFS feed (-gtfs-in) or a plain stops CSV
(-stops-csv) — exactly one of the two. GTFS is required if you want
transfers.txt written back out (-gtfs-out); a CSV basis can only produce a
plain transfers CSV (-transfers-out) and/or a DIMACS graph export
(-dimacs-out).

Usage:
  gtfs-transfers [flags]

Flags:
`)
	flag.PrintDefaults()
	fmt.Fprintf(os.Stderr, `
Examples:
  # Default: walking transfers.txt written into the feed, 5-minute budget
  gtfs-transfers -gtfs-in feed.zip -osm city.osm.pbf -gtfs-out output/

  # Slower walkers, 3-minute budget
  gtfs-transfers -gtfs-in feed.zip -osm city.osm.pbf -gtfs-out output/ \
    -modes "walking:3.5" -max-walk 180

  # Also export the prepared graph (walking + bike weights) in DIMACS format
  gtfs-transfers -gtfs-in feed.zip -osm city.osm.pbf -gtfs-out output/ \
    -dimacs-out dimacs/

  # Stop basis from a plain CSV instead of GTFS, DIMACS export only
  gtfs-transfers -stops-csv stops.csv -osm city.osm.pbf \
    -no-transfers -dimacs-out dimacs/ -modes "walking:4.5,bike:15,car:140"

  # Stop basis from a plain CSV, write computed transfers as CSV instead
  gtfs-transfers -stops-csv stops.csv -osm city.osm.pbf \
    -transfers-out transfers.csv

  # Explicit bounding box instead of the automatic stop-extent + padding one
  gtfs-transfers -gtfs-in feed.zip -osm city.osm.pbf -gtfs-out output/ \
    -bbox "49.3,8.6,49.5,8.8"
`)
}
