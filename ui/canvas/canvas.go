// Package canvas provides an image canvas with pan, zoom, and selection.
package canvas

import (
	"image"
	"image/color"

	pcbimage "pcb-tracer/internal/image"
	"pcb-tracer/pkg/geometry"

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

// Overlay represents a drawable overlay on the canvas.
type Overlay struct {
	Rectangles []OverlayRect
	Polygons   []OverlayPolygon
	Color      color.RGBA
}

// FillPattern indicates how to fill a rectangle.
type FillPattern int

const (
	FillNone       FillPattern = iota // Just outline
	FillSolid                         // Solid fill
	FillStripe                        // Diagonal stripe
	FillCrosshatch                    // Diagonal crosshatch
	FillTarget                        // Crosshairs through center (target marker)
)

// OverlayRect represents a rectangle to draw on the overlay.
type OverlayRect struct {
	X, Y, Width, Height int
	Label               string      // Optional label to draw centered in the rectangle
	Fill                FillPattern // Fill pattern for the rectangle
	StripeInterval      int         // Interval for stripe/crosshatch patterns (0 = use width)
}

// OverlayPolygon represents a polygon to draw on the overlay.
type OverlayPolygon struct {
	Points []geometry.Point2D // Polygon vertices in image coordinates
	Label  string             // Optional label to draw at center
	Filled bool               // If true, fill the polygon; otherwise just outline
}

// digitPatterns contains 3x5 pixel patterns for digits 0-9.
// Each digit is represented as 5 rows of 3 bits.
var digitPatterns = [10][5]uint8{
	{0b111, 0b101, 0b101, 0b101, 0b111}, // 0
	{0b010, 0b110, 0b010, 0b010, 0b111}, // 1
	{0b111, 0b001, 0b111, 0b100, 0b111}, // 2
	{0b111, 0b001, 0b111, 0b001, 0b111}, // 3
	{0b101, 0b101, 0b111, 0b001, 0b001}, // 4
	{0b111, 0b100, 0b111, 0b001, 0b111}, // 5
	{0b111, 0b100, 0b111, 0b101, 0b111}, // 6
	{0b111, 0b001, 0b001, 0b001, 0b001}, // 7
	{0b111, 0b101, 0b111, 0b101, 0b111}, // 8
	{0b111, 0b101, 0b111, 0b001, 0b111}, // 9
}

// ImageCanvas provides an image display with pan, zoom, and selection.
type ImageCanvas struct {
	widget.BaseWidget

	// Layer stack
	layers []*pcbimage.Layer

	// Overlays (keyed by name, e.g., "front_contacts", "back_contacts")
	overlays map[string]*Overlay

	// Display state
	raster  *fynecanvas.Raster
	zoom    float64

	// Interaction state
	tool       Tool
	dragging   bool
	dragStartX float32
	dragStartY float32

	// Selection (rubber-band)
	selecting      bool
	selectMode     bool // When true, next drag creates a selection
	selectStart    fyne.Position
	selectEnd      fyne.Position
	selectionRect  *OverlayRect // Current selection rectangle (in image coords)

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
	onZoomChange  func(zoom float64)
	onSelect      func(x1, y1, x2, y2 float64) // Called with canvas coordinates
	onLeftClick   func(x, y float64)           // Left click at image coordinates
	onRightClick  func(x, y float64)           // Right click at image coordinates
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

	// Composite each visible layer
	for _, layer := range ic.layers {
		if layer == nil || layer.Image == nil || !layer.Visible {
			continue
		}
		ic.compositeLayer(output, layer, w, h)
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

// drawSelectionRect draws a selection rectangle with a distinctive pattern.
func (ic *ImageCanvas) drawSelectionRect(output *image.RGBA, rect *OverlayRect) {
	// Use yellow for selection
	col := color.RGBA{R: 255, G: 255, B: 0, A: 255}

	// rect is already in canvas coordinates
	x1 := rect.X
	y1 := rect.Y
	x2 := rect.X + rect.Width
	y2 := rect.Y + rect.Height

	bounds := output.Bounds()

	// Draw dashed rectangle outline (alternate pixels)
	// Top edge
	for x := x1; x <= x2; x++ {
		if (x+y1)%4 < 2 && x >= bounds.Min.X && x < bounds.Max.X && y1 >= bounds.Min.Y && y1 < bounds.Max.Y {
			output.Set(x, y1, col)
		}
	}
	// Bottom edge
	for x := x1; x <= x2; x++ {
		if (x+y2)%4 < 2 && x >= bounds.Min.X && x < bounds.Max.X && y2 >= bounds.Min.Y && y2 < bounds.Max.Y {
			output.Set(x, y2, col)
		}
	}
	// Left edge
	for y := y1; y <= y2; y++ {
		if (x1+y)%4 < 2 && x1 >= bounds.Min.X && x1 < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
			output.Set(x1, y, col)
		}
	}
	// Right edge
	for y := y1; y <= y2; y++ {
		if (x2+y)%4 < 2 && x2 >= bounds.Min.X && x2 < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
			output.Set(x2, y, col)
		}
	}
}

// drawOverlay draws an overlay on the output image.
func (ic *ImageCanvas) drawOverlay(output *image.RGBA, overlay *Overlay) {
	col := overlay.Color
	for _, rect := range overlay.Rectangles {
		// Scale rectangle coordinates by zoom
		x1 := int(float64(rect.X) * ic.zoom)
		y1 := int(float64(rect.Y) * ic.zoom)
		x2 := int(float64(rect.X+rect.Width) * ic.zoom)
		y2 := int(float64(rect.Y+rect.Height) * ic.zoom)

		bounds := output.Bounds()

		// Draw fill pattern first (before outline)
		if rect.Fill != FillNone {
			interval := rect.StripeInterval
			if interval <= 0 {
				interval = rect.Width // Default to contact width
			}
			// Scale interval by zoom
			interval = int(float64(interval) * ic.zoom)
			if interval < 2 {
				interval = 2
			}

			ic.drawFillPattern(output, x1, y1, x2, y2, col, rect.Fill, interval)
		}

		// Draw rectangle outline (2 pixel thick)
		for t := 0; t < 2; t++ {
			// Top edge
			for x := x1; x <= x2; x++ {
				if x >= bounds.Min.X && x < bounds.Max.X && y1+t >= bounds.Min.Y && y1+t < bounds.Max.Y {
					output.Set(x, y1+t, col)
				}
			}
			// Bottom edge
			for x := x1; x <= x2; x++ {
				if x >= bounds.Min.X && x < bounds.Max.X && y2-t >= bounds.Min.Y && y2-t < bounds.Max.Y {
					output.Set(x, y2-t, col)
				}
			}
			// Left edge
			for y := y1; y <= y2; y++ {
				if x1+t >= bounds.Min.X && x1+t < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
					output.Set(x1+t, y, col)
				}
			}
			// Right edge
			for y := y1; y <= y2; y++ {
				if x2-t >= bounds.Min.X && x2-t < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
					output.Set(x2-t, y, col)
				}
			}
		}

		// Draw label if present
		if rect.Label != "" {
			ic.drawLabel(output, rect.Label, x1, y1, x2, y2, col)
		}
	}

	// Draw polygons
	for _, poly := range overlay.Polygons {
		if len(poly.Points) < 3 {
			continue
		}
		ic.drawPolygon(output, poly, col)
	}
}

// drawPolygon draws a filled or outlined polygon on the output image.
func (ic *ImageCanvas) drawPolygon(output *image.RGBA, poly OverlayPolygon, col color.RGBA) {
	if len(poly.Points) < 3 {
		return
	}

	bounds := output.Bounds()

	// Scale points by zoom
	scaledPoints := make([]geometry.Point2D, len(poly.Points))
	var minX, minY, maxX, maxY float64
	minX, minY = poly.Points[0].X*ic.zoom, poly.Points[0].Y*ic.zoom
	maxX, maxY = minX, minY

	for i, p := range poly.Points {
		scaledPoints[i] = geometry.Point2D{X: p.X * ic.zoom, Y: p.Y * ic.zoom}
		if scaledPoints[i].X < minX {
			minX = scaledPoints[i].X
		}
		if scaledPoints[i].X > maxX {
			maxX = scaledPoints[i].X
		}
		if scaledPoints[i].Y < minY {
			minY = scaledPoints[i].Y
		}
		if scaledPoints[i].Y > maxY {
			maxY = scaledPoints[i].Y
		}
	}

	if poly.Filled {
		// Fill polygon using scanline algorithm
		for y := int(minY); y <= int(maxY); y++ {
			if y < bounds.Min.Y || y >= bounds.Max.Y {
				continue
			}

			// Find all x intersections with polygon edges at this y
			var xIntersections []float64
			n := len(scaledPoints)
			for i := 0; i < n; i++ {
				p1 := scaledPoints[i]
				p2 := scaledPoints[(i+1)%n]

				// Check if edge crosses this scanline
				if (p1.Y <= float64(y) && p2.Y > float64(y)) ||
					(p2.Y <= float64(y) && p1.Y > float64(y)) {
					// Calculate x intersection
					t := (float64(y) - p1.Y) / (p2.Y - p1.Y)
					xInt := p1.X + t*(p2.X-p1.X)
					xIntersections = append(xIntersections, xInt)
				}
			}

			// Sort intersections
			for i := 0; i < len(xIntersections)-1; i++ {
				for j := i + 1; j < len(xIntersections); j++ {
					if xIntersections[j] < xIntersections[i] {
						xIntersections[i], xIntersections[j] = xIntersections[j], xIntersections[i]
					}
				}
			}

			// Fill between pairs of intersections
			for i := 0; i+1 < len(xIntersections); i += 2 {
				x1 := int(xIntersections[i])
				x2 := int(xIntersections[i+1])
				for x := x1; x <= x2; x++ {
					if x >= bounds.Min.X && x < bounds.Max.X {
						output.Set(x, y, col)
					}
				}
			}
		}
	}

	// Draw polygon outline (always, thicker for filled)
	thickness := 2
	if poly.Filled {
		thickness = 3
	}
	n := len(scaledPoints)
	for i := 0; i < n; i++ {
		p1 := scaledPoints[i]
		p2 := scaledPoints[(i+1)%n]
		ic.drawLine(output, int(p1.X), int(p1.Y), int(p2.X), int(p2.Y), col, thickness)
	}

	// Draw label if present
	if poly.Label != "" {
		// Calculate centroid for label placement
		var sumX, sumY float64
		for _, p := range scaledPoints {
			sumX += p.X
			sumY += p.Y
		}
		centerX := int(sumX / float64(len(scaledPoints)))
		centerY := int(sumY / float64(len(scaledPoints)))

		// Create a bounding box for the label
		labelSize := 20 * int(ic.zoom)
		if labelSize < 10 {
			labelSize = 10
		}
		ic.drawLabel(output, poly.Label, centerX-labelSize, centerY-labelSize/2,
			centerX+labelSize, centerY+labelSize/2, col)
	}
}

// drawLine draws a line between two points using Bresenham's algorithm.
func (ic *ImageCanvas) drawLine(output *image.RGBA, x1, y1, x2, y2 int, col color.RGBA, thickness int) {
	bounds := output.Bounds()

	dx := x2 - x1
	dy := y2 - y1
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}

	sx := 1
	if x1 > x2 {
		sx = -1
	}
	sy := 1
	if y1 > y2 {
		sy = -1
	}

	err := dx - dy

	for {
		// Draw thick point
		for t := -thickness / 2; t <= thickness/2; t++ {
			for s := -thickness / 2; s <= thickness/2; s++ {
				px, py := x1+s, y1+t
				if px >= bounds.Min.X && px < bounds.Max.X && py >= bounds.Min.Y && py < bounds.Max.Y {
					output.Set(px, py, col)
				}
			}
		}

		if x1 == x2 && y1 == y2 {
			break
		}

		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x1 += sx
		}
		if e2 < dx {
			err += dx
			y1 += sy
		}
	}
}

// drawFillPattern fills a rectangle with the specified pattern.
func (ic *ImageCanvas) drawFillPattern(output *image.RGBA, x1, y1, x2, y2 int, col color.RGBA, pattern FillPattern, interval int) {
	bounds := output.Bounds()

	switch pattern {
	case FillSolid:
		// Fill entire rectangle
		for y := y1; y <= y2; y++ {
			for x := x1; x <= x2; x++ {
				if x >= bounds.Min.X && x < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
					output.Set(x, y, col)
				}
			}
		}

	case FillStripe:
		// Diagonal stripes (top-left to bottom-right)
		// A pixel is on the stripe if (x + y) mod interval < lineWidth
		lineWidth := interval / 4
		if lineWidth < 1 {
			lineWidth = 1
		}
		for y := y1; y <= y2; y++ {
			for x := x1; x <= x2; x++ {
				if ((x + y) % interval) < lineWidth {
					if x >= bounds.Min.X && x < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
						output.Set(x, y, col)
					}
				}
			}
		}

	case FillCrosshatch:
		// Diagonal crosshatch (both directions)
		lineWidth := interval / 4
		if lineWidth < 1 {
			lineWidth = 1
		}
		for y := y1; y <= y2; y++ {
			for x := x1; x <= x2; x++ {
				// Stripe in one direction OR stripe in other direction
				stripe1 := ((x + y) % interval) < lineWidth
				stripe2 := ((x - y + 10000*interval) % interval) < lineWidth // +10000*interval to keep positive
				if stripe1 || stripe2 {
					if x >= bounds.Min.X && x < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
						output.Set(x, y, col)
					}
				}
			}
		}

	case FillTarget:
		// Crosshairs through center (target marker)
		centerX := (x1 + x2) / 2
		centerY := (y1 + y2) / 2
		lineWidth := 2
		if ic.zoom > 1 {
			lineWidth = int(2 * ic.zoom)
		}

		// Horizontal line through center
		for x := x1; x <= x2; x++ {
			for t := -lineWidth / 2; t <= lineWidth/2; t++ {
				py := centerY + t
				if x >= bounds.Min.X && x < bounds.Max.X && py >= bounds.Min.Y && py < bounds.Max.Y {
					output.Set(x, py, col)
				}
			}
		}

		// Vertical line through center
		for y := y1; y <= y2; y++ {
			for t := -lineWidth / 2; t <= lineWidth/2; t++ {
				px := centerX + t
				if px >= bounds.Min.X && px < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
					output.Set(px, y, col)
				}
			}
		}
	}
}

// drawLabel draws a centered label inside a rectangle.
func (ic *ImageCanvas) drawLabel(output *image.RGBA, label string, x1, y1, x2, y2 int, col color.RGBA) {
	// Calculate scale based on zoom (base scale is 2 pixels per font pixel at zoom 1.0)
	scale := int(ic.zoom * 2)
	if scale < 1 {
		scale = 1
	}
	if scale > 6 {
		scale = 6
	}

	// Calculate total width of label (3 pixels per digit + 1 pixel spacing)
	charWidth := 3 * scale
	charHeight := 5 * scale
	spacing := scale
	labelWidth := len(label)*charWidth + (len(label)-1)*spacing

	// Calculate center position
	centerX := (x1 + x2) / 2
	centerY := (y1 + y2) / 2

	// Start position for first character
	startX := centerX - labelWidth/2
	startY := centerY - charHeight/2

	bounds := output.Bounds()

	// Draw each character
	for i, ch := range label {
		if ch < '0' || ch > '9' {
			continue
		}
		digit := int(ch - '0')
		pattern := digitPatterns[digit]

		charX := startX + i*(charWidth+spacing)

		// Draw the digit pattern
		for row := 0; row < 5; row++ {
			for col := 0; col < 3; col++ {
				if (pattern[row] & (1 << (2 - col))) != 0 {
					// Draw a scaled pixel block
					for dy := 0; dy < scale; dy++ {
						for dx := 0; dx < scale; dx++ {
							px := charX + col*scale + dx
							py := startY + row*scale + dy
							if px >= bounds.Min.X && px < bounds.Max.X &&
								py >= bounds.Min.Y && py < bounds.Max.Y {
								output.Set(px, py, color.RGBA{R: 255, G: 255, B: 255, A: 255})
							}
						}
					}
				}
			}
		}
	}
}

// compositeLayer draws a single layer onto the output with opacity.
func (ic *ImageCanvas) compositeLayer(output *image.RGBA, layer *pcbimage.Layer, w, h int) {
	src := layer.Image
	srcBounds := src.Bounds()
	opacity := layer.Opacity

	for y := 0; y < h; y++ {
		srcY := int(float64(y)/ic.zoom) + srcBounds.Min.Y
		if srcY >= srcBounds.Max.Y {
			continue
		}

		for x := 0; x < w; x++ {
			srcX := int(float64(x)/ic.zoom) + srcBounds.Min.X
			if srcX >= srcBounds.Max.X {
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
