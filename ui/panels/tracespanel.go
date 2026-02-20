package panels

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
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
	"pcb-tracer/ui/prefs"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

// Overlay names for persistent board features, split by visibility.
const (
	OverlayFeaturesFront = "features_front" // Front-side connectors + traces (visible when front raised)
	OverlayFeaturesBack  = "features_back"  // Back-side connectors + traces (visible when back raised)
	OverlayFeaturesVias  = "features_vias"  // Confirmed + detected vias (always visible)
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
	traceMode               bool
	traceStartVia           *via.ConfirmedVia
	traceStartConn          *connector.Connector
	traceStartJunctionTrace string // trace ID when starting from a junction vertex
	traceEndVia             *via.ConfirmedVia
	traceEndConn            *connector.Connector
	traceEndJunctionTrace   string // trace ID when ending at a junction vertex
	tracePoints             []geometry.Point2D
	traceLayer              pcbtrace.TraceLayer

	// Last created trace ID (for adding to net)
	lastTraceID string

	// Vertex drag state
	draggingVertex bool
	dragTraceID    string
	dragPointIndex int

	// Selected via for arrow-key nudging
	selectedVia *via.ConfirmedVia

	// Selected connector for arrow-key nudging
	selectedConnector *connector.Connector

	// Add connectors button
	addConnectorsBtn *gtk.Button

	// Default via radius for manual addition
	defaultViaRadius float64

	// Hover state for net info display
	hoverNetID string

	// Preferences
	prefs          *prefs.Prefs
	showViaNumbers bool
}

// NewTracesPanel creates a new traces panel.
const prefKeyShowViaNumbers = "showViaNumbers"

func NewTracesPanel(state *app.State, cvs *canvas.ImageCanvas, win *gtk.Window, p *prefs.Prefs) *TracesPanel {
	tp := &TracesPanel{
		state:            state,
		canvas:           cvs,
		win:              win,
		defaultViaRadius: 15,
		prefs:            p,
		showViaNumbers:   p.Bool(prefKeyShowViaNumbers, true),
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

	showViaNumCheck, _ := gtk.CheckButtonNewWithLabel("Show via numbers")
	showViaNumCheck.SetActive(tp.showViaNumbers)
	showViaNumCheck.Connect("toggled", func() {
		tp.showViaNumbers = showViaNumCheck.GetActive()
		tp.prefs.SetBool(prefKeyShowViaNumbers, tp.showViaNumbers)
		tp.prefs.Save()
		tp.rebuildFeaturesOverlay()
		tp.canvas.Refresh()
	})
	viaBox.PackStart(showViaNumCheck, false, false, 0)

	tp.addConnectorsBtn, _ = gtk.ButtonNewWithLabel("Add Connectors")
	tp.addConnectorsBtn.Connect("clicked", func() { tp.onAddConnectors() })
	viaBox.PackStart(tp.addConnectorsBtn, false, false, 0)

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
		"Click via/conn: start trace  Click empty: add via",
		"While drawing: click waypoints, end on via/conn",
		"Right-click: menu  Arrow keys: nudge selected via",
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
		tp.canvas.ClearOverlay("vertex_drag")
		tp.canvas.HideRubberBand()
		tp.traceMode = false
		tp.traceStartVia = nil
		tp.traceStartConn = nil
		tp.traceStartJunctionTrace = ""
		tp.traceEndVia = nil
		tp.traceEndConn = nil
		tp.traceEndJunctionTrace = ""
		tp.tracePoints = nil
		tp.draggingVertex = false
		tp.canvas.OnMouseMove(nil)
		tp.traceStatusLabel.SetText("Cleared")
		tp.canvas.Refresh()
	})
	traceBox.PackStart(tp.detectTracesBtn, false, false, 0)

	tp.traceStatusLabel, _ = gtk.LabelNew("Click via/connector to start trace")
	tp.traceStatusLabel.SetLineWrap(true)
	tp.traceStatusLabel.SetHAlign(gtk.ALIGN_START)
	traceBox.PackStart(tp.traceStatusLabel, false, false, 0)

	traceFrame.Add(traceBox)
	tp.box.PackStart(traceFrame, false, false, 0)

	// Load training set
	tp.loadTrainingSet()
	tp.updateTrainingLabel()

	// Redraw features overlay when connectors are created/rebuilt
	state.On(app.EventConnectorsCreated, func(_ interface{}) {
		tp.rebuildFeaturesOverlay()
	})

	// Rebuild overlay when nets change (trace colors depend on net status)
	state.On(app.EventNetlistModified, func(_ interface{}) {
		tp.rebuildFeaturesOverlay()
		tp.canvas.Refresh()
	})

	// Auto-match vias when alignment completes
	state.On(app.EventAlignmentComplete, func(data interface{}) {
		tp.refreshConnectors()

		frontVias := tp.state.FeaturesLayer.GetViasBySide(pcbimage.SideFront)
		backVias := tp.state.FeaturesLayer.GetViasBySide(pcbimage.SideBack)
		if len(frontVias) > 0 && len(backVias) > 0 {
			fmt.Printf("Auto-matching vias after alignment complete...\n")
			tp.tryMatchVias()
		}
	})

	// Restore overlays on project load
	state.On(app.EventProjectLoaded, func(_ interface{}) {
		glib.IdleAdd(func() {
			tp.rebuildFeaturesOverlay()
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

// selectedSide returns the pcbimage.Side matching the current layer selection.
func (tp *TracesPanel) selectedSide() pcbimage.Side {
	if tp.viaLayerFront.GetActive() {
		return pcbimage.SideFront
	}
	return pcbimage.SideBack
}

// selectedTraceLayer returns the trace layer matching the current layer selection.
func (tp *TracesPanel) selectedTraceLayer() pcbtrace.TraceLayer {
	if tp.viaLayerFront.GetActive() {
		return pcbtrace.LayerFront
	}
	return pcbtrace.LayerBack
}

// SetEnabled enables or disables the panel's interactive widgets.
func (tp *TracesPanel) SetEnabled(enabled bool) {
	tp.detectViasBtn.SetSensitive(enabled)
	tp.clearViasBtn.SetSensitive(enabled)
	tp.matchViasBtn.SetSensitive(enabled)
	tp.addConnectorsBtn.SetSensitive(enabled)
	tp.detectTracesBtn.SetSensitive(enabled)
}

// OnKeyPressed handles keyboard input for arrow-key via nudging and Escape cancellation.
// Called from the window-level key-press-event.
func (tp *TracesPanel) OnKeyPressed(ev *gdk.EventKey) bool {
	keyval := ev.KeyVal()

	// Escape cancels active operations
	if keyval == gdk.KEY_Escape {
		if tp.draggingVertex {
			tp.cancelVertexDrag()
			return true
		}
		if tp.traceMode {
			tp.cancelTrace()
			return true
		}
		if tp.selectedVia != nil {
			tp.deselectVia()
			return true
		}
		if tp.selectedConnector != nil {
			tp.deselectConnector()
			return true
		}
		return false
	}

	// Arrow-key nudging for selected via
	if tp.selectedVia != nil {
		step := 1.0
		if ev.State()&uint(gdk.SHIFT_MASK) != 0 {
			step = 5.0
		}

		cv := tp.selectedVia
		switch keyval {
		case gdk.KEY_Up:
			cv.Center.Y -= step
		case gdk.KEY_Down:
			cv.Center.Y += step
		case gdk.KEY_Left:
			cv.Center.X -= step
		case gdk.KEY_Right:
			cv.Center.X += step
		default:
			return false
		}

		cv.IntersectionBoundary = geometry.GenerateCirclePoints(cv.Center.X, cv.Center.Y, cv.Radius, 32)
		tp.rebuildFeaturesOverlay()
		tp.updateSelectedViaOverlay()
		tp.canvas.Refresh()
		tp.viaStatusLabel.SetText(fmt.Sprintf("%s center: (%.0f, %.0f)", cv.ID, cv.Center.X, cv.Center.Y))
		tp.state.Emit(app.EventConfirmedViasChanged, nil)
		return true
	}

	// Arrow-key nudging for selected connector
	if tp.selectedConnector != nil {
		step := 1.0
		if ev.State()&uint(gdk.SHIFT_MASK) != 0 {
			step = 5.0
		}

		conn := tp.selectedConnector
		switch keyval {
		case gdk.KEY_Up:
			conn.Center.Y -= step
			conn.Bounds.Y -= int(step)
		case gdk.KEY_Down:
			conn.Center.Y += step
			conn.Bounds.Y += int(step)
		case gdk.KEY_Left:
			conn.Center.X -= step
			conn.Bounds.X -= int(step)
		case gdk.KEY_Right:
			conn.Center.X += step
			conn.Bounds.X += int(step)
		default:
			return false
		}

		tp.rebuildFeaturesOverlay()
		tp.updateSelectedConnectorOverlay()
		tp.canvas.Refresh()
		tp.viaStatusLabel.SetText(fmt.Sprintf("%s center: (%.0f, %.0f)", conn.ID, conn.Center.X, conn.Center.Y))
		tp.state.Emit(app.EventConnectorsChanged, nil)
		return true
	}

	return false
}

// cancelTrace cancels the in-progress trace drawing.
func (tp *TracesPanel) cancelTrace() {
	tp.canvas.HideRubberBand()
	tp.canvas.ClearOverlay("trace_segments")
	tp.canvas.OnMouseMove(nil)
	tp.traceMode = false
	tp.traceStartVia = nil
	tp.traceStartConn = nil
	tp.traceStartJunctionTrace = ""
	tp.traceEndVia = nil
	tp.traceEndConn = nil
	tp.traceEndJunctionTrace = ""
	tp.tracePoints = nil
	tp.traceStatusLabel.SetText("Trace cancelled")
	tp.canvas.Refresh()
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
			tp.rebuildFeaturesOverlay()
			front, back := tp.state.FeaturesLayer.ViaCountBySide()
			tp.viaCountLabel.SetText(fmt.Sprintf("Vias: %d front, %d back", front, back))
			tp.viaStatusLabel.SetText(fmt.Sprintf("%s: %d vias (%d Hough, %d contour)",
				layerName, len(result.Vias), houghCount, contourCount))
			tp.state.Emit(app.EventFeaturesChanged, nil)
		})
	}()
}

// rebuildFeaturesOverlay rebuilds the three feature overlays from all model data.
// Front/back overlays track their respective image layers for visibility;
// the vias overlay is always visible (vias penetrate both sides).
func (tp *TracesPanel) rebuildFeaturesOverlay() {
	// Clear import panel diagnostic overlays so they don't cover features
	tp.canvas.ClearOverlay("front_contacts")
	tp.canvas.ClearOverlay("back_contacts")

	frontOverlay := &canvas.Overlay{Layer: canvas.LayerFront}
	backOverlay := &canvas.Overlay{Layer: canvas.LayerBack}
	viasOverlay := &canvas.Overlay{}

	cyan := &colorutil.Cyan
	magenta := &colorutil.Magenta
	blue := &colorutil.Blue

	// 1. Connectors: split by side
	for _, c := range tp.state.FeaturesLayer.GetConnectors() {
		label := c.SignalName
		if label == "" {
			label = fmt.Sprintf("P%d", c.PinNumber)
		}
		var col *color.RGBA
		var target *canvas.Overlay
		if c.Side == pcbimage.SideFront {
			col = cyan
			target = frontOverlay
		} else {
			col = magenta
			target = backOverlay
		}
		target.Rectangles = append(target.Rectangles, canvas.OverlayRect{
			X: c.Bounds.X, Y: c.Bounds.Y, Width: c.Bounds.Width, Height: c.Bounds.Height,
			Fill:         canvas.FillSolid,
			Label:        label,
			LabelRotated: true,
			Color:        col,
		})
	}

	// 2. Confirmed vias: blue, filled, labeled — always visible
	for _, cv := range tp.state.FeaturesLayer.GetConfirmedVias() {
		label := ""
		if cv.ComponentID != "" && cv.PinNumber != "" {
			label = fmt.Sprintf("%s-%s", cv.ComponentID, cv.PinNumber)
		} else if tp.showViaNumbers {
			var viaNum int
			if _, err := fmt.Sscanf(cv.ID, "cvia-%d", &viaNum); err == nil {
				label = fmt.Sprintf("%d", viaNum)
			} else {
				label = cv.ID
			}
		}
		if len(cv.IntersectionBoundary) >= 3 {
			viasOverlay.Polygons = append(viasOverlay.Polygons, canvas.OverlayPolygon{
				Points: cv.IntersectionBoundary,
				Label:  label,
				Filled: true,
				Color:  blue,
			})
		} else {
			bounds := cv.Bounds()
			viasOverlay.Rectangles = append(viasOverlay.Rectangles, canvas.OverlayRect{
				X: bounds.X, Y: bounds.Y, Width: bounds.Width, Height: bounds.Height,
				Fill: canvas.FillSolid, Label: label,
				Color: blue,
			})
		}
	}

	// 3. Detected vias: cyan (front) / magenta (back) outline — always visible
	skipMatched := tp.state.FeaturesLayer.ConfirmedViaCount() > 0
	for _, side := range []pcbimage.Side{pcbimage.SideFront, pcbimage.SideBack} {
		var col *color.RGBA
		if side == pcbimage.SideFront {
			col = cyan
		} else {
			col = magenta
		}
		for _, v := range tp.state.FeaturesLayer.GetViasBySide(side) {
			if skipMatched && v.BothSidesConfirmed {
				continue
			}
			if len(v.PadBoundary) >= 3 {
				viasOverlay.Polygons = append(viasOverlay.Polygons, canvas.OverlayPolygon{
					Points: v.PadBoundary,
					Filled: false,
					Color:  col,
				})
			} else {
				bounds := v.Bounds()
				viasOverlay.Rectangles = append(viasOverlay.Rectangles, canvas.OverlayRect{
					X: bounds.X, Y: bounds.Y, Width: bounds.Width, Height: bounds.Height,
					Fill:  canvas.FillNone,
					Color: col,
				})
			}
		}
	}

	// 4. Completed traces: split by layer, colored by net status
	red := &color.RGBA{R: 255, G: 0, B: 0, A: 255}
	for _, tid := range tp.state.FeaturesLayer.GetTraces() {
		tf := tp.state.FeaturesLayer.GetTraceFeature(tid)
		if tf == nil || len(tf.Points) < 2 {
			continue
		}
		var target *canvas.Overlay
		// Pick color: named net → cyan (front) / magenta (back), unnamed → red
		net := tp.state.FeaturesLayer.GetNetForElement(tid)
		hasNamedNet := net != nil && net.Name != "" && net.Name != net.ID
		var traceColor *color.RGBA
		if tf.Layer == pcbtrace.LayerBack {
			target = backOverlay
			if hasNamedNet {
				traceColor = magenta
			} else {
				traceColor = red
			}
		} else {
			target = frontOverlay
			if hasNamedNet {
				traceColor = cyan
			} else {
				traceColor = red
			}
		}
		for i := 1; i < len(tf.Points); i++ {
			target.Lines = append(target.Lines, canvas.OverlayLine{
				X1: tf.Points[i-1].X, Y1: tf.Points[i-1].Y,
				X2: tf.Points[i].X, Y2: tf.Points[i].Y,
				Thickness: 1,
				Color:     traceColor,
			})
		}
		if len(tf.Points) > 2 {
			for _, pt := range tf.Points[1 : len(tf.Points)-1] {
				target.Circles = append(target.Circles, canvas.OverlayCircle{
					X: pt.X, Y: pt.Y, Radius: 4, Filled: true,
					Color: traceColor,
				})
			}
		}
	}

	tp.canvas.SetOverlay(OverlayFeaturesFront, frontOverlay)
	tp.canvas.SetOverlay(OverlayFeaturesBack, backOverlay)
	tp.canvas.SetOverlay(OverlayFeaturesVias, viasOverlay)
}

// onClearVias clears all detected vias.
func (tp *TracesPanel) onClearVias() {
	tp.state.FeaturesLayer.ClearVias()
	tp.state.FeaturesLayer.ClearConfirmedVias()
	tp.rebuildFeaturesOverlay()
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

// onLeftClick handles left-click for polyline trace drawing, vertex dragging, and via/connector interaction.
func (tp *TracesPanel) onLeftClick(x, y float64) {
	// If dragging a vertex, place it
	if tp.draggingVertex {
		tp.finishVertexDrag(x, y)
		return
	}

	// If in trace drawing mode
	if tp.traceMode {
		// Hit-test confirmed via (not the start via) → finish trace
		cv := tp.state.FeaturesLayer.HitTestConfirmedVia(x, y)
		if cv != nil {
			startID := ""
			if tp.traceStartVia != nil {
				startID = tp.traceStartVia.ID
			}
			if cv.ID != startID {
				tp.tracePoints = append(tp.tracePoints, cv.Center)
				tp.finishTraceAtVia(cv)
				return
			}
		}

		// Hit-test connector on selected side → finish trace at click position
		conn := tp.state.FeaturesLayer.HitTestConnectorOnSide(x, y, tp.selectedSide())
		if conn != nil {
			startConnID := ""
			if tp.traceStartConn != nil {
				startConnID = tp.traceStartConn.ID
			}
			if conn.ID != startConnID {
				tp.tracePoints = append(tp.tracePoints, geometry.Point2D{X: x, Y: y})
				tp.finishTraceAtConnector(conn)
				return
			}
		}

		// Hit-test vertex on existing trace (junction) on selected layer
		if jTraceID, _, ok := tp.hitTestVertex(x, y); ok {
			tf := tp.state.FeaturesLayer.GetTraceFeature(jTraceID)
			if tf != nil {
				tp.tracePoints = append(tp.tracePoints, geometry.Point2D{X: x, Y: y})
				tp.finishTraceAtJunction(jTraceID)
				return
			}
		}

		// Otherwise add waypoint
		tp.tracePoints = append(tp.tracePoints, geometry.Point2D{X: x, Y: y})
		tp.canvas.ShowRubberBand(x, y)
		tp.updateTraceOverlay()
		startLabel := tp.traceStartLabel()
		tp.traceStatusLabel.SetText(fmt.Sprintf("Trace from %s — %d segments — click waypoints, end on via/connector/vertex",
			startLabel, len(tp.tracePoints)-1))
		return
	}

	// Hit-test confirmed via → start trace (takes priority over vertex drag)
	cv := tp.state.FeaturesLayer.HitTestConfirmedVia(x, y)
	if cv != nil {
		tp.startTraceFromVia(cv)
		return
	}

	// Hit-test connector on selected side → start trace at click position
	conn := tp.state.FeaturesLayer.HitTestConnectorOnSide(x, y, tp.selectedSide())
	if conn != nil {
		tp.startTraceFromConnector(conn, x, y)
		return
	}

	// Hit-test existing vertex on completed traces for drag
	if traceID, pointIdx, ok := tp.hitTestVertex(x, y); ok {
		tp.startVertexDrag(traceID, pointIdx)
		return
	}

	// Empty space → add confirmed via
	tp.addConfirmedViaAt(x, y)
}

// traceStartLabel returns a display label for the trace start point.
func (tp *TracesPanel) traceStartLabel() string {
	if tp.traceStartVia != nil {
		return tp.traceStartVia.ID
	}
	if tp.traceStartConn != nil {
		name := tp.traceStartConn.SignalName
		if name == "" {
			name = fmt.Sprintf("P%d", tp.traceStartConn.PinNumber)
		}
		return name
	}
	return "?"
}

// startTraceFromVia enters trace drawing mode starting from a confirmed via.
func (tp *TracesPanel) startTraceFromVia(cv *via.ConfirmedVia) {
	tp.traceMode = true
	tp.traceStartVia = cv
	tp.traceStartConn = nil
	tp.traceStartJunctionTrace = ""
	tp.tracePoints = []geometry.Point2D{cv.Center}
	if tp.selectedLayer() == "Front" {
		tp.traceLayer = pcbtrace.LayerFront
	} else {
		tp.traceLayer = pcbtrace.LayerBack
	}
	tp.setupTraceRubberBand()
	tp.traceStatusLabel.SetText(fmt.Sprintf("Trace from %s — click waypoints, end on via/connector", cv.ID))
}

// startTraceFromConnector enters trace drawing mode starting from a connector.
func (tp *TracesPanel) startTraceFromConnector(conn *connector.Connector, x, y float64) {
	tp.traceMode = true
	tp.traceStartVia = nil
	tp.traceStartConn = conn
	tp.traceStartJunctionTrace = ""
	tp.tracePoints = []geometry.Point2D{{X: x, Y: y}}
	if tp.selectedLayer() == "Front" {
		tp.traceLayer = pcbtrace.LayerFront
	} else {
		tp.traceLayer = pcbtrace.LayerBack
	}
	tp.setupTraceRubberBand()
	name := conn.SignalName
	if name == "" {
		name = fmt.Sprintf("P%d", conn.PinNumber)
	}
	tp.traceStatusLabel.SetText(fmt.Sprintf("Trace from connector %s — click waypoints, end on via/connector", name))
}

// selectVia makes a confirmed via the selected via for nudging.
func (tp *TracesPanel) selectVia(cv *via.ConfirmedVia) {
	tp.deselectConnector()
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

// selectConnector makes a connector the selected connector for nudging.
func (tp *TracesPanel) selectConnector(conn *connector.Connector) {
	tp.deselectVia()
	tp.selectedConnector = conn
	label := conn.SignalName
	if label == "" {
		label = fmt.Sprintf("P%d", conn.PinNumber)
	}
	tp.viaStatusLabel.SetText(fmt.Sprintf("Selected %s (%s) — arrow keys to nudge, Shift for 5px", conn.ID, label))
	tp.updateSelectedConnectorOverlay()
}

// deselectConnector clears the selected connector.
func (tp *TracesPanel) deselectConnector() {
	if tp.selectedConnector == nil {
		return
	}
	tp.selectedConnector = nil
	tp.canvas.ClearOverlay("selected_connector")
	tp.viaStatusLabel.SetText("")
	tp.canvas.Refresh()
}

// updateSelectedConnectorOverlay draws a highlight rect around the selected connector.
func (tp *TracesPanel) updateSelectedConnectorOverlay() {
	if tp.selectedConnector == nil {
		tp.canvas.ClearOverlay("selected_connector")
		return
	}
	c := tp.selectedConnector
	pad := 2
	tp.canvas.SetOverlay("selected_connector", &canvas.Overlay{
		Rectangles: []canvas.OverlayRect{
			{
				X: c.Bounds.X - pad, Y: c.Bounds.Y - pad,
				Width: c.Bounds.Width + 2*pad, Height: c.Bounds.Height + 2*pad,
				Fill: canvas.FillNone,
			},
		},
		Color: color.RGBA{R: 255, G: 255, B: 255, A: 255},
	})
}

// onRightClickVia handles right-click on the canvas.
func (tp *TracesPanel) onRightClickVia(x, y float64) {
	// Cancel active operations on right-click
	if tp.draggingVertex {
		tp.cancelVertexDrag()
	}
	if tp.traceMode {
		tp.cancelTrace()
	}

	cv := tp.state.FeaturesLayer.HitTestConfirmedVia(x, y)
	if cv != nil {
		tp.selectVia(cv)
		tp.showConfirmedViaMenu(cv)
		return
	}

	conn := tp.state.FeaturesLayer.HitTestConnectorOnSide(x, y, tp.selectedSide())
	if conn != nil {
		tp.selectConnector(conn)
		tp.showConnectorMenu(conn)
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
	addSep()
	addItem("Auto-trace to next via", func() { tp.autoTraceToAdjacentVia(cv, +1) })
	addItem("Auto-trace to prev via", func() { tp.autoTraceToAdjacentVia(cv, -1) })

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

// hitTestTraceSegment returns the trace and segment closest to (x, y) on the selected layer.
func (tp *TracesPanel) hitTestTraceSegment(x, y float64) *traceHit {
	tolerance := 10.0
	if tp.state.DPI > 0 {
		tolerance = 0.015 * tp.state.DPI
	}

	activeLayer := tp.selectedTraceLayer()
	var bestHit *traceHit
	bestDist := tolerance

	for _, tid := range tp.state.FeaturesLayer.GetTraces() {
		tf := tp.state.FeaturesLayer.GetTraceFeature(tid)
		if tf == nil || len(tf.Points) < 2 {
			continue
		}
		if tf.Layer != activeLayer {
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
		tp.traceStatusLabel.SetText(fmt.Sprintf("Deleted %s", hit.traceID))
	} else {
		removeIdx := hit.segIndex + 1
		if removeIdx >= len(tf.Points)-1 {
			removeIdx = len(tf.Points) - 1
		}
		newPoints := make([]geometry.Point2D, 0, len(tf.Points)-1)
		newPoints = append(newPoints, tf.Points[:removeIdx]...)
		newPoints = append(newPoints, tf.Points[removeIdx+1:]...)

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
		tp.traceStatusLabel.SetText(fmt.Sprintf("%s: %d segments", hit.traceID, len(newPoints)-1))
	}
	tp.rebuildFeaturesOverlay()
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

	tp.rebuildFeaturesOverlay()
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
	tp.rebuildFeaturesOverlay()
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
	tp.rebuildFeaturesOverlay()
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
	tp.rebuildFeaturesOverlay()
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
			removed++
		}
	}
	tp.rebuildFeaturesOverlay()
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
	tp.rebuildFeaturesOverlay()
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
		[]interface{}{"Cancel", gtk.RESPONSE_CANCEL},
		[]interface{}{"OK", gtk.RESPONSE_OK})
	dlg.SetDefaultSize(300, 150)
	dlg.SetDefaultResponse(gtk.RESPONSE_OK)

	contentArea, _ := dlg.GetContentArea()
	entry, _ := gtk.EntryNew()
	entry.SetActivatesDefault(true)
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
		[]interface{}{"Cancel", gtk.RESPONSE_CANCEL},
		[]interface{}{"OK", gtk.RESPONSE_OK})
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
		tp.rebuildFeaturesOverlay()
		tp.canvas.Refresh()
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

// associateTraceEndpoints ensures both endpoints of a completed trace share the same
// electrical net. Handles all 4 endpoint combinations: via↔via, via↔connector,
// connector↔via, connector↔connector.
func (tp *TracesPanel) associateTraceEndpoints() {
	startVia := tp.traceStartVia
	startConn := tp.traceStartConn

	// Look up existing nets for each endpoint
	var startNet, endNet *netlist.ElectricalNet
	var startID, endID string

	if startVia != nil {
		startID = startVia.ID
		startNet = tp.state.FeaturesLayer.GetNetForElement(startVia.ID)
	} else if startConn != nil {
		startID = startConn.ID
		startNet = tp.state.FeaturesLayer.GetNetForElement(startConn.ID)
	} else if tp.traceStartJunctionTrace != "" {
		startID = tp.traceStartJunctionTrace
		startNet = tp.findNetForTrace(tp.traceStartJunctionTrace)
	}

	if tp.traceEndVia != nil {
		endID = tp.traceEndVia.ID
		endNet = tp.state.FeaturesLayer.GetNetForElement(tp.traceEndVia.ID)
	} else if tp.traceEndConn != nil {
		endID = tp.traceEndConn.ID
		endNet = tp.state.FeaturesLayer.GetNetForElement(tp.traceEndConn.ID)
	} else if tp.traceEndJunctionTrace != "" {
		endID = tp.traceEndJunctionTrace
		endNet = tp.findNetForTrace(tp.traceEndJunctionTrace)
	}

	// For connector endpoints with no existing net, create one from the connector's signal name
	if startConn != nil && startNet == nil {
		startNet = netlist.NewElectricalNet(startConn)
		tp.state.FeaturesLayer.AddNet(startNet)
		fmt.Printf("Created net %q from connector %s\n", startNet.Name, startConn.ID)
	}
	if tp.traceEndConn != nil && endNet == nil {
		endNet = netlist.NewElectricalNet(tp.traceEndConn)
		tp.state.FeaturesLayer.AddNet(endNet)
		fmt.Printf("Created net %q from connector %s\n", endNet.Name, tp.traceEndConn.ID)
	}

	// addTraceToNet is called at the end to register the new trace with the resulting net.
	addTraceToNet := func(net *netlist.ElectricalNet) {
		if net == nil || tp.lastTraceID == "" {
			return
		}
		if !net.ContainsElement(tp.lastTraceID) {
			net.TraceIDs = append(net.TraceIDs, tp.lastTraceID)
			net.Elements = append(net.Elements, netlist.NetElement{
				Type: netlist.ElementTrace, ID: tp.lastTraceID,
			})
		}
		// Also add the junction trace if ending at one
		if tp.traceEndJunctionTrace != "" && !net.ContainsElement(tp.traceEndJunctionTrace) {
			net.TraceIDs = append(net.TraceIDs, tp.traceEndJunctionTrace)
			net.Elements = append(net.Elements, netlist.NetElement{
				Type: netlist.ElementTrace, ID: tp.traceEndJunctionTrace,
			})
		}
		if tp.traceStartJunctionTrace != "" && !net.ContainsElement(tp.traceStartJunctionTrace) {
			net.TraceIDs = append(net.TraceIDs, tp.traceStartJunctionTrace)
			net.Elements = append(net.Elements, netlist.NetElement{
				Type: netlist.ElementTrace, ID: tp.traceStartJunctionTrace,
			})
		}
	}

	if startNet != nil && endNet != nil {
		// Both have nets
		if startNet.ID == endNet.ID {
			addTraceToNet(startNet)
			tp.state.Emit(app.EventNetlistModified, nil)
			return
		}
		// Merge: keep the net with the better name
		keepNet, absorbNet := startNet, endNet
		if netlist.BetterNetName(endNet.Name, startNet.Name) == endNet.Name &&
			netlist.BetterNetName(endNet.Name, startNet.Name) != startNet.Name {
			keepNet, absorbNet = endNet, startNet
		}
		// Absorb all elements from the losing net
		for _, vid := range absorbNet.ViaIDs {
			if !keepNet.ContainsVia(vid) {
				keepNet.ViaIDs = append(keepNet.ViaIDs, vid)
				keepNet.Elements = append(keepNet.Elements, netlist.NetElement{
					Type: netlist.ElementVia, ID: vid,
				})
			}
		}
		for _, cid := range absorbNet.ConnectorIDs {
			if !keepNet.ContainsConnector(cid) {
				keepNet.ConnectorIDs = append(keepNet.ConnectorIDs, cid)
				keepNet.Elements = append(keepNet.Elements, netlist.NetElement{
					Type: netlist.ElementConnector, ID: cid,
				})
			}
		}
		for _, tid := range absorbNet.TraceIDs {
			if !keepNet.ContainsElement(tid) {
				keepNet.TraceIDs = append(keepNet.TraceIDs, tid)
				keepNet.Elements = append(keepNet.Elements, netlist.NetElement{
					Type: netlist.ElementTrace, ID: tid,
				})
			}
		}
		for _, pid := range absorbNet.PadIDs {
			if !keepNet.ContainsElement(pid) {
				keepNet.PadIDs = append(keepNet.PadIDs, pid)
				keepNet.Elements = append(keepNet.Elements, netlist.NetElement{
					Type: netlist.ElementPad, ID: pid,
				})
			}
		}
		tp.state.FeaturesLayer.RemoveNet(absorbNet.ID)
		addTraceToNet(keepNet)
		fmt.Printf("Merged net %q into %q\n", absorbNet.Name, keepNet.Name)
	} else if startNet != nil {
		// Only start has a net — add end endpoint
		tp.addEndpointToNet(startNet, tp.traceEndVia, tp.traceEndConn)
		addTraceToNet(startNet)
		fmt.Printf("Added %s to net %q\n", endID, startNet.Name)
	} else if endNet != nil {
		// Only end has a net — add start endpoint
		tp.addEndpointToNet(endNet, startVia, startConn)
		addTraceToNet(endNet)
		fmt.Printf("Added %s to net %q\n", startID, endNet.Name)
	} else {
		// Neither has a net — create a new one
		netNum := tp.state.FeaturesLayer.NetCount() + 1
		id := fmt.Sprintf("net-%03d", netNum)
		name := id // default auto-generated name

		// Try to derive a better name from component pin assignments
		if startVia != nil && startVia.ComponentID != "" && startVia.PinNumber != "" {
			name = fmt.Sprintf("%s.%s", startVia.ComponentID, startVia.PinNumber)
		} else if tp.traceEndVia != nil && tp.traceEndVia.ComponentID != "" && tp.traceEndVia.PinNumber != "" {
			name = fmt.Sprintf("%s.%s", tp.traceEndVia.ComponentID, tp.traceEndVia.PinNumber)
		}

		net := netlist.NewElectricalNetWithName(id, name)
		tp.addEndpointToNet(net, startVia, startConn)
		tp.addEndpointToNet(net, tp.traceEndVia, tp.traceEndConn)
		addTraceToNet(net)
		tp.state.FeaturesLayer.AddNet(net)
		fmt.Printf("Created net %q for %s and %s\n", name, startID, endID)
	}
	tp.state.Emit(app.EventNetlistModified, nil)
}

// findNetForTrace finds the electrical net associated with a trace by checking
// the vias and connectors at its endpoints.
func (tp *TracesPanel) findNetForTrace(traceID string) *netlist.ElectricalNet {
	// First try direct lookup (trace may be registered as a net element)
	if net := tp.state.FeaturesLayer.GetNetForElement(traceID); net != nil {
		return net
	}

	// Look up the trace's endpoint coordinates
	tf := tp.state.FeaturesLayer.GetTraceFeature(traceID)
	if tf == nil || len(tf.Points) < 2 {
		return nil
	}

	tolerance := 5.0

	// Check each endpoint against confirmed vias and connectors
	for _, pt := range []geometry.Point2D{tf.Points[0], tf.Points[len(tf.Points)-1]} {
		// Check confirmed vias
		for _, cv := range tp.state.FeaturesLayer.GetConfirmedVias() {
			dx := cv.Center.X - pt.X
			dy := cv.Center.Y - pt.Y
			if math.Sqrt(dx*dx+dy*dy) <= tolerance {
				if net := tp.state.FeaturesLayer.GetNetForElement(cv.ID); net != nil {
					return net
				}
			}
		}
		// Check connectors on selected side
		for _, conn := range tp.state.FeaturesLayer.GetConnectors() {
			if conn.Side != tp.selectedSide() {
				continue
			}
			dx := conn.Center.X - pt.X
			dy := conn.Center.Y - pt.Y
			if math.Sqrt(dx*dx+dy*dy) <= tolerance {
				if net := tp.state.FeaturesLayer.GetNetForElement(conn.ID); net != nil {
					return net
				}
			}
		}
	}

	return nil
}

// addEndpointToNet adds a via or connector endpoint to an existing net.
func (tp *TracesPanel) addEndpointToNet(net *netlist.ElectricalNet, v *via.ConfirmedVia, conn *connector.Connector) {
	if v != nil {
		if !net.ContainsVia(v.ID) {
			net.AddVia(v)
		}
	} else if conn != nil {
		if !net.ContainsConnector(conn.ID) {
			net.AddConnector(conn)
		}
	}
}

// onAddConnectors copies alignment connectors into the features layer as persistent connectors.
func (tp *TracesPanel) onAddConnectors() {
	tp.state.CreateConnectorsFromAlignment()
	conns := tp.state.FeaturesLayer.GetConnectors()
	if len(conns) == 0 {
		tp.viaStatusLabel.SetText("No connectors — run contact detection + alignment first")
		return
	}
	tp.rebuildFeaturesOverlay()
	tp.canvas.Refresh()
	front, back := 0, 0
	for _, c := range conns {
		if c.Side == pcbimage.SideFront {
			front++
		} else {
			back++
		}
	}
	tp.viaStatusLabel.SetText(fmt.Sprintf("Added %d connectors (%d front, %d back)", len(conns), front, back))
	tp.state.SetModified(true)
}

// showConnectorMenu shows the context menu for a connector.
func (tp *TracesPanel) showConnectorMenu(conn *connector.Connector) {
	menu, _ := gtk.MenuNew()

	addItem := func(label string, cb func()) {
		item, _ := gtk.MenuItemNewWithLabel(label)
		item.Connect("activate", cb)
		menu.Append(item)
	}

	signalLabel := "Rename Signal..."
	if conn.SignalName != "" {
		signalLabel = fmt.Sprintf("Rename Signal (%s)...", conn.SignalName)
	}

	addItem(signalLabel, func() { tp.renameConnectorSignal(conn) })
	addItem("Delete Connector", func() { tp.deleteConnector(conn) })

	menu.ShowAll()
	menu.PopupAtPointer(nil)
}

// renameConnectorSignal opens a dialog to edit a connector's signal name.
func (tp *TracesPanel) renameConnectorSignal(conn *connector.Connector) {
	dlg, _ := gtk.DialogNewWithButtons("Rename Signal", tp.win,
		gtk.DIALOG_MODAL|gtk.DIALOG_DESTROY_WITH_PARENT,
		[]interface{}{"Cancel", gtk.RESPONSE_CANCEL},
		[]interface{}{"OK", gtk.RESPONSE_OK})
	dlg.SetDefaultSize(300, 150)

	contentArea, _ := dlg.GetContentArea()
	entry, _ := gtk.EntryNew()
	entry.SetText(conn.SignalName)
	entry.SetPlaceholderText("e.g. A0, D7, CLOCK")

	lbl, _ := gtk.LabelNew("Signal name:")
	lbl.SetHAlign(gtk.ALIGN_START)
	contentArea.PackStart(lbl, false, false, 4)
	contentArea.PackStart(entry, false, false, 4)
	dlg.ShowAll()

	response := dlg.Run()
	if response == gtk.RESPONSE_OK {
		name, _ := entry.GetText()
		conn.SignalName = name
		tp.rebuildFeaturesOverlay()
		tp.canvas.Refresh()
		tp.viaStatusLabel.SetText(fmt.Sprintf("%s signal: %s", conn.ID, name))
		tp.state.SetModified(true)
	}
	dlg.Destroy()
}

// deleteConnector removes a connector from the features layer.
func (tp *TracesPanel) deleteConnector(conn *connector.Connector) {
	tp.state.FeaturesLayer.RemoveConnector(conn.ID)
	tp.deselectConnector()
	tp.rebuildFeaturesOverlay()
	tp.canvas.Refresh()
	tp.viaStatusLabel.SetText(fmt.Sprintf("Deleted connector %s", conn.ID))
	tp.state.SetModified(true)
}

// refreshConnectors rebuilds connectors from detection results.
// The EventConnectorsCreated listener will update the overlay.
func (tp *TracesPanel) refreshConnectors() {
	tp.state.CreateConnectorsFromAlignment()
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

	tp.rebuildFeaturesOverlay()
	tp.updateViaCounts()
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
			Thickness: 1,
		})
	}
	// Vertex dots at each waypoint
	circles := make([]canvas.OverlayCircle, 0, len(tp.tracePoints))
	for _, pt := range tp.tracePoints {
		circles = append(circles, canvas.OverlayCircle{
			X: pt.X, Y: pt.Y, Radius: 4, Filled: true,
		})
	}
	tp.canvas.SetOverlay("trace_segments", &canvas.Overlay{
		Lines:   lines,
		Circles: circles,
		Color:   color.RGBA{R: 0, G: 255, B: 0, A: 255},
	})
}

// setupTraceRubberBand installs the rubber-band drawing from the last point.
func (tp *TracesPanel) setupTraceRubberBand() {
	if len(tp.tracePoints) > 0 {
		last := tp.tracePoints[len(tp.tracePoints)-1]
		tp.canvas.ShowRubberBand(last.X, last.Y)
	}
	tp.canvas.OnMouseMove(func(x, y float64) {
		tp.canvas.UpdateRubberBand(x, y)
	})
}

// finishTraceCommon contains the shared logic for completing a trace.
func (tp *TracesPanel) finishTraceCommon(endLabel string) string {
	tp.canvas.HideRubberBand()
	tp.canvas.ClearOverlay("trace_segments")
	tp.canvas.OnMouseMove(nil)
	tp.traceMode = false

	nSegs := len(tp.tracePoints) - 1
	startLabel := tp.traceStartLabel()
	fmt.Printf("Trace complete: %s -> %s (%d segments, %d points)\n",
		startLabel, endLabel, nSegs, len(tp.tracePoints))

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
	tp.lastTraceID = traceID

	return traceID
}

// finishTraceAtVia completes the polyline trace at a confirmed via.
func (tp *TracesPanel) finishTraceAtVia(endVia *via.ConfirmedVia) {
	tp.traceEndVia = endVia
	tp.traceEndConn = nil
	tp.traceEndJunctionTrace = ""
	tp.finishTraceCommon(endVia.ID)

	tp.associateTraceEndpoints()

	startLabel := tp.traceStartLabel()
	net := tp.state.FeaturesLayer.GetNetForElement(endVia.ID)
	netInfo := ""
	if net != nil {
		netInfo = fmt.Sprintf(" [%s]", net.Name)
	}
	nSegs := len(tp.tracePoints) - 1
	tp.traceStatusLabel.SetText(fmt.Sprintf("Trace: %s -> %s (%d segments)%s",
		startLabel, endVia.ID, nSegs, netInfo))
	tp.traceStartVia = nil
	tp.traceStartConn = nil
	tp.traceStartJunctionTrace = ""
	tp.traceEndVia = nil
	tp.traceEndConn = nil
	tp.traceEndJunctionTrace = ""
}

// finishTraceAtConnector completes the polyline trace at a connector.
func (tp *TracesPanel) finishTraceAtConnector(conn *connector.Connector) {
	endLabel := conn.SignalName
	if endLabel == "" {
		endLabel = fmt.Sprintf("P%d", conn.PinNumber)
	}
	tp.traceEndVia = nil
	tp.traceEndConn = conn
	tp.traceEndJunctionTrace = ""
	tp.finishTraceCommon(endLabel)

	tp.associateTraceEndpoints()

	startLabel := tp.traceStartLabel()
	net := tp.state.FeaturesLayer.GetNetForElement(conn.ID)
	netInfo := ""
	if net != nil {
		netInfo = fmt.Sprintf(" [%s]", net.Name)
	}
	nSegs := len(tp.tracePoints) - 1
	tp.traceStatusLabel.SetText(fmt.Sprintf("Trace: %s -> %s (%d segments)%s",
		startLabel, endLabel, nSegs, netInfo))
	tp.traceStartVia = nil
	tp.traceStartConn = nil
	tp.traceStartJunctionTrace = ""
	tp.traceEndVia = nil
	tp.traceEndConn = nil
	tp.traceEndJunctionTrace = ""
}

// finishTraceAtJunction completes the polyline trace at a vertex of an existing trace (junction).
func (tp *TracesPanel) finishTraceAtJunction(junctionTraceID string) {
	tp.traceEndVia = nil
	tp.traceEndConn = nil
	tp.traceEndJunctionTrace = junctionTraceID
	tp.finishTraceCommon(junctionTraceID)

	tp.associateTraceEndpoints()

	startLabel := tp.traceStartLabel()
	net := tp.state.FeaturesLayer.GetNetForElement(junctionTraceID)
	netInfo := ""
	if net != nil {
		netInfo = fmt.Sprintf(" [%s]", net.Name)
	}
	nSegs := len(tp.tracePoints) - 1
	tp.traceStatusLabel.SetText(fmt.Sprintf("Trace: %s -> %s junction (%d segments)%s",
		startLabel, junctionTraceID, nSegs, netInfo))
	tp.traceStartVia = nil
	tp.traceStartConn = nil
	tp.traceStartJunctionTrace = ""
	tp.traceEndVia = nil
	tp.traceEndConn = nil
	tp.traceEndJunctionTrace = ""
}

// hitTestVertex checks completed trace vertices on the selected layer for a hit near (x, y).
func (tp *TracesPanel) hitTestVertex(x, y float64) (traceID string, pointIdx int, ok bool) {
	tolerance := 5.0
	if tp.state.DPI > 0 {
		tolerance = 0.008 * tp.state.DPI
	}
	activeLayer := tp.selectedTraceLayer()
	bestDist := tolerance
	for _, tid := range tp.state.FeaturesLayer.GetTraces() {
		tf := tp.state.FeaturesLayer.GetTraceFeature(tid)
		if tf == nil {
			continue
		}
		if tf.Layer != activeLayer {
			continue
		}
		for i, pt := range tf.Points {
			d := math.Hypot(pt.X-x, pt.Y-y)
			if d < bestDist {
				bestDist = d
				traceID = tid
				pointIdx = i
				ok = true
			}
		}
	}
	return
}

// startVertexDrag begins dragging a trace vertex.
func (tp *TracesPanel) startVertexDrag(traceID string, pointIdx int) {
	tp.draggingVertex = true
	tp.dragTraceID = traceID
	tp.dragPointIndex = pointIdx

	tf := tp.state.FeaturesLayer.GetTraceFeature(traceID)
	if tf == nil {
		return
	}

	// Show drag preview overlay with lines from adjacent points
	tp.updateVertexDragOverlay(tf.Points, pointIdx, tf.Points[pointIdx])
	tp.traceStatusLabel.SetText(fmt.Sprintf("Dragging vertex %d of %s — click to place", pointIdx, traceID))

	// Install mouse-move handler to update drag preview
	tp.canvas.OnMouseMove(func(x, y float64) {
		tf := tp.state.FeaturesLayer.GetTraceFeature(tp.dragTraceID)
		if tf == nil {
			return
		}
		tp.updateVertexDragOverlay(tf.Points, tp.dragPointIndex, geometry.Point2D{X: x, Y: y})
		tp.canvas.Refresh()
	})
}

// updateVertexDragOverlay draws temporary lines from adjacent vertices to the cursor position.
func (tp *TracesPanel) updateVertexDragOverlay(points []geometry.Point2D, idx int, cursor geometry.Point2D) {
	var lines []canvas.OverlayLine
	if idx > 0 {
		prev := points[idx-1]
		lines = append(lines, canvas.OverlayLine{
			X1: prev.X, Y1: prev.Y, X2: cursor.X, Y2: cursor.Y, Thickness: 1,
		})
	}
	if idx < len(points)-1 {
		next := points[idx+1]
		lines = append(lines, canvas.OverlayLine{
			X1: cursor.X, Y1: cursor.Y, X2: next.X, Y2: next.Y, Thickness: 1,
		})
	}
	circles := []canvas.OverlayCircle{
		{X: cursor.X, Y: cursor.Y, Radius: 3, Filled: true},
	}
	tp.canvas.SetOverlay("vertex_drag", &canvas.Overlay{
		Lines:   lines,
		Circles: circles,
		Color:   color.RGBA{R: 255, G: 255, B: 0, A: 255},
	})
}

// finishVertexDrag places the dragged vertex at (x, y) and updates the trace.
func (tp *TracesPanel) finishVertexDrag(x, y float64) {
	tp.draggingVertex = false
	tp.canvas.ClearOverlay("vertex_drag")
	tp.canvas.OnMouseMove(nil)

	tf := tp.state.FeaturesLayer.GetTraceFeature(tp.dragTraceID)
	if tf == nil {
		return
	}

	newPoints := make([]geometry.Point2D, len(tf.Points))
	copy(newPoints, tf.Points)
	newPoints[tp.dragPointIndex] = geometry.Point2D{X: x, Y: y}

	tp.state.FeaturesLayer.UpdateTracePoints(tp.dragTraceID, newPoints)
	tp.rebuildFeaturesOverlay()
	tp.canvas.Refresh()
	tp.traceStatusLabel.SetText(fmt.Sprintf("Moved vertex %d of %s", tp.dragPointIndex, tp.dragTraceID))
}

// cancelVertexDrag cancels an in-progress vertex drag.
func (tp *TracesPanel) cancelVertexDrag() {
	tp.draggingVertex = false
	tp.canvas.ClearOverlay("vertex_drag")
	tp.canvas.OnMouseMove(nil)
	tp.canvas.Refresh()
	tp.traceStatusLabel.SetText("Vertex drag cancelled")
}

// autoTraceToAdjacentVia traces the copper path from cv to the next (+1) or previous (-1)
// confirmed via in creation order. The trace follows the skeleton of detected copper.
func (tp *TracesPanel) autoTraceToAdjacentVia(cv *via.ConfirmedVia, direction int) {
	allVias := tp.state.FeaturesLayer.GetConfirmedVias()
	if len(allVias) < 2 {
		tp.traceStatusLabel.SetText("Need at least 2 confirmed vias")
		return
	}

	// Find index of cv
	idx := -1
	for i, v := range allVias {
		if v.ID == cv.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		tp.traceStatusLabel.SetText(fmt.Sprintf("Via %s not found", cv.ID))
		return
	}

	adjIdx := idx + direction
	if adjIdx < 0 || adjIdx >= len(allVias) {
		label := "next"
		if direction < 0 {
			label = "previous"
		}
		tp.traceStatusLabel.SetText(fmt.Sprintf("No %s via (at boundary)", label))
		return
	}
	adjVia := allVias[adjIdx]

	// Get the board image for the selected layer
	var boardImg image.Image
	var traceLayer pcbtrace.TraceLayer
	if tp.selectedLayer() == "Front" {
		if tp.state.FrontImage == nil || tp.state.FrontImage.Image == nil {
			tp.traceStatusLabel.SetText("No front image loaded")
			return
		}
		boardImg = tp.state.FrontImage.Image
		traceLayer = pcbtrace.LayerFront
	} else {
		if tp.state.BackImage == nil || tp.state.BackImage.Image == nil {
			tp.traceStatusLabel.SetText("No back image loaded")
			return
		}
		boardImg = tp.state.BackImage.Image
		traceLayer = pcbtrace.LayerBack
	}

	startCenter := cv.Center
	endCenter := adjVia.Center

	tp.traceStatusLabel.SetText(fmt.Sprintf("Auto-tracing %s -> %s...", cv.ID, adjVia.ID))

	// Capture values for goroutine
	startID := cv.ID
	endID := adjVia.ID

	go func() {
		// Compute ROI
		minX := math.Min(startCenter.X, endCenter.X)
		minY := math.Min(startCenter.Y, endCenter.Y)
		maxX := math.Max(startCenter.X, endCenter.X)
		maxY := math.Max(startCenter.Y, endCenter.Y)

		dist := math.Sqrt((endCenter.X-startCenter.X)*(endCenter.X-startCenter.X) +
			(endCenter.Y-startCenter.Y)*(endCenter.Y-startCenter.Y))
		margin := dist * 2
		if margin < 200 {
			margin = 200
		}

		imgBounds := boardImg.Bounds()
		roiX := int(math.Max(float64(imgBounds.Min.X), minX-margin))
		roiY := int(math.Max(float64(imgBounds.Min.Y), minY-margin))
		roiX2 := int(math.Min(float64(imgBounds.Max.X), maxX+margin))
		roiY2 := int(math.Min(float64(imgBounds.Max.Y), maxY+margin))

		roiRect := image.Rect(roiX, roiY, roiX2, roiY2)
		roiW := roiRect.Dx()
		roiH := roiRect.Dy()
		if roiW <= 0 || roiH <= 0 {
			glib.IdleAdd(func() {
				tp.traceStatusLabel.SetText("ROI too small for auto-trace")
			})
			return
		}

		// Crop the board image to ROI
		cropped := image.NewRGBA(image.Rect(0, 0, roiW, roiH))
		draw.Draw(cropped, cropped.Bounds(), boardImg, roiRect.Min, draw.Src)

		// Convert to Mat
		mat, err := pcbtrace.ImageToMat(cropped)
		if err != nil {
			glib.IdleAdd(func() {
				tp.traceStatusLabel.SetText(fmt.Sprintf("Image conversion error: %v", err))
			})
			return
		}
		defer mat.Close()

		// Detect copper
		copperMask := pcbtrace.AutoDetectCopper(mat, 4)
		defer copperMask.Close()

		// Cleanup
		cleaned := pcbtrace.CleanupMask(copperMask, 2)
		defer cleaned.Close()

		// Skeletonize
		skeleton := pcbtrace.Skeletonize(cleaned)
		defer skeleton.Close()

		// Convert via centers to ROI-local coordinates
		localStart := geometry.Point2D{
			X: startCenter.X - float64(roiX),
			Y: startCenter.Y - float64(roiY),
		}
		localEnd := geometry.Point2D{
			X: endCenter.X - float64(roiX),
			Y: endCenter.Y - float64(roiY),
		}

		// Search radius for nearest skeleton pixel
		searchRadius := int(margin / 2)
		if searchRadius < 50 {
			searchRadius = 50
		}

		// Pathfind on skeleton
		path, ok := pcbtrace.FindPathOnSkeleton(skeleton, localStart, localEnd, searchRadius)
		if !ok {
			glib.IdleAdd(func() {
				tp.traceStatusLabel.SetText(fmt.Sprintf("No copper path found between %s and %s", startID, endID))
			})
			return
		}

		// Simplify
		path = pcbtrace.SimplifyPath(path, 2.0)

		// Offset from ROI-local back to image-global
		for i := range path {
			path[i].X += float64(roiX)
			path[i].Y += float64(roiY)
		}

		fmt.Printf("Auto-trace %s -> %s: %d points (simplified from skeleton)\n", startID, endID, len(path))

		// Create trace on UI thread
		glib.IdleAdd(func() {
			traceID := fmt.Sprintf("trace-%03d", tp.state.FeaturesLayer.TraceCount()+1)
			et := pcbtrace.ExtendedTrace{
				Trace: pcbtrace.Trace{
					ID: traceID, Layer: traceLayer, Points: path,
				},
				Source: pcbtrace.SourceDetected,
			}
			tp.state.FeaturesLayer.AddTrace(et)
			tp.lastTraceID = traceID

			// Set up endpoint state for net association
			tp.traceStartVia = cv
			tp.traceStartConn = nil
			tp.traceStartJunctionTrace = ""
			tp.traceEndVia = adjVia
			tp.traceEndConn = nil
			tp.traceEndJunctionTrace = ""
			tp.associateTraceEndpoints()

			tp.rebuildFeaturesOverlay()
			tp.canvas.Refresh()

			net := tp.state.FeaturesLayer.GetNetForElement(endID)
			netInfo := ""
			if net != nil {
				netInfo = fmt.Sprintf(" [%s]", net.Name)
			}
			tp.traceStatusLabel.SetText(fmt.Sprintf("Auto-trace: %s -> %s (%d pts)%s",
				startID, endID, len(path), netInfo))

			tp.traceStartVia = nil
			tp.traceEndVia = nil
		})
	}()
}

// onHover handles mouse hover to display netlist membership when over a trace, via, or connector.
func (tp *TracesPanel) onHover(x, y float64) {
	// Don't update hover info during active operations
	if tp.traceMode || tp.draggingVertex {
		return
	}

	// Hit-test confirmed via
	if cv := tp.state.FeaturesLayer.HitTestConfirmedVia(x, y); cv != nil {
		tp.showNetInfoForElement(cv.ID, cv.ID)
		return
	}

	// Hit-test connector on selected side
	if conn := tp.state.FeaturesLayer.HitTestConnectorOnSide(x, y, tp.selectedSide()); conn != nil {
		label := conn.SignalName
		if label == "" {
			label = fmt.Sprintf("P%d", conn.PinNumber)
		}
		tp.showNetInfoForElement(conn.ID, label)
		return
	}

	// Hit-test trace segment (already filtered by layer in hitTestTraceSegment)
	if hit := tp.hitTestTraceSegment(x, y); hit != nil {
		tp.showNetInfoForElement(hit.traceID, hit.traceID)
		return
	}

	// Hit-test trace vertex
	if traceID, _, ok := tp.hitTestVertex(x, y); ok {
		tp.showNetInfoForElement(traceID, traceID)
		return
	}

	// Nothing hovered — clear if we were showing hover info
	if tp.hoverNetID != "" {
		tp.hoverNetID = ""
		tp.traceStatusLabel.SetText("Click via/connector to start trace")
	}
}

// showNetInfoForElement displays the netlist membership for the given element.
func (tp *TracesPanel) showNetInfoForElement(elementID, displayLabel string) {
	net := tp.state.FeaturesLayer.GetNetForElement(elementID)
	if net == nil {
		if tp.hoverNetID != "" {
			tp.hoverNetID = ""
		}
		tp.traceStatusLabel.SetText(fmt.Sprintf("%s (no net)", displayLabel))
		return
	}

	// Avoid redundant updates
	if tp.hoverNetID == net.ID {
		return
	}
	tp.hoverNetID = net.ID

	// Build membership list
	parts := []string{fmt.Sprintf("Net: %s", net.Name)}

	for _, vid := range net.ViaIDs {
		label := vid
		cv := tp.state.FeaturesLayer.GetConfirmedViaByID(vid)
		if cv != nil && cv.ComponentID != "" && cv.PinNumber != "" {
			label = fmt.Sprintf("%s (%s.%s)", vid, cv.ComponentID, cv.PinNumber)
		}
		parts = append(parts, "  via: "+label)
	}
	for _, cid := range net.ConnectorIDs {
		label := cid
		conn := tp.state.FeaturesLayer.GetConnectorByID(cid)
		if conn != nil && conn.SignalName != "" {
			label = fmt.Sprintf("%s (%s)", cid, conn.SignalName)
		}
		parts = append(parts, "  conn: "+label)
	}

	var info string
	if len(parts) <= 6 {
		info = ""
		for i, p := range parts {
			if i > 0 {
				info += "\n"
			}
			info += p
		}
	} else {
		info = fmt.Sprintf("%s (%d vias, %d connectors)", parts[0], len(net.ViaIDs), len(net.ConnectorIDs))
	}
	tp.traceStatusLabel.SetText(info)
}
