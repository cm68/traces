// Package canvas provides an image canvas with pan, zoom, and selection.
package canvas

import (
	"fmt"
	"image"
	"image/color"
	"math"

	pcbimage "pcb-tracer/internal/image"

	"fyne.io/fyne/v2"
	fynecanvas "fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
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

// ImageCanvas provides an image display with pan, zoom, and selection.
type ImageCanvas struct {
	widget.BaseWidget

	// Layer stack
	layers []*pcbimage.Layer

	// Overlays (keyed by name, e.g., "front_contacts", "back_contacts")
	overlays map[string]*Overlay

	// Connector labels (drawn with layer opacity)
	connectorLabels []ConnectorLabel

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

	// Callbacks
	onZoomChange func(zoom float64)
	onSelect     func(x1, y1, x2, y2 float64) // Called with canvas coordinates
	onLeftClick  func(x, y float64)           // Left click at image coordinates
	onRightClick func(x, y float64)           // Right click at image coordinates
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

	// ev.Position is relative to viewport, add scroll offset for content position
	scrollOffset := dc.canvas.scroll.Offset()
	pos := fyne.Position{
		X: ev.Position.X + scrollOffset.X,
		Y: ev.Position.Y + scrollOffset.Y,
	}

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

	// Call callback with canvas coordinates
	if dc.canvas.onSelect != nil && dc.canvas.selectionRect != nil {
		rect := dc.canvas.selectionRect
		dc.canvas.onSelect(
			float64(rect.X),
			float64(rect.Y),
			float64(rect.X+rect.Width),
			float64(rect.Y+rect.Height),
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

	// Convert screen position to image coordinates
	scrollOffset := dc.canvas.scroll.Offset()
	canvasX := float64(ev.Position.X + scrollOffset.X)
	canvasY := float64(ev.Position.Y + scrollOffset.Y)

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

	// Convert screen position to image coordinates
	scrollOffset := dc.canvas.scroll.Offset()
	canvasX := float64(ev.Position.X + scrollOffset.X)
	canvasY := float64(ev.Position.Y + scrollOffset.Y)

	// Convert from canvas (zoomed) to image coordinates
	imgX := canvasX / dc.canvas.zoom
	imgY := canvasY / dc.canvas.zoom

	dc.canvas.onRightClick(imgX, imgY)
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

// GetRenderedOutput returns the last rendered canvas output for sampling.
func (ic *ImageCanvas) GetRenderedOutput() *image.RGBA {
	return ic.lastOutput
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

	// Fill with black background (set alpha channel)
	for i := 3; i < len(output.Pix); i += 4 {
		output.Pix[i] = 255
	}

	// Composite each visible layer and draw connector labels with matching opacity
	for _, layer := range ic.layers {
		if layer == nil || layer.Image == nil || !layer.Visible {
			continue
		}
		ic.compositeLayer(output, layer, w, h)

		// Draw connector labels for this layer's side with the layer's opacity
		ic.drawConnectorLabelsForLayer(output, layer)
	}

	// Store for sampling
	ic.lastOutput = output

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

// compositeLayer draws a single layer onto the output with opacity.
func (ic *ImageCanvas) compositeLayer(output *image.RGBA, layer *pcbimage.Layer, w, h int) {
	src := layer.Image
	srcBounds := src.Bounds()
	opacity := layer.Opacity

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

	// Source image dimensions and center
	srcW := float64(srcBounds.Max.X - srcBounds.Min.X)
	srcH := float64(srcBounds.Max.Y - srcBounds.Min.Y)
	srcCx := float64(srcBounds.Min.X+srcBounds.Max.X) / 2.0
	srcCy := float64(srcBounds.Min.Y+srcBounds.Max.Y) / 2.0

	// Check if we have any transform beyond offset
	hasTransform := rotation != 0 || shearTopX != 1.0 || shearBottomX != 1.0 ||
		shearLeftY != 1.0 || shearRightY != 1.0

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
