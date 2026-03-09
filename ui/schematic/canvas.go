package schematic

import (
	"fmt"
	"math"

	"pcb-tracer/pkg/geometry"

	"github.com/gotk3/gotk3/cairo"
	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

const (
	schMinZoom  = 0.1
	schMaxZoom  = 5.0
	schZoomStep = 1.15
	gridSnap    = 25.0
)

// SchematicCanvas is an interactive schematic drawing widget.
type SchematicCanvas struct {
	drawArea  *gtk.DrawingArea
	scrollWin *gtk.ScrolledWindow

	doc      *SchematicDoc
	sheetNum int           // Which sheet this canvas shows (1-based)
	manager  *SheetManager // Multi-window coordinator (nil for single-window)

	// View transform
	zoom float64
	// Content size in screen pixels (doc bounds * zoom)
	contentW int
	contentH int
	// Doc origin offset (so doc min coords map to 0,0 in content space)
	originX float64
	originY float64

	// Interaction state
	dragging              bool
	dragSymbol            *PlacedSymbol
	dragOffSheetConnector *OffSheetConnector
	dragWireCorner        *Wire // wire whose corner is being dragged
	dragCornerIdx         int   // index of the dragged corner in wire.Points
	dragOffsetX           float64 // offset from mouse to symbol/origin
	dragOffsetY           float64

	// Middle-button pan
	middleDragging bool
	panLastX       float64
	panLastY       float64

	// Selection
	selected map[string]bool // symbol IDs

	// Callbacks
	onStatusUpdate  func(string)
	onLayoutChanged func()                      // Called when symbol position or flip changes
	onNetRenamed    func(netID, newName string) // Called when a net is renamed via schematic
}

// NewSchematicCanvas creates a new schematic canvas widget.
func NewSchematicCanvas(doc *SchematicDoc) *SchematicCanvas {
	sc := &SchematicCanvas{
		doc:      doc,
		zoom:     1.0,
		selected: make(map[string]bool),
	}

	da, _ := gtk.DrawingAreaNew()
	sc.drawArea = da

	da.AddEvents(int(
		gdk.BUTTON_PRESS_MASK |
			gdk.BUTTON_RELEASE_MASK |
			gdk.POINTER_MOTION_MASK |
			gdk.SCROLL_MASK))

	// Drawing callback — pure Cairo vector rendering
	da.Connect("draw", func(da *gtk.DrawingArea, cr *cairo.Context) {
		alloc := da.GetAllocation()
		w, h := alloc.GetWidth(), alloc.GetHeight()
		if w <= 0 || h <= 0 {
			return
		}
		sc.render(cr, w, h)
	})

	// Mouse button press
	da.Connect("button-press-event", func(da *gtk.DrawingArea, ev *gdk.Event) bool {
		btn := gdk.EventButtonNewFromEvent(ev)
		x, y := btn.X(), btn.Y()
		schX, schY := sc.screenToSchematic(x, y)

		switch btn.Button() {
		case 1: // Left click — select/drag
			sc.onLeftPress(schX, schY)
		case 2: // Middle — start pan
			sc.middleDragging = true
			sc.panLastX = x
			sc.panLastY = y
		case 3: // Right click — context menu
			sc.onRightPress(schX, schY, ev)
		}
		return true
	})

	// Mouse button release
	da.Connect("button-release-event", func(da *gtk.DrawingArea, ev *gdk.Event) bool {
		btn := gdk.EventButtonNewFromEvent(ev)
		switch btn.Button() {
		case 1:
			sc.onLeftRelease()
		case 2:
			sc.middleDragging = false
		}
		return true
	})

	// Mouse motion
	da.Connect("motion-notify-event", func(da *gtk.DrawingArea, ev *gdk.Event) bool {
		motion := gdk.EventMotionNewFromEvent(ev)
		x, y := motion.MotionVal()

		// Middle-button pan
		if sc.middleDragging {
			dx := x - sc.panLastX
			dy := y - sc.panLastY
			sc.panLastX = x
			sc.panLastY = y

			hadj := sc.scrollWin.GetHAdjustment()
			vadj := sc.scrollWin.GetVAdjustment()
			hadj.SetValue(hadj.GetValue() - dx)
			vadj.SetValue(vadj.GetValue() - dy)
			return true
		}

		// Dragging a symbol, off-sheet connector, or wire corner
		if sc.dragging && (sc.dragSymbol != nil || sc.dragOffSheetConnector != nil || sc.dragWireCorner != nil) {
			schX, schY := sc.screenToSchematic(x, y)
			sc.onDragMove(schX, schY)
			return true
		}

		return false
	})

	// Scroll for zoom
	da.Connect("scroll-event", func(da *gtk.DrawingArea, ev *gdk.Event) bool {
		scroll := gdk.EventScrollNewFromEvent(ev)
		evtX, evtY := scroll.X(), scroll.Y()
		schX, schY := sc.screenToSchematic(evtX, evtY)

		switch scroll.Direction() {
		case gdk.SCROLL_UP:
			sc.zoomAtPoint(sc.zoom*schZoomStep, schX, schY, evtX, evtY)
		case gdk.SCROLL_DOWN:
			sc.zoomAtPoint(sc.zoom/schZoomStep, schX, schY, evtX, evtY)
		}
		return true
	})

	// Wrap in ScrolledWindow
	sw, _ := gtk.ScrolledWindowNew(nil, nil)
	sw.SetPolicy(gtk.POLICY_AUTOMATIC, gtk.POLICY_AUTOMATIC)
	sw.Add(da)
	sc.scrollWin = sw

	sc.updateContentSize()

	return sc
}

// Widget returns the GTK widget for embedding.
func (sc *SchematicCanvas) Widget() gtk.IWidget {
	return sc.scrollWin
}

// SetDoc sets the schematic document and refreshes.
func (sc *SchematicCanvas) SetDoc(doc *SchematicDoc) {
	sc.doc = doc
	sc.updateContentSize()
	sc.drawArea.QueueDraw()
}

// Refresh triggers a redraw.
func (sc *SchematicCanvas) Refresh() {
	sc.drawArea.QueueDraw()
}

// FitToWindow adjusts zoom to fit the schematic in the visible area.
func (sc *SchematicCanvas) FitToWindow() {
	if sc.doc == nil {
		return
	}
	alloc := sc.scrollWin.GetAllocation()
	vw := float64(alloc.GetWidth())
	vh := float64(alloc.GetHeight())
	if vw <= 0 || vh <= 0 {
		return
	}

	minX, minY, maxX, maxY := sc.doc.Bounds()
	docW := maxX - minX
	docH := maxY - minY
	if docW <= 0 || docH <= 0 {
		return
	}

	zoomX := vw / docW
	zoomY := vh / docH
	zoom := math.Min(zoomX, zoomY) * 0.95
	sc.zoom = zoom
	sc.updateContentSize()
	sc.drawArea.QueueDraw()
}

// GetZoom returns the current zoom level.
func (sc *SchematicCanvas) GetZoom() float64 {
	return sc.zoom
}

// SetZoom sets the zoom level.
func (sc *SchematicCanvas) SetZoom(z float64) {
	if z < schMinZoom {
		z = schMinZoom
	}
	if z > schMaxZoom {
		z = schMaxZoom
	}
	sc.zoom = z
	sc.updateContentSize()
	sc.drawArea.QueueDraw()
}

// OnStatusUpdate sets a callback for status bar updates.
func (sc *SchematicCanvas) OnStatusUpdate(fn func(string)) {
	sc.onStatusUpdate = fn
}

// OnLayoutChanged sets a callback for when symbol layout changes (drag, flip).
func (sc *SchematicCanvas) OnLayoutChanged(fn func()) {
	sc.onLayoutChanged = fn
}

// OnNetRenamed sets a callback for when a net is renamed via the schematic.
func (sc *SchematicCanvas) OnNetRenamed(fn func(netID, newName string)) {
	sc.onNetRenamed = fn
}

// --- Coordinate transforms ---

// screenToSchematic converts screen (DrawingArea) coordinates to schematic coordinates.
func (sc *SchematicCanvas) screenToSchematic(sx, sy float64) (float64, float64) {
	return sx/sc.zoom + sc.originX, sy/sc.zoom + sc.originY
}

// schematicToScreen converts schematic coordinates to screen (DrawingArea) coordinates.
func (sc *SchematicCanvas) schematicToScreen(schX, schY float64) (float64, float64) {
	return (schX - sc.originX) * sc.zoom, (schY - sc.originY) * sc.zoom
}

// updateContentSize recomputes the drawing area size from doc bounds and zoom.
func (sc *SchematicCanvas) updateContentSize() {
	if sc.doc == nil {
		sc.contentW = 1000
		sc.contentH = 1000
		sc.originX = 0
		sc.originY = 0
		sc.drawArea.SetSizeRequest(sc.contentW, sc.contentH)
		return
	}

	minX, minY, maxX, maxY := sc.doc.Bounds()
	sc.originX = minX
	sc.originY = minY
	docW := maxX - minX
	docH := maxY - minY
	sc.contentW = int(docW*sc.zoom) + 100
	sc.contentH = int(docH*sc.zoom) + 100
	if sc.contentW < 800 {
		sc.contentW = 800
	}
	if sc.contentH < 600 {
		sc.contentH = 600
	}
	sc.drawArea.SetSizeRequest(sc.contentW, sc.contentH)
}

// zoomAtPoint zooms to newZoom keeping the schematic point (schX, schY) under the cursor.
func (sc *SchematicCanvas) zoomAtPoint(newZoom, schX, schY, evtX, evtY float64) {
	if newZoom < schMinZoom {
		newZoom = schMinZoom
	}
	if newZoom > schMaxZoom {
		newZoom = schMaxZoom
	}

	hadj := sc.scrollWin.GetHAdjustment()
	vadj := sc.scrollWin.GetVAdjustment()

	// Cursor position relative to viewport
	vpX := evtX - hadj.GetValue()
	vpY := evtY - vadj.GetValue()

	sc.zoom = newZoom
	sc.updateContentSize()

	// Scroll to keep schematic point under cursor
	newScrollX := (schX-sc.originX)*newZoom - vpX
	newScrollY := (schY-sc.originY)*newZoom - vpY

	glib.IdleAdd(func() {
		hadj.SetValue(newScrollX)
		vadj.SetValue(newScrollY)
	})
}

// --- Interaction ---

func (sc *SchematicCanvas) onLeftPress(schX, schY float64) {
	// Hit test wire corners first (small targets, check before wire segments)
	if wire, idx := sc.hitTestWireCorner(schX, schY); wire != nil {
		sc.clearSelection()
		wire.Selected = true
		sc.dragging = true
		sc.dragWireCorner = wire
		sc.dragCornerIdx = idx
		sc.drawArea.QueueDraw()
		return
	}

	// Hit test off-sheet connectors
	osc := sc.hitTestOffSheetConnector(schX, schY)
	if osc != nil {
		sc.clearSelection()
		osc.Selected = true
		sc.dragging = true
		sc.dragOffSheetConnector = osc
		sc.dragOffsetX = schX - osc.X
		sc.dragOffsetY = schY - osc.Y
		sc.drawArea.QueueDraw()
		return
	}

	// Hit test symbols
	sym := sc.hitTestSymbol(schX, schY)
	if sym != nil {
		// Select and start drag
		sc.clearSelection()
		sym.Selected = true
		sc.selected[sym.ID] = true
		sc.dragging = true
		sc.dragSymbol = sym
		sc.dragOffsetX = schX - sym.X
		sc.dragOffsetY = schY - sym.Y
		sc.drawArea.QueueDraw()
		return
	}

	// Nothing hit — clear selection
	sc.clearSelection()
	sc.drawArea.QueueDraw()
}

func (sc *SchematicCanvas) onLeftRelease() {
	if sc.dragging && (sc.dragSymbol != nil || sc.dragOffSheetConnector != nil || sc.dragWireCorner != nil) {
		if sc.onLayoutChanged != nil {
			sc.onLayoutChanged()
		}
	}
	sc.dragging = false
	sc.dragSymbol = nil
	sc.dragOffSheetConnector = nil
	sc.dragWireCorner = nil
	sc.dragCornerIdx = 0
}

func (sc *SchematicCanvas) onDragMove(schX, schY float64) {
	// Handle wire corner dragging
	if sc.dragWireCorner != nil {
		newX := math.Round(schX/gridSnap) * gridSnap
		newY := math.Round(schY/gridSnap) * gridSnap
		sc.dragWireCorner.Points[sc.dragCornerIdx] = geometry.Point2D{X: newX, Y: newY}
		sc.drawArea.QueueDraw()
		return
	}

	// Handle off-sheet connector dragging
	if sc.dragOffSheetConnector != nil {
		// Snap to grid
		newX := math.Round((schX-sc.dragOffsetX)/gridSnap) * gridSnap
		newY := math.Round((schY-sc.dragOffsetY)/gridSnap) * gridSnap
		sc.dragOffSheetConnector.X = newX
		sc.dragOffSheetConnector.Y = newY

		// Re-route affected wires
		sc.rerouteWiresForOffSheetConnector(sc.dragOffSheetConnector)
		sc.drawArea.QueueDraw()
		return
	}

	// Handle symbol dragging
	if sc.dragSymbol == nil {
		return
	}

	// Snap to grid
	newX := math.Round((schX-sc.dragOffsetX)/gridSnap) * gridSnap
	newY := math.Round((schY-sc.dragOffsetY)/gridSnap) * gridSnap
	sc.dragSymbol.X = newX
	sc.dragSymbol.Y = newY

	// Recompute pin positions
	def := GetSymbolDef(sc.dragSymbol.GateType,
		countPinsByDir(sc.dragSymbol, "input"),
		countPinsByDir(sc.dragSymbol, "output"),
		countPinsByDir(sc.dragSymbol, "enable"),
		countPinsByDir(sc.dragSymbol, "clock"))
	ComputePinPositions(sc.dragSymbol, def)

	// Update power port positions so GND/VCC symbols follow their pins.
	positionPowerPorts(sc.doc)

	// Re-route connected wires
	sc.rerouteWiresForSymbol(sc.dragSymbol)

	sc.drawArea.QueueDraw()
}

func (sc *SchematicCanvas) clearSelection() {
	for id := range sc.selected {
		if sym := sc.doc.SymbolByID(id); sym != nil {
			sym.Selected = false
		}
	}
	sc.selected = make(map[string]bool)
	// Clear off-sheet connector selection
	if sc.doc != nil {
		for _, osc := range sc.doc.OffSheetConnectors {
			osc.Selected = false
		}
	}
	// Clear wire selection
	if sc.doc != nil {
		for _, w := range sc.doc.Wires {
			w.Selected = false
		}
	}
}

// hitTestOffSheetConnector returns the off-sheet connector at (x,y) or nil.
func (sc *SchematicCanvas) hitTestOffSheetConnector(x, y float64) *OffSheetConnector {
	if sc.doc == nil {
		return nil
	}
	// Check in reverse order (last drawn = on top)
	oscs := sc.visibleOffSheetConnectors()
	for i := len(oscs) - 1; i >= 0; i-- {
		osc := oscs[i]
		w := 80.0
		h := 20.0
		// Hit test the bounding box of the connector
		if x >= osc.X-10 && x <= osc.X+w+15 && y >= osc.Y-h/2-10 && y <= osc.Y+h/2+10 {
			return osc
		}
	}
	return nil
}

// rerouteWiresForOffSheetConnector rebuilds wires for the net connected to an off-sheet connector.
func (sc *SchematicCanvas) rerouteWiresForOffSheetConnector(osc *OffSheetConnector) {
	if sc.doc == nil {
		return
	}

	// Route wires for the affected net
	affectedNets := make(map[string]bool)
	affectedNets[osc.NetID] = true

	// Remove wires for affected net from current sheet
	kept := sc.doc.Wires[:0]
	for _, w := range sc.doc.Wires {
		if !affectedNets[w.NetID] || effectiveSheet(w.Sheet) != sc.sheetNum {
			kept = append(kept, w)
		}
	}
	sc.doc.Wires = kept

	// Rebuild wires for current sheet
	wireID := len(sc.doc.Wires)
	sc.routeSheetWiresForSymbol(sc.sheetNum, affectedNets, wireID)
	regenerateNetLabels(sc.doc)
}

// hitTestSymbol returns the symbol at (x,y) or nil.
func (sc *SchematicCanvas) hitTestSymbol(x, y float64) *PlacedSymbol {
	if sc.doc == nil {
		return nil
	}
	// Check in reverse order (last drawn = on top)
	syms := sc.visibleSymbols()
	for i := len(syms) - 1; i >= 0; i-- {
		sym := syms[i]
		def := GetSymbolDef(sym.GateType,
			countPinsByDir(sym, "input"),
			countPinsByDir(sym, "output"),
			countPinsByDir(sym, "enable"),
			countPinsByDir(sym, "clock"))
		if def == nil {
			continue
		}
		hw := def.BodyWidth/2 + stubLength
		hh := def.BodyHeight/2 + stubLength
		if sym.Rotation == 90 || sym.Rotation == 270 {
			hw, hh = hh, hw
		}
		if x >= sym.X-hw && x <= sym.X+hw && y >= sym.Y-hh && y <= sym.Y+hh {
			return sym
		}
	}
	return nil
}

// rerouteWiresForSymbol rebuilds wires for all nets connected to a symbol.
// This properly handles multi-pin nets by recomputing the MST routing,
// and preserves wires on other sheets.
func (sc *SchematicCanvas) rerouteWiresForSymbol(sym *PlacedSymbol) {
	if sc.doc == nil {
		return
	}
	// Collect affected net IDs
	affectedNets := make(map[string]bool)
	for _, pin := range sym.Pins {
		if pin.NetID != "" {
			affectedNets[pin.NetID] = true
		}
	}

	// Find all sheets that have pins on affected nets (not just current sheet)
	affectedSheets := make(map[int]bool)
	affectedSheets[sc.sheetNum] = true // Always rebuild current sheet
	for _, sym := range sc.doc.Symbols {
		for _, pin := range sym.Pins {
			if affectedNets[pin.NetID] {
				affectedSheets[effectiveSheet(sym.Sheet)] = true
			}
		}
	}

	// Remove wires for affected nets only from affected sheets
	kept := sc.doc.Wires[:0]
	for _, w := range sc.doc.Wires {
		if !affectedNets[w.NetID] || !affectedSheets[effectiveSheet(w.Sheet)] {
			kept = append(kept, w)
		}
	}
	sc.doc.Wires = kept

	// Rebuild wires for all affected sheets
	wireID := len(sc.doc.Wires)
	for sheetNum := range affectedSheets {
		wireID = sc.routeSheetWiresForSymbol(sheetNum, affectedNets, wireID)
	}
	regenerateNetLabels(sc.doc)
}

// routeSheetWiresForSymbol routes wires on a specific sheet for affected nets.
// This is similar to routeSheetWires in route.go but handles incremental updates.
func (sc *SchematicCanvas) routeSheetWiresForSymbol(sheetNum int, affectedNets map[string]bool, wireID int) int {
	if sc.doc == nil {
		return wireID
	}

	// Build pin-to-net index for this sheet and affected nets only.
	// Skip "power"-direction pins on power nets — they use PowerPort symbols.
	netPins := make(map[string][]pinPos)
	for _, sym := range sc.doc.Symbols {
		if effectiveSheet(sym.Sheet) != sheetNum {
			continue
		}
		for _, pin := range sym.Pins {
			if pin.NetID == "" || !affectedNets[pin.NetID] {
				continue
			}
			if sc.doc.PowerNetIDs[pin.NetID] && pin.Direction == "power" {
				continue // supply pin — represented by PowerPort, not wire
			}
			netPins[pin.NetID] = append(netPins[pin.NetID], pinPos{
				X: pin.X, Y: pin.Y, Dir: pin.Direction,
			})
		}
	}

	// Include off-sheet connector positions as wire endpoints
	for _, osc := range sc.doc.OffSheetConnectors {
		if osc.Sheet != sheetNum {
			continue
		}
		if affectedNets[osc.NetID] {
			netPins[osc.NetID] = append(netPins[osc.NetID], pinPos{
				X: osc.X, Y: osc.Y, Dir: osc.Direction,
			})
		}
	}

	// Route each affected net
	for netID, pins := range netPins {
		if len(pins) < 2 {
			continue
		}

		if len(pins) == 2 {
			wireID++
			sc.doc.Wires = append(sc.doc.Wires, &Wire{
				ID:     fmt.Sprintf("wire-%d", wireID),
				NetID:  netID,
				Points: ManhattanRoute(pins[0], pins[1]),
				Sheet:  sheetNum,
			})
		} else {
			edges := mstEdges(pins)
			for _, edge := range edges {
				wireID++
				sc.doc.Wires = append(sc.doc.Wires, &Wire{
					ID:     fmt.Sprintf("wire-%d", wireID),
					NetID:  netID,
					Points: ManhattanRoute(pins[edge[0]], pins[edge[1]]),
					Sheet:  sheetNum,
				})
			}
		}
	}
	return wireID
}

// findPinsForNet returns all pin positions that belong to a net on the current sheet.
func (sc *SchematicCanvas) findPinsForNet(netID string) []pinPos {
	var result []pinPos
	for _, sym := range sc.visibleSymbols() {
		for _, pin := range sym.Pins {
			if pin.NetID == netID {
				result = append(result, pinPos{X: pin.X, Y: pin.Y, Dir: pin.Direction})
			}
		}
	}
	// Include off-sheet connector positions
	for _, osc := range sc.visibleOffSheetConnectors() {
		if osc.NetID == netID {
			result = append(result, pinPos{X: osc.X, Y: osc.Y, Dir: osc.Direction})
		}
	}
	return result
}

type pinPos struct {
	X, Y float64
	Dir  string
}

func (sc *SchematicCanvas) onRightPress(schX, schY float64, ev *gdk.Event) {
	// Try off-sheet connector first
	osc := sc.hitTestOffSheetConnector(schX, schY)
	if osc != nil {
		sc.onRightPressOffSheet(osc, ev)
		return
	}

	// Try symbol
	sym := sc.hitTestSymbol(schX, schY)
	if sym != nil {
		sc.onRightPressSymbol(sym, ev)
		return
	}

	// Try wire
	wire := sc.hitTestWire(schX, schY)
	if wire != nil {
		sc.onRightPressWire(wire, schX, schY, ev)
		return
	}
}

func (sc *SchematicCanvas) onRightPressOffSheet(osc *OffSheetConnector, ev *gdk.Event) {
	sc.clearSelection()
	osc.Selected = true
	sc.drawArea.QueueDraw()

	menu, _ := gtk.MenuNew()

	moveItem, _ := gtk.MenuItemNewWithLabel("Move")
	moveItem.Connect("activate", func() {
		// nothing special, drag will handle
	})
	menu.Append(moveItem)

	reverseItem, _ := gtk.MenuItemNewWithLabel("Reverse Direction")
	reverseItem.Connect("activate", func() {
		if osc.Direction == "input" {
			osc.Direction = "output"
		} else {
			osc.Direction = "input"
		}
		if sc.onLayoutChanged != nil {
			sc.onLayoutChanged()
		}
		sc.drawArea.QueueDraw()
	})
	menu.Append(reverseItem)

	flipHItem, _ := gtk.MenuItemNewWithLabel("Flip Horizontal")
	flipHItem.Connect("activate", func() {
		osc.FlipH = !osc.FlipH
		if sc.onLayoutChanged != nil {
			sc.onLayoutChanged()
		}
		sc.drawArea.QueueDraw()
	})
	menu.Append(flipHItem)

	flipVItem, _ := gtk.MenuItemNewWithLabel("Flip Vertical")
	flipVItem.Connect("activate", func() {
		osc.FlipV = !osc.FlipV
		if sc.onLayoutChanged != nil {
			sc.onLayoutChanged()
		}
		sc.drawArea.QueueDraw()
	})
	menu.Append(flipVItem)

	rotateItem, _ := gtk.MenuItemNewWithLabel("Rotate 90°")
	rotateItem.Connect("activate", func() {
		osc.Rotation = (osc.Rotation + 90) % 360
		if sc.onLayoutChanged != nil {
			sc.onLayoutChanged()
		}
		sc.drawArea.QueueDraw()
	})
	menu.Append(rotateItem)

	menu.ShowAll()
	menu.PopupAtPointer(ev)
}

func (sc *SchematicCanvas) onRightPressSymbol(sym *PlacedSymbol, ev *gdk.Event) {
	sc.clearSelection()
	sym.Selected = true
	sc.selected[sym.ID] = true
	sc.drawArea.QueueDraw()

	menu, _ := gtk.MenuNew()

	flipHItem, _ := gtk.MenuItemNewWithLabel("Flip Horizontal")
	flipHItem.Connect("activate", func() {
		sc.flipSymbol(sym, true, false)
	})
	menu.Append(flipHItem)

	flipVItem, _ := gtk.MenuItemNewWithLabel("Flip Vertical")
	flipVItem.Connect("activate", func() {
		sc.flipSymbol(sym, false, true)
	})
	menu.Append(flipVItem)

	rotateItem, _ := gtk.MenuItemNewWithLabel("Rotate 90°")
	rotateItem.Connect("activate", func() {
		sc.rotateSymbol(sym)
	})
	menu.Append(rotateItem)

	// "Move to Sheet" submenu (only if manager is available)
	if sc.manager != nil {
		sepItem, _ := gtk.SeparatorMenuItemNew()
		menu.Append(sepItem)

		moveMenu, _ := gtk.MenuNew()
		moveItem, _ := gtk.MenuItemNewWithLabel("Move to Sheet")
		moveItem.SetSubmenu(moveMenu)

		currentSheet := effectiveSheet(sym.Sheet)

		// Add existing sheets (excluding current)
		for _, sheet := range sc.manager.Sheets() {
			if sheet.Number == currentSheet {
				continue
			}
			s := sheet // capture
			label := fmt.Sprintf("Sheet %d: %s", s.Number, s.Name)
			item, _ := gtk.MenuItemNewWithLabel(label)
			item.Connect("activate", func() {
				sc.manager.MoveSymbolToSheet(sym.ID, s.Number)
			})
			moveMenu.Append(item)
		}

		// "New Sheet..." option
		newSheetItem, _ := gtk.MenuItemNewWithLabel("New Sheet...")
		newSheetItem.Connect("activate", func() {
			sc.promptNewSheet(sym)
		})
		moveMenu.Append(newSheetItem)

		menu.Append(moveItem)
	}

	menu.ShowAll()
	menu.PopupAtPointer(ev)
}

// promptNewSheet shows a dialog to create a new sheet and move the symbol to it.
func (sc *SchematicCanvas) promptNewSheet(sym *PlacedSymbol) {
	if sc.manager == nil {
		return
	}

	dialog, _ := gtk.DialogNewWithButtons(
		"New Sheet",
		nil,
		gtk.DIALOG_MODAL|gtk.DIALOG_DESTROY_WITH_PARENT,
		[]interface{}{"Cancel", gtk.RESPONSE_CANCEL},
		[]interface{}{"OK", gtk.RESPONSE_OK},
	)
	dialog.SetDefaultSize(300, 100)
	dialog.SetDefaultResponse(gtk.RESPONSE_OK)

	box, _ := dialog.GetContentArea()
	label, _ := gtk.LabelNew("Sheet name:")
	box.PackStart(label, false, false, 4)

	entry, _ := gtk.EntryNew()
	entry.SetText("New Sheet")
	entry.SetActivatesDefault(true)
	box.PackStart(entry, false, false, 4)

	dialog.ShowAll()
	response := dialog.Run()
	if response == gtk.RESPONSE_OK {
		name, _ := entry.GetText()
		if name == "" {
			name = "New Sheet"
		}
		sheet := sc.manager.AddSheet(name)
		sc.manager.MoveSymbolToSheet(sym.ID, sheet.Number)
		sc.manager.OpenSheet(sheet.Number)
	}
	dialog.Destroy()
}

// onRightPressWire shows a context menu for a wire (highlight net, add/remove corner, rename).
func (sc *SchematicCanvas) onRightPressWire(wire *Wire, schX, schY float64, ev *gdk.Event) {
	sc.clearSelection()

	netID := wire.NetID

	// Highlight the entire net
	for _, w := range sc.doc.Wires {
		if w.NetID == netID {
			w.Selected = true
		}
	}
	for _, sym := range sc.doc.Symbols {
		for _, pin := range sym.Pins {
			if pin.NetID == netID {
				sym.Selected = true
				sc.selected[sym.ID] = true
				break
			}
		}
	}
	sc.drawArea.QueueDraw()

	menu, _ := gtk.MenuNew()

	// Show net name in label
	netName := wire.NetName
	if netName == "" {
		netName = wire.NetID
	}
	headerItem, _ := gtk.MenuItemNewWithLabel(fmt.Sprintf("Net: %s", netName))
	headerItem.SetSensitive(false)
	menu.Append(headerItem)

	sepItem, _ := gtk.SeparatorMenuItemNew()
	menu.Append(sepItem)

	// Find which segment was clicked and the nearest corner index (if any)
	clickedSeg := sc.findClickedSegment(wire, schX, schY)
	nearCornerIdx := sc.findNearCorner(wire, schX, schY)

	addCornerItem, _ := gtk.MenuItemNewWithLabel("Add Corner Here")
	addCornerItem.Connect("activate", func() {
		sc.insertWireCorner(wire, clickedSeg, schX, schY)
		if sc.onLayoutChanged != nil {
			sc.onLayoutChanged()
		}
		sc.drawArea.QueueDraw()
	})
	menu.Append(addCornerItem)

	if nearCornerIdx >= 0 {
		removeCornerItem, _ := gtk.MenuItemNewWithLabel("Remove Corner")
		removeCornerItem.Connect("activate", func() {
			sc.removeWireCorner(wire, nearCornerIdx)
			if sc.onLayoutChanged != nil {
				sc.onLayoutChanged()
			}
			sc.drawArea.QueueDraw()
		})
		menu.Append(removeCornerItem)
	}

	sep2Item, _ := gtk.SeparatorMenuItemNew()
	menu.Append(sep2Item)

	renameItem, _ := gtk.MenuItemNewWithLabel("Rename Net...")
	renameItem.Connect("activate", func() {
		sc.promptRenameNet(wire)
	})
	menu.Append(renameItem)

	menu.ShowAll()
	menu.PopupAtPointer(ev)
}

// findClickedSegment returns the index of the first point of the segment closest to (x,y).
func (sc *SchematicCanvas) findClickedSegment(wire *Wire, x, y float64) int {
	bestIdx := 0
	bestDist := math.MaxFloat64
	for i := 0; i < len(wire.Points)-1; i++ {
		d := pointToSegmentDist(x, y,
			wire.Points[i].X, wire.Points[i].Y,
			wire.Points[i+1].X, wire.Points[i+1].Y)
		if d < bestDist {
			bestDist = d
			bestIdx = i
		}
	}
	return bestIdx
}

// findNearCorner returns the index of an internal corner near (x,y), or -1.
func (sc *SchematicCanvas) findNearCorner(wire *Wire, x, y float64) int {
	const tolerance = 15.0
	for i := 1; i < len(wire.Points)-1; i++ {
		p := wire.Points[i]
		if math.Hypot(x-p.X, y-p.Y) < tolerance {
			return i
		}
	}
	return -1
}

// insertWireCorner inserts a new corner into wire after segment index segIdx.
// The corner is placed at the cursor position snapped to the segment's axis.
func (sc *SchematicCanvas) insertWireCorner(wire *Wire, segIdx int, x, y float64) {
	if segIdx < 0 || segIdx >= len(wire.Points)-1 {
		return
	}
	a := wire.Points[segIdx]
	b := wire.Points[segIdx+1]

	// Snap the new point to grid, then constrain to segment axis for clean Manhattan routing
	newX := math.Round(x/gridSnap) * gridSnap
	newY := math.Round(y/gridSnap) * gridSnap

	// If segment is horizontal, constrain to same Y; if vertical, constrain to same X
	if math.Abs(a.Y-b.Y) < 1 {
		newY = a.Y // horizontal segment
	} else if math.Abs(a.X-b.X) < 1 {
		newX = a.X // vertical segment
	}

	newPt := geometry.Point2D{X: newX, Y: newY}

	// Insert after segIdx
	pts := make([]geometry.Point2D, 0, len(wire.Points)+1)
	pts = append(pts, wire.Points[:segIdx+1]...)
	pts = append(pts, newPt)
	pts = append(pts, wire.Points[segIdx+1:]...)
	wire.Points = pts
}

// removeWireCorner removes the internal corner at idx from wire.Points.
func (sc *SchematicCanvas) removeWireCorner(wire *Wire, idx int) {
	if idx <= 0 || idx >= len(wire.Points)-1 {
		return
	}
	wire.Points = append(wire.Points[:idx], wire.Points[idx+1:]...)
}

// promptRenameNet shows a dialog to rename a net and propagates to app state.
func (sc *SchematicCanvas) promptRenameNet(wire *Wire) {
	dialog, _ := gtk.DialogNewWithButtons(
		"Rename Net",
		nil,
		gtk.DIALOG_MODAL|gtk.DIALOG_DESTROY_WITH_PARENT,
		[]interface{}{"Cancel", gtk.RESPONSE_CANCEL},
		[]interface{}{"OK", gtk.RESPONSE_OK},
	)
	dialog.SetDefaultSize(300, 100)
	dialog.SetDefaultResponse(gtk.RESPONSE_OK)

	box, _ := dialog.GetContentArea()
	label, _ := gtk.LabelNew("Net name:")
	box.PackStart(label, false, false, 4)

	entry, _ := gtk.EntryNew()
	if wire.NetName != "" {
		entry.SetText(wire.NetName)
	}
	entry.SetActivatesDefault(true)
	box.PackStart(entry, false, false, 4)

	dialog.ShowAll()
	response := dialog.Run()
	if response == gtk.RESPONSE_OK {
		newName, _ := entry.GetText()
		if newName != "" {
			// Update all wires on this net
			for _, w := range sc.doc.Wires {
				if w.NetID == wire.NetID {
					w.NetName = newName
				}
			}
			// Update net labels
			for _, nl := range sc.doc.NetLabels {
				if nl.NetID == wire.NetID {
					nl.NetName = newName
				}
			}
			// Update pin net names on symbols
			for _, sym := range sc.doc.Symbols {
				for _, pin := range sym.Pins {
					if pin.NetID == wire.NetID {
						pin.NetName = newName
					}
				}
			}
			sc.drawArea.QueueDraw()

			// Propagate to app state
			if sc.onNetRenamed != nil {
				sc.onNetRenamed(wire.NetID, newName)
			}
		}
	}
	dialog.Destroy()
}

// hitTestWireCorner returns the wire and point index of an internal corner near (x,y), or (nil,-1).
// Only internal waypoints (index 1..n-2) are checked, not endpoints.
func (sc *SchematicCanvas) hitTestWireCorner(x, y float64) (*Wire, int) {
	if sc.doc == nil {
		return nil, -1
	}
	const tolerance = 10.0
	for _, wire := range sc.visibleWires() {
		for i := 1; i < len(wire.Points)-1; i++ {
			p := wire.Points[i]
			if math.Hypot(x-p.X, y-p.Y) < tolerance {
				return wire, i
			}
		}
	}
	return nil, -1
}

// hitTestWire returns the wire closest to (x, y) within tolerance, or nil.
func (sc *SchematicCanvas) hitTestWire(x, y float64) *Wire {
	if sc.doc == nil {
		return nil
	}
	const tolerance = 8.0
	var bestWire *Wire
	bestDist := tolerance

	for _, wire := range sc.visibleWires() {
		for i := 0; i < len(wire.Points)-1; i++ {
			d := pointToSegmentDist(x, y,
				wire.Points[i].X, wire.Points[i].Y,
				wire.Points[i+1].X, wire.Points[i+1].Y)
			if d < bestDist {
				bestDist = d
				bestWire = wire
			}
		}
	}
	return bestWire
}

// pointToSegmentDist returns the distance from point (px,py) to the line segment (ax,ay)-(bx,by).
func pointToSegmentDist(px, py, ax, ay, bx, by float64) float64 {
	dx := bx - ax
	dy := by - ay
	lenSq := dx*dx + dy*dy
	if lenSq < 1e-10 {
		return math.Hypot(px-ax, py-ay)
	}
	t := ((px-ax)*dx + (py-ay)*dy) / lenSq
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	closestX := ax + t*dx
	closestY := ay + t*dy
	return math.Hypot(px-closestX, py-closestY)
}

func (sc *SchematicCanvas) flipSymbol(sym *PlacedSymbol, flipH, flipV bool) {
	if flipH {
		sym.FlipH = !sym.FlipH
	}
	if flipV {
		sym.FlipV = !sym.FlipV
	}

	// Recompute pin positions with new flip state
	def := GetSymbolDef(sym.GateType,
		countPinsByDir(sym, "input"),
		countPinsByDir(sym, "output"),
		countPinsByDir(sym, "enable"),
		countPinsByDir(sym, "clock"))
	ComputePinPositions(sym, def)

	// Re-route connected wires
	sc.rerouteWiresForSymbol(sym)

	sc.drawArea.QueueDraw()

	// Notify for persistence
	if sc.onLayoutChanged != nil {
		sc.onLayoutChanged()
	}
}

func (sc *SchematicCanvas) rotateSymbol(sym *PlacedSymbol) {
	sym.Rotation = (sym.Rotation + 90) % 360

	// Recompute pin positions with new rotation
	def := GetSymbolDef(sym.GateType,
		countPinsByDir(sym, "input"),
		countPinsByDir(sym, "output"),
		countPinsByDir(sym, "enable"),
		countPinsByDir(sym, "clock"))
	ComputePinPositions(sym, def)

	// Re-route connected wires
	sc.rerouteWiresForSymbol(sym)

	sc.drawArea.QueueDraw()

	// Notify for persistence
	if sc.onLayoutChanged != nil {
		sc.onLayoutChanged()
	}
}

func countPinsByDir(sym *PlacedSymbol, dir string) int {
	n := 0
	for _, p := range sym.Pins {
		if p.Direction == dir {
			n++
		}
	}
	return n
}

// --- Sheet filtering ---

// visibleSymbols returns symbols on the current sheet.
func (sc *SchematicCanvas) visibleSymbols() []*PlacedSymbol {
	if sc.sheetNum <= 0 || sc.doc == nil {
		return sc.doc.Symbols
	}
	var result []*PlacedSymbol
	for _, sym := range sc.doc.Symbols {
		if effectiveSheet(sym.Sheet) == sc.sheetNum {
			result = append(result, sym)
		}
	}
	return result
}

// visibleWires returns wires on the current sheet.
func (sc *SchematicCanvas) visibleWires() []*Wire {
	if sc.sheetNum <= 0 || sc.doc == nil {
		return sc.doc.Wires
	}
	var result []*Wire
	for _, w := range sc.doc.Wires {
		if effectiveSheet(w.Sheet) == sc.sheetNum {
			result = append(result, w)
		}
	}
	return result
}

// visiblePowerPorts returns power ports on the current sheet.
func (sc *SchematicCanvas) visiblePowerPorts() []*PowerPort {
	if sc.sheetNum <= 0 || sc.doc == nil {
		return sc.doc.PowerPorts
	}
	var result []*PowerPort
	for _, pp := range sc.doc.PowerPorts {
		if effectiveSheet(pp.Sheet) == sc.sheetNum {
			result = append(result, pp)
		}
	}
	return result
}

// visibleNetLabels returns net labels on the current sheet.
func (sc *SchematicCanvas) visibleNetLabels() []*NetLabel {
	if sc.sheetNum <= 0 || sc.doc == nil {
		return sc.doc.NetLabels
	}
	var result []*NetLabel
	for _, label := range sc.doc.NetLabels {
		if effectiveSheet(label.Sheet) == sc.sheetNum {
			result = append(result, label)
		}
	}
	return result
}

// visibleOffSheetConnectors returns off-sheet connectors on the current sheet.
func (sc *SchematicCanvas) visibleOffSheetConnectors() []*OffSheetConnector {
	if sc.sheetNum <= 0 || sc.doc == nil {
		return sc.doc.OffSheetConnectors
	}
	var result []*OffSheetConnector
	for _, osc := range sc.doc.OffSheetConnectors {
		if osc.Sheet == sc.sheetNum {
			result = append(result, osc)
		}
	}
	return result
}
