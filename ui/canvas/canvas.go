// Package canvas provides an image canvas with pan, zoom, and selection.
package canvas

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"

	pcbimage "pcb-tracer/internal/image"

	"fyne.io/fyne/v2"
	fynecanvas "fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
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
	widget.BaseWidget

	// Layer stack
	layers []*pcbimage.Layer

	// Overlays (keyed by name, e.g., "front_contacts", "back_contacts")
	overlays map[string]*Overlay

	// Connector labels (drawn with layer opacity)
	connectorLabels []ConnectorLabel

	// Step-edge alignment visualization
	stepEdgeViz StepEdgeViz

	// Display state
	raster *fynecanvas.Raster
	zoom   float64

	// Interaction state
	tool       Tool
	dragging   bool
	dragStartX float32
	dragStartY float32

	// Selection (rubber-band)
	selecting     bool
	selectMode    bool // When true, next drag creates a selection
	selectStart   fyne.Position
	selectEnd     fyne.Position
	selectionRect *OverlayRect // Current selection rectangle (in image coords)

	// Container
	scroll  *zoomScroll
	content *draggableContent
	imgSize fyne.Size // Current image display size

	// Fit to window
	fitToWindow    bool
	lastScrollSize fyne.Size

	// Last rendered output for sampling
	lastOutput *image.RGBA

	// Background grid DPI (1mm grid when > 0)
	gridDPI float64

	// Background mode: false = checkerboard, true = solid black
	solidBlackBackground bool

	// Callbacks
	onZoomChange   func(zoom float64)
	onSelect       func(x1, y1, x2, y2 float64) // Called with canvas coordinates
	onLeftClick    func(x, y float64)           // Left click at image coordinates
	onRightClick   func(x, y float64)           // Right click at image coordinates
	onMiddleClick  func(x, y float64)           // Middle click at canvas coordinates (use GetRenderedOutput)
	onMouseMove    func(x, y float64)           // Mouse move at image coordinates
	onTypedKey     func(ev *fyne.KeyEvent)      // Key press callback
}

// zoomScroll is a widget that wraps a scroll container but intercepts wheel for zoom.
type zoomScroll struct {
	widget.BaseWidget
	scroll *container.Scroll
	canvas *ImageCanvas
}

func newZoomScroll(content fyne.CanvasObject, canvas *ImageCanvas) *zoomScroll {
	scroll := container.NewScroll(content)
	scroll.Direction = container.ScrollBoth
	zs := &zoomScroll{scroll: scroll, canvas: canvas}
	zs.ExtendBaseWidget(zs)
	return zs
}

func (zs *zoomScroll) Scrolled(ev *fyne.ScrollEvent) {
	// Use wheel for zoom, not scroll
	if ev.Scrolled.DY > 0 {
		zs.canvas.ZoomIn()
	} else if ev.Scrolled.DY < 0 {
		zs.canvas.ZoomOut()
	}
}

func (zs *zoomScroll) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(zs.scroll)
}

// Offset returns the scroll container's current offset.
func (zs *zoomScroll) Offset() fyne.Position {
	return zs.scroll.Offset
}

// Size returns the scroll container's size.
func (zs *zoomScroll) Size() fyne.Size {
	return zs.scroll.Size()
}

// Refresh refreshes the scroll container.
func (zs *zoomScroll) Refresh() {
	zs.scroll.Refresh()
	zs.BaseWidget.Refresh()
}

// Resize sets the size of the scroll container.
func (zs *zoomScroll) Resize(size fyne.Size) {
	zs.scroll.Resize(size)
	zs.BaseWidget.Resize(size)
}

// draggableContent wraps the raster to handle mouse events.
type draggableContent struct {
	widget.BaseWidget
	canvas *ImageCanvas
	raster *fynecanvas.Raster
}

func newDraggableContent(ic *ImageCanvas, raster *fynecanvas.Raster) *draggableContent {
	dc := &draggableContent{
		canvas: ic,
		raster: raster,
	}
	dc.ExtendBaseWidget(dc)
	return dc
}

func (dc *draggableContent) CreateRenderer() fyne.WidgetRenderer {
	return &draggableContentRenderer{content: dc}
}

func (dc *draggableContent) MinSize() fyne.Size {
	return dc.raster.MinSize()
}

func (dc *draggableContent) Dragged(ev *fyne.DragEvent) {
	if !dc.canvas.selectMode {
		return
	}

	// ev.Position is relative to the draggableContent widget (content coordinates)
	// Fyne handles scroll offset internally, so no adjustment needed
	pos := ev.Position

	if !dc.canvas.selecting {
		dc.canvas.selecting = true
		dc.canvas.selectStart = pos
	}
	dc.canvas.selectEnd = pos

	// Use canvas coordinates directly
	x1, y1 := float64(dc.canvas.selectStart.X), float64(dc.canvas.selectStart.Y)
	x2, y2 := float64(dc.canvas.selectEnd.X), float64(dc.canvas.selectEnd.Y)

	// Normalize
	if x1 > x2 {
		x1, x2 = x2, x1
	}
	if y1 > y2 {
		y1, y2 = y2, y1
	}

	// Store in canvas coordinates (selectionRect now holds canvas coords)
	dc.canvas.selectionRect = &OverlayRect{
		X:      int(x1),
		Y:      int(y1),
		Width:  int(x2 - x1),
		Height: int(y2 - y1),
	}
	dc.canvas.Refresh()
}

func (dc *draggableContent) DragEnd() {
	if !dc.canvas.selectMode || !dc.canvas.selecting {
		return
	}

	dc.canvas.selecting = false
	dc.canvas.selectMode = false // Auto-disable after selection

	// Call callback with image coordinates (convert from canvas coords by dividing by zoom)
	if dc.canvas.onSelect != nil && dc.canvas.selectionRect != nil {
		rect := dc.canvas.selectionRect
		zoom := dc.canvas.zoom
		dc.canvas.onSelect(
			float64(rect.X)/zoom,
			float64(rect.Y)/zoom,
			float64(rect.X+rect.Width)/zoom,
			float64(rect.Y+rect.Height)/zoom,
		)
	}

	// Clear selection rectangle
	dc.canvas.selectionRect = nil
	dc.canvas.Refresh()
}

func (dc *draggableContent) Scrolled(ev *fyne.ScrollEvent) {
	// Use mouse wheel for zooming
	if ev.Scrolled.DY > 0 {
		dc.canvas.ZoomIn()
	} else if ev.Scrolled.DY < 0 {
		dc.canvas.ZoomOut()
	}
}

// Tapped handles left-click events.
func (dc *draggableContent) Tapped(ev *fyne.PointEvent) {
	if dc.canvas.onLeftClick == nil {
		return
	}

	// Workaround for Fyne bug: reject clicks outside widget bounds
	// ev.Position should be relative to the widget, so check for valid range
	size := dc.Size()
	if ev.Position.X < 0 || ev.Position.Y < 0 ||
		ev.Position.X > size.Width || ev.Position.Y > size.Height {
		return
	}

	// ev.Position is relative to the draggableContent widget (content/canvas coordinates)
	// Fyne handles scroll offset internally, so no adjustment needed
	canvasX := float64(ev.Position.X)
	canvasY := float64(ev.Position.Y)

	// Convert from canvas (zoomed) to image coordinates
	imgX := canvasX / dc.canvas.zoom
	imgY := canvasY / dc.canvas.zoom

	dc.canvas.onLeftClick(imgX, imgY)
}

// TappedSecondary handles right-click events.
func (dc *draggableContent) TappedSecondary(ev *fyne.PointEvent) {
	if dc.canvas.onRightClick == nil {
		return
	}

	// Workaround for Fyne bug: reject clicks outside widget bounds
	size := dc.Size()
	if ev.Position.X < 0 || ev.Position.Y < 0 ||
		ev.Position.X > size.Width || ev.Position.Y > size.Height {
		return
	}

	// ev.Position is relative to the draggableContent widget (content/canvas coordinates)
	// Fyne handles scroll offset internally, so no adjustment needed
	canvasX := float64(ev.Position.X)
	canvasY := float64(ev.Position.Y)

	// Convert from canvas (zoomed) to image coordinates
	imgX := canvasX / dc.canvas.zoom
	imgY := canvasY / dc.canvas.zoom

	dc.canvas.onRightClick(imgX, imgY)
}

// MouseDown implements desktop.Mouseable for middle-click support.
func (dc *draggableContent) MouseDown(ev *desktop.MouseEvent) {
	// Only handle middle button
	if ev.Button != desktop.MouseButtonTertiary {
		return
	}

	if dc.canvas.onMiddleClick == nil {
		return
	}

	// Workaround for Fyne bug: reject clicks outside widget bounds
	size := dc.Size()
	if ev.Position.X < 0 || ev.Position.Y < 0 ||
		ev.Position.X > size.Width || ev.Position.Y > size.Height {
		return
	}

	// desktop.MouseEvent.Position is relative to the widget (content), not the viewport.
	// No need to add scroll offset - Fyne already delivers positions in content coordinates.
	canvasX := float64(ev.Position.X)
	canvasY := float64(ev.Position.Y)

	dc.canvas.onMiddleClick(canvasX, canvasY)
}

// MouseUp implements desktop.Mouseable (required but unused).
func (dc *draggableContent) MouseUp(ev *desktop.MouseEvent) {}

// MouseIn implements desktop.Hoverable.
func (dc *draggableContent) MouseIn(ev *desktop.MouseEvent) {}

// MouseMoved implements desktop.Hoverable for rubber-band feedback.
func (dc *draggableContent) MouseMoved(ev *desktop.MouseEvent) {
	if dc.canvas.onMouseMove == nil {
		return
	}
	// Convert canvas coordinates to image coordinates
	imgX := float64(ev.Position.X) / dc.canvas.zoom
	imgY := float64(ev.Position.Y) / dc.canvas.zoom
	dc.canvas.onMouseMove(imgX, imgY)
}

// MouseOut implements desktop.Hoverable.
func (dc *draggableContent) MouseOut() {}

// FocusGained implements fyne.Focusable.
func (dc *draggableContent) FocusGained() {}

// FocusLost implements fyne.Focusable.
func (dc *draggableContent) FocusLost() {}

// TypedRune implements fyne.Focusable (required but unused).
func (dc *draggableContent) TypedRune(r rune) {}

// TypedKey implements fyne.Focusable for keyboard input.
func (dc *draggableContent) TypedKey(ev *fyne.KeyEvent) {
	if dc.canvas.onTypedKey != nil {
		dc.canvas.onTypedKey(ev)
	}
}

type draggableContentRenderer struct {
	content *draggableContent
}

func (r *draggableContentRenderer) Layout(size fyne.Size) {
	r.content.raster.Resize(size)
}

func (r *draggableContentRenderer) MinSize() fyne.Size {
	return r.content.raster.MinSize()
}

func (r *draggableContentRenderer) Refresh() {
	r.content.raster.Refresh()
}

func (r *draggableContentRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.content.raster}
}

func (r *draggableContentRenderer) Destroy() {}

// NewImageCanvas creates a new image canvas.
func NewImageCanvas() *ImageCanvas {
	ic := &ImageCanvas{
		zoom:     1.0,
		tool:     ToolPan,
		imgSize:  fyne.NewSize(400, 300),
		layers:   make([]*pcbimage.Layer, 0),
		overlays: make(map[string]*Overlay),
	}

	// Create the raster for drawing
	ic.raster = fynecanvas.NewRaster(ic.draw)
	ic.raster.ScaleMode = fynecanvas.ImageScalePixels
	ic.raster.SetMinSize(ic.imgSize)

	// Wrap raster in draggable content for mouse events
	ic.content = newDraggableContent(ic, ic.raster)

	// Create zoomable scroll container (wheel = zoom, drag = pan)
	ic.scroll = newZoomScroll(ic.content, ic)

	ic.ExtendBaseWidget(ic)
	return ic
}

// EnableSelectMode enables selection mode for the next drag.
func (ic *ImageCanvas) EnableSelectMode() {
	ic.selectMode = true
	ic.selecting = false
	ic.selectionRect = nil
}

// Container returns the canvas container for embedding in layouts.
func (ic *ImageCanvas) Container() fyne.CanvasObject {
	return ic.scroll
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
			// Remove from current position
			ic.layers = append(ic.layers[:i], ic.layers[i+1:]...)
			// Add to end (top)
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
			// Remove from current position
			ic.layers = append(ic.layers[:i], ic.layers[i+1:]...)
			// Add to end (top)
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
// Labels are drawn with the same opacity as their corresponding layer.
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
// stepY is unused (kept for API compatibility).
// dpi is used to calculate 1cm checkerboard cell size.
func (ic *ImageCanvas) SetStepEdgeViz(enabled bool, stepY, dpi float64) {
	ic.stepEdgeViz.Enabled = enabled
	_ = stepY // unused, kept for API compatibility
	if dpi > 0 {
		// 1 cm = 0.3937 inches
		ic.stepEdgeViz.BandWidth = dpi * 0.3937
		ic.stepEdgeViz.Height = dpi * 0.5 // 0.5 inch visualization region
	} else {
		ic.stepEdgeViz.BandWidth = 100 // fallback
		ic.stepEdgeViz.Height = 150
	}
	ic.Refresh()
}

// GetStepEdgeViz returns the current step-edge visualization settings.
func (ic *ImageCanvas) GetStepEdgeViz() StepEdgeViz {
	return ic.stepEdgeViz
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

// FitToWindow adjusts zoom to fit the image in the visible area.
func (ic *ImageCanvas) FitToWindow() {
	bounds := ic.getLayerBounds()
	if bounds.Dx() == 0 || bounds.Dy() == 0 {
		return
	}

	// Get viewport size
	viewSize := ic.scroll.Size()
	if viewSize.Width <= 0 || viewSize.Height <= 0 {
		return
	}

	// Calculate zoom to fit both dimensions
	zoomX := float64(viewSize.Width) / float64(bounds.Dx())
	zoomY := float64(viewSize.Height) / float64(bounds.Dy())

	zoom := zoomX
	if zoomY < zoomX {
		zoom = zoomY
	}

	ic.SetZoom(zoom * 0.95) // Leave a small margin
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

// CheckResize checks if scroll container was resized and auto-fits if enabled.
func (ic *ImageCanvas) CheckResize(size fyne.Size) {
	if !ic.fitToWindow {
		return
	}
	if size.Width > 0 && size.Height > 0 && size != ic.lastScrollSize {
		ic.lastScrollSize = size
		ic.FitToWindow()
	}
}

// ScrollToRegion scrolls the canvas so that the given image coordinates are visible.
// x, y are in source image coordinates (not canvas/zoomed).
func (ic *ImageCanvas) ScrollToRegion(x, y, width, height int) {
	if ic.scroll == nil || ic.scroll.scroll == nil {
		return
	}

	// Convert image coordinates to canvas coordinates
	canvasX := float64(x) * ic.zoom
	canvasY := float64(y) * ic.zoom
	canvasW := float64(width) * ic.zoom
	canvasH := float64(height) * ic.zoom

	// Get viewport size
	viewSize := ic.scroll.scroll.Size()
	if viewSize.Width <= 0 || viewSize.Height <= 0 {
		return
	}

	// Calculate center of the region in canvas coordinates
	centerX := canvasX + canvasW/2
	centerY := canvasY + canvasH/2

	// Calculate scroll offset to center the region in the viewport
	offsetX := centerX - float64(viewSize.Width)/2
	offsetY := centerY - float64(viewSize.Height)/2

	// Clamp to valid scroll range
	if offsetX < 0 {
		offsetX = 0
	}
	if offsetY < 0 {
		offsetY = 0
	}

	maxX := float64(ic.imgSize.Width) - float64(viewSize.Width)
	maxY := float64(ic.imgSize.Height) - float64(viewSize.Height)
	if offsetX > maxX {
		offsetX = maxX
	}
	if offsetY > maxY {
		offsetY = maxY
	}

	// Set the scroll offset
	ic.scroll.scroll.Offset = fyne.NewPos(float32(offsetX), float32(offsetY))
	ic.scroll.scroll.Refresh()
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
// Coordinates are in image space (not zoomed).
func (ic *ImageCanvas) OnLeftClick(callback func(x, y float64)) {
	ic.onLeftClick = callback
}

// OnRightClick sets a callback for right-click events.
// Coordinates are in image space (not zoomed).
func (ic *ImageCanvas) OnRightClick(callback func(x, y float64)) {
	ic.onRightClick = callback
}

// OnMiddleClick sets a callback for middle-click events.
// Coordinates are in image space (not zoomed).
func (ic *ImageCanvas) OnMiddleClick(callback func(x, y float64)) {
	ic.onMiddleClick = callback
}

// OnMouseMove sets a callback for mouse-move events.
// Coordinates are in image space (not zoomed).
func (ic *ImageCanvas) OnMouseMove(callback func(x, y float64)) {
	ic.onMouseMove = callback
}

// OnTypedKey sets a callback for key press events.
func (ic *ImageCanvas) OnTypedKey(callback func(ev *fyne.KeyEvent)) {
	ic.onTypedKey = callback
}

// FocusCanvas requests keyboard focus on the canvas content widget.
func (ic *ImageCanvas) FocusCanvas(w fyne.Window) {
	if ic.content != nil {
		w.Canvas().Focus(ic.content)
	}
}

// GetRenderedOutput returns the last rendered canvas output for sampling.
func (ic *ImageCanvas) GetRenderedOutput() *image.RGBA {
	return ic.lastOutput
}

// ScrollOffset returns the current scroll offset.
func (ic *ImageCanvas) ScrollOffset() fyne.Position {
	if ic.scroll != nil {
		return ic.scroll.Offset()
	}
	return fyne.NewPos(0, 0)
}

// Refresh refreshes the canvas display.
func (ic *ImageCanvas) Refresh() {
	ic.raster.Refresh()
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

// updateContentSize updates the content size based on image and zoom.
func (ic *ImageCanvas) updateContentSize() {
	bounds := ic.getLayerBounds()
	if bounds.Dx() == 0 || bounds.Dy() == 0 {
		ic.imgSize = fyne.NewSize(400, 300)
	} else {
		width := float32(float64(bounds.Dx()) * ic.zoom)
		height := float32(float64(bounds.Dy()) * ic.zoom)
		ic.imgSize = fyne.NewSize(width, height)
	}

	ic.raster.SetMinSize(ic.imgSize)
	ic.raster.Resize(ic.imgSize)
	if ic.content != nil {
		ic.content.Resize(ic.imgSize)
		ic.content.Refresh()
	}
	ic.raster.Refresh()
	if ic.scroll != nil {
		ic.scroll.Refresh()
	}
}

// draw is the raster drawing function.
func (ic *ImageCanvas) draw(w, h int) image.Image {
	// Check for size change and auto-fit if enabled
	currentSize := fyne.NewSize(float32(w), float32(h))
	if ic.fitToWindow && currentSize != ic.lastScrollSize && w > 0 && h > 0 {
		ic.lastScrollSize = currentSize
		// Schedule fit after this draw completes
		go func() {
			ic.FitToWindow()
		}()
	}

	output := image.NewRGBA(image.Rect(0, 0, w, h))

	// Fill with 1mm grid background (black and white squares)
	ic.drawGridBackground(output, w, h)

	// Composite each visible layer and draw connector labels with matching opacity
	for _, layer := range ic.layers {
		if layer == nil || layer.Image == nil || !layer.Visible {
			continue
		}
		ic.compositeLayer(output, layer, w, h)

		// Draw connector labels for this layer's side with the layer's opacity
		ic.drawConnectorLabelsForLayer(output, layer)
	}

	// Store for sampling (copy to avoid including overlays)
	ic.lastOutput = image.NewRGBA(output.Bounds())
	draw.Draw(ic.lastOutput, ic.lastOutput.Bounds(), output, image.Point{}, draw.Src)

	// Draw overlays
	for _, overlay := range ic.overlays {
		if overlay != nil {
			ic.drawOverlay(output, overlay)
		}
	}

	// Draw selection rectangle if selecting
	if ic.selecting && ic.selectionRect != nil {
		ic.drawSelectionRect(output, ic.selectionRect)
	}

	return output
}

// drawGridBackground fills the output with a 1mm black and white grid pattern,
// or solid black if solidBlackBackground is set.
func (ic *ImageCanvas) drawGridBackground(output *image.RGBA, w, h int) {
	black := color.RGBA{R: 0, G: 0, B: 0, A: 255}

	// Solid black mode - just fill with black
	if ic.solidBlackBackground {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				output.Set(x, y, black)
			}
		}
		return
	}

	// Checkerboard mode
	// 1mm = 0.03937 inches
	// Grid size in image pixels = dpi * 0.03937
	// Grid size in canvas pixels = gridSize * zoom
	var gridSize float64
	if ic.gridDPI > 0 {
		gridSize = ic.gridDPI * 0.03937 * ic.zoom
	} else {
		// Fallback: assume 1200 DPI
		gridSize = 1200 * 0.03937 * ic.zoom
	}

	// Minimum visible grid size (avoid too small squares)
	if gridSize < 4 {
		gridSize = 4
	}

	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}

	for y := 0; y < h; y++ {
		gridY := int(float64(y) / gridSize)
		for x := 0; x < w; x++ {
			gridX := int(float64(x) / gridSize)
			// Checkerboard pattern
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

	// Fast path for normalized layers: all transforms are baked in, just zoom + copy
	if layer.IsNormalized {
		ic.compositeLayerNormalized(output, layer, w, h)
		return
	}

	// Manual transform parameters
	offsetX := float64(layer.ManualOffsetX)
	offsetY := float64(layer.ManualOffsetY)
	rotation := layer.ManualRotation * math.Pi / 180.0 // Convert to radians

	// Shear parameters (default to 1.0 if not set)
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

	// Precompute rotation sin/cos
	cosR := math.Cos(-rotation)
	sinR := math.Sin(-rotation)

	// Source image dimensions
	srcW := float64(srcBounds.Max.X - srcBounds.Min.X)
	srcH := float64(srcBounds.Max.Y - srcBounds.Min.Y)

	// Rotation center: use layer's rotation center if set, otherwise image center
	var srcCx, srcCy float64
	if layer.RotationCenterX != 0 || layer.RotationCenterY != 0 {
		// Use board center for rotation
		srcCx = layer.RotationCenterX
		srcCy = layer.RotationCenterY
	} else {
		// Fall back to image center
		srcCx = float64(srcBounds.Min.X+srcBounds.Max.X) / 2.0
		srcCy = float64(srcBounds.Min.Y+srcBounds.Max.Y) / 2.0
	}

	// Check if we have any transform beyond offset
	hasTransform := rotation != 0 || shearTopX != 1.0 || shearBottomX != 1.0 ||
		shearLeftY != 1.0 || shearRightY != 1.0

	// Checkerboard alignment visualization parameters
	vizEnabled := ic.stepEdgeViz.Enabled
	vizBandWidth := ic.stepEdgeViz.BandWidth

	// Determine if this layer should show on even or odd checkerboard cells
	// Front = even cells (bandX+bandY even), Back = odd cells (bandX+bandY odd)
	isFront := layer.Side == pcbimage.SideFront

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// Convert canvas coords to image coords (accounting for zoom and offset)
			imgX := float64(x)/ic.zoom - offsetX
			imgY := float64(y)/ic.zoom - offsetY

			var srcX, srcY int

			if hasTransform {
				// Convert to source image coordinates first
				srcPosX := imgX + float64(srcBounds.Min.X)
				srcPosY := imgY + float64(srcBounds.Min.Y)

				// 1. Translate to center (relative coords)
				relX := srcPosX - srcCx
				relY := srcPosY - srcCy

				// 2. Inverse rotate
				rotX := relX*cosR - relY*sinR
				rotY := relX*sinR + relY*cosR

				// 3. Apply inverse shear (position-dependent scale)
				// Normalized position in source (0 to 1)
				// Use pre-shear position to determine scale factors
				normY := (rotY + srcH/2) / srcH // 0 at top, 1 at bottom
				normX := (rotX + srcW/2) / srcW // 0 at left, 1 at right

				// Clamp to [0,1] for interpolation
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

				// Interpolate scale factors
				scaleX := shearTopX + (shearBottomX-shearTopX)*normY
				scaleY := shearLeftY + (shearRightY-shearLeftY)*normX

				// Inverse scale
				scaledX := rotX / scaleX
				scaledY := rotY / scaleY

				// 4. Translate back to source coords
				srcX = int(scaledX + srcCx)
				srcY = int(scaledY + srcCy)
			} else {
				// Simple case: just offset
				srcX = int(imgX) + srcBounds.Min.X
				srcY = int(imgY) + srcBounds.Min.Y
			}

			// Bounds check
			if srcX < srcBounds.Min.X || srcX >= srcBounds.Max.X ||
				srcY < srcBounds.Min.Y || srcY >= srcBounds.Max.Y {
				continue
			}

			srcColor := src.At(srcX, srcY)
			sr, sg, sb, sa := srcColor.RGBA()

			// Apply layer opacity to alpha
			effectiveAlpha := float64(sa) / 0xffff * opacity

			// Apply checkerboard alignment visualization if enabled
			if vizEnabled && vizBandWidth > 0 {
				// Use canvas coordinates (without layer offset) so checkerboard stays stationary
				// while images move beneath it when using compass nudge controls
				canvasImgX := float64(x) / ic.zoom
				canvasImgY := float64(y) / ic.zoom
				bandX := int(canvasImgX / vizBandWidth)
				bandY := int(canvasImgY / vizBandWidth)
				isEvenCell := (bandX+bandY)%2 == 0

				// Front shows on even cells, back shows on odd cells
				if (isFront && !isEvenCell) || (!isFront && isEvenCell) {
					effectiveAlpha = 0 // Hide this layer in this cell
				}
			}

			if effectiveAlpha >= 0.999 {
				// Fully opaque - just copy
				output.Set(x, y, srcColor)
			} else if effectiveAlpha > 0.001 {
				// Partially transparent - blend with background
				dr, dg, db, _ := output.At(x, y).RGBA()
				invAlpha := 1 - effectiveAlpha

				r := uint8((float64(sr>>8)*effectiveAlpha + float64(dr>>8)*invAlpha))
				g := uint8((float64(sg>>8)*effectiveAlpha + float64(dg>>8)*invAlpha))
				b := uint8((float64(sb>>8)*effectiveAlpha + float64(db>>8)*invAlpha))

				output.Set(x, y, color.RGBA{r, g, b, 255})
			}
			// effectiveAlpha near 0: keep background (black)
		}
	}
}

// compositeLayerNormalized is the fast path for layers where all transforms are baked in.
// Only zoom and opacity need to be applied — no rotation, shear, or offset math.
func (ic *ImageCanvas) compositeLayerNormalized(output *image.RGBA, layer *pcbimage.Layer, w, h int) {
	src := layer.Image
	srcBounds := src.Bounds()
	opacity := layer.Opacity

	// Checkerboard viz
	vizEnabled := ic.stepEdgeViz.Enabled
	vizBandWidth := ic.stepEdgeViz.BandWidth
	isFront := layer.Side == pcbimage.SideFront

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// Canvas pixel → source pixel via zoom only
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

	// Use black text color for labels (visible on gold contacts)
	labelColor := color.RGBA{R: 0, G: 0, B: 0, A: 255}

	// Calculate scale based on zoom - use larger minimum for readability
	scale := int(ic.zoom * 3)
	if scale < 2 {
		scale = 2
	}
	if scale > 6 {
		scale = 6
	}

	drawn := 0
	for _, cl := range ic.connectorLabels {
		// Only draw labels that match this layer's side
		if cl.Side != layer.Side {
			continue
		}

		// Convert image coordinates to canvas coordinates
		canvasX := int(cl.CenterX * ic.zoom)
		canvasY := int(cl.CenterY * ic.zoom)

		// Draw the rotated label with the layer's opacity
		DrawRotatedLabelWithOpacity(output, cl.Label, canvasX, canvasY, labelColor, scale, layer.Opacity)
		drawn++
	}
	if drawn > 0 {
		fmt.Printf("  drawConnectorLabelsForLayer: drew %d labels for side %v, scale=%d, opacity=%.2f\n",
			drawn, layer.Side, scale, layer.Opacity)
	}
}

// ImageToCanvas converts image coordinates to canvas coordinates.
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

// CreateRenderer implements fyne.Widget.
func (ic *ImageCanvas) CreateRenderer() fyne.WidgetRenderer {
	return &imageCanvasRenderer{canvas: ic}
}

type imageCanvasRenderer struct {
	canvas *ImageCanvas
}

func (r *imageCanvasRenderer) Layout(size fyne.Size) {
	if r.canvas.scroll != nil {
		r.canvas.scroll.Resize(size)
	} else if r.canvas.content != nil {
		r.canvas.content.Resize(size)
	}
	// Check for resize and auto-fit if enabled
	r.canvas.CheckResize(size)
}

func (r *imageCanvasRenderer) MinSize() fyne.Size {
	return fyne.NewSize(100, 100)
}

func (r *imageCanvasRenderer) Refresh() {
	r.canvas.raster.Refresh()
}

func (r *imageCanvasRenderer) Objects() []fyne.CanvasObject {
	if r.canvas.scroll != nil {
		return []fyne.CanvasObject{r.canvas.scroll}
	}
	return []fyne.CanvasObject{r.canvas.content}
}

func (r *imageCanvasRenderer) Destroy() {}
