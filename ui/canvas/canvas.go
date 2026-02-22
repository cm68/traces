// Package canvas provides an image canvas with pan, zoom, and selection.
package canvas

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	pcbimage "pcb-tracer/internal/image"
	"pcb-tracer/pkg/geometry"

	"github.com/gotk3/gotk3/cairo"
	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

const (
	minZoom  = 0.1
	maxZoom  = 10.0
	zoomStep = 1.25
)

// Tool represents the current interaction tool.
type Tool int

const (
	ToolPan Tool = iota
	ToolSelect
	ToolMeasure
	ToolDraw
)

// ConnectorLabel represents a label to draw on a connector contact.
type ConnectorLabel struct {
	Label   string  // The text to display (e.g., "A0", "D7", "GND")
	CenterX float64 // Center X in image coordinates
	CenterY float64 // Center Y in image coordinates
	Side    pcbimage.Side
}

// StepEdgeViz controls the checkerboard alignment visualization.
type StepEdgeViz struct {
	Enabled   bool    // Whether to show checkerboard visualization
	StepY     float64 // Y coordinate of step edge (image coords) - unused for checkerboard
	BandWidth float64 // Width of each square in pixels (1cm at DPI)
	Height    float64 // Height of visualization region - unused for checkerboard
}

// ImageCanvas provides an image display with pan, zoom, and selection.
type ImageCanvas struct {
	// GTK widgets
	drawArea  *gtk.DrawingArea
	scrollWin *gtk.ScrolledWindow

	// Layer stack
	layers []*pcbimage.Layer

	// Overlays (keyed by name, e.g., "front_contacts", "back_contacts")
	overlays map[string]*Overlay

	// Connector labels (drawn with layer opacity)
	connectorLabels []ConnectorLabel

	// Step-edge alignment visualization
	stepEdgeViz StepEdgeViz

	// Display state
	zoom float64

	// Interaction state
	tool       Tool
	dragging   bool
	dragStartX float64
	dragStartY float64

	// Selection (rubber-band)
	selecting     bool
	selectMode    bool // When true, next drag creates a selection
	selectStart   geometry.Point2D
	selectEnd     geometry.Point2D
	selectionRect *OverlayRect // Current selection rectangle (in image coords)

	// Canvas size tracking
	imgWidth  int
	imgHeight int

	// Fit to window
	fitToWindow bool

	// Last rendered output for sampling
	lastOutput *image.RGBA

	// Background grid DPI (1mm grid when > 0)
	gridDPI float64

	// Background mode: false = checkerboard, true = solid black
	solidBlackBackground bool

	// Callbacks
	onZoomChange  func(zoom float64)
	onSelect      func(x1, y1, x2, y2 float64) // Called with image coordinates
	onLeftClick   func(x, y float64)            // Left click at image coordinates
	onRightClick  func(x, y float64)            // Right click at image coordinates
	onMiddleClick func(x, y float64)            // Middle click at image coordinates
	onMouseMove   func(x, y float64)            // Mouse move at image coordinates
	onHover       func(x, y float64)            // Always-active hover callback

	// Rubber band line (image coordinates)
	rubberBandFrom geometry.Point2D
	rubberBandTo   geometry.Point2D
	rubberBandOn   bool

	// Middle-button pan state
	middleDragging bool
	panLastX       float64
	panLastY       float64

	// Left-button drag for selection
	leftDragging bool

	// Pending labels accumulated during draw(), rendered with Cairo text
	pendingLabels []pendingLabel
}

// pendingLabel is a text label to be rendered with Cairo after the bitmap is blitted.
type pendingLabel struct {
	text         string
	x, y         int
	rotated      bool
	col          color.RGBA
	boundW, boundH int // available space in screen pixels (0 = no constraint)
}

// NewImageCanvas creates a new image canvas.
func NewImageCanvas() *ImageCanvas {
	ic := &ImageCanvas{
		zoom:     1.0,
		tool:     ToolPan,
		layers:   make([]*pcbimage.Layer, 0),
		overlays: make(map[string]*Overlay),
	}

	da, _ := gtk.DrawingAreaNew()
	ic.drawArea = da

	// Enable events
	da.AddEvents(int(
		gdk.BUTTON_PRESS_MASK |
			gdk.BUTTON_RELEASE_MASK |
			gdk.POINTER_MOTION_MASK |
			gdk.SCROLL_MASK))

	// Drawing callback
	da.Connect("draw", func(da *gtk.DrawingArea, cr *cairo.Context) {
		alloc := da.GetAllocation()
		w, h := alloc.GetWidth(), alloc.GetHeight()
		if w <= 0 || h <= 0 {
			return
		}
		img := ic.draw(w, h)
		if img == nil {
			return
		}
		rgba, ok := img.(*image.RGBA)
		if !ok {
			return
		}
		blitRGBAToCairo(cr, rgba)
		ic.drawLabelsWithCairo(cr)
	})

	// Mouse button press
	da.Connect("button-press-event", func(da *gtk.DrawingArea, ev *gdk.Event) bool {
		btn := gdk.EventButtonNewFromEvent(ev)
		x, y := btn.X(), btn.Y()
		imgX, imgY := x/ic.zoom, y/ic.zoom

		switch btn.Button() {
		case 1: // Left click
			if ic.selectMode {
				ic.leftDragging = true
				ic.selecting = true
				ic.selectStart = geometry.Point2D{X: imgX, Y: imgY}
				ic.selectEnd = ic.selectStart
				return true
			}
			if ic.onLeftClick != nil {
				ic.onLeftClick(imgX, imgY)
			}
		case 2: // Middle click/drag start
			ic.middleDragging = true
			ic.panLastX = x
			ic.panLastY = y
			if ic.onMiddleClick != nil {
				ic.onMiddleClick(imgX, imgY)
			}
		case 3: // Right click
			if ic.onRightClick != nil {
				ic.onRightClick(imgX, imgY)
			}
		}
		return true
	})

	// Mouse button release
	da.Connect("button-release-event", func(da *gtk.DrawingArea, ev *gdk.Event) bool {
		btn := gdk.EventButtonNewFromEvent(ev)
		switch btn.Button() {
		case 1: // Left release
			if ic.leftDragging && ic.selecting {
				ic.leftDragging = false
				ic.selecting = false
				ic.selectMode = false
				if ic.onSelect != nil && ic.selectionRect != nil {
					rect := ic.selectionRect
					ic.onSelect(
						float64(rect.X), float64(rect.Y),
						float64(rect.X+rect.Width), float64(rect.Y+rect.Height),
					)
				}
				ic.selectionRect = nil
				ic.drawArea.QueueDraw()
			}
		case 2: // Middle release
			ic.middleDragging = false
		}
		return true
	})

	// Mouse motion
	da.Connect("motion-notify-event", func(da *gtk.DrawingArea, ev *gdk.Event) bool {
		motion := gdk.EventMotionNewFromEvent(ev)
		x, y := motion.MotionVal()
		imgX, imgY := x/ic.zoom, y/ic.zoom

		// Middle-button pan
		if ic.middleDragging {
			dx := x - ic.panLastX
			dy := y - ic.panLastY
			ic.panLastX = x
			ic.panLastY = y

			hadj := ic.scrollWin.GetHAdjustment()
			vadj := ic.scrollWin.GetVAdjustment()
			hadj.SetValue(hadj.GetValue() - dx)
			vadj.SetValue(vadj.GetValue() - dy)
			return true
		}

		// Left-button drag for selection
		if ic.leftDragging && ic.selecting {
			ic.selectEnd = geometry.Point2D{X: imgX, Y: imgY}
			x1, y1 := ic.selectStart.X, ic.selectStart.Y
			x2, y2 := ic.selectEnd.X, ic.selectEnd.Y
			if x1 > x2 {
				x1, x2 = x2, x1
			}
			if y1 > y2 {
				y1, y2 = y2, y1
			}
			ic.selectionRect = &OverlayRect{
				X:      int(x1),
				Y:      int(y1),
				Width:  int(x2 - x1),
				Height: int(y2 - y1),
			}
			ic.drawArea.QueueDraw()
			return true
		}

		// Mouse move callback
		if ic.onMouseMove != nil {
			ic.onMouseMove(imgX, imgY)
		}
		// Always-active hover callback
		if ic.onHover != nil {
			ic.onHover(imgX, imgY)
		}
		return false
	})

	// Scroll for zoom — centered on cursor position
	da.Connect("scroll-event", func(da *gtk.DrawingArea, ev *gdk.Event) bool {
		scroll := gdk.EventScrollNewFromEvent(ev)
		// Event coords are in DrawingArea space (zoomed + scrolled).
		// Convert to image space.
		evtX, evtY := scroll.X(), scroll.Y()
		imgX := evtX / ic.zoom
		imgY := evtY / ic.zoom
		switch scroll.Direction() {
		case gdk.SCROLL_UP:
			ic.ZoomAtPoint(ic.zoom*zoomStep, imgX, imgY, evtX, evtY)
		case gdk.SCROLL_DOWN:
			ic.ZoomAtPoint(ic.zoom/zoomStep, imgX, imgY, evtX, evtY)
		}
		return true
	})

	// Wrap in ScrolledWindow
	sw, _ := gtk.ScrolledWindowNew(nil, nil)
	sw.SetPolicy(gtk.POLICY_AUTOMATIC, gtk.POLICY_AUTOMATIC)
	sw.Add(da)
	ic.scrollWin = sw

	return ic
}

// Widget returns the GTK widget for embedding in layouts.
func (ic *ImageCanvas) Widget() gtk.IWidget {
	return ic.scrollWin
}

// EnableSelectMode enables selection mode for the next drag.
func (ic *ImageCanvas) EnableSelectMode() {
	ic.selectMode = true
	ic.selecting = false
	ic.selectionRect = nil
}

// SetLayers sets the layers to display.
func (ic *ImageCanvas) SetLayers(layers []*pcbimage.Layer) {
	ic.layers = layers
	ic.updateContentSize()
}

// AddLayer adds a layer to the stack.
func (ic *ImageCanvas) AddLayer(layer *pcbimage.Layer) {
	ic.layers = append(ic.layers, layer)
	ic.updateContentSize()
}

// ClearLayers removes all layers.
func (ic *ImageCanvas) ClearLayers() {
	ic.layers = nil
	ic.updateContentSize()
}

// GetLayers returns the current layers.
func (ic *ImageCanvas) GetLayers() []*pcbimage.Layer {
	return ic.layers
}

// RaiseLayer moves the specified layer to the top of the stack.
func (ic *ImageCanvas) RaiseLayer(layer *pcbimage.Layer) {
	for i, l := range ic.layers {
		if l == layer {
			ic.layers = append(ic.layers[:i], ic.layers[i+1:]...)
			ic.layers = append(ic.layers, layer)
			ic.Refresh()
			return
		}
	}
}

// RaiseLayerBySide raises the layer matching the given side to the top.
func (ic *ImageCanvas) RaiseLayerBySide(side pcbimage.Side) {
	for i, l := range ic.layers {
		if l != nil && l.Side == side {
			ic.layers = append(ic.layers[:i], ic.layers[i+1:]...)
			ic.layers = append(ic.layers, l)
			ic.Refresh()
			return
		}
	}
}

// SetOverlay sets an overlay with the given name.
func (ic *ImageCanvas) SetOverlay(name string, overlay *Overlay) {
	ic.overlays[name] = overlay
	ic.Refresh()
}

// ClearOverlay removes an overlay by name.
func (ic *ImageCanvas) ClearOverlay(name string) {
	delete(ic.overlays, name)
	ic.Refresh()
}

// ClearAllOverlays removes all overlays.
func (ic *ImageCanvas) ClearAllOverlays() {
	ic.overlays = make(map[string]*Overlay)
	ic.Refresh()
}

// SetConnectorLabels sets the connector labels to draw on the image.
func (ic *ImageCanvas) SetConnectorLabels(labels []ConnectorLabel) {
	ic.connectorLabels = labels
	ic.Refresh()
}

// ClearConnectorLabels removes all connector labels.
func (ic *ImageCanvas) ClearConnectorLabels() {
	ic.connectorLabels = nil
	ic.Refresh()
}

// SetStepEdgeViz enables or disables the checkerboard alignment visualization.
func (ic *ImageCanvas) SetStepEdgeViz(enabled bool, stepY, dpi float64) {
	ic.stepEdgeViz.Enabled = enabled
	_ = stepY
	if dpi > 0 {
		ic.stepEdgeViz.BandWidth = dpi * 0.3937
		ic.stepEdgeViz.Height = dpi * 0.5
	} else {
		ic.stepEdgeViz.BandWidth = 100
		ic.stepEdgeViz.Height = 150
	}
	ic.Refresh()
}

// GetStepEdgeViz returns the current step-edge visualization settings.
func (ic *ImageCanvas) GetStepEdgeViz() StepEdgeViz {
	return ic.stepEdgeViz
}

// ShowRubberBand sets the start point for the rubber band line.
func (ic *ImageCanvas) ShowRubberBand(fromX, fromY float64) {
	ic.rubberBandFrom = geometry.Point2D{X: fromX, Y: fromY}
	ic.rubberBandTo = ic.rubberBandFrom
	ic.rubberBandOn = true
}

// UpdateRubberBand moves the endpoint of the rubber band line.
func (ic *ImageCanvas) UpdateRubberBand(toX, toY float64) {
	if !ic.rubberBandOn {
		return
	}
	ic.rubberBandTo = geometry.Point2D{X: toX, Y: toY}
	ic.drawArea.QueueDraw()
}

// HideRubberBand disables the rubber band line.
func (ic *ImageCanvas) HideRubberBand() {
	ic.rubberBandOn = false
	ic.drawArea.QueueDraw()
}

// SetDPI sets the DPI for the background grid (1mm squares).
func (ic *ImageCanvas) SetDPI(dpi float64) {
	ic.gridDPI = dpi
	ic.Refresh()
}

// SetSolidBlackBackground sets whether to use solid black or checkerboard background.
func (ic *ImageCanvas) SetSolidBlackBackground(solid bool) {
	ic.solidBlackBackground = solid
	ic.Refresh()
}

// SetImage sets a single image to display (convenience method).
func (ic *ImageCanvas) SetImage(img image.Image) {
	if img == nil {
		ic.layers = nil
	} else {
		layer := pcbimage.NewLayer()
		layer.Image = img
		layer.Visible = true
		layer.Opacity = 1.0
		ic.layers = []*pcbimage.Layer{layer}
	}
	ic.updateContentSize()
}

// GetImage returns the first layer's image (convenience method).
func (ic *ImageCanvas) GetImage() image.Image {
	if len(ic.layers) == 0 || ic.layers[0] == nil {
		return nil
	}
	return ic.layers[0].Image
}

// SetZoom sets the zoom level.
func (ic *ImageCanvas) SetZoom(zoom float64) {
	if zoom < minZoom {
		zoom = minZoom
	}
	if zoom > maxZoom {
		zoom = maxZoom
	}
	ic.zoom = zoom
	ic.updateContentSize()

	if ic.onZoomChange != nil {
		ic.onZoomChange(zoom)
	}
}

// GetZoom returns the current zoom level.
func (ic *ImageCanvas) GetZoom() float64 {
	return ic.zoom
}

// ZoomIn increases the zoom level.
func (ic *ImageCanvas) ZoomIn() {
	ic.SetZoom(ic.zoom * zoomStep)
}

// ZoomOut decreases the zoom level.
func (ic *ImageCanvas) ZoomOut() {
	ic.SetZoom(ic.zoom / zoomStep)
}

// ZoomAtPoint zooms to the given level, keeping the image point (imgX, imgY)
// under the cursor. evtX/evtY are the original DrawingArea-relative event coords
// (before zoom change) used to compute the viewport-relative cursor position.
func (ic *ImageCanvas) ZoomAtPoint(newZoom, imgX, imgY, evtX, evtY float64) {
	if newZoom < minZoom {
		newZoom = minZoom
	}
	if newZoom > maxZoom {
		newZoom = maxZoom
	}

	hadj := ic.scrollWin.GetHAdjustment()
	vadj := ic.scrollWin.GetVAdjustment()

	// Cursor position relative to the viewport
	vpX := evtX - hadj.GetValue()
	vpY := evtY - vadj.GetValue()

	// After zoom, the image point is at imgX*newZoom in canvas space.
	// Set scroll so that position is still vpX/vpY from viewport edge.
	newScrollX := imgX*newZoom - vpX
	newScrollY := imgY*newZoom - vpY

	// Apply the new zoom
	ic.zoom = newZoom
	ic.updateContentSize()

	glib.IdleAdd(func() {
		hadj.SetValue(newScrollX)
		vadj.SetValue(newScrollY)
	})

	if ic.onZoomChange != nil {
		ic.onZoomChange(newZoom)
	}
}

// FitToWindow adjusts zoom to fit the image in the visible area.
func (ic *ImageCanvas) FitToWindow() {
	bounds := ic.getLayerBounds()
	if bounds.Dx() == 0 || bounds.Dy() == 0 {
		return
	}

	alloc := ic.scrollWin.GetAllocation()
	vw := float64(alloc.GetWidth())
	vh := float64(alloc.GetHeight())
	if vw <= 0 || vh <= 0 {
		return
	}

	zoomX := vw / float64(bounds.Dx())
	zoomY := vh / float64(bounds.Dy())
	zoom := math.Min(zoomX, zoomY)
	ic.SetZoom(zoom * 0.95)
}

// SetFitToWindow enables or disables auto-fit on resize.
func (ic *ImageCanvas) SetFitToWindow(fit bool) {
	ic.fitToWindow = fit
	if fit {
		ic.FitToWindow()
	}
}

// GetFitToWindow returns the current fit-to-window state.
func (ic *ImageCanvas) GetFitToWindow() bool {
	return ic.fitToWindow
}

// ScrollToRegion pans the canvas so that the given image coordinates are centered.
func (ic *ImageCanvas) ScrollToRegion(x, y, width, height int) {
	canvasX := float64(x) * ic.zoom
	canvasY := float64(y) * ic.zoom
	canvasW := float64(width) * ic.zoom
	canvasH := float64(height) * ic.zoom

	alloc := ic.scrollWin.GetAllocation()
	vw := float64(alloc.GetWidth())
	vh := float64(alloc.GetHeight())
	if vw <= 0 || vh <= 0 {
		return
	}

	centerX := canvasX + canvasW/2
	centerY := canvasY + canvasH/2

	hadj := ic.scrollWin.GetHAdjustment()
	vadj := ic.scrollWin.GetVAdjustment()
	hadj.SetValue(centerX - vw/2)
	vadj.SetValue(centerY - vh/2)
}

// ScrollOffset returns the current scroll offset as (x, y).
func (ic *ImageCanvas) ScrollOffset() (float64, float64) {
	hadj := ic.scrollWin.GetHAdjustment()
	vadj := ic.scrollWin.GetVAdjustment()
	return hadj.GetValue(), vadj.GetValue()
}

// SetScrollOffset sets the scroll position to (x, y).
func (ic *ImageCanvas) SetScrollOffset(x, y float64) {
	hadj := ic.scrollWin.GetHAdjustment()
	vadj := ic.scrollWin.GetVAdjustment()
	hadj.SetValue(x)
	vadj.SetValue(y)
}

// SetTool sets the current interaction tool.
func (ic *ImageCanvas) SetTool(tool Tool) {
	ic.tool = tool
}

// OnZoomChange sets a callback for zoom changes.
func (ic *ImageCanvas) OnZoomChange(callback func(zoom float64)) {
	ic.onZoomChange = callback
}

// OnSelect sets a callback for selection completion.
func (ic *ImageCanvas) OnSelect(callback func(x1, y1, x2, y2 float64)) {
	ic.onSelect = callback
}

// OnLeftClick sets a callback for left-click events.
func (ic *ImageCanvas) OnLeftClick(callback func(x, y float64)) {
	ic.onLeftClick = callback
}

// OnRightClick sets a callback for right-click events.
func (ic *ImageCanvas) OnRightClick(callback func(x, y float64)) {
	ic.onRightClick = callback
}

// OnMiddleClick sets a callback for middle-click events.
func (ic *ImageCanvas) OnMiddleClick(callback func(x, y float64)) {
	ic.onMiddleClick = callback
}

// OnMouseMove sets a callback for mouse-move events.
func (ic *ImageCanvas) OnMouseMove(callback func(x, y float64)) {
	ic.onMouseMove = callback
}

// OnHover sets a permanent callback for mouse hover events.
// Unlike OnMouseMove, this callback is never cleared by trace/drag operations.
func (ic *ImageCanvas) OnHover(callback func(x, y float64)) {
	ic.onHover = callback
}

// GetRenderedOutput returns the last rendered canvas output for sampling.
func (ic *ImageCanvas) GetRenderedOutput() *image.RGBA {
	return ic.lastOutput
}

// Refresh redraws the canvas.
func (ic *ImageCanvas) Refresh() {
	ic.drawArea.QueueDraw()
}

// ImageToCanvas converts image coordinates to canvas (zoomed) coordinates.
func (ic *ImageCanvas) ImageToCanvas(imgX, imgY float64) (canvasX, canvasY float64) {
	canvasX = imgX * ic.zoom
	canvasY = imgY * ic.zoom
	return
}

// CanvasToImage converts canvas coordinates to image coordinates.
func (ic *ImageCanvas) CanvasToImage(canvasX, canvasY float64) (imgX, imgY float64) {
	imgX = canvasX / ic.zoom
	imgY = canvasY / ic.zoom
	return
}

// getLayerBounds returns the maximum bounds across all layers.
func (ic *ImageCanvas) getLayerBounds() image.Rectangle {
	var maxWidth, maxHeight int
	for _, layer := range ic.layers {
		if layer != nil && layer.Image != nil {
			bounds := layer.Image.Bounds()
			if bounds.Dx() > maxWidth {
				maxWidth = bounds.Dx()
			}
			if bounds.Dy() > maxHeight {
				maxHeight = bounds.Dy()
			}
		}
	}
	return image.Rect(0, 0, maxWidth, maxHeight)
}

// updateContentSize updates the DrawingArea size based on image and zoom.
func (ic *ImageCanvas) updateContentSize() {
	bounds := ic.getLayerBounds()
	if bounds.Dx() == 0 || bounds.Dy() == 0 {
		ic.imgWidth = 400
		ic.imgHeight = 300
	} else {
		ic.imgWidth = int(float64(bounds.Dx()) * ic.zoom)
		ic.imgHeight = int(float64(bounds.Dy()) * ic.zoom)
	}

	ic.drawArea.SetSizeRequest(ic.imgWidth, ic.imgHeight)
	ic.drawArea.QueueDraw()
}

// drawGridBackground fills the output with a 1mm black and white grid pattern.
func (ic *ImageCanvas) drawGridBackground(output *image.RGBA, w, h int) {
	black := color.RGBA{R: 0, G: 0, B: 0, A: 255}

	if ic.solidBlackBackground {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				output.Set(x, y, black)
			}
		}
		return
	}

	var gridSize float64
	if ic.gridDPI > 0 {
		gridSize = ic.gridDPI * 0.03937 * ic.zoom
	} else {
		gridSize = 1200 * 0.03937 * ic.zoom
	}
	if gridSize < 4 {
		gridSize = 4
	}

	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	for y := 0; y < h; y++ {
		gridY := int(float64(y) / gridSize)
		for x := 0; x < w; x++ {
			gridX := int(float64(x) / gridSize)
			if (gridX+gridY)%2 == 0 {
				output.Set(x, y, black)
			} else {
				output.Set(x, y, white)
			}
		}
	}
}

// compositeLayer draws a single layer onto the output with opacity.
func (ic *ImageCanvas) compositeLayer(output *image.RGBA, layer *pcbimage.Layer, w, h int) {
	src := layer.Image
	srcBounds := src.Bounds()
	opacity := layer.Opacity

	if layer.IsNormalized {
		ic.compositeLayerNormalized(output, layer, w, h)
		return
	}

	offsetX := float64(layer.ManualOffsetX)
	offsetY := float64(layer.ManualOffsetY)
	rotation := layer.ManualRotation * math.Pi / 180.0

	shearTopX := layer.ShearTopX
	shearBottomX := layer.ShearBottomX
	shearLeftY := layer.ShearLeftY
	shearRightY := layer.ShearRightY
	if shearTopX == 0 {
		shearTopX = 1.0
	}
	if shearBottomX == 0 {
		shearBottomX = 1.0
	}
	if shearLeftY == 0 {
		shearLeftY = 1.0
	}
	if shearRightY == 0 {
		shearRightY = 1.0
	}

	cosR := math.Cos(-rotation)
	sinR := math.Sin(-rotation)

	srcW := float64(srcBounds.Max.X - srcBounds.Min.X)
	srcH := float64(srcBounds.Max.Y - srcBounds.Min.Y)

	var srcCx, srcCy float64
	if layer.RotationCenterX != 0 || layer.RotationCenterY != 0 {
		srcCx = layer.RotationCenterX
		srcCy = layer.RotationCenterY
	} else {
		srcCx = float64(srcBounds.Min.X+srcBounds.Max.X) / 2.0
		srcCy = float64(srcBounds.Min.Y+srcBounds.Max.Y) / 2.0
	}

	hasTransform := rotation != 0 || shearTopX != 1.0 || shearBottomX != 1.0 ||
		shearLeftY != 1.0 || shearRightY != 1.0

	vizEnabled := ic.stepEdgeViz.Enabled
	vizBandWidth := ic.stepEdgeViz.BandWidth
	isFront := layer.Side == pcbimage.SideFront

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			imgX := float64(x)/ic.zoom - offsetX
			imgY := float64(y)/ic.zoom - offsetY

			var srcX, srcY int

			if hasTransform {
				srcPosX := imgX + float64(srcBounds.Min.X)
				srcPosY := imgY + float64(srcBounds.Min.Y)

				relX := srcPosX - srcCx
				relY := srcPosY - srcCy

				rotX := relX*cosR - relY*sinR
				rotY := relX*sinR + relY*cosR

				normY := (rotY + srcH/2) / srcH
				normX := (rotX + srcW/2) / srcW
				if normY < 0 {
					normY = 0
				} else if normY > 1 {
					normY = 1
				}
				if normX < 0 {
					normX = 0
				} else if normX > 1 {
					normX = 1
				}

				scaleX := shearTopX + (shearBottomX-shearTopX)*normY
				scaleY := shearLeftY + (shearRightY-shearLeftY)*normX

				scaledX := rotX / scaleX
				scaledY := rotY / scaleY

				srcX = int(scaledX + srcCx)
				srcY = int(scaledY + srcCy)
			} else {
				srcX = int(imgX) + srcBounds.Min.X
				srcY = int(imgY) + srcBounds.Min.Y
			}

			if srcX < srcBounds.Min.X || srcX >= srcBounds.Max.X ||
				srcY < srcBounds.Min.Y || srcY >= srcBounds.Max.Y {
				continue
			}

			srcColor := src.At(srcX, srcY)
			sr, sg, sb, sa := srcColor.RGBA()
			effectiveAlpha := float64(sa) / 0xffff * opacity

			if vizEnabled && vizBandWidth > 0 {
				canvasImgX := float64(x) / ic.zoom
				canvasImgY := float64(y) / ic.zoom
				bandX := int(canvasImgX / vizBandWidth)
				bandY := int(canvasImgY / vizBandWidth)
				isEvenCell := (bandX+bandY)%2 == 0
				if (isFront && !isEvenCell) || (!isFront && isEvenCell) {
					effectiveAlpha = 0
				}
			}

			if effectiveAlpha >= 0.999 {
				output.Set(x, y, srcColor)
			} else if effectiveAlpha > 0.001 {
				dr, dg, db, _ := output.At(x, y).RGBA()
				invAlpha := 1 - effectiveAlpha
				r := uint8((float64(sr>>8)*effectiveAlpha + float64(dr>>8)*invAlpha))
				g := uint8((float64(sg>>8)*effectiveAlpha + float64(dg>>8)*invAlpha))
				b := uint8((float64(sb>>8)*effectiveAlpha + float64(db>>8)*invAlpha))
				output.Set(x, y, color.RGBA{r, g, b, 255})
			}
		}
	}
}

// compositeLayerNormalized is the fast path for normalized layers.
func (ic *ImageCanvas) compositeLayerNormalized(output *image.RGBA, layer *pcbimage.Layer, w, h int) {
	src := layer.Image
	srcBounds := src.Bounds()
	opacity := layer.Opacity

	vizEnabled := ic.stepEdgeViz.Enabled
	vizBandWidth := ic.stepEdgeViz.BandWidth
	isFront := layer.Side == pcbimage.SideFront

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			srcX := int(float64(x)/ic.zoom) + srcBounds.Min.X
			srcY := int(float64(y)/ic.zoom) + srcBounds.Min.Y

			if srcX < srcBounds.Min.X || srcX >= srcBounds.Max.X ||
				srcY < srcBounds.Min.Y || srcY >= srcBounds.Max.Y {
				continue
			}

			srcColor := src.At(srcX, srcY)
			sr, sg, sb, sa := srcColor.RGBA()
			effectiveAlpha := float64(sa) / 0xffff * opacity

			if vizEnabled && vizBandWidth > 0 {
				canvasImgX := float64(x) / ic.zoom
				canvasImgY := float64(y) / ic.zoom
				bandX := int(canvasImgX / vizBandWidth)
				bandY := int(canvasImgY / vizBandWidth)
				isEvenCell := (bandX+bandY)%2 == 0
				if (isFront && !isEvenCell) || (!isFront && isEvenCell) {
					effectiveAlpha = 0
				}
			}

			if effectiveAlpha >= 0.999 {
				output.Set(x, y, srcColor)
			} else if effectiveAlpha > 0.001 {
				dr, dg, db, _ := output.At(x, y).RGBA()
				invAlpha := 1 - effectiveAlpha
				r := uint8((float64(sr>>8)*effectiveAlpha + float64(dr>>8)*invAlpha))
				g := uint8((float64(sg>>8)*effectiveAlpha + float64(dg>>8)*invAlpha))
				b := uint8((float64(sb>>8)*effectiveAlpha + float64(db>>8)*invAlpha))
				output.Set(x, y, color.RGBA{r, g, b, 255})
			}
		}
	}
}

// drawConnectorLabelsForLayer draws connector labels for a specific layer with opacity.
func (ic *ImageCanvas) drawConnectorLabelsForLayer(output *image.RGBA, layer *pcbimage.Layer) {
	if len(ic.connectorLabels) == 0 {
		return
	}

	labelColor := color.RGBA{R: 0, G: 0, B: 0, A: 255}
	scale := int(ic.zoom * 3)
	if scale < 2 {
		scale = 2
	}
	if scale > 6 {
		scale = 6
	}

	for _, cl := range ic.connectorLabels {
		if cl.Side != layer.Side {
			continue
		}
		canvasX := int(cl.CenterX * ic.zoom)
		canvasY := int(cl.CenterY * ic.zoom)
		DrawRotatedLabelWithOpacity(output, cl.Label, canvasX, canvasY, labelColor, scale, layer.Opacity)
	}
}

// draw is the raster drawing function.
func (ic *ImageCanvas) draw(w, h int) image.Image {
	output := image.NewRGBA(image.Rect(0, 0, w, h))
	ic.pendingLabels = ic.pendingLabels[:0]

	// Fill with 1mm grid background (black and white squares)
	ic.drawGridBackground(output, w, h)

	// Composite each visible layer and draw connector labels with matching opacity
	for _, layer := range ic.layers {
		if layer == nil || layer.Image == nil || !layer.Visible {
			continue
		}
		ic.compositeLayer(output, layer, w, h)
		ic.drawConnectorLabelsForLayer(output, layer)
	}

	// Store for sampling (copy to avoid including overlays)
	ic.lastOutput = image.NewRGBA(output.Bounds())
	draw.Draw(ic.lastOutput, ic.lastOutput.Bounds(), output, image.Point{}, draw.Src)

	// Determine which layer is on top for overlay filtering
	topLayerRef := LayerNone
	if len(ic.layers) > 0 {
		topLayer := ic.layers[len(ic.layers)-1]
		if topLayer != nil {
			if topLayer.Side == pcbimage.SideFront {
				topLayerRef = LayerFront
			} else if topLayer.Side == pcbimage.SideBack {
				topLayerRef = LayerBack
			}
		}
	}

	// Draw overlays, skipping layer-specific overlays not on the top layer
	for _, overlay := range ic.overlays {
		if overlay != nil {
			if overlay.Layer != LayerNone && overlay.Layer != topLayerRef {
				continue
			}
			ic.drawOverlay(output, overlay)
		}
	}

	// Draw rubber band line if active
	if ic.rubberBandOn {
		rbColor := color.RGBA{R: 255, G: 255, B: 0, A: 255}
		x1 := int(ic.rubberBandFrom.X * ic.zoom)
		y1 := int(ic.rubberBandFrom.Y * ic.zoom)
		x2 := int(ic.rubberBandTo.X * ic.zoom)
		y2 := int(ic.rubberBandTo.Y * ic.zoom)
		ic.drawLine(output, x1, y1, x2, y2, rbColor, 2)
	}

	// Draw selection rectangle if selecting
	if ic.selecting && ic.selectionRect != nil {
		ic.drawSelectionRect(output, ic.selectionRect)
	}

	return output
}

// blitRGBAToCairo converts a Go image.RGBA to Cairo's ARGB32 format and paints it.
func blitRGBAToCairo(cr *cairo.Context, img *image.RGBA) {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return
	}

	stride := cairo.FormatStrideForWidth(cairo.FORMAT_ARGB32, w)
	data := make([]byte, stride*h)

	// Convert Go RGBA (R,G,B,A) to Cairo ARGB32 (B,G,R,A on little-endian)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			si := img.PixOffset(x+bounds.Min.X, y+bounds.Min.Y)
			di := y*stride + x*4
			data[di+0] = img.Pix[si+2] // B
			data[di+1] = img.Pix[si+1] // G
			data[di+2] = img.Pix[si+0] // R
			data[di+3] = img.Pix[si+3] // A
		}
	}

	surface, err := cairo.CreateImageSurfaceForData(data, cairo.FORMAT_ARGB32, w, h, stride)
	if err != nil {
		return
	}

	cr.SetSourceSurface(surface, 0, 0)
	cr.Paint()
}

// drawLabelsWithCairo renders accumulated text labels using Cairo's font engine.
// Labels with bounds get a best-fit font size: the largest size where every
// bounded label fits inside its rect with 20% margin.
func (ic *ImageCanvas) drawLabelsWithCairo(cr *cairo.Context) {
	if len(ic.pendingLabels) == 0 {
		return
	}

	cr.SelectFontFace("sans-serif", cairo.FONT_SLANT_NORMAL, cairo.FONT_WEIGHT_BOLD)

	// Compute best-fit font size for bounded labels (connector rects).
	// Find the font size where the tightest label just fits with 20% margin.
	bestFit := ic.zoom * 9 // fallback
	if bestFit < 4 {
		bestFit = 4
	}
	hasBounded := false
	for _, lbl := range ic.pendingLabels {
		if lbl.boundW > 0 && lbl.boundH > 0 {
			hasBounded = true
			break
		}
	}
	if hasBounded {
		// Binary search for the largest font size that fits all bounded labels.
		lo, hi := 1.0, 200.0
		for hi-lo > 0.5 {
			mid := (lo + hi) / 2
			cr.SetFontSize(mid)
			fits := true
			for _, lbl := range ic.pendingLabels {
				if lbl.boundW <= 0 || lbl.boundH <= 0 {
					continue
				}
				ext := cr.TextExtents(lbl.text)
				// Available space with 20% margin
				availW := float64(lbl.boundW) * 0.8
				availH := float64(lbl.boundH) * 0.8
				if lbl.rotated {
					// Rotated: text width fits in rect height, text height fits in rect width
					if ext.Width > availH || ext.Height > availW {
						fits = false
						break
					}
				} else {
					if ext.Width > availW || ext.Height > availH {
						fits = false
						break
					}
				}
			}
			if fits {
				lo = mid
			} else {
				hi = mid
			}
		}
		bestFit = lo
	}

	cr.SetFontSize(bestFit)

	for _, lbl := range ic.pendingLabels {
		cr.Save()
		ext := cr.TextExtents(lbl.text)

		if lbl.rotated {
			cr.Translate(float64(lbl.x), float64(lbl.y))
			cr.Rotate(-math.Pi / 2)
			tx := -ext.Width / 2
			ty := ext.Height / 2

			cr.SetSourceRGBA(1, 1, 1, 0.8)
			for _, off := range [][2]float64{{-1, -1}, {1, -1}, {-1, 1}, {1, 1}} {
				cr.MoveTo(tx+off[0], ty+off[1])
				cr.ShowText(lbl.text)
			}

			cr.SetSourceRGBA(
				float64(lbl.col.R)/255, float64(lbl.col.G)/255,
				float64(lbl.col.B)/255, float64(lbl.col.A)/255,
			)
			cr.MoveTo(tx, ty)
			cr.ShowText(lbl.text)
		} else {
			tx := float64(lbl.x) - ext.Width/2
			ty := float64(lbl.y) + ext.Height/2

			cr.SetSourceRGBA(1, 1, 1, 0.8)
			for _, off := range [][2]float64{{-1, -1}, {1, -1}, {-1, 1}, {1, 1}} {
				cr.MoveTo(tx+off[0], ty+off[1])
				cr.ShowText(lbl.text)
			}

			cr.SetSourceRGBA(
				float64(lbl.col.R)/255, float64(lbl.col.G)/255,
				float64(lbl.col.B)/255, float64(lbl.col.A)/255,
			)
			cr.MoveTo(tx, ty)
			cr.ShowText(lbl.text)
		}
		cr.Restore()
	}
}
