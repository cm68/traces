// kicadtest loads a project headlessly, generates its schematic (applying any
// saved layout), and writes KiCad .kicad_sch files for validation with kicad-cli.
//
// Usage: kicadtest <project.pcbproj> <output-prefix>
//
// Output files are written as <output-prefix>_schematic.kicad_sch (and
// _sheetN variants for multi-sheet schematics).
package main

import (
	"fmt"
	"log"
	"os"

	"pcb-tracer/internal/app"
	"pcb-tracer/ui/schematic"
)

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

	fmt.Printf("sheets=%d symbols=%d wires=%d labels=%d powerports=%d offsheet=%d\n",
		len(doc.Sheets), len(doc.Symbols), len(doc.Wires),
		len(doc.NetLabels), len(doc.PowerPorts), len(doc.OffSheetConnectors))

	if err := schematic.ExportKiCadSchematic(doc, outPrefix); err != nil {
		log.Fatalf("export: %v", err)
	}
	fmt.Println("export OK")
}
