// gtfs-transfers generates pedestrian transfer entries for a GTFS feed
// by routing through an OpenStreetMap street graph.
//
// Usage:
//
//	gtfs-transfers [flags] <gtfs-input> <osm-input.osm> <gtfs-output>
//
// Flags:
//
//	-max-walk        Maximum walking time in seconds (default 300 / 5 min)
//	-flat-speed      Walking speed on flat footways, m/s   (default 1.39)
//	-stair-up        Speed ascending stairs, m/s           (default 0.50)
//	-stair-down      Speed descending stairs, m/s          (default 0.70)
//	-wheelchair      Exclude stairs and inaccessible paths (default false)
//	-penalty         Fixed transfer penalty in seconds     (default 0)
package main

import (
	"flag"
	"fmt"
	"os"
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
	fmt.Println("───────────────────────────────────────────")

	t0 := time.Now()
	fmt.Println("[1/4] Parsing GTFS feed …")
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

	t1 := time.Now()
	fmt.Println("[2/4] Parsing OSM graph …")
	graph, err := osm.Parse(osmIn, cfg.WheelchairAccessible)
	if err != nil {
		fatalf("OSM parse error: %v", err)
	}
	fmt.Printf("Nodes: %d  Edge lists: %d  (%.1fs)\n",
		len(graph.Nodes), len(graph.Edges), time.Since(t1).Seconds())

	t2 := time.Now()
	fmt.Println("[3/4] Generating transfers …")
	transfers.GenerateTransfers(feed, graph, cfg)
	fmt.Printf("Total transfers in feed: %d  (%.1fs)\n",
		len(feed.Transfers), time.Since(t2).Seconds())

	t3 := time.Now()
	fmt.Println("\n[4/4] Writing GTFS feed …")
	writer := gtfswriter.Writer{
		Sorted:           true,
		ExplicitCalendar: false,
	}
	err = writer.Write(feed, gtfsOut)
	if err != nil {
		fatalf("GTFS write error: %v", err)
	}
	fmt.Printf("Written to %s  (%.1fs)\n", gtfsOut, time.Since(t3).Seconds())

	fmt.Printf("Done in %.2fs total\n", time.Since(t0).Seconds())
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
`)
}
