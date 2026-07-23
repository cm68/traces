// Package kicad exports detection state as KiCad board files, seeding a
// KiCad project with the physical layout recovered from board scans.
package kicad

import (
	"crypto/sha256"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"pcb-tracer/internal/app"
	"pcb-tracer/internal/component"
	pcbimage "pcb-tracer/internal/image"
	"pcb-tracer/internal/trace"
)

// writer builds indented S-expression output.
type writer struct {
	sb    strings.Builder
	depth int
}

func (w *writer) indent() {
	for i := 0; i < w.depth; i++ {
		w.sb.WriteString("  ")
	}
}

func (w *writer) open(tag string) {
	w.indent()
	w.sb.WriteString("(")
	w.sb.WriteString(tag)
	w.sb.WriteString("\n")
	w.depth++
}

func (w *writer) close() {
	w.depth--
	w.indent()
	w.sb.WriteString(")\n")
}

func (w *writer) line(format string, args ...any) {
	w.indent()
	fmt.Fprintf(&w.sb, format, args...)
	w.sb.WriteString("\n")
}

// uuid generates a stable UUID from a namespace and id string.
func uuid(namespace, id string) string {
	h := sha256.Sum256([]byte("pcb:" + namespace + ":" + id))
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		h[0:4], h[4:6],
		[]byte{(h[6] & 0x0f) | 0x50, h[7]},
		[]byte{(h[8] & 0x3f) | 0x80, h[9]},
		h[10:16])
}

func escape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}

// BoardPath returns the .kicad_pcb path for a project, alongside the
// schematic export (shared "<base>_schematic" basename ties them into one
// KiCad project).
func BoardPath(projectPath string) string {
	if projectPath == "" {
		return ""
	}
	base := strings.TrimSuffix(projectPath, ".pcbproj")
	base = strings.TrimSuffix(base, ".json")
	return base + "_schematic.kicad_pcb"
}

// ProjectPath returns the .kicad_pro path for a project.
func ProjectPath(projectPath string) string {
	p := BoardPath(projectPath)
	if p == "" {
		return ""
	}
	return strings.TrimSuffix(p, ".kicad_pcb") + ".kicad_pro"
}

// ExportBoard writes a .kicad_pcb seeded from the detected components, vias,
// traces, and edge connectors. Coordinates convert from image pixels to mm
// via the scan DPI, so the board is dimensionally faithful.
//
// fpPaths maps component IDs to their schematic symbol KIID paths (see
// schematic.KiCadFootprintPaths); footprints carrying them associate with
// their symbols in "Update PCB from Schematic". May be nil.
func ExportBoard(state *app.State, fpPaths map[string]string, path string) error {
	if state == nil || state.FeaturesLayer == nil {
		return fmt.Errorf("no detection data to export")
	}
	if path == "" {
		return fmt.Errorf("no output path")
	}

	dpi := state.DPI
	if dpi <= 0 {
		dpi = 600
	}
	mm := func(px float64) float64 { return px * 25.4 / dpi }

	fl := state.FeaturesLayer
	nets := fl.GetNets()

	// Net numbering: KiCad net 0 is always the unconnected net.
	netNum := make(map[string]int)   // netID → KiCad net number
	netName := make(map[string]string)
	ordered := make([]string, 0, len(nets))
	for _, n := range nets {
		ordered = append(ordered, n.ID)
		name := n.Name
		if name == "" {
			name = n.ID
		}
		netName[n.ID] = name
	}
	sort.Strings(ordered)
	for i, id := range ordered {
		netNum[id] = i + 1
	}

	// Pad → net index ("U3.5" → netID), from net pad lists and via pin links.
	padToNet := make(map[string]string)
	for _, n := range nets {
		for _, padID := range n.PadIDs {
			padToNet[padID] = n.ID
		}
		for _, viaID := range n.ViaIDs {
			cv := fl.GetConfirmedViaByID(viaID)
			if cv == nil || cv.ComponentID == "" || cv.PinNumber == "" {
				continue
			}
			padToNet[cv.ComponentID+"."+cv.PinNumber] = n.ID
		}
	}

	// netRef formats a pad's net reference "(net N "name")", or net 0.
	// Segments and vias use the bare-number form via netNumOf.
	netRef := func(netID string) string {
		if num, ok := netNum[netID]; ok {
			return fmt.Sprintf("(net %d \"%s\")", num, escape(netName[netID]))
		}
		return "(net 0 \"\")"
	}
	netNumOf := func(netID string) int {
		return netNum[netID] // zero value = unconnected net 0
	}

	w := &writer{}
	w.open("kicad_pcb")
	w.line("(version 20240108)")
	w.line("(generator \"pcb-tracer\")")
	w.line("(generator_version \"0.1\")")
	w.open("general")
	w.line("(thickness 1.6)")
	w.line("(legacy_teardrops no)")
	w.close()
	w.line("(paper \"A3\")")
	writeLayerTable(w)
	w.open("setup")
	w.line("(pad_to_mask_clearance 0)")
	w.close()

	// Nets
	w.line("(net 0 \"\")")
	for _, id := range ordered {
		w.line("(net %d \"%s\")", netNum[id], escape(netName[id]))
	}

	// Track the extent of everything placed, for the board outline.
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	extend := func(xPx, yPx float64) {
		x, y := mm(xPx), mm(yPx)
		minX, minY = math.Min(minX, x), math.Min(minY, y)
		maxX, maxY = math.Max(maxX, x), math.Max(maxY, y)
	}

	// Component footprints
	for _, comp := range state.Components {
		writeComponentFootprint(w, comp, dpi, mm, padToNet, netRef, extend, fpPaths[comp.ID])
	}

	// Edge connector contacts as one footprint of surface pads
	writeConnectorFootprint(w, state, mm, netRef, extend)

	// Vias
	for _, cv := range fl.GetConfirmedVias() {
		size := mm(cv.Radius * 2)
		if size < 0.6 {
			size = 0.6
		}
		drill := size * 0.5
		if drill < 0.3 {
			drill = 0.3
		}
		netID := ""
		if n := fl.GetNetForElement(cv.ID); n != nil {
			netID = n.ID
		}
		extend(cv.Center.X, cv.Center.Y)
		w.line("(via (at %.4f %.4f) (size %.3f) (drill %.3f) (layers \"F.Cu\" \"B.Cu\") (net %d) (uuid \"%s\"))",
			mm(cv.Center.X), mm(cv.Center.Y), size, drill, netNumOf(netID), uuid("via", cv.ID))
	}

	// Trace segments
	for _, tf := range fl.GetAllTraces() {
		if len(tf.Points) < 2 {
			continue
		}
		layer := "F.Cu"
		if tf.Layer == trace.LayerBack {
			layer = "B.Cu"
		}
		width := mm(tf.Width)
		if width < 0.2 {
			width = 0.25
		}
		netID := ""
		if n := fl.GetNetForElement(tf.ID); n != nil {
			netID = n.ID
		}
		for i := 1; i < len(tf.Points); i++ {
			p, q := tf.Points[i-1], tf.Points[i]
			extend(p.X, p.Y)
			extend(q.X, q.Y)
			w.line("(segment (start %.4f %.4f) (end %.4f %.4f) (width %.3f) (layer \"%s\") (net %d) (uuid \"%s\"))",
				mm(p.X), mm(p.Y), mm(q.X), mm(q.Y), width, layer,
				netNumOf(netID), uuid("seg", fmt.Sprintf("%s-%d", tf.ID, i)))
		}
	}

	// Board outline: bounding box of everything placed, with margin.
	// The user refines Edge.Cuts in pcbnew; this seeds a sensible extent.
	if !math.IsInf(minX, 1) {
		const margin = 2.0
		w.line("(gr_rect (start %.4f %.4f) (end %.4f %.4f) (stroke (width 0.1) (type default)) (fill none) (layer \"Edge.Cuts\") (uuid \"%s\"))",
			minX-margin, minY-margin, maxX+margin, maxY+margin, uuid("edge", "outline"))
	}

	w.close() // kicad_pcb
	return os.WriteFile(path, []byte(w.sb.String()), 0644)
}

// writeLayerTable emits the standard 2-layer board layer set.
func writeLayerTable(w *writer) {
	w.open("layers")
	w.line("(0 \"F.Cu\" signal)")
	w.line("(31 \"B.Cu\" signal)")
	w.line("(32 \"B.Adhes\" user \"B.Adhesive\")")
	w.line("(33 \"F.Adhes\" user \"F.Adhesive\")")
	w.line("(34 \"B.Paste\" user)")
	w.line("(35 \"F.Paste\" user)")
	w.line("(36 \"B.SilkS\" user \"B.Silkscreen\")")
	w.line("(37 \"F.SilkS\" user \"F.Silkscreen\")")
	w.line("(38 \"B.Mask\" user)")
	w.line("(39 \"F.Mask\" user)")
	w.line("(40 \"Dwgs.User\" user \"User.Drawings\")")
	w.line("(41 \"Cmts.User\" user \"User.Comments\")")
	w.line("(44 \"Edge.Cuts\" user)")
	w.line("(46 \"B.CrtYd\" user \"B.Courtyard\")")
	w.line("(47 \"F.CrtYd\" user \"F.Courtyard\")")
	w.line("(48 \"B.Fab\" user)")
	w.line("(49 \"F.Fab\" user)")
	w.close()
}

// writeComponentFootprint emits one footprint with pads at the detected pin
// positions (falling back to ideal DIP geometry when pins were not detected).
func writeComponentFootprint(w *writer, comp *component.Component, dpi float64,
	mm func(float64) float64, padToNet map[string]string,
	netRef func(string) string, extend func(x, y float64), symPath string) {

	cx := comp.Bounds.X + comp.Bounds.Width/2
	cy := comp.Bounds.Y + comp.Bounds.Height/2

	// Pin positions in absolute image coordinates.
	type pad struct {
		num  int
		x, y float64
	}
	var pads []pad
	for _, p := range comp.Pins {
		pads = append(pads, pad{p.Number, p.Position.X, p.Position.Y})
	}
	if len(pads) == 0 {
		for _, ep := range component.ExpectedDIPPinPositions(comp, dpi) {
			pads = append(pads, pad{ep.Number, ep.Position.X, ep.Position.Y})
		}
	}

	fpName := comp.Package
	if fpName == "" {
		fpName = "UNKNOWN"
	}

	layer := "F.Cu"
	if comp.Layer == pcbimage.SideBack {
		layer = "B.Cu"
	}

	w.open(fmt.Sprintf("footprint \"pcb-tracer:%s\" (layer \"%s\")", escape(fpName), layer))
	w.line("(uuid \"%s\")", uuid("fp", comp.ID))
	w.line("(at %.4f %.4f)", mm(cx), mm(cy))
	if symPath != "" {
		w.line("(path \"%s\")", symPath)
	}
	w.line("(attr through_hole)")

	w.line("(property \"Reference\" \"%s\"", escape(comp.ID))
	w.depth++
	w.line("(at 0 %.4f 0)", mm(-comp.Bounds.Height/2)-1)
	w.line("(layer \"F.SilkS\")")
	w.line("(uuid \"%s\")", uuid("fpref", comp.ID))
	w.line("(effects (font (size 1 1) (thickness 0.15)))")
	w.depth--
	w.line(")")

	w.line("(property \"Value\" \"%s\"", escape(comp.PartNumber))
	w.depth++
	w.line("(at 0 %.4f 0)", mm(comp.Bounds.Height/2)+1)
	w.line("(layer \"F.Fab\")")
	w.line("(uuid \"%s\")", uuid("fpval", comp.ID))
	w.line("(effects (font (size 1 1) (thickness 0.15)))")
	w.depth--
	w.line(")")

	// Body outline on silkscreen from the detected bounds.
	w.line("(fp_rect (start %.4f %.4f) (end %.4f %.4f) (stroke (width 0.12) (type default)) (fill none) (layer \"F.SilkS\") (uuid \"%s\"))",
		mm(-comp.Bounds.Width/2), mm(-comp.Bounds.Height/2),
		mm(comp.Bounds.Width/2), mm(comp.Bounds.Height/2),
		uuid("fpbody", comp.ID))
	extend(comp.Bounds.X, comp.Bounds.Y)
	extend(comp.Bounds.X+comp.Bounds.Width, comp.Bounds.Y+comp.Bounds.Height)

	for _, p := range pads {
		shape := "circle"
		if p.num == 1 {
			shape = "rect"
		}
		netID := padToNet[fmt.Sprintf("%s.%d", comp.ID, p.num)]
		extend(p.x, p.y)
		w.line("(pad \"%d\" thru_hole %s (at %.4f %.4f) (size 1.6 1.6) (drill 0.8) (layers \"*.Cu\" \"*.Mask\") %s (uuid \"%s\"))",
			p.num, shape, mm(p.x-cx), mm(p.y-cy), netRef(netID),
			uuid("pad", fmt.Sprintf("%s-%d", comp.ID, p.num)))
	}

	w.close() // footprint
}

// writeConnectorFootprint emits the board-edge contacts as a single footprint
// of surface pads on the appropriate copper side.
func writeConnectorFootprint(w *writer, state *app.State,
	mm func(float64) float64, netRef func(string) string, extend func(x, y float64)) {

	conns := state.FeaturesLayer.GetConnectors()
	if len(conns) == 0 {
		return
	}

	w.open("footprint \"pcb-tracer:EDGE_CONN\" (layer \"F.Cu\")")
	w.line("(uuid \"%s\")", uuid("fp", "edge-conn"))
	w.line("(at 0 0)")
	// board_only: the schematic represents the edge connector as per-signal
	// port symbols, not one component — keep Update PCB from flagging it.
	w.line("(attr exclude_from_pos_files board_only exclude_from_bom)")

	w.line("(property \"Reference\" \"J1\"")
	w.depth++
	w.line("(at 0 -2 0)")
	w.line("(layer \"F.SilkS\")")
	w.line("(uuid \"%s\")", uuid("fpref", "edge-conn"))
	w.line("(effects (font (size 1 1) (thickness 0.15)))")
	w.depth--
	w.line(")")

	w.line("(property \"Value\" \"EDGE\"")
	w.depth++
	w.line("(at 0 2 0)")
	w.line("(layer \"F.Fab\")")
	w.line("(uuid \"%s\")", uuid("fpval", "edge-conn"))
	w.line("(effects (font (size 1 1) (thickness 0.15)))")
	w.depth--
	w.line(")")

	for _, c := range conns {
		layer := "F.Cu"
		if c.Side == pcbimage.SideBack {
			layer = "B.Cu"
		}
		cw := mm(float64(c.Bounds.Width))
		ch := mm(float64(c.Bounds.Height))
		if cw < 0.5 {
			cw = 1.5
		}
		if ch < 0.5 {
			ch = 6.0
		}
		netID := c.NetID
		if netID == "" {
			if n := state.FeaturesLayer.GetNetForElement(c.ID); n != nil {
				netID = n.ID
			}
		}
		extend(c.Center.X, c.Center.Y)
		w.line("(pad \"%d\" smd rect (at %.4f %.4f) (size %.3f %.3f) (layers \"%s\") %s (uuid \"%s\"))",
			c.PinNumber, mm(c.Center.X), mm(c.Center.Y), cw, ch, layer,
			netRef(netID), uuid("cpad", c.ID))
	}

	w.close() // footprint
}

// ExportProject writes a minimal .kicad_pro so KiCad opens the schematic and
// board as one project.
func ExportProject(projectPath string) error {
	path := ProjectPath(projectPath)
	if path == "" {
		return nil
	}
	if _, err := os.Stat(path); err == nil {
		return nil // never clobber an existing project file (user settings live there)
	}
	content := "{\n  \"meta\": {\n    \"filename\": \"" + escape(filepath.Base(path)) + "\",\n    \"version\": 3\n  }\n}\n"
	return os.WriteFile(path, []byte(content), 0644)
}
