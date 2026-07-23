package schematic

import (
	"crypto/sha256"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// schToMM converts schematic units (1/100 inch) to millimeters.
const schToMM = 0.254

func toMM(v float64) float64 { return v * schToMM }

// snapGrid aligns a schematic-unit coordinate to KiCad's 1.27mm connection
// grid (5 schematic units). Symbol pin offsets are all multiples of 5 units,
// so snapping origins and wire endpoints keeps pins and wires coincident.
func snapGrid(v float64) float64 { return math.Round(v/5) * 5 }

// snapMM converts a schematic-unit coordinate to mm, snapped to the KiCad grid.
func snapMM(v float64) float64 { return toMM(snapGrid(v)) }

// libBaseName returns the symbol-name portion of a lib ID ("74xx:74LS00" → "74LS00").
// Child unit symbols inside a lib_symbols definition must use the bare name
// without the library nickname, or KiCad refuses to load the file.
func libBaseName(libID string) string {
	if i := strings.LastIndex(libID, ":"); i >= 0 {
		return libID[i+1:]
	}
	return libID
}

// sexpWriter builds indented S-expression output.
type sexpWriter struct {
	sb    strings.Builder
	depth int
}

func (w *sexpWriter) indent() {
	for i := 0; i < w.depth; i++ {
		w.sb.WriteString("  ")
	}
}

func (w *sexpWriter) open(tag string) {
	w.indent()
	w.sb.WriteString("(")
	w.sb.WriteString(tag)
	w.sb.WriteString("\n")
	w.depth++
}

func (w *sexpWriter) close() {
	w.depth--
	w.indent()
	w.sb.WriteString(")\n")
}

// inlineOpen writes "(tag " without a newline, for compact single-line expressions.
func (w *sexpWriter) inlineOpen(tag string) {
	w.indent()
	w.sb.WriteString("(")
	w.sb.WriteString(tag)
}

// inlineClose writes ")\n".
func (w *sexpWriter) inlineClose() {
	w.sb.WriteString(")\n")
}

func (w *sexpWriter) line(format string, args ...any) {
	w.indent()
	fmt.Fprintf(&w.sb, format, args...)
	w.sb.WriteString("\n")
}

func (w *sexpWriter) String() string { return w.sb.String() }

// deterministicUUID generates a stable UUID from a namespace and id string.
func deterministicUUID(namespace, id string) string {
	h := sha256.Sum256([]byte(namespace + ":" + id))
	// Format as UUID v5-style
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		h[0:4], h[4:6],
		// Set version nibble to 5
		[]byte{(h[6] & 0x0f) | 0x50, h[7]},
		// Set variant bits
		[]byte{(h[8] & 0x3f) | 0x80, h[9]},
		h[10:16])
}

// paperSize picks the smallest landscape standard paper that fits the given mm bounds.
func paperSize(widthMM, heightMM float64) string {
	// Standard landscape sizes
	sizes := []struct {
		name string
		w, h float64
	}{
		{"A4", 297, 210},
		{"A3", 420, 297},
		{"A2", 594, 420},
		{"A1", 841, 594},
		{"A0", 1189, 841},
	}
	for _, s := range sizes {
		if widthMM <= s.w && heightMM <= s.h {
			return s.name
		}
	}
	return "A0"
}

// kicadSchPath returns the .kicad_sch file path for a given sheet number.
// Sheet 1 (or single-sheet) uses the base name; higher sheets get a suffix.
func kicadSchPath(projectPath string, sheetNum int) string {
	if projectPath == "" {
		return ""
	}
	ext := filepath.Ext(projectPath)
	base := strings.TrimSuffix(projectPath, ext)
	if sheetNum <= 1 {
		return base + "_schematic.kicad_sch"
	}
	return fmt.Sprintf("%s_schematic_sheet%d.kicad_sch", base, sheetNum)
}

// ExportKiCadSchematic writes KiCad .kicad_sch files for the schematic.
// For single-sheet designs, one file is written. For multi-sheet, a root file
// plus one sub-file per additional sheet.
func ExportKiCadSchematic(doc *SchematicDoc, projectPath string) error {
	if doc == nil || projectPath == "" {
		return nil
	}

	sheets := doc.Sheets
	if len(sheets) == 0 {
		sheets = []Sheet{{Number: 1, Name: "Main"}}
	}

	if len(sheets) == 1 {
		// Single sheet: write everything to one file
		return writeSheetFile(doc, projectPath, sheets[0].Number, sheets[0].Name, true)
	}

	// Multi-sheet: root file has sheet 1 content + sheet references
	if err := writeRootFile(doc, projectPath, sheets); err != nil {
		return err
	}
	// Write sub-sheet files for sheets 2+
	for _, sh := range sheets[1:] {
		if err := writeSheetFile(doc, projectPath, sh.Number, sh.Name, false); err != nil {
			return err
		}
	}
	return nil
}

// writeRootFile writes the root .kicad_sch with sheet 1 content and hierarchy references.
func writeRootFile(doc *SchematicDoc, projectPath string, sheets []Sheet) error {
	path := kicadSchPath(projectPath, 1)
	if path == "" {
		return nil
	}

	w := &sexpWriter{}
	writeHeader(w, doc, 1, sheets[0].Name)
	writeLibSymbols(w, doc, 1)
	writeSheetContent(w, doc, 1)

	// Hierarchical sheet references for sheets 2+
	for i, sh := range sheets[1:] {
		writeSheetReference(w, doc, projectPath, sh, i)
	}

	writeSheetInstances(w, sheets)
	w.close() // close kicad_sch

	return os.WriteFile(path, []byte(w.String()), 0644)
}

// writeSheetFile writes a complete .kicad_sch for a single sheet.
func writeSheetFile(doc *SchematicDoc, projectPath string, sheetNum int, sheetName string, isSingle bool) error {
	path := kicadSchPath(projectPath, sheetNum)
	if path == "" {
		return nil
	}

	w := &sexpWriter{}
	writeHeader(w, doc, sheetNum, sheetName)
	writeLibSymbols(w, doc, sheetNum)
	writeSheetContent(w, doc, sheetNum)

	if isSingle {
		writeSheetInstances(w, doc.Sheets)
	}

	w.close() // close kicad_sch

	return os.WriteFile(path, []byte(w.String()), 0644)
}

// writeHeader writes the file header, paper size, and title block.
func writeHeader(w *sexpWriter, doc *SchematicDoc, sheetNum int, sheetName string) {
	w.open("kicad_sch")
	w.line("(version 20231120)")
	w.line("(generator \"pcb-tracer\")")
	w.line("(generator_version \"0.1\")")
	w.line("(uuid \"%s\")", deterministicUUID("root", fmt.Sprintf("sheet-%d", sheetNum)))

	// Compute bounds for this sheet to pick paper size
	minX, minY, maxX, maxY := sheetBounds(doc, sheetNum)
	wMM := toMM(maxX - minX)
	hMM := toMM(maxY - minY)
	paper := paperSize(wMM, hMM)
	w.line("(paper \"%s\")", paper)

	// Title block
	w.open("title_block")
	title := doc.ProjectName
	if sheetName != "" {
		title = fmt.Sprintf("%s - %s", doc.ProjectName, sheetName)
	}
	w.line("(title \"%s\")", escapeString(title))
	w.close()
}

// writeLibSymbols emits the lib_symbols section with definitions for all symbols on the sheet.
func writeLibSymbols(w *sexpWriter, doc *SchematicDoc, sheetNum int) {
	w.open("lib_symbols")

	// Collect unique lib IDs needed on this sheet
	type libKey struct {
		libID    string
		gateType string
		partNum  string
	}
	seen := make(map[string]bool)

	// Symbols
	for _, sym := range doc.Symbols {
		if effectiveSheet(sym.Sheet) != sheetNum {
			continue
		}
		lid := kicadLibID(sym)
		if seen[lid] {
			continue
		}
		seen[lid] = true
		writeLibSymbolDef(w, sym, lid, doc, sheetNum)
	}

	// Power symbols
	havePower := false
	for _, pp := range doc.PowerPorts {
		if effectiveSheet(pp.Sheet) != sheetNum {
			continue
		}
		havePower = true
		lid := kicadPowerLibID(pp.NetName)
		if seen[lid] {
			continue
		}
		seen[lid] = true
		writePowerLibSymbolDef(w, pp.NetName, lid)
	}
	if havePower {
		writePwrFlagLibSymbolDef(w)
	}

	w.close()
}

// writeSheetContent writes all placed elements for one sheet.
func writeSheetContent(w *sexpWriter, doc *SchematicDoc, sheetNum int) {
	// Symbols
	for _, sym := range doc.Symbols {
		if effectiveSheet(sym.Sheet) != sheetNum {
			continue
		}
		writeSymbolInstance(w, doc, sym)
	}

	// Wires
	for _, wire := range doc.Wires {
		if effectiveSheet(wire.Sheet) != sheetNum {
			continue
		}
		writeWireSegments(w, wire)
	}

	// Junctions
	junctions := findJunctions(doc, sheetNum)
	for _, j := range junctions {
		w.line("(junction (at %.4f %.4f) (diameter 0) (color 0 0 0 0) (uuid \"%s\"))",
			toMM(j.x), toMM(j.y),
			deterministicUUID("junction", fmt.Sprintf("%d-%.2f-%.2f", sheetNum, j.x, j.y)))
	}

	// Net labels
	for _, nl := range doc.NetLabels {
		if effectiveSheet(nl.Sheet) != sheetNum {
			continue
		}
		writeNetLabel(w, nl)
	}

	// Power ports
	for _, pp := range doc.PowerPorts {
		if effectiveSheet(pp.Sheet) != sheetNum {
			continue
		}
		writePowerPortInstance(w, doc, pp)
	}

	// One PWR_FLAG per power net per sheet marks the net as driven,
	// silencing KiCad's "power pin not driven" ERC error.
	seenPwrNet := make(map[string]bool)
	for _, pp := range doc.PowerPorts {
		if effectiveSheet(pp.Sheet) != sheetNum || seenPwrNet[pp.NetName] {
			continue
		}
		seenPwrNet[pp.NetName] = true
		writePwrFlagInstance(w, doc, pp, sheetNum)
	}

	// Off-sheet connectors as global labels
	for _, osc := range doc.OffSheetConnectors {
		if effectiveSheet(osc.Sheet) != sheetNum {
			continue
		}
		writeGlobalLabel(w, osc)
	}
}

// --- Symbol mapping ---

// kicadLibMap maps known part numbers to KiCad library symbol IDs.
var kicadLibMap = map[string]string{
	// 74xx TTL family
	"7400": "74xx:74LS00", "74LS00": "74xx:74LS00", "74S00": "74xx:74S00", "74F00": "74xx:74F00", "74HCT00": "74xx:74HCT00",
	"7402": "74xx:74LS02", "74LS02": "74xx:74LS02", "74S02": "74xx:74S02",
	"7404": "74xx:74LS04", "74LS04": "74xx:74LS04", "74S04": "74xx:74S04", "74F04": "74xx:74F04",
	"7408": "74xx:74LS08", "74LS08": "74xx:74LS08", "74S08": "74xx:74S08",
	"7410": "74xx:74LS10", "74LS10": "74xx:74LS10",
	"7411": "74xx:74LS11", "74LS11": "74xx:74LS11",
	"7420": "74xx:74LS20", "74LS20": "74xx:74LS20",
	"7421": "74xx:74LS21", "74LS21": "74xx:74LS21",
	"7427": "74xx:74LS27", "74LS27": "74xx:74LS27",
	"7430": "74xx:74LS30", "74LS30": "74xx:74LS30",
	"7432": "74xx:74LS32", "74LS32": "74xx:74LS32", "74S32": "74xx:74S32",
	"7442": "74xx:74LS42", "74LS42": "74xx:74LS42",
	"7474": "74xx:74LS74", "74LS74": "74xx:74LS74", "74S74": "74xx:74S74",
	"7486": "74xx:74LS86", "74LS86": "74xx:74LS86",
	"74125": "74xx:74LS125", "74LS125": "74xx:74LS125",
	"74126": "74xx:74LS126", "74LS126": "74xx:74LS126",
	"74138": "74xx:74LS138", "74LS138": "74xx:74LS138",
	"74139": "74xx:74LS139", "74LS139": "74xx:74LS139",
	"74148": "74xx:74LS148", "74LS148": "74xx:74LS148",
	"74151": "74xx:74LS151", "74LS151": "74xx:74LS151",
	"74153": "74xx:74LS153", "74LS153": "74xx:74LS153",
	"74154": "74xx:74LS154", "74LS154": "74xx:74LS154",
	"74157": "74xx:74LS157", "74LS157": "74xx:74LS157",
	"74161": "74xx:74LS161", "74LS161": "74xx:74LS161",
	"74163": "74xx:74LS163", "74LS163": "74xx:74LS163",
	"74164": "74xx:74LS164", "74LS164": "74xx:74LS164",
	"74165": "74xx:74LS165", "74LS165": "74xx:74LS165",
	"74166": "74xx:74LS166", "74LS166": "74xx:74LS166",
	"74173": "74xx:74LS173", "74LS173": "74xx:74LS173",
	"74174": "74xx:74LS174", "74LS174": "74xx:74LS174",
	"74175": "74xx:74LS175", "74LS175": "74xx:74LS175",
	"74181": "74xx:74LS181", "74LS181": "74xx:74LS181",
	"74191": "74xx:74LS191", "74LS191": "74xx:74LS191",
	"74193": "74xx:74LS193", "74LS193": "74xx:74LS193",
	"74240": "74xx:74LS240", "74LS240": "74xx:74LS240",
	"74241": "74xx:74LS241", "74LS241": "74xx:74LS241",
	"74244": "74xx:74LS244", "74LS244": "74xx:74LS244",
	"74245": "74xx:74LS245", "74LS245": "74xx:74LS245",
	"74253": "74xx:74LS253", "74LS253": "74xx:74LS253",
	"74257": "74xx:74LS257", "74LS257": "74xx:74LS257",
	"74259": "74xx:74LS259", "74LS259": "74xx:74LS259",
	"74273": "74xx:74LS273", "74LS273": "74xx:74LS273",
	"74283": "74xx:74LS283", "74LS283": "74xx:74LS283",
	"74367": "74xx:74LS367", "74LS367": "74xx:74LS367",
	"74368": "74xx:74LS368", "74LS368": "74xx:74LS368",
	"74373": "74xx:74LS373", "74LS373": "74xx:74LS373",
	"74374": "74xx:74LS374", "74LS374": "74xx:74LS374",
	"74393": "74xx:74LS393", "74LS393": "74xx:74LS393",
	"74670": "74xx:74LS670", "74LS670": "74xx:74LS670",

	// 4000 series CMOS
	"4001": "4xxx:4001", "CD4001": "4xxx:4001",
	"4011": "4xxx:4011", "CD4011": "4xxx:4011",
	"4013": "4xxx:4013", "CD4013": "4xxx:4013",
	"4017": "4xxx:4017", "CD4017": "4xxx:4017",
	"4040": "4xxx:4040", "CD4040": "4xxx:4040",
	"4049": "4xxx:4049", "CD4049": "4xxx:4049",
	"4050": "4xxx:4050", "CD4050": "4xxx:4050",
	"4066": "4xxx:4066", "CD4066": "4xxx:4066",
	"4069": "4xxx:4069", "CD4069": "4xxx:4069",
	"4071": "4xxx:4071", "CD4071": "4xxx:4071",
	"4081": "4xxx:4081", "CD4081": "4xxx:4081",

	// Memory
	"2114": "Memory_RAM:2114",
	"2716": "Memory_EPROM:2716",
	"2732": "Memory_EPROM:2732",
	"2764": "Memory_EPROM:2764",
	"27128": "Memory_EPROM:27128",
	"6116": "Memory_RAM:6116",
	"6264": "Memory_RAM:6264",
}

// kicadPowerMap maps power net names to KiCad power library symbols.
var kicadPowerMap = map[string]string{
	"VCC":  "power:VCC",
	"GND":  "power:GND",
	"+5V":  "power:+5V",
	"-5V":  "power:-5V",
	"+12V": "power:+12V",
	"-12V": "power:-12V",
	"+3V3": "power:+3V3",
	"+3.3V": "power:+3V3",
	"VDD":  "power:VDD",
	"VSS":  "power:VSS",
}

// kicadLibID returns the KiCad library ID for a placed symbol.
func kicadLibID(sym *PlacedSymbol) string {
	if lid, ok := kicadLibMap[sym.PartNumber]; ok {
		return lid
	}
	// Fallback: generate an inline symbol ID
	return fmt.Sprintf("pcb-tracer:%s", escapeString(sym.PartNumber))
}

// kicadPowerLibID returns the KiCad power library ID for a net name.
func kicadPowerLibID(netName string) string {
	if lid, ok := kicadPowerMap[netName]; ok {
		return lid
	}
	upper := strings.ToUpper(netName)
	if lid, ok := kicadPowerMap[upper]; ok {
		return lid
	}
	return fmt.Sprintf("power:%s", netName)
}

// componentUnitNumber returns the 1-based KiCad unit for a symbol: the
// position of its function among the component's distinct functions in
// document order. Positional numbering never collides, unlike parsing
// function names ("1".."8" and "A".."D" work, but "1A1".."2A4" do not).
func componentUnitNumber(doc *SchematicDoc, sym *PlacedSymbol) int {
	n := 0
	seen := make(map[string]bool)
	for _, s := range doc.Symbols {
		if s.ComponentID != sym.ComponentID || seen[s.FunctionName] {
			continue
		}
		seen[s.FunctionName] = true
		n++
		if s.FunctionName == sym.FunctionName {
			return n
		}
	}
	return 1
}

// kicadPinType maps a pin to a KiCad electrical type, considering the symbol
// it belongs to.
func kicadPinType(sym *PlacedSymbol, pin *SchematicPin) string {
	// Board-edge connector pins are multi-drop bus lines: bidirectional
	// avoids driver-conflict and not-driven ERC noise in either direction.
	if sym.GateType == "CONNECTOR" {
		return "bidirectional"
	}
	switch pin.Direction {
	case "input", "enable", "clock":
		return "input"
	case "output":
		// Outputs gated by an enable (tristate buffers, latches/registers
		// with OE) legally share bus nets with other outputs.
		if sym.GateType == "TRISTATE" || countPinsByDir(sym, "enable") > 0 {
			return "tri_state"
		}
		return "output"
	case "power":
		return "power_in"
	default:
		return "passive"
	}
}

// --- Inline lib_symbol generation ---

// writeLibSymbolDef writes a lib_symbols entry for a placed symbol.
func writeLibSymbolDef(w *sexpWriter, sym *PlacedSymbol, libID string, doc *SchematicDoc, sheetNum int) {
	w.open(fmt.Sprintf("symbol \"%s\"", libID))

	// Determine how many units this component has on this sheet
	numUnits := countComponentUnits(doc, sym.ComponentID, sheetNum)
	if numUnits < 1 {
		numUnits = 1
	}

	w.line("(pin_names (offset 1.016))")
	w.line("(in_bom yes)")
	w.line("(on_board yes)")

	// We emit one sub-symbol per unit. For simplicity with inline symbols,
	// emit the pin layout for each unit based on the actual placed symbols.
	unitSyms := componentUnitsOnSheet(doc, sym.ComponentID, sheetNum)
	for _, us := range unitSyms {
		unit := componentUnitNumber(doc, us)
		writeLibSymbolUnit(w, us, libID, unit)
	}

	w.close() // symbol
}

// writeLibSymbolUnit writes one unit definition within a lib_symbol.
// Geometry comes from the untransformed SymbolDef stubs (not the placed pin
// positions, which have the instance's rotation/flip baked in — KiCad applies
// the instance transform itself). KiCad lib coordinates are Y-up, so all Y
// values from our Y-down model are negated.
func writeLibSymbolUnit(w *sexpWriter, sym *PlacedSymbol, libID string, unit int) {
	def := GetSymbolDef(sym.GateType,
		countPinsByDir(sym, "input"),
		countPinsByDir(sym, "output"),
		countPinsByDir(sym, "enable"),
		countPinsByDir(sym, "clock"))
	if def == nil {
		return
	}

	bw := toMM(def.BodyWidth)
	bh := toMM(def.BodyHeight)

	// Body graphics sub-symbol
	w.open(fmt.Sprintf("symbol \"%s_%d_1\"", libBaseName(libID), unit))

	// Draw a rectangle for the body (all gate types get rectangles in KiCad inline)
	w.open("rectangle")
	w.line("(start %.4f %.4f)", -bw/2, -bh/2)
	w.line("(end %.4f %.4f)", bw/2, bh/2)
	w.line("(stroke (width 0.254) (type default))")
	w.line("(fill (type background))")
	w.close() // rectangle

	// Pins. A pin number listed under more than one direction (terminator
	// functions declare the same physical pin as both input and output) is
	// emitted once, as passive.
	stubs := assignStubs(sym, def)
	numCount := make(map[int]int)
	for _, pin := range sym.Pins {
		if pin.Direction != "power" {
			numCount[pin.PinNumber]++
		}
	}
	emitted := make(map[int]bool)
	for i, pin := range sym.Pins {
		if pin.Direction == "power" {
			continue // Power pins go in unit 0 or are handled as PowerPorts
		}
		pinType := kicadPinType(sym, pin)
		if numCount[pin.PinNumber] > 1 {
			if emitted[pin.PinNumber] {
				continue
			}
			emitted[pin.PinNumber] = true
			pinType = "passive"
		}
		writeLibPin(w, pinType, pin, stubs[i])
	}

	w.close() // sub-symbol
}

// writeLibPin writes a pin definition in a lib_symbol from its untransformed stub.
func writeLibPin(w *sexpWriter, pinType string, pin *SchematicPin, stub *PinStub) {

	// Graphic style
	graphicStyle := "line"
	if pin.Negated {
		graphicStyle = "inverted"
	}
	if pin.Clock {
		graphicStyle = "clock"
	}

	// Tip position and tip→body direction in lib coordinates (Y-up).
	var tipX, tipY, dx, dy float64
	if stub != nil {
		tipX, tipY = stub.TipX, -stub.TipY
		dx = stub.BodyX - stub.TipX
		dy = -(stub.BodyY - stub.TipY)
	}
	length := math.Abs(dx) + math.Abs(dy)

	// KiCad pin angle points from the tip toward the body.
	angle := 0
	switch {
	case dx > 0:
		angle = 0
	case dx < 0:
		angle = 180
	case dy > 0:
		angle = 90
	case dy < 0:
		angle = 270
	}

	name := pin.Name
	if name == "" {
		name = "~"
	}

	w.open(fmt.Sprintf("pin %s %s", pinType, graphicStyle))
	w.line("(at %.4f %.4f %d)", toMM(tipX), toMM(tipY), angle)
	w.line("(length %.4f)", toMM(length))
	w.line("(name \"%s\" (effects (font (size 1.27 1.27))))", escapeString(name))
	w.line("(number \"%d\" (effects (font (size 1.27 1.27))))", pin.PinNumber)
	w.close()
}

// writePowerLibSymbolDef writes a lib_symbols entry for a power symbol.
func writePowerLibSymbolDef(w *sexpWriter, netName string, libID string) {
	w.open(fmt.Sprintf("symbol \"%s\"", libID))
	w.line("(power)")
	w.line("(pin_names (offset 0))")
	w.line("(in_bom yes)")
	w.line("(on_board yes)")

	isGround := isGroundNet(netName)

	// Unit 1
	w.open(fmt.Sprintf("symbol \"%s_0_1\"", libBaseName(libID)))

	if isGround {
		// GND symbol: horizontal line with downward lines
		w.open("polyline")
		w.line("(pts (xy 0 0) (xy 0 -1.27) (xy -1.27 -1.27) (xy 0 -2.54) (xy 1.27 -1.27) (xy 0 -1.27))")
		w.line("(stroke (width 0) (type default))")
		w.line("(fill (type none))")
		w.close()
	} else {
		// VCC/power symbol: line up with bar
		w.open("polyline")
		w.line("(pts (xy 0 0) (xy 0 1.27))")
		w.line("(stroke (width 0) (type default))")
		w.line("(fill (type none))")
		w.close()
		w.open("polyline")
		w.line("(pts (xy -1.27 1.27) (xy 1.27 1.27))")
		w.line("(stroke (width 0) (type default))")
		w.line("(fill (type none))")
		w.close()
	}

	// Power pin (hidden)
	pinDir := 90 // pointing up
	pinY := 0.0
	if isGround {
		pinDir = 270 // pointing down
	}
	w.open("pin power_in line")
	w.line("(at 0 %.4f %d)", pinY, pinDir)
	w.line("(length 0)")
	w.line("(name \"%s\" (effects (font (size 1.27 1.27))))", escapeString(netName))
	w.line("(number \"1\" (effects (font (size 1.27 1.27))))")
	w.close()

	w.close() // sub-symbol
	w.close() // symbol
}

// --- Symbol instance writing ---

// writeSymbolInstance writes a placed symbol instance.
func writeSymbolInstance(w *sexpWriter, doc *SchematicDoc, sym *PlacedSymbol) {
	libID := kicadLibID(sym)
	uuid := deterministicUUID("symbol", sym.ID)
	unit := componentUnitNumber(doc, sym)

	// Our model rotates clockwise in Y-down coordinates; KiCad rotates
	// counter-clockwise in Y-up. Same matrix, so the angle negates.
	kicadRot := (360 - sym.Rotation) % 360

	w.open(fmt.Sprintf("symbol (lib_id \"%s\") (at %.4f %.4f %d)",
		libID, snapMM(sym.X), snapMM(sym.Y), kicadRot))

	// Mirror
	if sym.FlipH {
		w.line("(mirror y)")
	}
	if sym.FlipV {
		w.line("(mirror x)")
	}

	w.line("(unit %d)", unit)

	// Board-edge connector symbols have no footprint of their own — the
	// board seeds one shared edge-connector footprint marked board_only.
	onBoard := "yes"
	if sym.GateType == "CONNECTOR" {
		onBoard = "no"
	}
	w.line("(exclude_from_sim no)")
	w.line("(in_bom yes)")
	w.line("(on_board %s)", onBoard)
	w.line("(dnp no)")

	w.line("(uuid \"%s\")", uuid)

	// Footprint matches the board export's lib id so Update PCB from
	// Schematic associates rather than replaces.
	footprint := ""
	if sym.Package != "" {
		footprint = "pcb-tracer:" + sym.Package
	}

	// Properties
	writeProperty(w, "Reference", sym.ComponentID, sym.X, sym.Y-60, 0)
	writeProperty(w, "Value", sym.PartNumber, sym.X, sym.Y+60, 1)
	writeProperty(w, "Footprint", footprint, sym.X, sym.Y+80, 2)

	// Pin instances
	w.open("instances")
	w.open("project \"pcb-tracer\"")
	w.line("(path \"/%s\" (reference \"%s\") (unit %d))",
		deterministicUUID("root", "sheet-1"),
		sym.ComponentID, unit)
	w.close() // project
	w.close() // instances

	w.close() // symbol
}

// writeProperty writes a symbol property.
func writeProperty(w *sexpWriter, key, value string, x, y float64, id int) {
	w.line("(property \"%s\" \"%s\"", escapeString(key), escapeString(value))
	w.depth++
	w.line("(at %.4f %.4f 0)", toMM(x), toMM(y))
	w.line("(effects (font (size 1.27 1.27))%s)", propVisibility(key))
	w.depth--
	w.line(")")
}

func propVisibility(key string) string {
	switch key {
	case "Footprint", "Datasheet":
		return " hide"
	}
	return ""
}

// --- Wire writing ---

// writeWireSegments writes individual 2-point wire segments for a wire.
func writeWireSegments(w *sexpWriter, wire *Wire) {
	for i := 0; i < len(wire.Points)-1; i++ {
		p1 := wire.Points[i]
		p2 := wire.Points[i+1]
		if snapGrid(p1.X) == snapGrid(p2.X) && snapGrid(p1.Y) == snapGrid(p2.Y) {
			continue // segment collapses to a point after snapping
		}
		uuid := deterministicUUID("wire", fmt.Sprintf("%s-%d", wire.ID, i))
		w.line("(wire (pts (xy %.4f %.4f) (xy %.4f %.4f)) (stroke (width 0) (type default)) (uuid \"%s\"))",
			snapMM(p1.X), snapMM(p1.Y), snapMM(p2.X), snapMM(p2.Y), uuid)
	}
}

// --- Junction detection ---

type junctionPt struct {
	x, y float64
}

// findJunctions finds points where 3+ wire segment endpoints meet.
func findJunctions(doc *SchematicDoc, sheetNum int) []junctionPt {
	// Count wire endpoints at each point (quantized to avoid float issues)
	// Quantize on the same snapped grid the wires are written with.
	type ptKey struct {
		x, y int64
	}
	counts := make(map[ptKey]int)
	coords := make(map[ptKey]junctionPt)

	for _, wire := range doc.Wires {
		if effectiveSheet(wire.Sheet) != sheetNum {
			continue
		}
		for _, p := range wire.Points {
			sx, sy := snapGrid(p.X), snapGrid(p.Y)
			k := ptKey{int64(sx), int64(sy)}
			counts[k]++
			coords[k] = junctionPt{sx, sy}
		}
	}

	var result []junctionPt
	for k, c := range counts {
		if c >= 3 {
			result = append(result, coords[k])
		}
	}

	// Sort for deterministic output
	sort.Slice(result, func(i, j int) bool {
		if result[i].x != result[j].x {
			return result[i].x < result[j].x
		}
		return result[i].y < result[j].y
	})
	return result
}

// --- Net label writing ---

func writeNetLabel(w *sexpWriter, nl *NetLabel) {
	uuid := deterministicUUID("label", fmt.Sprintf("%s-%.0f-%.0f", nl.NetID, nl.X, nl.Y))
	// A KiCad label attaches to the wire under its anchor point. Our labels
	// are drawn 8 units above the wire midpoint, so undo that visual offset.
	w.line("(label \"%s\" (at %.4f %.4f 0) (effects (font (size 1.27 1.27)) (justify left bottom)) (uuid \"%s\"))",
		escapeString(nl.NetName), snapMM(nl.X), snapMM(nl.Y+8), uuid)
}

// --- Power port instance writing ---

// powerPortRef returns a unique annotated reference like "#PWR012" for a
// power port, numbered by its position in doc.PowerPorts so every sheet file
// agrees. Unannotated "#PWR?" references trip KiCad's annotation check.
func powerPortRef(doc *SchematicDoc, pp *PowerPort) string {
	for i, p := range doc.PowerPorts {
		if p == pp {
			return fmt.Sprintf("#PWR%03d", i+1)
		}
	}
	return "#PWR999"
}

// pwrFlagRef returns a unique annotated reference like "#FLG003" for the
// PWR_FLAG on a given sheet and power net.
func pwrFlagRef(doc *SchematicDoc, sheetNum int, netName string) string {
	n := 0
	seen := make(map[string]bool)
	for _, pp := range doc.PowerPorts {
		key := fmt.Sprintf("%d:%s", effectiveSheet(pp.Sheet), pp.NetName)
		if seen[key] {
			continue
		}
		seen[key] = true
		n++
		if effectiveSheet(pp.Sheet) == sheetNum && pp.NetName == netName {
			return fmt.Sprintf("#FLG%03d", n)
		}
	}
	return "#FLG999"
}

func writePowerPortInstance(w *sexpWriter, doc *SchematicDoc, pp *PowerPort) {
	libID := kicadPowerLibID(pp.NetName)
	uuid := deterministicUUID("power", fmt.Sprintf("%s-%s-%d", pp.OwnerSymbolID, pp.NetName, pp.OwnerPinNum))
	ref := powerPortRef(doc, pp)

	// GND symbols point down (rotation 0 in our model, but KiCad power symbols
	// default to pointing up, so GND needs no rotation since the lib_symbol is drawn downward).
	// VCC symbols point up (no rotation needed).
	rotation := 0

	w.open(fmt.Sprintf("symbol (lib_id \"%s\") (at %.4f %.4f %d)",
		libID, snapMM(pp.X), snapMM(pp.Y), rotation))
	w.line("(unit 1)")
	w.line("(uuid \"%s\")", uuid)

	writeProperty(w, "Reference", ref, pp.X, pp.Y-20, 0)
	writeProperty(w, "Value", pp.NetName, pp.X, pp.Y+20, 1)

	w.open("instances")
	w.open("project \"pcb-tracer\"")
	w.line("(path \"/%s\" (reference \"%s\") (unit 1))",
		deterministicUUID("root", "sheet-1"), ref)
	w.close()
	w.close()

	w.close() // symbol

	// Wire from power port to the pin it's attached to
	if snapGrid(pp.PinX) != snapGrid(pp.X) || snapGrid(pp.PinY) != snapGrid(pp.Y) {
		wuuid := deterministicUUID("pwire", fmt.Sprintf("%s-%d", pp.OwnerSymbolID, pp.OwnerPinNum))
		w.line("(wire (pts (xy %.4f %.4f) (xy %.4f %.4f)) (stroke (width 0) (type default)) (uuid \"%s\"))",
			snapMM(pp.X), snapMM(pp.Y), snapMM(pp.PinX), snapMM(pp.PinY), wuuid)
	}
}

// writePwrFlagLibSymbolDef writes the lib_symbols entry for power:PWR_FLAG.
func writePwrFlagLibSymbolDef(w *sexpWriter) {
	w.open("symbol \"power:PWR_FLAG\"")
	w.line("(power)")
	w.line("(pin_names (offset 0))")
	w.line("(in_bom yes)")
	w.line("(on_board yes)")
	w.open("symbol \"PWR_FLAG_0_1\"")
	w.open("polyline")
	w.line("(pts (xy 0 0) (xy 0 1.27) (xy -1.016 1.905) (xy 0 2.54) (xy 1.016 1.905) (xy 0 1.27))")
	w.line("(stroke (width 0) (type default))")
	w.line("(fill (type none))")
	w.close() // polyline
	w.open("pin power_out line")
	w.line("(at 0 0 90)")
	w.line("(length 0)")
	w.line("(name \"pwr\" (effects (font (size 1.27 1.27))))")
	w.line("(number \"1\" (effects (font (size 1.27 1.27))))")
	w.close() // pin
	w.close() // sub-symbol
	w.close() // symbol
}

// writePwrFlagInstance places a PWR_FLAG on a power port's connection point.
func writePwrFlagInstance(w *sexpWriter, doc *SchematicDoc, pp *PowerPort, sheetNum int) {
	uuid := deterministicUUID("pwrflag", fmt.Sprintf("%d-%s", sheetNum, pp.NetName))
	ref := pwrFlagRef(doc, sheetNum, pp.NetName)

	w.open(fmt.Sprintf("symbol (lib_id \"power:PWR_FLAG\") (at %.4f %.4f 0)",
		snapMM(pp.X), snapMM(pp.Y)))
	w.line("(unit 1)")
	w.line("(uuid \"%s\")", uuid)

	writeProperty(w, "Reference", ref, pp.X, pp.Y-30, 0)
	writeProperty(w, "Value", "PWR_FLAG", pp.X, pp.Y+30, 1)

	w.open("instances")
	w.open("project \"pcb-tracer\"")
	w.line("(path \"/%s\" (reference \"%s\") (unit 1))",
		deterministicUUID("root", "sheet-1"), ref)
	w.close()
	w.close()

	w.close() // symbol
}

// --- Global label (off-sheet connector) writing ---

func writeGlobalLabel(w *sexpWriter, osc *OffSheetConnector) {
	uuid := deterministicUUID("glabel", fmt.Sprintf("%s-%d", osc.NetID, osc.Sheet))

	shape := "bidirectional"
	switch osc.Direction {
	case "input":
		shape = "input"
	case "output":
		shape = "output"
	}

	angle := osc.Rotation

	w.open(fmt.Sprintf("global_label \"%s\" (shape %s) (at %.4f %.4f %d)",
		escapeString(osc.NetName), shape, snapMM(osc.X), snapMM(osc.Y), angle))
	w.line("(effects (font (size 1.27 1.27)))")
	w.line("(uuid \"%s\")", uuid)
	w.line("(property \"Intersheetrefs\" \"${INTERSHEET_REFS}\"")
	w.depth++
	w.line("(at %.4f %.4f 0)", toMM(osc.X+40), toMM(osc.Y))
	w.line("(effects (font (size 1.27 1.27)) hide)")
	w.depth--
	w.line(")")
	w.close()
}

// --- Multi-sheet hierarchy ---

// writeSheetReference writes a hierarchical sheet reference in the root file.
func writeSheetReference(w *sexpWriter, doc *SchematicDoc, projectPath string, sh Sheet, index int) {
	uuid := deterministicUUID("sheet", fmt.Sprintf("sheet-%d", sh.Number))
	subFile := filepath.Base(kicadSchPath(projectPath, sh.Number))

	// Position sheets in a row
	x := 20.0 + float64(index)*60.0
	y := 20.0

	w.open(fmt.Sprintf("sheet (at %.4f %.4f) (size 50 30)", x, y))
	w.line("(stroke (width 0.1524) (type solid) (color 0 0 0 0))")
	w.line("(fill (type color) (color 255 255 255 0))")
	w.line("(uuid \"%s\")", uuid)

	w.line("(property \"Sheetname\" \"%s\"", escapeString(sh.Name))
	w.depth++
	w.line("(at %.4f %.4f 0)", x+1, y+1)
	w.line("(effects (font (size 1.27 1.27)))")
	w.depth--
	w.line(")")

	w.line("(property \"Sheetfile\" \"%s\"", escapeString(subFile))
	w.depth++
	w.line("(at %.4f %.4f 0)", x+1, y+28)
	w.line("(effects (font (size 1.27 1.27)))")
	w.depth--
	w.line(")")

	// No hierarchical sheet pins: cross-sheet nets connect via global labels,
	// so the sheet symbol is only a navigation aid. Mixing sheet pins with
	// global labels triggers hier_label_mismatch ERC errors.

	w.close() // sheet
}

// writeSheetInstances writes the sheet_instances section.
func writeSheetInstances(w *sexpWriter, sheets []Sheet) {
	w.open("sheet_instances")
	// Root sheet
	w.line("(path \"/\" (page \"1\"))")
	// Sub-sheets
	for _, sh := range sheets {
		if sh.Number <= 1 {
			continue
		}
		uuid := deterministicUUID("sheet", fmt.Sprintf("sheet-%d", sh.Number))
		w.line("(path \"/%s\" (page \"%d\"))", uuid, sh.Number)
	}
	w.close()
}

// --- Helpers ---

// sheetBounds computes the bounding box for elements on a specific sheet.
func sheetBounds(doc *SchematicDoc, sheetNum int) (minX, minY, maxX, maxY float64) {
	minX, minY = 1e9, 1e9
	maxX, maxY = -1e9, -1e9
	found := false

	for _, sym := range doc.Symbols {
		if effectiveSheet(sym.Sheet) != sheetNum {
			continue
		}
		found = true
		for _, pin := range sym.Pins {
			if pin.X < minX {
				minX = pin.X
			}
			if pin.Y < minY {
				minY = pin.Y
			}
			if pin.X > maxX {
				maxX = pin.X
			}
			if pin.Y > maxY {
				maxY = pin.Y
			}
		}
		if sym.X-100 < minX {
			minX = sym.X - 100
		}
		if sym.Y-100 < minY {
			minY = sym.Y - 100
		}
		if sym.X+200 > maxX {
			maxX = sym.X + 200
		}
		if sym.Y+100 > maxY {
			maxY = sym.Y + 100
		}
	}

	if !found {
		return 0, 0, 1000, 1000
	}

	minX -= 100
	minY -= 100
	maxX += 100
	maxY += 100
	return
}

// countComponentUnits counts how many PlacedSymbol entries share a ComponentID on a sheet.
func countComponentUnits(doc *SchematicDoc, compID string, sheetNum int) int {
	n := 0
	for _, sym := range doc.Symbols {
		if sym.ComponentID == compID {
			n++
		}
	}
	return n
}

// componentUnitsOnSheet returns all PlacedSymbols for a component, across all sheets.
// We need all units for the lib_symbol definition regardless of sheet.
func componentUnitsOnSheet(doc *SchematicDoc, compID string, sheetNum int) []*PlacedSymbol {
	// For lib_symbol defs, collect all units of this component (any sheet)
	// but deduplicate by FunctionName to avoid repeats from multi-sheet overlap.
	seen := make(map[string]bool)
	var result []*PlacedSymbol
	for _, sym := range doc.Symbols {
		if sym.ComponentID == compID && !seen[sym.FunctionName] {
			seen[sym.FunctionName] = true
			result = append(result, sym)
		}
	}
	return result
}

// KiCadFootprintPaths returns, for each PCB component with a schematic
// symbol, the KiCad KIID path of one of its units ("/<unit-uuid>", prefixed
// with the sheet uuid for sub-sheets). Board footprints carrying these paths
// associate with their symbols in "Update PCB from Schematic".
func KiCadFootprintPaths(doc *SchematicDoc) map[string]string {
	out := make(map[string]string)
	for _, sym := range doc.Symbols {
		if sym.GateType == "CONNECTOR" {
			continue
		}
		if _, ok := out[sym.ComponentID]; ok {
			continue
		}
		path := "/" + deterministicUUID("symbol", sym.ID)
		if s := effectiveSheet(sym.Sheet); s > 1 {
			path = "/" + deterministicUUID("sheet", fmt.Sprintf("sheet-%d", s)) + path
		}
		out[sym.ComponentID] = path
	}
	return out
}

// escapeString escapes special characters for KiCad S-expression strings.
func escapeString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}
