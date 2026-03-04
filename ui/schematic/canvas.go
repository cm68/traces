package schematic

import (
	"fmt"
	"math"

	"github.com/gotk3/gotk3/cairo"
	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

const (
	schMinZoom = 0.1
	schMaxZoom = 5.0
	schZoomStep = 1.15
	gridSnap   = 25.0
)

// SchematicCanvas is an interactive schematic drawing widget.
type SchematicCanvas struct {
	drawArea  *gtk.DrawingArea
	scrollWin *gtk.ScrolledWindow

	doc *SchematicDoc

	// View transform
	zoom float64
	// Content size in screen pixels (doc bounds * zoom)
	contentW int
	contentH int
	// Doc origin offset (so doc min coords map to 0,0 in content space)
	originX float64
	originY float64

	// Interaction state
	dragging    bool
	dragSymbol  *PlacedSymbol
	dragOffsetX float64 // offset from mouse to symbol origin
	dragOffsetY float64

	// Middle-button pan
	middleDragging bool
	panLastX       float64
	panLastY       float64

	// Selection
	selected map[string]bool // symbol IDs

	// Callbacks
	onStatusUpdate func(string)
	onLayoutChanged func() // Called when symbol position or flip changes
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

		// Dragging a symbol
		if sc.dragging && sc.dragSymbol != nil {
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
	if sc.dragging && sc.dragSymbol != nil {
		if sc.onLayoutChanged != nil {
			sc.onLayoutChanged()
		}
	}
	sc.dragging = false
	sc.dragSymbol = nil
}

func (sc *SchematicCanvas) onDragMove(schX, schY float64) {
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
	// Clear wire selection
	if sc.doc != nil {
		for _, w := range sc.doc.Wires {
			w.Selected = false
		}
	}
}

// hitTestSymbol returns the symbol at (x,y) or nil.
func (sc *SchematicCanvas) hitTestSymbol(x, y float64) *PlacedSymbol {
	if sc.doc == nil {
		return nil
	}
	// Check in reverse order (last drawn = on top)
	for i := len(sc.doc.Symbols) - 1; i >= 0; i-- {
		sym := sc.doc.Symbols[i]
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
// This properly handles multi-pin nets by recomputing the MST routing.
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

	// Remove wires for affected nets, keep the rest
	kept := sc.doc.Wires[:0]
	for _, w := range sc.doc.Wires {
		if !affectedNets[w.NetID] {
			kept = append(kept, w)
		}
	}
	sc.doc.Wires = kept

	// Rebuild wires for affected nets using proper MST routing
	wireID := len(sc.doc.Wires)
	for netID := range affectedNets {
		pins := sc.findPinsForNet(netID)
		if len(pins) < 2 {
			continue
		}
		if len(pins) == 2 {
			wireID++
			sc.doc.Wires = append(sc.doc.Wires, &Wire{
				ID:     fmt.Sprintf("wire-%d", wireID),
				NetID:  netID,
				Points: ManhattanRoute(pins[0], pins[1]),
			})
		} else {
			edges := mstEdges(pins)
			for _, edge := range edges {
				wireID++
				sc.doc.Wires = append(sc.doc.Wires, &Wire{
					ID:     fmt.Sprintf("wire-%d", wireID),
					NetID:  netID,
					Points: ManhattanRoute(pins[edge[0]], pins[edge[1]]),
				})
			}
		}
	}
}

// findPinsForNet returns all pin positions that belong to a net.
func (sc *SchematicCanvas) findPinsForNet(netID string) []pinPos {
	var result []pinPos
	for _, sym := range sc.doc.Symbols {
		for _, pin := range sym.Pins {
			if pin.NetID == netID {
				result = append(result, pinPos{X: pin.X, Y: pin.Y, Dir: pin.Direction})
			}
		}
	}
	for _, pp := range sc.doc.PowerPorts {
		// Power ports connect via net name matching
		if pp.NetName == netID {
			result = append(result, pinPos{X: pp.PinX, Y: pp.PinY, Dir: "power"})
		}
	}
	return result
}

type pinPos struct {
	X, Y float64
	Dir  string
}

func (sc *SchematicCanvas) onRightPress(schX, schY float64, ev *gdk.Event) {
	// Try symbol first
	sym := sc.hitTestSymbol(schX, schY)
	if sym != nil {
		sc.onRightPressSymbol(sym, ev)
		return
	}

	// Try wire
	wire := sc.hitTestWire(schX, schY)
	if wire != nil {
		sc.onRightPressWire(wire)
		return
	}
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

	menu.ShowAll()
	menu.PopupAtPointer(ev)
}

// onRightPressWire highlights the entire net: all wires, symbols, and junctions.
func (sc *SchematicCanvas) onRightPressWire(wire *Wire) {
	sc.clearSelection()

	netID := wire.NetID

	// Select all wires on this net
	for _, w := range sc.doc.Wires {
		if w.NetID == netID {
			w.Selected = true
		}
	}

	// Select all symbols that have a pin on this net
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
}

// hitTestWire returns the wire closest to (x, y) within tolerance, or nil.
func (sc *SchematicCanvas) hitTestWire(x, y float64) *Wire {
	if sc.doc == nil {
		return nil
	}
	const tolerance = 8.0
	var bestWire *Wire
	bestDist := tolerance

	for _, wire := range sc.doc.Wires {
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
