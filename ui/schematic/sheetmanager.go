package schematic

import (
	"fmt"

	"pcb-tracer/internal/app"
)

// SheetManager coordinates multiple schematic windows over a single SchematicDoc.
type SheetManager struct {
	doc     *SchematicDoc
	state   *app.State
	windows map[int]*SchematicWindow // sheetNum → window

	showStubs bool
}

// NewSheetManager generates a schematic from app state and returns a manager.
func NewSheetManager(state *app.State) *SheetManager {
	sm := &SheetManager{
		state:   state,
		windows: make(map[int]*SchematicWindow),
	}
	sm.doc = GenerateSchematic(state)

	// Restore saved layout
	if layout := LoadLayout(state.ProjectPath); layout != nil {
		ApplyLayout(sm.doc, layout)
	}

	return sm
}

// NewSheetManagerFresh generates a schematic ignoring any saved layout.
func NewSheetManagerFresh(state *app.State) *SheetManager {
	sm := &SheetManager{
		state:   state,
		windows: make(map[int]*SchematicWindow),
	}
	sm.doc = GenerateSchematic(state)
	// Save the fresh layout immediately
	SaveLayout(sm.doc, state.ProjectPath)
	return sm
}

// Doc returns the shared schematic document.
func (sm *SheetManager) Doc() *SchematicDoc {
	return sm.doc
}

// OpenSheet opens (or focuses) a window for the given sheet number.
func (sm *SheetManager) OpenSheet(sheetNum int) {
	if w, ok := sm.windows[sheetNum]; ok {
		w.Show()
		return
	}

	// Find sheet name
	sheetName := ""
	for _, s := range sm.doc.Sheets {
		if s.Number == sheetNum {
			sheetName = s.Name
			break
		}
	}

	w, err := newSchematicWindowForSheet(sm.state, sm.doc, sheetNum, sheetName, sm)
	if err != nil {
		return
	}
	sm.windows[sheetNum] = w

	// Clean up when window is destroyed
	w.win.Connect("destroy", func() {
		delete(sm.windows, sheetNum)
	})

	w.Show()
}

// MoveSymbolToSheet moves a symbol and its power ports to a different sheet,
// re-routes affected sheets, regenerates off-sheet connectors, and refreshes windows.
func (sm *SheetManager) MoveSymbolToSheet(symID string, targetSheet int) {
	sym := sm.doc.SymbolByID(symID)
	if sym == nil {
		return
	}

	oldSheet := effectiveSheet(sym.Sheet)
	sym.Sheet = targetSheet

	// Reposition symbol to avoid overlapping existing symbols on the target sheet
	repositionForSheet(sm.doc, sym, targetSheet)

	// Move associated power ports
	for _, pp := range sm.doc.PowerPorts {
		if pp.OwnerSymbolID == symID {
			pp.Sheet = targetSheet
		}
	}

	// Recompute pin positions at new location
	def := GetSymbolDef(sym.GateType,
		countPinsByDir(sym, "input"),
		countPinsByDir(sym, "output"),
		countPinsByDir(sym, "enable"),
		countPinsByDir(sym, "clock"))
	ComputePinPositions(sym, def)
	positionPowerPorts(sm.doc)

	// Save existing off-sheet connector positions before wiping and regenerating.
	type oscPos struct {
		x, y     float64
		flipH    bool
		flipV    bool
		rotation int
	}
	savedOSC := make(map[string]oscPos, len(sm.doc.OffSheetConnectors))
	for _, osc := range sm.doc.OffSheetConnectors {
		key := fmt.Sprintf("%d:%s", osc.Sheet, osc.NetID)
		savedOSC[key] = oscPos{osc.X, osc.Y, osc.FlipH, osc.FlipV, osc.Rotation}
	}

	// Regenerate off-sheet connectors (before routing so they become wire endpoints).
	generateOffSheetConnectors(sm.doc)

	// Restore saved positions for connectors that still exist (e.g. net still crosses
	// the same sheet boundary). Newly created connectors keep their auto-placed position.
	for _, osc := range sm.doc.OffSheetConnectors {
		key := fmt.Sprintf("%d:%s", osc.Sheet, osc.NetID)
		if saved, ok := savedOSC[key]; ok {
			osc.X = saved.x
			osc.Y = saved.y
			osc.FlipH = saved.flipH
			osc.FlipV = saved.flipV
			osc.Rotation = saved.rotation
		}
	}

	RouteAllWires(sm.doc)
	regenerateNetLabels(sm.doc)

	// Debug: print all objects on the target sheet
	fmt.Printf("=== Sheet %d after moving %s ===\n", targetSheet, symID)
	for _, s := range sm.doc.Symbols {
		if effectiveSheet(s.Sheet) == targetSheet {
			fmt.Printf("  SYMBOL   %s (%s) at (%.0f, %.0f)\n", s.ID, s.GateType, s.X, s.Y)
		}
	}
	for _, pp := range sm.doc.PowerPorts {
		if effectiveSheet(pp.Sheet) == targetSheet {
			fmt.Printf("  POWER    %s at (%.0f, %.0f) owner=%s\n", pp.NetName, pp.X, pp.Y, pp.OwnerSymbolID)
		}
	}
	for _, osc := range sm.doc.OffSheetConnectors {
		if osc.Sheet == targetSheet {
			fmt.Printf("  OSC      net=%s name=%q → sheet %d  at (%.0f, %.0f) dir=%s\n",
				osc.NetID, osc.NetName, osc.TargetSheet, osc.X, osc.Y, osc.Direction)
		}
	}
	for _, nl := range sm.doc.NetLabels {
		if effectiveSheet(nl.Sheet) == targetSheet {
			fmt.Printf("  NETLABEL net=%s name=%q at (%.0f, %.0f)\n", nl.NetID, nl.NetName, nl.X, nl.Y)
		}
	}
	for _, w := range sm.doc.Wires {
		if effectiveSheet(w.Sheet) == targetSheet {
			fmt.Printf("  WIRE     net=%s %d pts\n", w.NetID, len(w.Points))
		}
	}
	fmt.Printf("===\n")

	// Save layout
	SaveLayout(sm.doc, sm.state.ProjectPath)

	// Refresh open windows
	for sheetNum, w := range sm.windows {
		if sheetNum == oldSheet || sheetNum == targetSheet {
			w.refreshView()
		}
	}
}

// AddSheet creates a new sheet with the given name and returns it.
func (sm *SheetManager) AddSheet(name string) Sheet {
	maxNum := 0
	for _, s := range sm.doc.Sheets {
		if s.Number > maxNum {
			maxNum = s.Number
		}
	}
	sheet := Sheet{Number: maxNum + 1, Name: name}
	sm.doc.Sheets = append(sm.doc.Sheets, sheet)
	SaveLayout(sm.doc, sm.state.ProjectPath)
	return sheet
}

// Sheets returns the current sheet list.
func (sm *SheetManager) Sheets() []Sheet {
	return sm.doc.Sheets
}

// Regenerate regenerates the schematic from current state, preserving layout.
func (sm *SheetManager) Regenerate() {
	sm.doc = GenerateSchematic(sm.state, sm.showStubs)
	if layout := LoadLayout(sm.state.ProjectPath); layout != nil {
		ApplyLayout(sm.doc, layout)
	}

	// Update all open windows
	for _, w := range sm.windows {
		w.doc = sm.doc
		w.refreshView()
	}
}

// SetShowStubs sets stub visibility and regenerates.
func (sm *SheetManager) SetShowStubs(show bool) {
	sm.showStubs = show
	sm.Regenerate()
}

// repositionForSheet places a symbol at a non-overlapping position on the target sheet.
func repositionForSheet(doc *SchematicDoc, sym *PlacedSymbol, sheetNum int) {
	// Collect bounding boxes of existing symbols on the target sheet
	type rect struct{ x1, y1, x2, y2 float64 }
	var occupied []rect
	for _, s := range doc.Symbols {
		if s.ID == sym.ID {
			continue
		}
		if effectiveSheet(s.Sheet) != sheetNum {
			continue
		}
		def := GetSymbolDef(s.GateType,
			countPinsByDir(s, "input"),
			countPinsByDir(s, "output"),
			countPinsByDir(s, "enable"),
			countPinsByDir(s, "clock"))
		if def == nil {
			continue
		}
		hw := def.BodyWidth/2 + stubLength + 20
		hh := def.BodyHeight/2 + 20
		occupied = append(occupied, rect{s.X - hw, s.Y - hh, s.X + hw, s.Y + hh})
	}

	if len(occupied) == 0 {
		// Empty sheet — start further right and down to give room for OSCs on all sides.
		// Input OSCs sit 140 units left of the input pin tips; top pins of tall symbols
		// can be far above sym.Y, so we push Y down by an extra row too.
		sym.X = startX + colSpacing/2
		sym.Y = startY + rowSpacing
		return
	}

	// Try placing at the symbol's current position first
	symDef := GetSymbolDef(sym.GateType,
		countPinsByDir(sym, "input"),
		countPinsByDir(sym, "output"),
		countPinsByDir(sym, "enable"),
		countPinsByDir(sym, "clock"))
	shw := 100.0
	shh := 60.0
	if symDef != nil {
		shw = symDef.BodyWidth/2 + stubLength + 20
		shh = symDef.BodyHeight/2 + 20
	}

	overlaps := func(x, y float64) bool {
		for _, r := range occupied {
			if x+shw > r.x1 && x-shw < r.x2 && y+shh > r.y1 && y-shh < r.y2 {
				return true
			}
		}
		return false
	}

	if !overlaps(sym.X, sym.Y) {
		return
	}

	// Find the bottom-most symbol on the sheet and place below it
	maxY := occupied[0].y2
	for _, r := range occupied[1:] {
		if r.y2 > maxY {
			maxY = r.y2
		}
	}
	sym.X = startX
	sym.Y = maxY + rowSpacing
}

// StatusForSheet returns the status text for a sheet.
func (sm *SheetManager) StatusForSheet(sheetNum int) string {
	symCount := 0
	wireCount := 0
	for _, sym := range sm.doc.Symbols {
		if effectiveSheet(sym.Sheet) == sheetNum {
			symCount++
		}
	}
	for _, w := range sm.doc.Wires {
		if effectiveSheet(w.Sheet) == sheetNum {
			wireCount++
		}
	}
	return fmt.Sprintf("Sheet %d: %d symbols, %d wires", sheetNum, symCount, wireCount)
}
