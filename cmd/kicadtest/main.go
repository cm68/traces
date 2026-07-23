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
	"os"
	"sort"

	"pcb-tracer/internal/app"
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
	if layout := schematic.LoadLayout(projPath); layout != nil {
		restored := schematic.ApplyLayout(doc, layout)
		fmt.Printf("applied saved layout: %d symbols restored\n", restored)
	}

	fmt.Printf("sheets=%d symbols=%d wires=%d labels=%d powerports=%d offsheet=%d routeFallbacks=%d\n",
		len(doc.Sheets), len(doc.Symbols), len(doc.Wires),
		len(doc.NetLabels), len(doc.PowerPorts), len(doc.OffSheetConnectors),
		schematic.RouteFallbackCount)

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

	// Dump wire geometry with net IDs for external analysis.
	if wf, err := os.Create(outPrefix + "_wires.json"); err == nil {
		json.NewEncoder(wf).Encode(doc.Wires)
		wf.Close()
	}

	fmt.Println("export OK")
}
