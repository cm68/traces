package panels

import (
	"fmt"
	"image/color"
	"math"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"pcb-tracer/internal/app"
	"pcb-tracer/internal/connector"
	pcbimage "pcb-tracer/internal/image"
	"pcb-tracer/internal/netlist"
	pcbtrace "pcb-tracer/internal/trace"
	"pcb-tracer/internal/via"
	"pcb-tracer/pkg/colorutil"
	"pcb-tracer/pkg/geometry"
	"pcb-tracer/ui/canvas"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

// Overlay name constants for via and connector overlays.
const (
	OverlayFrontVias     = "front_vias"
	OverlayBackVias      = "back_vias"
	OverlayConfirmedVias = "confirmed_vias"
	OverlayConnectors    = "connectors"
)

// TracesPanel displays and manages detected vias and traces.
type TracesPanel struct {
	state  *app.State
	canvas *canvas.ImageCanvas
	win    *gtk.Window
	box    *gtk.Box

	// Via detection UI
	viaLayerFront       *gtk.RadioButton
	viaLayerBack        *gtk.RadioButton
	detectViasBtn       *gtk.Button
	clearViasBtn        *gtk.Button
	matchViasBtn        *gtk.Button
	viaStatusLabel      *gtk.Label
	viaCountLabel       *gtk.Label
	confirmedCountLabel *gtk.Label
	trainingLabel       *gtk.Label

	// Trace detection UI
	detectTracesBtn  *gtk.Button
	traceStatusLabel *gtk.Label

	// Trace drawing state (polyline mode)
	traceMode     bool
	traceStartVia *via.ConfirmedVia
	tracePoints   []geometry.Point2D
	traceLayer    pcbtrace.TraceLayer

	// Selected via for arrow-key nudging
	selectedVia *via.ConfirmedVia

	// Default via radius for manual addition
	defaultViaRadius float64
}

// NewTracesPanel creates a new traces panel.
func NewTracesPanel(state *app.State, cvs *canvas.ImageCanvas, win *gtk.Window) *TracesPanel {
	tp := &TracesPanel{
		state:            state,
		canvas:           cvs,
		win:              win,
		defaultViaRadius: 15,
	}

	tp.box, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4)
	tp.box.SetMarginStart(4)
	tp.box.SetMarginEnd(4)
	tp.box.SetMarginTop(4)
	tp.box.SetMarginBottom(4)

	// --- Via Detection section ---
	viaFrame, _ := gtk.FrameNew("Via Detection")
	viaBox, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4)
	viaBox.SetMarginStart(4)
	viaBox.SetMarginEnd(4)
	viaBox.SetMarginTop(4)
	viaBox.SetMarginBottom(4)

	layerLabel, _ := gtk.LabelNew("Layer:")
	layerLabel.SetHAlign(gtk.ALIGN_START)
	viaBox.PackStart(layerLabel, false, false, 0)

	// Radio buttons for layer selection
	layerRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	tp.viaLayerFront, _ = gtk.RadioButtonNewWithLabel(nil, "Front")
	tp.viaLayerBack, _ = gtk.RadioButtonNewWithLabelFromWidget(tp.viaLayerFront, "Back")
	tp.viaLayerFront.SetActive(true)
	tp.viaLayerFront.Connect("toggled", func() {
		if tp.viaLayerFront.GetActive() {
			cvs.RaiseLayerBySide(pcbimage.SideFront)
		}
	})
	tp.viaLayerBack.Connect("toggled", func() {
		if tp.viaLayerBack.GetActive() {
			cvs.RaiseLayerBySide(pcbimage.SideBack)
		}
	})
	layerRow.PackStart(tp.viaLayerFront, false, false, 0)
	layerRow.PackStart(tp.viaLayerBack, false, false, 0)
	viaBox.PackStart(layerRow, false, false, 0)

	// Detect/Clear buttons
	btnRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	tp.detectViasBtn, _ = gtk.ButtonNewWithLabel("Detect Vias")
	tp.detectViasBtn.Connect("clicked", func() { tp.onDetectVias() })
	tp.clearViasBtn, _ = gtk.ButtonNewWithLabel("Clear")
	tp.clearViasBtn.Connect("clicked", func() { tp.onClearVias() })
	btnRow.PackStart(tp.detectViasBtn, false, false, 0)
	btnRow.PackStart(tp.clearViasBtn, false, false, 0)
	viaBox.PackStart(btnRow, false, false, 0)

	tp.matchViasBtn, _ = gtk.ButtonNewWithLabel("Match Vias")
	tp.matchViasBtn.Connect("clicked", func() { tp.tryMatchVias() })
	viaBox.PackStart(tp.matchViasBtn, false, false, 0)

	tp.viaStatusLabel, _ = gtk.LabelNew("")
	tp.viaStatusLabel.SetLineWrap(true)
	tp.viaStatusLabel.SetHAlign(gtk.ALIGN_START)
	viaBox.PackStart(tp.viaStatusLabel, false, false, 0)

	tp.viaCountLabel, _ = gtk.LabelNew("No vias detected")
	tp.viaCountLabel.SetHAlign(gtk.ALIGN_START)
	viaBox.PackStart(tp.viaCountLabel, false, false, 0)

	tp.confirmedCountLabel, _ = gtk.LabelNew("Confirmed: 0")
	tp.confirmedCountLabel.SetHAlign(gtk.ALIGN_START)
	viaBox.PackStart(tp.confirmedCountLabel, false, false, 0)

	tp.trainingLabel, _ = gtk.LabelNew("Training: 0 pos, 0 neg")
	tp.trainingLabel.SetHAlign(gtk.ALIGN_START)
	viaBox.PackStart(tp.trainingLabel, false, false, 0)

	sep, _ := gtk.SeparatorNew(gtk.ORIENTATION_HORIZONTAL)
	viaBox.PackStart(sep, false, false, 2)

	helpTexts := []string{
		"Cyan=front  Magenta=back  Blue=both",
		"Left-click: select via  Right-click: menu",
		"Arrow keys: nudge selected via",
		"Middle-click: draw trace between vias",
	}
	for _, t := range helpTexts {
		lbl, _ := gtk.LabelNew(t)
		lbl.SetHAlign(gtk.ALIGN_START)
		viaBox.PackStart(lbl, false, false, 0)
	}

	viaFrame.Add(viaBox)
	tp.box.PackStart(viaFrame, false, false, 0)

	// --- Trace Detection section ---
	traceFrame, _ := gtk.FrameNew("Trace Detection")
	traceBox, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4)
	traceBox.SetMarginStart(4)
	traceBox.SetMarginEnd(4)
	traceBox.SetMarginTop(4)
	traceBox.SetMarginBottom(4)

	tp.detectTracesBtn, _ = gtk.ButtonNewWithLabel("Clear Traces")
	tp.detectTracesBtn.Connect("clicked", func() {
		tp.canvas.ClearOverlay("trace_segments")
		tp.canvas.HideRubberBand()
		tp.traceMode = false
		tp.traceStartVia = nil
		tp.tracePoints = nil
		tp.canvas.OnMouseMove(nil)
		tp.traceStatusLabel.SetText("Cleared")
		tp.canvas.Refresh()
	})
	traceBox.PackStart(tp.detectTracesBtn, false, false, 0)

	tp.traceStatusLabel, _ = gtk.LabelNew("Click a via twice to start trace")
	tp.traceStatusLabel.SetLineWrap(true)
	tp.traceStatusLabel.SetHAlign(gtk.ALIGN_START)
	traceBox.PackStart(tp.traceStatusLabel, false, false, 0)

	traceFrame.Add(traceBox)
	tp.box.PackStart(traceFrame, false, false, 0)

	// Load training set
	tp.loadTrainingSet()
	tp.updateTrainingLabel()

	// Auto-match vias when alignment completes
	state.On(app.EventAlignmentComplete, func(data interface{}) {
		tp.state.CreateConnectorsFromAlignment()
		connCount := tp.state.FeaturesLayer.ConnectorCount()
		fmt.Printf("Created %d connectors from alignment contacts\n", connCount)

		frontVias := tp.state.FeaturesLayer.GetViasBySide(pcbimage.SideFront)
		backVias := tp.state.FeaturesLayer.GetViasBySide(pcbimage.SideBack)
		if len(frontVias) > 0 && len(backVias) > 0 {
			fmt.Printf("Auto-matching vias after alignment complete...\n")
			tp.tryMatchVias()
		}
	})

	// Restore via overlays on project load
	state.On(app.EventProjectLoaded, func(_ interface{}) {
		glib.IdleAdd(func() {
			tp.refreshAllViaOverlays()
			front, back := tp.state.FeaturesLayer.ViaCountBySide()
			if front+back > 0 {
				tp.viaCountLabel.SetText(fmt.Sprintf("Vias: %d front, %d back", front, back))
			}
			confirmed := tp.state.FeaturesLayer.GetConfirmedVias()
			if len(confirmed) > 0 {
				tp.confirmedCountLabel.SetText(fmt.Sprintf("Confirmed: %d", len(confirmed)))
			}
		})
	})

	return tp
}

// Widget returns the panel widget for embedding.
func (tp *TracesPanel) Widget() gtk.IWidget {
	return tp.box
}

// selectedLayer returns "Front" or "Back".
func (tp *TracesPanel) selectedLayer() string {
	if tp.viaLayerFront.GetActive() {
		return "Front"
	}
	return "Back"
}

// SetEnabled enables or disables the panel's interactive widgets.
func (tp *TracesPanel) SetEnabled(enabled bool) {
	tp.detectViasBtn.SetSensitive(enabled)
	tp.clearViasBtn.SetSensitive(enabled)
	tp.matchViasBtn.SetSensitive(enabled)
	tp.detectTracesBtn.SetSensitive(enabled)
}

// OnKeyPressed handles keyboard input for arrow-key via nudging.
// Called from the window-level key-press-event.
func (tp *TracesPanel) OnKeyPressed(ev *gdk.EventKey) bool {
	if tp.selectedVia == nil {
		return false
	}

	step := 1.0
	if ev.State()&uint(gdk.SHIFT_MASK) != 0 {
		step = 5.0
	}

	cv := tp.selectedVia
	keyval := ev.KeyVal()
	switch keyval {
	case gdk.KEY_Up:
		cv.Center.Y -= step
	case gdk.KEY_Down:
		cv.Center.Y += step
	case gdk.KEY_Left:
		cv.Center.X -= step
	case gdk.KEY_Right:
		cv.Center.X += step
	case gdk.KEY_Escape:
		tp.deselectVia()
		return true
	default:
		return false
	}

	cv.IntersectionBoundary = geometry.GenerateCirclePoints(cv.Center.X, cv.Center.Y, cv.Radius, 32)
	tp.refreshConfirmedViaOverlay()
	tp.updateSelectedViaOverlay()
	tp.canvas.Refresh()
	tp.viaStatusLabel.SetText(fmt.Sprintf("%s center: (%.0f, %.0f)", cv.ID, cv.Center.X, cv.Center.Y))
	tp.state.Emit(app.EventConfirmedViasChanged, nil)
	return true
}

// onDetectVias runs via detection on the selected layer.
func (tp *TracesPanel) onDetectVias() {
	isFront := tp.selectedLayer() == "Front"

	var img *pcbimage.Layer
	var side pcbimage.Side
	var layerName string

	if isFront {
		img = tp.state.FrontImage
		side = pcbimage.SideFront
		layerName = "Front"
	} else {
		img = tp.state.BackImage
		side = pcbimage.SideBack
		layerName = "Back"
	}

	if img == nil || img.Image == nil {
		tp.viaStatusLabel.SetText(fmt.Sprintf("No %s image loaded", layerName))
		return
	}

	dpi := tp.state.DPI
	if dpi == 0 && img.DPI > 0 {
		dpi = img.DPI
	}
	if dpi == 0 {
		tp.viaStatusLabel.SetText("DPI unknown - load a TIFF with DPI metadata")
		return
	}

	tp.viaStatusLabel.SetText(fmt.Sprintf("Detecting vias on %s...", layerName))
	tp.detectViasBtn.SetSensitive(false)

	go func() {
		params := via.DefaultParams().WithDPI(dpi)
		result, err := via.DetectViasFromImage(img.Image, side, params)

		glib.IdleAdd(func() {
			tp.detectViasBtn.SetSensitive(true)
		})

		if err != nil {
			glib.IdleAdd(func() {
				tp.viaStatusLabel.SetText(fmt.Sprintf("Error: %v", err))
			})
			return
		}

		// Post-process: detect metal boundaries
		numVias := len(result.Vias)
		fmt.Printf("Post-processing %d detected vias to find metal boundaries (parallel)...\n", numVias)
		maxRadius := 0.030 * dpi

		startTime := time.Now()
		numWorkers := runtime.NumCPU()
		if numWorkers > numVias {
			numWorkers = numVias
		}
		if numWorkers < 1 {
			numWorkers = 1
		}

		var wg sync.WaitGroup
		viaChan := make(chan int, numVias)

		for w := 0; w < numWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range viaChan {
					v := &result.Vias[i]
					boundary := via.DetectMetalBoundary(img.Image, v.Center.X, v.Center.Y, maxRadius)
					v.PadBoundary = boundary.Boundary
					v.Center = boundary.Center
					v.Radius = boundary.Radius
				}
			}()
		}
		for i := range result.Vias {
			viaChan <- i
		}
		close(viaChan)
		wg.Wait()
		elapsed := time.Since(startTime)
		fmt.Printf("Post-processing complete (%d workers, %.1fms)\n", numWorkers, float64(elapsed.Microseconds())/1000)

		tp.state.FeaturesLayer.AddVias(result.Vias)

		houghCount := 0
		contourCount := 0
		for _, v := range result.Vias {
			switch v.Method {
			case via.MethodHoughCircle:
				houghCount++
			case via.MethodContourFit:
				contourCount++
			}
		}

		glib.IdleAdd(func() {
			tp.createViaOverlay(result.Vias, side, false)
			front, back := tp.state.FeaturesLayer.ViaCountBySide()
			tp.viaCountLabel.SetText(fmt.Sprintf("Vias: %d front, %d back", front, back))
			tp.viaStatusLabel.SetText(fmt.Sprintf("%s: %d vias (%d Hough, %d contour)",
				layerName, len(result.Vias), houghCount, contourCount))
			tp.state.Emit(app.EventFeaturesChanged, nil)
		})
	}()
}

// createViaOverlay creates a canvas overlay to visualize detected vias.
func (tp *TracesPanel) createViaOverlay(vias []via.Via, side pcbimage.Side, skipMatched bool) {
	fmt.Printf("  createViaOverlay: %d vias for side=%v skipMatched=%v\n", len(vias), side, skipMatched)
	var overlayName string
	var overlayColor color.RGBA

	if side == pcbimage.SideFront {
		overlayName = OverlayFrontVias
		overlayColor = colorutil.Cyan
	} else {
		overlayName = OverlayBackVias
		overlayColor = colorutil.Magenta
	}

	overlay := &canvas.Overlay{
		Color:      overlayColor,
		Rectangles: make([]canvas.OverlayRect, 0),
		Polygons:   make([]canvas.OverlayPolygon, 0),
	}

	for _, v := range vias {
		if skipMatched && v.BothSidesConfirmed {
			continue
		}
		if len(v.PadBoundary) >= 3 {
			overlay.Polygons = append(overlay.Polygons, canvas.OverlayPolygon{
				Points: v.PadBoundary,
				Filled: false,
			})
		} else {
			bounds := v.Bounds()
			overlay.Rectangles = append(overlay.Rectangles, canvas.OverlayRect{
				X: bounds.X, Y: bounds.Y, Width: bounds.Width, Height: bounds.Height,
				Fill: canvas.FillNone,
			})
		}
	}

	fmt.Printf("  Setting overlay '%s': %d rects, %d polygons\n", overlayName, len(overlay.Rectangles), len(overlay.Polygons))
	tp.canvas.SetOverlay(overlayName, overlay)
}

// onClearVias clears all detected vias.
func (tp *TracesPanel) onClearVias() {
	tp.state.FeaturesLayer.ClearVias()
	tp.state.FeaturesLayer.ClearConfirmedVias()
	tp.canvas.ClearOverlay(OverlayFrontVias)
	tp.canvas.ClearOverlay(OverlayBackVias)
	tp.canvas.ClearOverlay(OverlayConfirmedVias)
	tp.viaCountLabel.SetText("No vias detected")
	tp.confirmedCountLabel.SetText("Confirmed: 0")
	tp.viaStatusLabel.SetText("Cleared")
	tp.state.Emit(app.EventFeaturesChanged, nil)
}

// loadTrainingSet loads the training set from the default location.
func (tp *TracesPanel) loadTrainingSet() {
	trainingPath := "via_training.json"
	if tp.state.ProjectPath != "" {
		trainingPath = filepath.Join(filepath.Dir(tp.state.ProjectPath), "via_training.json")
	}
	ts, err := via.LoadTrainingSet(trainingPath)
	if err != nil {
		fmt.Printf("Warning: failed to load training set: %v\n", err)
		ts = via.NewTrainingSet()
	}
	ts.SetFilePath(trainingPath)
	tp.state.ViaTrainingSet = ts
}

// updateTrainingLabel updates the training label with current counts.
func (tp *TracesPanel) updateTrainingLabel() {
	if tp.state.ViaTrainingSet == nil {
		tp.trainingLabel.SetText("Training: not loaded")
		return
	}
	pos := tp.state.ViaTrainingSet.PositiveCount()
	neg := tp.state.ViaTrainingSet.NegativeCount()
	tp.trainingLabel.SetText(fmt.Sprintf("Training: %d pos, %d neg", pos, neg))
}

// onLeftClick handles left-click for polyline trace drawing and via selection.
func (tp *TracesPanel) onLeftClick(x, y float64) {
	if tp.traceMode {
		cv := tp.state.FeaturesLayer.HitTestConfirmedVia(x, y)
		if cv != nil && cv.ID != tp.traceStartVia.ID {
			tp.tracePoints = append(tp.tracePoints, cv.Center)
			tp.finishTrace(cv)
			return
		}
		tp.tracePoints = append(tp.tracePoints, geometry.Point2D{X: x, Y: y})
		tp.canvas.ShowRubberBand(x, y)
		tp.updateTraceOverlay()
		tp.traceStatusLabel.SetText(fmt.Sprintf("Trace from %s — %d segments — click waypoints, end on a via",
			tp.traceStartVia.ID, len(tp.tracePoints)-1))
		return
	}

	cv := tp.state.FeaturesLayer.HitTestConfirmedVia(x, y)
	if cv != nil {
		if tp.selectedVia != nil && tp.selectedVia.ID == cv.ID {
			tp.traceMode = true
			tp.traceStartVia = cv
			tp.tracePoints = []geometry.Point2D{cv.Center}
			if tp.selectedLayer() == "Front" {
				tp.traceLayer = pcbtrace.LayerFront
			} else {
				tp.traceLayer = pcbtrace.LayerBack
			}
			tp.setupTraceRubberBand()
			tp.traceStatusLabel.SetText(fmt.Sprintf("Trace from %s — click waypoints, end on a via", cv.ID))
			return
		}
		tp.selectVia(cv)
	} else {
		tp.deselectVia()
	}
}

// selectVia makes a confirmed via the selected via for nudging.
func (tp *TracesPanel) selectVia(cv *via.ConfirmedVia) {
	tp.selectedVia = cv
	tp.viaStatusLabel.SetText(fmt.Sprintf("Selected %s — arrow keys to nudge, Shift for 5px", cv.ID))
	tp.updateSelectedViaOverlay()
}

// deselectVia clears the selected via.
func (tp *TracesPanel) deselectVia() {
	if tp.selectedVia == nil {
		return
	}
	tp.selectedVia = nil
	tp.canvas.ClearOverlay("selected_via")
	tp.viaStatusLabel.SetText("")
	tp.canvas.Refresh()
}

// updateSelectedViaOverlay draws a highlight ring around the selected via.
func (tp *TracesPanel) updateSelectedViaOverlay() {
	if tp.selectedVia == nil {
		tp.canvas.ClearOverlay("selected_via")
		return
	}
	cv := tp.selectedVia
	tp.canvas.SetOverlay("selected_via", &canvas.Overlay{
		Circles: []canvas.OverlayCircle{
			{X: cv.Center.X, Y: cv.Center.Y, Radius: cv.Radius + 3, Filled: false},
		},
		Color: color.RGBA{R: 255, G: 255, B: 255, A: 255},
	})
}

// onRightClickVia handles right-click on the canvas.
func (tp *TracesPanel) onRightClickVia(x, y float64) {
	cv := tp.state.FeaturesLayer.HitTestConfirmedVia(x, y)
	if cv != nil {
		tp.showConfirmedViaMenu(cv)
		return
	}
	tp.showGeneralViaMenu(x, y)
}

// showConfirmedViaMenu shows the context menu for a confirmed via.
func (tp *TracesPanel) showConfirmedViaMenu(cv *via.ConfirmedVia) {
	radiusStep := 2.0
	if tp.state.DPI > 0 {
		radiusStep = 0.005 * tp.state.DPI
	}

	net := tp.state.FeaturesLayer.GetNetForElement(cv.ID)
	netLabel := "Name Netlist..."
	if net != nil {
		netLabel = fmt.Sprintf("Netlist: %s", net.Name)
	}

	pinLabel := "Name Pin..."
	if cv.ComponentID != "" && cv.PinNumber != "" {
		pinLabel = fmt.Sprintf("Pin: %s-%s", cv.ComponentID, cv.PinNumber)
	}

	menu, _ := gtk.MenuNew()

	addItem := func(label string, cb func()) {
		item, _ := gtk.MenuItemNewWithLabel(label)
		item.Connect("activate", cb)
		menu.Append(item)
	}
	addSep := func() {
		sep, _ := gtk.SeparatorMenuItemNew()
		menu.Append(sep)
	}

	addItem(netLabel, func() { tp.nameNetlist(cv) })
	addItem(pinLabel, func() { tp.namePin(cv) })
	addSep()
	addItem("Delete Via", func() { tp.deleteConfirmedVia(cv) })
	addItem("Delete Front", func() { tp.deleteConfirmedViaSide(cv, pcbimage.SideFront) })
	addItem("Delete Back", func() { tp.deleteConfirmedViaSide(cv, pcbimage.SideBack) })
	addItem("Delete Connected Trace", func() { tp.deleteConnectedTrace(cv) })
	addSep()
	addItem("Decrease Radius", func() { tp.adjustConfirmedViaRadius(cv, -radiusStep) })
	addItem("Increase Radius", func() { tp.adjustConfirmedViaRadius(cv, radiusStep) })

	menu.ShowAll()
	menu.PopupAtPointer(nil)
}

// showGeneralViaMenu shows the context menu when not on a confirmed via.
func (tp *TracesPanel) showGeneralViaMenu(imgX, imgY float64) {
	menu, _ := gtk.MenuNew()

	addItem := func(label string, cb func()) {
		item, _ := gtk.MenuItemNewWithLabel(label)
		item.Connect("activate", cb)
		menu.Append(item)
	}

	addItem("Add Confirmed Via", func() { tp.addConfirmedViaAt(imgX, imgY) })
	addItem("Delete Front Via", func() { tp.deleteNearestVia(imgX, imgY, pcbimage.SideFront) })
	addItem("Delete Back Via", func() { tp.deleteNearestVia(imgX, imgY, pcbimage.SideBack) })

	if hit := tp.hitTestTraceSegment(imgX, imgY); hit != nil {
		h := hit
		sep, _ := gtk.SeparatorMenuItemNew()
		menu.Append(sep)
		addItem("Delete Segment", func() { tp.deleteTraceSegment(h) })
	}

	menu.ShowAll()
	menu.PopupAtPointer(nil)
}

// traceHit identifies a hit on a trace segment.
type traceHit struct {
	traceID  string
	segIndex int
}

// hitTestTraceSegment returns the trace and segment closest to (x, y).
func (tp *TracesPanel) hitTestTraceSegment(x, y float64) *traceHit {
	tolerance := 10.0
	if tp.state.DPI > 0 {
		tolerance = 0.015 * tp.state.DPI
	}

	var bestHit *traceHit
	bestDist := tolerance

	for _, tid := range tp.state.FeaturesLayer.GetTraces() {
		tf := tp.state.FeaturesLayer.GetTraceFeature(tid)
		if tf == nil || len(tf.Points) < 2 {
			continue
		}
		for i := 1; i < len(tf.Points); i++ {
			d := pointToSegmentDist(x, y,
				tf.Points[i-1].X, tf.Points[i-1].Y,
				tf.Points[i].X, tf.Points[i].Y)
			if d < bestDist {
				bestDist = d
				bestHit = &traceHit{traceID: tid, segIndex: i - 1}
			}
		}
	}
	return bestHit
}

func pointToSegmentDist(px, py, x1, y1, x2, y2 float64) float64 {
	dx := x2 - x1
	dy := y2 - y1
	lenSq := dx*dx + dy*dy
	if lenSq == 0 {
		ddx := px - x1
		ddy := py - y1
		return math.Sqrt(ddx*ddx + ddy*ddy)
	}
	t := ((px-x1)*dx + (py-y1)*dy) / lenSq
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	projX := x1 + t*dx
	projY := y1 + t*dy
	ddx := px - projX
	ddy := py - projY
	return math.Sqrt(ddx*ddx + ddy*ddy)
}

// deleteTraceSegment removes a trace segment.
func (tp *TracesPanel) deleteTraceSegment(hit *traceHit) {
	if hit == nil {
		return
	}
	tf := tp.state.FeaturesLayer.GetTraceFeature(hit.traceID)
	if tf == nil {
		return
	}

	nSegs := len(tf.Points) - 1
	if nSegs <= 1 {
		tp.state.FeaturesLayer.RemoveTrace(hit.traceID)
		tp.canvas.ClearOverlay(hit.traceID)
		tp.traceStatusLabel.SetText(fmt.Sprintf("Deleted %s", hit.traceID))
	} else {
		removeIdx := hit.segIndex + 1
		if removeIdx >= len(tf.Points)-1 {
			removeIdx = len(tf.Points) - 1
		}
		newPoints := make([]geometry.Point2D, 0, len(tf.Points)-1)
		newPoints = append(newPoints, tf.Points[:removeIdx]...)
		newPoints = append(newPoints, tf.Points[removeIdx+1:]...)

		layerRef := canvas.LayerFront
		if tf.Layer == pcbtrace.LayerBack {
			layerRef = canvas.LayerBack
		}
		tp.state.FeaturesLayer.RemoveTrace(hit.traceID)
		et := pcbtrace.ExtendedTrace{
			Trace: pcbtrace.Trace{
				ID:     hit.traceID,
				Layer:  tf.Layer,
				Points: newPoints,
			},
			Source: pcbtrace.SourceManual,
		}
		tp.state.FeaturesLayer.AddTrace(et)
		tp.renderTraceOverlay(hit.traceID, newPoints, layerRef)
		tp.traceStatusLabel.SetText(fmt.Sprintf("%s: %d segments", hit.traceID, len(newPoints)-1))
	}
	tp.canvas.Refresh()
}

// addConfirmedViaAt places a confirmed via at the exact click point.
func (tp *TracesPanel) addConfirmedViaAt(x, y float64) {
	radius := 12.0
	if tp.state.DPI > 0 {
		radius = 0.018 * tp.state.DPI
	}

	center := geometry.Point2D{X: x, Y: y}
	boundary := geometry.GenerateCirclePoints(x, y, radius, 32)

	viaNum := tp.state.FeaturesLayer.NextViaNumber()
	frontVia := via.Via{
		ID: fmt.Sprintf("via-%03d", viaNum), Center: center,
		Radius: radius, Side: pcbimage.SideFront, Confidence: 1.0,
		Method: via.MethodManual, Circularity: 1.0, PadBoundary: boundary,
		BothSidesConfirmed: true,
	}
	tp.state.FeaturesLayer.AddVia(frontVia)

	backVia := via.Via{
		ID: fmt.Sprintf("via-%03d", viaNum+1), Center: center,
		Radius: radius, Side: pcbimage.SideBack, Confidence: 1.0,
		Method: via.MethodManual, Circularity: 1.0, PadBoundary: boundary,
		BothSidesConfirmed: true, MatchedViaID: frontVia.ID,
	}
	frontVia.MatchedViaID = backVia.ID
	tp.state.FeaturesLayer.UpdateVia(frontVia)
	tp.state.FeaturesLayer.AddVia(backVia)

	cvNum := tp.state.FeaturesLayer.NextConfirmedViaNumber()
	cvID := fmt.Sprintf("cvia-%03d", cvNum)
	cv := via.NewConfirmedVia(cvID, &frontVia, &backVia)
	tp.state.FeaturesLayer.AddConfirmedVia(cv)

	fmt.Printf("Added confirmed via %s at (%.0f, %.0f) r=%.0f\n", cvID, x, y, radius)

	tp.refreshAllViaOverlays()
	tp.updateViaCounts()
	tp.selectVia(cv)

	tp.state.Emit(app.EventFeaturesChanged, nil)
	tp.state.Emit(app.EventConfirmedViasChanged, nil)
}

// deleteNearestVia removes the closest via on the given side near (x, y).
func (tp *TracesPanel) deleteNearestVia(x, y float64, side pcbimage.Side) {
	tolerance := 20.0
	if tp.state.DPI > 0 {
		tolerance = 0.030 * tp.state.DPI
	}

	vias := tp.state.FeaturesLayer.GetViasBySide(side)
	var closestVia *via.Via
	closestDist := tolerance * tolerance

	for i := range vias {
		v := &vias[i]
		dx := v.Center.X - x
		dy := v.Center.Y - y
		dist := dx*dx + dy*dy
		if dist < closestDist {
			closestDist = dist
			closestVia = v
		}
	}

	if closestVia == nil {
		tp.viaStatusLabel.SetText(fmt.Sprintf("No %s via near (%.0f, %.0f)", side.String(), x, y))
		return
	}

	tp.state.FeaturesLayer.RemoveVia(closestVia.ID)
	tp.refreshViaOverlay(side)
	tp.updateViaCounts()
	tp.viaStatusLabel.SetText(fmt.Sprintf("Removed %s via %s", side.String(), closestVia.ID))
	tp.state.Emit(app.EventFeaturesChanged, nil)
}

// deleteConfirmedVia removes a confirmed via and its underlying front/back vias.
func (tp *TracesPanel) deleteConfirmedVia(cv *via.ConfirmedVia) {
	fmt.Printf("Delete confirmed via %s (front=%s, back=%s)\n", cv.ID, cv.FrontViaID, cv.BackViaID)
	tp.state.FeaturesLayer.RemoveVia(cv.FrontViaID)
	tp.state.FeaturesLayer.RemoveVia(cv.BackViaID)
	tp.state.FeaturesLayer.RemoveConfirmedVia(cv.ID)
	tp.refreshAllViaOverlays()
	tp.updateViaCounts()
	tp.viaStatusLabel.SetText(fmt.Sprintf("Deleted %s", cv.ID))
	tp.state.Emit(app.EventFeaturesChanged, nil)
	tp.state.Emit(app.EventConfirmedViasChanged, nil)
}

// deleteConfirmedViaSide removes one side of a confirmed via.
func (tp *TracesPanel) deleteConfirmedViaSide(cv *via.ConfirmedVia, side pcbimage.Side) {
	var removeID string
	if side == pcbimage.SideFront {
		removeID = cv.FrontViaID
	} else {
		removeID = cv.BackViaID
	}
	tp.state.FeaturesLayer.RemoveVia(removeID)
	tp.state.FeaturesLayer.RemoveConfirmedVia(cv.ID)
	tp.refreshAllViaOverlays()
	tp.updateViaCounts()
	tp.viaStatusLabel.SetText(fmt.Sprintf("Deleted %s side of %s", side.String(), cv.ID))
	tp.state.Emit(app.EventFeaturesChanged, nil)
	tp.state.Emit(app.EventConfirmedViasChanged, nil)
}

// deleteConnectedTrace removes traces connected to a confirmed via.
func (tp *TracesPanel) deleteConnectedTrace(cv *via.ConfirmedVia) {
	tolerance := 5.0
	removed := 0
	for _, tid := range tp.state.FeaturesLayer.GetTraces() {
		tf := tp.state.FeaturesLayer.GetTraceFeature(tid)
		if tf == nil || len(tf.Points) < 2 {
			continue
		}
		start := tf.Points[0]
		end := tf.Points[len(tf.Points)-1]
		startDist := math.Sqrt((start.X-cv.Center.X)*(start.X-cv.Center.X) + (start.Y-cv.Center.Y)*(start.Y-cv.Center.Y))
		endDist := math.Sqrt((end.X-cv.Center.X)*(end.X-cv.Center.X) + (end.Y-cv.Center.Y)*(end.Y-cv.Center.Y))
		if startDist <= tolerance || endDist <= tolerance {
			tp.state.FeaturesLayer.RemoveTrace(tid)
			tp.canvas.ClearOverlay(tid)
			removed++
		}
	}
	tp.traceStatusLabel.SetText(fmt.Sprintf("Deleted %d trace(s) from %s", removed, cv.ID))
	tp.canvas.Refresh()
}

// adjustConfirmedViaRadius changes the radius of a confirmed via.
func (tp *TracesPanel) adjustConfirmedViaRadius(cv *via.ConfirmedVia, delta float64) {
	newRadius := cv.Radius + delta
	if newRadius < 2 {
		newRadius = 2
	}
	fmt.Printf("Adjust %s radius: %.1f -> %.1f\n", cv.ID, cv.Radius, newRadius)
	cv.Radius = newRadius
	cv.IntersectionBoundary = geometry.GenerateCirclePoints(cv.Center.X, cv.Center.Y, newRadius, 32)
	tp.refreshConfirmedViaOverlay()
	tp.canvas.Refresh()
	tp.viaStatusLabel.SetText(fmt.Sprintf("%s radius: %.0f px", cv.ID, newRadius))
	tp.state.Emit(app.EventConfirmedViasChanged, nil)
}

// updateViaCounts refreshes the via count labels.
func (tp *TracesPanel) updateViaCounts() {
	front, back := tp.state.FeaturesLayer.ViaCountBySide()
	tp.viaCountLabel.SetText(fmt.Sprintf("Vias: %d front, %d back", front, back))
	confirmed := tp.state.FeaturesLayer.ConfirmedViaCount()
	tp.confirmedCountLabel.SetText(fmt.Sprintf("Confirmed: %d", confirmed))
}

// nameNetlist opens a dialog to name the netlist associated with a via.
func (tp *TracesPanel) nameNetlist(cv *via.ConfirmedVia) {
	net := tp.state.FeaturesLayer.GetNetForElement(cv.ID)

	dlg, _ := gtk.DialogNewWithButtons("Name Netlist", tp.win,
		gtk.DIALOG_MODAL|gtk.DIALOG_DESTROY_WITH_PARENT,
		[]interface{}{"Cancel", gtk.RESPONSE_CANCEL, "OK", gtk.RESPONSE_OK})
	dlg.SetDefaultSize(300, 150)

	contentArea, _ := dlg.GetContentArea()
	entry, _ := gtk.EntryNew()
	if net != nil {
		entry.SetText(net.Name)
	}
	entry.SetPlaceholderText("e.g. VCC, GND, D0")

	lbl, _ := gtk.LabelNew("Net name:")
	lbl.SetHAlign(gtk.ALIGN_START)
	contentArea.PackStart(lbl, false, false, 4)
	contentArea.PackStart(entry, false, false, 4)
	dlg.ShowAll()

	response := dlg.Run()
	if response == gtk.RESPONSE_OK {
		name, _ := entry.GetText()
		if name != "" {
			if net != nil {
				net.Name = name
				net.ID = "net-" + name
				fmt.Printf("Renamed net to %q (%d vias)\n", name, len(net.ViaIDs))
			} else {
				net = netlist.NewElectricalNetWithName("net-"+name, name)
				net.AddVia(cv)
				tp.state.FeaturesLayer.AddNet(net)
				fmt.Printf("Created net %q for %s\n", name, cv.ID)
			}
			tp.viaStatusLabel.SetText(fmt.Sprintf("%s -> net %q", cv.ID, name))
			tp.state.Emit(app.EventNetlistModified, nil)
		}
	}
	dlg.Destroy()
}

// namePin shows a dialog to associate a confirmed via with a component pin.
func (tp *TracesPanel) namePin(cv *via.ConfirmedVia) {
	closestID := ""
	if len(tp.state.Components) > 0 {
		bestDist := math.MaxFloat64
		for _, comp := range tp.state.Components {
			center := comp.Center()
			dx := center.X - cv.Center.X
			dy := center.Y - cv.Center.Y
			dist := math.Sqrt(dx*dx + dy*dy)
			if dist < bestDist {
				bestDist = dist
				closestID = comp.ID
			}
		}
	}

	dlg, _ := gtk.DialogNewWithButtons("Name Pin", tp.win,
		gtk.DIALOG_MODAL|gtk.DIALOG_DESTROY_WITH_PARENT,
		[]interface{}{"Cancel", gtk.RESPONSE_CANCEL, "OK", gtk.RESPONSE_OK})
	dlg.SetDefaultSize(300, 200)

	contentArea, _ := dlg.GetContentArea()

	compEntry, _ := gtk.EntryNew()
	if cv.ComponentID != "" {
		compEntry.SetText(cv.ComponentID)
	} else if closestID != "" {
		compEntry.SetText(closestID)
	}
	compEntry.SetPlaceholderText("e.g. B13, U1")

	pinEntry, _ := gtk.EntryNew()
	if cv.PinNumber != "" {
		pinEntry.SetText(cv.PinNumber)
	} else {
		compText, _ := compEntry.GetText()
		guessedPin := tp.guessPin(cv, compText)
		if guessedPin != "" {
			pinEntry.SetText(guessedPin)
		}
	}
	pinEntry.SetPlaceholderText("e.g. 1, 14, VCC")

	compLabel, _ := gtk.LabelNew("Component:")
	compLabel.SetHAlign(gtk.ALIGN_START)
	pinLabel, _ := gtk.LabelNew("Pin:")
	pinLabel.SetHAlign(gtk.ALIGN_START)

	contentArea.PackStart(compLabel, false, false, 4)
	contentArea.PackStart(compEntry, false, false, 4)
	contentArea.PackStart(pinLabel, false, false, 4)
	contentArea.PackStart(pinEntry, false, false, 4)
	dlg.ShowAll()

	response := dlg.Run()
	if response == gtk.RESPONSE_OK {
		compText, _ := compEntry.GetText()
		pinText, _ := pinEntry.GetText()
		cv.ComponentID = compText
		cv.PinNumber = pinText

		label := ""
		if cv.ComponentID != "" && cv.PinNumber != "" {
			label = fmt.Sprintf("%s-%s", cv.ComponentID, cv.PinNumber)
		} else if cv.ComponentID != "" {
			label = cv.ComponentID
		}
		if label != "" {
			tp.viaStatusLabel.SetText(fmt.Sprintf("%s -> pin %s", cv.ID, label))
			fmt.Printf("Via %s associated with pin %s\n", cv.ID, label)
		}
		tp.state.Emit(app.EventConfirmedViasChanged, nil)
	}
	dlg.Destroy()
}

// guessPin estimates a pin number for a via based on distance from already-named pins.
func (tp *TracesPanel) guessPin(cv *via.ConfirmedVia, componentID string) string {
	if tp.state.DPI <= 0 || componentID == "" {
		return ""
	}
	pitch := 0.1 * tp.state.DPI

	type namedPin struct {
		cv  *via.ConfirmedVia
		num int
	}
	var closest *namedPin
	bestDist := math.MaxFloat64

	for _, other := range tp.state.FeaturesLayer.GetConfirmedVias() {
		if other == cv || other.ComponentID != componentID || other.PinNumber == "" {
			continue
		}
		n, err := strconv.Atoi(other.PinNumber)
		if err != nil || n <= 0 {
			continue
		}
		dx := cv.Center.X - other.Center.X
		dy := cv.Center.Y - other.Center.Y
		dist := math.Sqrt(dx*dx + dy*dy)
		if dist < bestDist {
			bestDist = dist
			closest = &namedPin{other, n}
		}
	}

	if closest == nil {
		return ""
	}

	steps := math.Round(bestDist / pitch)
	if steps < 1 {
		return ""
	}

	var secondClosest *namedPin
	secondBest := math.MaxFloat64
	for _, other := range tp.state.FeaturesLayer.GetConfirmedVias() {
		if other == cv || other == closest.cv || other.ComponentID != componentID || other.PinNumber == "" {
			continue
		}
		n, err := strconv.Atoi(other.PinNumber)
		if err != nil || n <= 0 {
			continue
		}
		dx := cv.Center.X - other.Center.X
		dy := cv.Center.Y - other.Center.Y
		dist := math.Sqrt(dx*dx + dy*dy)
		if dist < secondBest {
			secondBest = dist
			secondClosest = &namedPin{other, n}
		}
	}

	if secondClosest != nil {
		refDx := secondClosest.cv.Center.X - closest.cv.Center.X
		refDy := secondClosest.cv.Center.Y - closest.cv.Center.Y
		refPinDiff := secondClosest.num - closest.num

		tgtDx := cv.Center.X - closest.cv.Center.X
		tgtDy := cv.Center.Y - closest.cv.Center.Y

		dot := tgtDx*refDx + tgtDy*refDy
		pinsPerStep := float64(refPinDiff) / math.Round(math.Sqrt(refDx*refDx+refDy*refDy)/pitch)

		if pinsPerStep != 0 {
			pinNum := closest.num + int(math.Round(float64(steps)*(pinsPerStep/math.Abs(pinsPerStep))))
			if dot < 0 {
				pinNum = closest.num - int(math.Round(float64(steps)*(pinsPerStep/math.Abs(pinsPerStep))))
			}
			if pinNum >= 1 {
				return strconv.Itoa(pinNum)
			}
		}
	}

	guess := closest.num + int(steps)
	if guess >= 1 {
		return strconv.Itoa(guess)
	}
	return ""
}

// getOrCreateNetForVia returns the net a via belongs to, or creates a new unnamed one.
func (tp *TracesPanel) getOrCreateNetForVia(cv *via.ConfirmedVia) *netlist.ElectricalNet {
	net := tp.state.FeaturesLayer.GetNetForElement(cv.ID)
	if net != nil {
		return net
	}
	netNum := tp.state.FeaturesLayer.NetCount() + 1
	id := fmt.Sprintf("net-%03d", netNum)
	net = netlist.NewElectricalNetWithName(id, id)
	net.AddVia(cv)
	tp.state.FeaturesLayer.AddNet(net)
	return net
}

// associateTraceVias ensures both vias of a completed trace share the same netlist.
func (tp *TracesPanel) associateTraceVias(startVia, endVia *via.ConfirmedVia) {
	startNet := tp.state.FeaturesLayer.GetNetForElement(startVia.ID)
	endNet := tp.state.FeaturesLayer.GetNetForElement(endVia.ID)

	if startNet != nil && endNet != nil {
		if startNet.ID == endNet.ID {
			return
		}
		for _, vid := range endNet.ViaIDs {
			if !startNet.ContainsVia(vid) {
				startNet.ViaIDs = append(startNet.ViaIDs, vid)
				startNet.Elements = append(startNet.Elements, netlist.NetElement{
					Type: netlist.ElementVia,
					ID:   vid,
				})
			}
		}
		tp.state.FeaturesLayer.RemoveNet(endNet.ID)
		fmt.Printf("Merged net %q into %q\n", endNet.Name, startNet.Name)
	} else if startNet != nil {
		startNet.AddVia(endVia)
		fmt.Printf("Added %s to net %q\n", endVia.ID, startNet.Name)
	} else if endNet != nil {
		endNet.AddVia(startVia)
		fmt.Printf("Added %s to net %q\n", startVia.ID, endNet.Name)
	} else {
		netNum := tp.state.FeaturesLayer.NetCount() + 1
		id := fmt.Sprintf("net-%03d", netNum)
		net := netlist.NewElectricalNetWithName(id, id)
		net.AddVia(startVia)
		net.AddVia(endVia)
		tp.state.FeaturesLayer.AddNet(net)
		fmt.Printf("Created net %q for %s and %s\n", id, startVia.ID, endVia.ID)
	}
	tp.state.Emit(app.EventNetlistModified, nil)
}

// refreshViaOverlay recreates the via overlay for the specified side.
func (tp *TracesPanel) refreshViaOverlay(side pcbimage.Side) {
	vias := tp.state.FeaturesLayer.GetViasBySide(side)
	skipMatched := tp.state.FeaturesLayer.ConfirmedViaCount() > 0
	tp.createViaOverlay(vias, side, skipMatched)
}

// refreshConfirmedViaOverlay recreates just the confirmed via overlay.
func (tp *TracesPanel) refreshConfirmedViaOverlay() {
	confirmedVias := tp.state.FeaturesLayer.GetConfirmedVias()
	tp.createConfirmedViaOverlay(confirmedVias)
	tp.confirmedCountLabel.SetText(fmt.Sprintf("Confirmed: %d", len(confirmedVias)))
}

// refreshAllViaOverlays recreates all via overlays.
func (tp *TracesPanel) refreshAllViaOverlays() {
	confirmedVias := tp.state.FeaturesLayer.GetConfirmedVias()
	tp.createConfirmedViaOverlay(confirmedVias)

	skipMatched := len(confirmedVias) > 0
	frontVias := tp.state.FeaturesLayer.GetViasBySide(pcbimage.SideFront)
	tp.createViaOverlay(frontVias, pcbimage.SideFront, skipMatched)

	backVias := tp.state.FeaturesLayer.GetViasBySide(pcbimage.SideBack)
	tp.createViaOverlay(backVias, pcbimage.SideBack, skipMatched)

	front, back := tp.state.FeaturesLayer.ViaCountBySide()
	tp.viaCountLabel.SetText(fmt.Sprintf("Vias: %d front, %d back", front, back))
	tp.confirmedCountLabel.SetText(fmt.Sprintf("Confirmed: %d", len(confirmedVias)))
}

// createConfirmedViaOverlay creates a canvas overlay for confirmed vias (blue).
func (tp *TracesPanel) createConfirmedViaOverlay(confirmedVias []*via.ConfirmedVia) {
	overlay := &canvas.Overlay{
		Color:      colorutil.Blue,
		Rectangles: make([]canvas.OverlayRect, 0),
		Polygons:   make([]canvas.OverlayPolygon, 0),
	}

	for _, cv := range confirmedVias {
		label := cv.ID
		var viaNum int
		if _, err := fmt.Sscanf(cv.ID, "cvia-%d", &viaNum); err == nil {
			label = fmt.Sprintf("%d", viaNum)
		}

		if len(cv.IntersectionBoundary) >= 3 {
			overlay.Polygons = append(overlay.Polygons, canvas.OverlayPolygon{
				Points: cv.IntersectionBoundary,
				Label:  label,
				Filled: true,
			})
		} else {
			bounds := cv.Bounds()
			overlay.Rectangles = append(overlay.Rectangles, canvas.OverlayRect{
				X: bounds.X, Y: bounds.Y, Width: bounds.Width, Height: bounds.Height,
				Fill: canvas.FillSolid, Label: label,
			})
		}
	}

	tp.canvas.SetOverlay(OverlayConfirmedVias, overlay)
}

// createConnectorOverlay creates a canvas overlay for board edge connectors.
func (tp *TracesPanel) createConnectorOverlay() {
	connectors := tp.state.FeaturesLayer.GetConnectors()
	fmt.Printf("  createConnectorOverlay: %d connectors\n", len(connectors))

	overlay := &canvas.Overlay{
		Color:      colorutil.Green,
		Rectangles: make([]canvas.OverlayRect, 0, len(connectors)),
		Polygons:   make([]canvas.OverlayPolygon, 0),
	}

	labels := make([]canvas.ConnectorLabel, 0, len(connectors))

	for _, c := range connectors {
		label := c.SignalName
		if label == "" {
			label = fmt.Sprintf("P%d", c.PinNumber)
		}

		overlay.Rectangles = append(overlay.Rectangles, canvas.OverlayRect{
			X: c.Bounds.X, Y: c.Bounds.Y, Width: c.Bounds.Width, Height: c.Bounds.Height,
			Fill: canvas.FillNone,
		})
		labels = append(labels, canvas.ConnectorLabel{
			Label: label, CenterX: c.Center.X, CenterY: c.Center.Y, Side: c.Side,
		})
	}

	tp.canvas.SetOverlay(OverlayConnectors, overlay)
	tp.canvas.SetConnectorLabels(labels)
}

// tryMatchVias attempts to match front and back vias to create confirmed vias.
func (tp *TracesPanel) tryMatchVias() {
	frontVias := tp.state.FeaturesLayer.GetViasBySide(pcbimage.SideFront)
	backVias := tp.state.FeaturesLayer.GetViasBySide(pcbimage.SideBack)

	if len(frontVias) == 0 || len(backVias) == 0 {
		tp.viaStatusLabel.SetText("Need vias on both sides to match")
		return
	}
	if !tp.state.Aligned {
		tp.viaStatusLabel.SetText("Images must be aligned before matching")
		return
	}

	tp.viaStatusLabel.SetText("Matching vias...")
	tp.state.FeaturesLayer.ClearConfirmedVias()

	tolerance := via.SuggestMatchTolerance(tp.state.DPI)
	fmt.Printf("tryMatchVias: %d front, %d back, tolerance=%.1f px\n", len(frontVias), len(backVias), tolerance)

	result := via.MatchViasAcrossSides(frontVias, backVias, tolerance)

	for _, cv := range result.ConfirmedVias {
		tp.state.FeaturesLayer.AddConfirmedVia(cv)
	}
	for _, v := range frontVias {
		tp.state.FeaturesLayer.UpdateVia(v)
	}
	for _, v := range backVias {
		tp.state.FeaturesLayer.UpdateVia(v)
	}

	tp.refreshAllViaOverlays()
	tp.viaStatusLabel.SetText(fmt.Sprintf("Matched %d vias (avg err: %.1f px)", result.Matched, result.AvgError))
	tp.state.Emit(app.EventConfirmedViasChanged, nil)
}

// updateTraceOverlay rebuilds the in-progress trace overlay.
func (tp *TracesPanel) updateTraceOverlay() {
	if len(tp.tracePoints) < 2 {
		tp.canvas.ClearOverlay("trace_segments")
		return
	}
	lines := make([]canvas.OverlayLine, 0, len(tp.tracePoints)-1)
	for i := 1; i < len(tp.tracePoints); i++ {
		lines = append(lines, canvas.OverlayLine{
			X1: tp.tracePoints[i-1].X, Y1: tp.tracePoints[i-1].Y,
			X2: tp.tracePoints[i].X, Y2: tp.tracePoints[i].Y,
			Thickness: 2,
		})
	}
	tp.canvas.SetOverlay("trace_segments", &canvas.Overlay{
		Lines: lines,
		Color: color.RGBA{R: 0, G: 255, B: 0, A: 255},
	})
}

// setupTraceRubberBand installs the rubber-band drawing from the last point.
func (tp *TracesPanel) setupTraceRubberBand() {
	if len(tp.tracePoints) > 0 {
		last := tp.tracePoints[len(tp.tracePoints)-1]
		tp.canvas.ShowRubberBand(last.X, last.Y)
	}
	tp.canvas.OnMouseMove(nil)
}

// finishTrace is called when the polyline terminates at a confirmed via.
func (tp *TracesPanel) finishTrace(endVia *via.ConfirmedVia) {
	tp.canvas.HideRubberBand()
	tp.canvas.ClearOverlay("trace_segments")
	tp.canvas.OnMouseMove(nil)
	tp.traceMode = false

	nSegs := len(tp.tracePoints) - 1
	fmt.Printf("Trace complete: %s -> %s (%d segments, %d points)\n",
		tp.traceStartVia.ID, endVia.ID, nSegs, len(tp.tracePoints))

	traceID := fmt.Sprintf("trace-%03d", tp.state.FeaturesLayer.TraceCount()+1)
	points := make([]geometry.Point2D, len(tp.tracePoints))
	copy(points, tp.tracePoints)
	et := pcbtrace.ExtendedTrace{
		Trace: pcbtrace.Trace{
			ID: traceID, Layer: tp.traceLayer, Points: points,
		},
		Source: pcbtrace.SourceManual,
	}
	tp.state.FeaturesLayer.AddTrace(et)

	layerRef := canvas.LayerFront
	if tp.traceLayer == pcbtrace.LayerBack {
		layerRef = canvas.LayerBack
	}
	tp.renderTraceOverlay(traceID, points, layerRef)

	tp.associateTraceVias(tp.traceStartVia, endVia)

	net := tp.state.FeaturesLayer.GetNetForElement(tp.traceStartVia.ID)
	netInfo := ""
	if net != nil {
		netInfo = fmt.Sprintf(" [%s]", net.Name)
	}
	tp.traceStatusLabel.SetText(fmt.Sprintf("Trace: %s -> %s (%d segments)%s",
		tp.traceStartVia.ID, endVia.ID, nSegs, netInfo))
	tp.traceStartVia = nil
}

// renderTraceOverlay draws a completed trace as a persistent overlay.
func (tp *TracesPanel) renderTraceOverlay(id string, points []geometry.Point2D, layer canvas.LayerRef) {
	if len(points) < 2 {
		return
	}
	lines := make([]canvas.OverlayLine, 0, len(points)-1)
	for i := 1; i < len(points); i++ {
		lines = append(lines, canvas.OverlayLine{
			X1: points[i-1].X, Y1: points[i-1].Y,
			X2: points[i].X, Y2: points[i].Y,
			Thickness: 2,
		})
	}
	tp.canvas.SetOverlay(id, &canvas.Overlay{
		Lines: lines,
		Color: color.RGBA{R: 0, G: 255, B: 0, A: 255},
		Layer: layer,
	})
}

// Ensure connector import is used (referenced in createConnectorOverlay).
var _ = (*connector.Connector)(nil)
