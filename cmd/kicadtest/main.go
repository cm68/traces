// kicadtest loads a project headlessly, generates its schematic (applying any
// saved layout), and writes KiCad .kicad_sch files for validation with kicad-cli.
//
// Usage: kicadtest <project.pcbproj> <output-prefix>
//
// Output files are written as <output-prefix>_schematic.kicad_sch (and
// _sheetN variants for multi-sheet schematics).
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"sort"

	"pcb-tracer/internal/app"
	"pcb-tracer/internal/kicad"
	"pcb-tracer/ui/schematic"
)

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: %s <project.pcbproj> <output-prefix>\n", os.Args[0])
		os.Exit(2)
	}
	projPath, outPrefix := os.Args[1], os.Args[2]

	state := app.NewState()
	if err := state.LoadProject(projPath); err != nil {
		log.Fatalf("load project: %v", err)
	}

	doc := schematic.GenerateSchematic(state)
	if os.Getenv("FRESH_LAYOUT") == "" {
		if layout := schematic.LoadLayout(projPath); layout != nil {
			restored := schematic.ApplyLayout(doc, layout)
			fmt.Printf("applied saved layout: %d symbols restored\n", restored)
		}
	} else {
		fmt.Println("using fresh auto-layout (FRESH_LAYOUT set)")
	}

	wireLen := 0.0
	bends := 0
	for _, w := range doc.Wires {
		for i := 1; i < len(w.Points); i++ {
			wireLen += math.Abs(w.Points[i].X-w.Points[i-1].X) +
				math.Abs(w.Points[i].Y-w.Points[i-1].Y)
		}
		if len(w.Points) > 2 {
			bends += len(w.Points) - 2
		}
	}
	flips := 0
	for _, s := range doc.Symbols {
		if s.FlipV || s.FlipH {
			flips++
		}
	}
	fmt.Printf("sheets=%d symbols=%d wires=%d labels=%d powerports=%d offsheet=%d routeFallbacks=%d\n",
		len(doc.Sheets), len(doc.Symbols), len(doc.Wires),
		len(doc.NetLabels), len(doc.PowerPorts), len(doc.OffSheetConnectors),
		schematic.RouteFallbackCount)
	// Count crossings between wires of different nets — the visual-clutter
	// metric layout changes should drive down.
	type seg struct {
		x1, y1, x2, y2 float64
		net            string
	}
	var segs []seg
	for _, w := range doc.Wires {
		for i := 1; i < len(w.Points); i++ {
			segs = append(segs, seg{
				w.Points[i-1].X, w.Points[i-1].Y,
				w.Points[i].X, w.Points[i].Y, w.NetID,
			})
		}
	}
	crossings := 0
	for i := range segs {
		for j := i + 1; j < len(segs); j++ {
			a, b := segs[i], segs[j]
			if a.net == b.net {
				continue
			}
			// horizontal a vs vertical b (and vice versa), strict interior crossing
			cross := func(h, v seg) bool {
				if math.Abs(h.y1-h.y2) > 0.01 || math.Abs(v.x1-v.x2) > 0.01 {
					return false
				}
				xlo, xhi := math.Min(h.x1, h.x2), math.Max(h.x1, h.x2)
				ylo, yhi := math.Min(v.y1, v.y2), math.Max(v.y1, v.y2)
				return v.x1 > xlo+0.01 && v.x1 < xhi-0.01 && h.y1 > ylo+0.01 && h.y1 < yhi-0.01
			}
			if cross(a, b) || cross(b, a) {
				crossings++
			}
		}
	}
	fmt.Printf("wireLen=%.0f bends=%d flips=%d crossings=%d\n", wireLen, bends, flips, crossings)

	for _, e := range schematic.RouteFallbackEdges {
		fmt.Println("fallback:", e)
	}

	// Report pins of different nets at identical coordinates — placement-level
	// shorts the router cannot avoid (usually overlapping symbols).
	type pinAt struct {
		sym   string
		netID string
	}
	byPos := make(map[[3]int][]pinAt)
	for _, sym := range doc.Symbols {
		sheet := sym.Sheet
		if sheet <= 0 {
			sheet = 1
		}
		for _, pin := range sym.Pins {
			k := [3]int{sheet, int(pin.X), int(pin.Y)}
			byPos[k] = append(byPos[k], pinAt{sym.ID, pin.NetID})
		}
	}
	overlaps := 0
	for k, pins := range byPos {
		nets := make(map[string]bool)
		syms := make(map[string]bool)
		for _, p := range pins {
			nets[p.netID] = true
			syms[p.sym] = true
		}
		if len(nets) > 1 {
			overlaps++
			if overlaps <= 10 {
				fmt.Printf("coincident pins: sheet %d (%d,%d): %d pins, %d nets, symbols %v\n",
					k[0], k[1], k[2], len(pins), len(nets), keys(syms))
			}
		}
	}
	if overlaps > 0 {
		fmt.Printf("coincident-pin locations with multiple nets: %d\n", overlaps)
	}

	if err := schematic.ExportKiCadSchematic(doc, outPrefix); err != nil {
		log.Fatalf("export: %v", err)
	}

	// Capture the board outline when the project doesn't have one yet.
	if len(state.BoardOutline) < 3 {
		if err := state.DetectBoardOutline(); err != nil {
			fmt.Println("board outline:", err)
		}
	}
	fmt.Printf("board outline: %d points\n", len(state.BoardOutline))

	boardPath := kicad.BoardPath(outPrefix + ".pcbproj")
	if err := kicad.ExportBoard(state, schematic.KiCadFootprintPaths(doc), boardPath); err != nil {
		log.Fatalf("board export: %v", err)
	}
	fmt.Println("board exported:", boardPath)
	if err := kicad.ExportProject(outPrefix + ".pcbproj"); err != nil {
		log.Fatalf("project export: %v", err)
	}

	// Dump wire geometry with net IDs for external analysis.
	if wf, err := os.Create(outPrefix + "_wires.json"); err == nil {
		json.NewEncoder(wf).Encode(doc.Wires)
		wf.Close()
	}

	fmt.Println("export OK")
}
