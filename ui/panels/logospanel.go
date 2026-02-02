// Package panels provides UI panels for the application.
package panels

import (
	"fmt"
	"image"
	"image/color"
	"time"

	"pcb-tracer/internal/app"
	"pcb-tracer/internal/logo"
	"pcb-tracer/pkg/geometry"
	"pcb-tracer/ui/canvas"

	"fyne.io/fyne/v2"
	fynecanvas "fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// LogosPanel displays and manages logo templates.
type LogosPanel struct {
	state     *app.State
	canvas    *canvas.ImageCanvas
	container fyne.CanvasObject
	window    fyne.Window

	list              *widget.List
	detailCard        *widget.Card
	rawImage          *fynecanvas.Image // Displays raw captured region (before quantization)
	quantizedImage    *fynecanvas.Image // Displays monochrome quantization result
	nameEntry         *widget.Entry
	manufacturerEntry *widget.Entry // Manufacturer ID entry for selected logo
	selectedIdx       int           // Currently selected logo index, -1 if none

	// Logo capture state
	capturing         bool             // true when waiting for middle-click
	captureSize       int              // Current capture size (pixels, square)
	bitmapResolution  int              // Resolution of the quantized bitmap (e.g., 128)
	captureBounds     geometry.RectInt // Last capture bounds for keyboard adjustment

	// Focus wrapper for keyboard events
	focusWrapper fyne.Focusable
}

// NewLogosPanel creates a new logos panel.
func NewLogosPanel(state *app.State, canv *canvas.ImageCanvas) *LogosPanel {
	lp := &LogosPanel{
		state:            state,
		canvas:           canv,
		selectedIdx:      -1,
		captureSize:      64,  // Default capture region size in source image pixels
		bitmapResolution: 128, // Default quantized bitmap resolution (independent of capture size)
	}

	// Logo list
	lp.list = widget.NewList(
		func() int {
			if state.LogoLibrary == nil {
				return 0
			}
			return len(state.LogoLibrary.Logos)
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("Logo Name")
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			label := obj.(*widget.Label)
			if state.LogoLibrary != nil && id < len(state.LogoLibrary.Logos) {
				logo := state.LogoLibrary.Logos[id]
				mfr := logo.ManufacturerID
				if mfr == "" {
					mfr = "-"
				}
				label.SetText(fmt.Sprintf("<%s> %s %dx%d", logo.Name, mfr, logo.Width, logo.Height))
			}
		},
	)

	lp.list.OnSelected = func(id widget.ListItemID) {
		lp.selectedIdx = int(id)
		lp.showLogoDetail(id)
		// Defer focus to wrapper so keyboard events work (list tries to keep focus)
		go func() {
			// Small delay to let the list and canvas finish their focus handling
			time.Sleep(50 * time.Millisecond)
			if lp.focusWrapper != nil {
				if c := fyne.CurrentApp().Driver().CanvasForObject(lp.list); c != nil {
					c.Focus(lp.focusWrapper)
				}
			}
		}()
	}

	// Name entry for new/edit
	lp.nameEntry = widget.NewEntry()
	lp.nameEntry.SetPlaceHolder("Logo name (e.g., ST, NS, TI)")

	// Raw image display (captured region before quantization)
	lp.rawImage = fynecanvas.NewImageFromImage(nil)
	lp.rawImage.FillMode = fynecanvas.ImageFillContain
	lp.rawImage.SetMinSize(fyne.NewSize(128, 128))

	// Quantized image display (monochrome quantization result)
	lp.quantizedImage = fynecanvas.NewImageFromImage(nil)
	lp.quantizedImage.FillMode = fynecanvas.ImageFillContain
	lp.quantizedImage.SetMinSize(fyne.NewSize(128, 128))

	// Capture button
	captureBtn := widget.NewButton("Capture Logo", func() {
		lp.startCapture()
	})

	// Size controls - capture region size in source image pixels
	sizeLabel := widget.NewLabel(fmt.Sprintf("Region: %dpx", lp.captureSize))
	sizeSlider := widget.NewSlider(16, 256)
	sizeSlider.Value = float64(lp.captureSize)
	sizeSlider.OnChanged = func(v float64) {
		lp.captureSize = int(v)
		sizeLabel.SetText(fmt.Sprintf("Region: %dpx", lp.captureSize))
	}

	// Resolution controls - quantized bitmap resolution (independent of capture size)
	resLabel := widget.NewLabel(fmt.Sprintf("Resolution: %d", lp.bitmapResolution))
	resSlider := widget.NewSlider(32, 256)
	resSlider.Value = float64(lp.bitmapResolution)
	resSlider.OnChanged = func(v float64) {
		lp.bitmapResolution = int(v)
		resLabel.SetText(fmt.Sprintf("Resolution: %d", lp.bitmapResolution))
	}

	// Delete button
	deleteBtn := widget.NewButton("Delete", func() {
		lp.deleteSelected()
	})

	// Save button
	saveBtn := widget.NewButton("Save Library", func() {
		if err := lp.state.SaveLogoLibrary(); err != nil {
			fmt.Printf("Error saving logo library: %v\n", err)
		} else {
			fmt.Println("Logo library saved")
		}
	})

	// Status label
	statusLabel := widget.NewLabel("Middle-click to capture. Arrow keys adjust, +/- resize.")

	// Controls card
	controlsCard := widget.NewCard("Capture", "",
		container.NewVBox(
			lp.nameEntry,
			container.NewHBox(sizeLabel, sizeSlider),
			container.NewHBox(resLabel, resSlider),
			container.NewHBox(captureBtn, deleteBtn, saveBtn),
			statusLabel,
		),
	)

	// Manufacturer ID entry for selected logo
	lp.manufacturerEntry = widget.NewEntry()
	lp.manufacturerEntry.SetPlaceHolder("Manufacturer (e.g., National Semiconductor)")
	lp.manufacturerEntry.OnChanged = func(text string) {
		lp.updateSelectedManufacturer(text)
	}

	// Detail card with two image previews side by side
	rawPreview := container.NewVBox(
		widget.NewLabel("Raw Capture:"),
		container.NewCenter(lp.rawImage),
	)
	quantizedPreview := container.NewVBox(
		widget.NewLabel("Quantized:"),
		container.NewCenter(lp.quantizedImage),
	)
	imageRow := container.NewHBox(rawPreview, quantizedPreview)
	detailContent := container.NewVBox(
		imageRow,
		widget.NewLabel("Manufacturer:"),
		lp.manufacturerEntry,
	)
	lp.detailCard = widget.NewCard("Selected Logo", "", detailContent)

	// Wrap list in a scroll container with fixed height
	listScroll := container.NewVScroll(lp.list)
	listScroll.SetMinSize(fyne.NewSize(0, 150))

	// Create focusable wrapper with key handler
	focusWrapper := newFocusableContainer(listScroll, lp.onKeyPressed)
	lp.focusWrapper = focusWrapper

	// Layout
	lp.container = container.NewBorder(
		controlsCard,
		lp.detailCard,
		nil, nil,
		focusWrapper,
	)

	return lp
}

// Container returns the panel container.
func (lp *LogosPanel) Container() fyne.CanvasObject {
	return lp.container
}

// SetWindow sets the parent window for dialogs.
func (lp *LogosPanel) SetWindow(w fyne.Window) {
	lp.window = w
}

// startCapture initiates logo capture mode.
func (lp *LogosPanel) startCapture() {
	name := lp.nameEntry.Text
	if name == "" {
		if lp.window != nil {
			dialog.ShowInformation("Name Required", "Please enter a logo name first", lp.window)
		}
		return
	}

	lp.capturing = true
	fmt.Printf("Logo capture started: middle-click on a logo (name=%s, size=%d)\n", name, lp.captureSize)
}

// OnMiddleClick handles middle-click for logo capture.
// Called from main app when logos panel is active.
func (lp *LogosPanel) OnMiddleClick(x, y float64) {
	if !lp.capturing {
		return
	}

	name := lp.nameEntry.Text
	if name == "" {
		return
	}

	// Get the rendered canvas output
	img := lp.canvas.GetRenderedOutput()
	if img == nil {
		fmt.Println("No image available for logo capture")
		return
	}

	// Center the capture region on the click point
	halfSize := lp.captureSize / 2
	bounds := geometry.RectInt{
		X:      int(x) - halfSize,
		Y:      int(y) - halfSize,
		Width:  lp.captureSize,
		Height: lp.captureSize,
	}

	// Clamp to image bounds
	imgBounds := img.Bounds()
	imgW := imgBounds.Max.X - imgBounds.Min.X
	imgH := imgBounds.Max.Y - imgBounds.Min.Y

	// Check if click is outside image bounds entirely
	if int(x) < imgBounds.Min.X || int(x) >= imgBounds.Max.X ||
		int(y) < imgBounds.Min.Y || int(y) >= imgBounds.Max.Y {
		fmt.Printf("Logo capture: click position (%.0f,%.0f) outside image bounds (%dx%d)\n",
			x, y, imgW, imgH)
		return
	}

	if bounds.X < imgBounds.Min.X {
		bounds.X = imgBounds.Min.X
	}
	if bounds.Y < imgBounds.Min.Y {
		bounds.Y = imgBounds.Min.Y
	}
	if bounds.X+bounds.Width > imgBounds.Max.X {
		bounds.Width = imgBounds.Max.X - bounds.X
	}
	if bounds.Y+bounds.Height > imgBounds.Max.Y {
		bounds.Height = imgBounds.Max.Y - bounds.Y
	}

	// Ensure minimum capture size
	const minCaptureSize = 8
	if bounds.Width < minCaptureSize || bounds.Height < minCaptureSize {
		fmt.Printf("Logo capture: region too small (%dx%d) after clamping to image bounds\n",
			bounds.Width, bounds.Height)
		return
	}

	// Store bounds for keyboard adjustment (in canvas coordinates)
	lp.captureBounds = bounds

	// Convert bounds to source image coordinates for storage (zoom-independent)
	zoom := lp.canvas.GetZoom()
	sourceBounds := geometry.RectInt{
		X:      int(float64(bounds.X) / zoom),
		Y:      int(float64(bounds.Y) / zoom),
		Width:  int(float64(bounds.Width) / zoom),
		Height: int(float64(bounds.Height) / zoom),
	}

	// Create logo from region (use bitmapResolution for quantization, not captureSize)
	// Pass canvas bounds for quantization, but store source bounds in the logo
	newLogo := logo.NewLogo(name, img, bounds, lp.bitmapResolution)
	if newLogo != nil {
		// Override bounds with source coordinates for zoom-independent storage
		newLogo.Bounds = sourceBounds
	}
	if newLogo == nil {
		fmt.Println("Failed to create logo")
		return
	}

	// Add to library and save to preferences
	lp.state.LogoLibrary.Add(newLogo)
	if err := lp.state.SaveLogoLibrary(); err != nil {
		fmt.Printf("Warning: could not save logo library: %v\n", err)
	}

	fmt.Printf("Captured logo <%s> at (%d,%d) %dx%d -> %dx%d quantized\n",
		name, bounds.X, bounds.Y, bounds.Width, bounds.Height,
		newLogo.Width, newLogo.Height)

	// Update UI
	lp.list.Refresh()
	lp.capturing = false
	lp.nameEntry.SetText("")

	// Select the newly added logo to populate the detail panels
	// Find by name since library is sorted
	newIdx := 0
	for i, l := range lp.state.LogoLibrary.Logos {
		if l.Name == name {
			newIdx = i
			break
		}
	}
	lp.list.Select(newIdx)
}

// showCaptureOverlay displays the capture region on the canvas.
func (lp *LogosPanel) showCaptureOverlay(bounds geometry.RectInt) {
	zoom := lp.canvas.GetZoom()
	lp.canvas.SetOverlay("logo_capture", &canvas.Overlay{
		Rectangles: []canvas.OverlayRect{{
			X:      int(float64(bounds.X) / zoom),
			Y:      int(float64(bounds.Y) / zoom),
			Width:  int(float64(bounds.Width) / zoom),
			Height: int(float64(bounds.Height) / zoom),
		}},
		Color: color.RGBA{R: 255, G: 0, B: 255, A: 200}, // Magenta
	})
}

// showLogoDetail displays both raw captured region and quantized image.
func (lp *LogosPanel) showLogoDetail(idx int) {
	if lp.state.LogoLibrary == nil || idx < 0 || idx >= len(lp.state.LogoLibrary.Logos) {
		return
	}

	l := lp.state.LogoLibrary.Logos[idx]

	// The bounds are stored in source image coordinates (zoom-independent).
	// Convert to canvas coordinates for extraction and overlay display.
	zoom := lp.canvas.GetZoom()
	canvasBounds := geometry.RectInt{
		X:      int(float64(l.Bounds.X) * zoom),
		Y:      int(float64(l.Bounds.Y) * zoom),
		Width:  int(float64(l.Bounds.Width) * zoom),
		Height: int(float64(l.Bounds.Height) * zoom),
	}

	// Get raw image from canvas to show the captured region
	canvasImg := lp.canvas.GetRenderedOutput()
	if canvasImg != nil {
		rawRegion := lp.extractRawRegion(canvasImg, canvasBounds)
		if rawRegion != nil {
			lp.rawImage.Image = rawRegion
			lp.rawImage.Refresh()
		} else {
			// Clear the raw image if extraction failed
			lp.rawImage.Image = nil
			lp.rawImage.Refresh()
		}
	}

	// Create scaled quantized image for display
	scaled := l.ToScaledImage(1) // 1:1 scale, image is already at quantized size
	if scaled == nil {
		return
	}

	// Convert to RGBA for fyne (show black/white quantization)
	rgba := image.NewRGBA(scaled.Bounds())
	for y := 0; y < scaled.Bounds().Dy(); y++ {
		for x := 0; x < scaled.Bounds().Dx(); x++ {
			gray := scaled.GrayAt(x, y)
			rgba.Set(x, y, color.RGBA{R: gray.Y, G: gray.Y, B: gray.Y, A: 255})
		}
	}

	lp.quantizedImage.Image = rgba
	lp.quantizedImage.Refresh()

	lp.detailCard.SetTitle(fmt.Sprintf("<%s>", l.Name))
	lp.detailCard.SetSubTitle(fmt.Sprintf("Bitmap: %dx%d, Source: %dx%d @ (%d,%d)",
		l.Width, l.Height, l.Bounds.Width, l.Bounds.Height, l.Bounds.X, l.Bounds.Y))

	// Populate manufacturer entry
	lp.manufacturerEntry.SetText(l.ManufacturerID)

	// Show overlay on canvas for selected logo (using canvas bounds computed above)
	lp.showCaptureOverlay(canvasBounds)

	// Scroll canvas to show the logo region (use source bounds)
	lp.canvas.ScrollToRegion(l.Bounds.X, l.Bounds.Y, l.Bounds.Width, l.Bounds.Height)
}

// extractRawRegion extracts a region from the canvas image without any processing.
func (lp *LogosPanel) extractRawRegion(img *image.RGBA, bounds geometry.RectInt) *image.RGBA {
	// Clamp bounds to image
	imgBounds := img.Bounds()
	x0 := bounds.X
	y0 := bounds.Y
	x1 := bounds.X + bounds.Width
	y1 := bounds.Y + bounds.Height

	if x0 < imgBounds.Min.X {
		x0 = imgBounds.Min.X
	}
	if y0 < imgBounds.Min.Y {
		y0 = imgBounds.Min.Y
	}
	if x1 > imgBounds.Max.X {
		x1 = imgBounds.Max.X
	}
	if y1 > imgBounds.Max.Y {
		y1 = imgBounds.Max.Y
	}

	w := x1 - x0
	h := y1 - y0
	if w <= 0 || h <= 0 {
		return nil
	}

	// Copy raw pixel data
	result := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			result.Set(x, y, img.At(x0+x, y0+y))
		}
	}

	return result
}

// updateSelectedManufacturer updates the manufacturer ID for the selected logo.
func (lp *LogosPanel) updateSelectedManufacturer(manufacturer string) {
	if lp.selectedIdx < 0 || lp.state.LogoLibrary == nil {
		return
	}
	if lp.selectedIdx >= len(lp.state.LogoLibrary.Logos) {
		return
	}

	l := lp.state.LogoLibrary.Logos[lp.selectedIdx]
	if l.ManufacturerID == manufacturer {
		return // No change
	}

	l.ManufacturerID = manufacturer

	// Save to preferences
	if err := lp.state.SaveLogoLibrary(); err != nil {
		fmt.Printf("Warning: could not save logo library: %v\n", err)
	}

	fmt.Printf("Updated logo <%s> manufacturer to %q\n", l.Name, manufacturer)
}

// deleteSelected removes the selected logo from the library.
func (lp *LogosPanel) deleteSelected() {
	if lp.selectedIdx < 0 || lp.state.LogoLibrary == nil {
		return
	}

	if lp.selectedIdx >= len(lp.state.LogoLibrary.Logos) {
		return
	}

	name := lp.state.LogoLibrary.Logos[lp.selectedIdx].Name

	if lp.window != nil {
		dialog.ShowConfirm("Delete Logo",
			fmt.Sprintf("Delete logo <%s>?", name),
			func(confirmed bool) {
				if confirmed {
					lp.state.LogoLibrary.Remove(name)
					if err := lp.state.SaveLogoLibrary(); err != nil {
						fmt.Printf("Warning: could not save logo library: %v\n", err)
					}
					lp.selectedIdx = -1
					lp.list.UnselectAll()
					lp.list.Refresh()
					lp.rawImage.Image = nil
					lp.rawImage.Refresh()
					lp.quantizedImage.Image = nil
					lp.quantizedImage.Refresh()
					lp.detailCard.SetTitle("Selected Logo")
					lp.detailCard.SetSubTitle("")
					// Clear the selection overlay
					lp.canvas.ClearOverlay("logo_capture")
					fmt.Printf("Deleted logo <%s>\n", name)
				}
			},
			lp.window)
	}
}

// Refresh refreshes the panel display.
func (lp *LogosPanel) Refresh() {
	lp.list.Refresh()
}

// IsCapturing returns true if logo capture mode is active.
func (lp *LogosPanel) IsCapturing() bool {
	return lp.capturing
}

// AdjustCaptureBounds adjusts the last capture bounds and re-captures.
// dx, dy are pixel offsets in source image coordinates.
func (lp *LogosPanel) AdjustCaptureBounds(dx, dy int) {
	if lp.selectedIdx < 0 || lp.state.LogoLibrary == nil {
		return
	}

	if lp.selectedIdx >= len(lp.state.LogoLibrary.Logos) {
		return
	}

	l := lp.state.LogoLibrary.Logos[lp.selectedIdx]

	// Adjust the source bounds (in source image coordinates)
	l.Bounds.X += dx
	l.Bounds.Y += dy

	// Re-quantize from the adjusted bounds
	img := lp.canvas.GetRenderedOutput()
	if img == nil {
		return
	}

	// Convert source bounds to canvas coordinates for NewLogo
	zoom := lp.canvas.GetZoom()
	canvasBounds := geometry.RectInt{
		X:      int(float64(l.Bounds.X) * zoom),
		Y:      int(float64(l.Bounds.Y) * zoom),
		Width:  int(float64(l.Bounds.Width) * zoom),
		Height: int(float64(l.Bounds.Height) * zoom),
	}

	// Use stored quantized size, or default if not set
	qSize := l.QuantizedSize
	if qSize < 8 {
		qSize = 64 // Default for legacy logos
	}

	// Create new quantized version using canvas coordinates
	newLogo := logo.NewLogo(l.Name, img, canvasBounds, qSize)
	if newLogo == nil {
		return
	}

	// Update the logo in place
	l.Bits = newLogo.Bits
	l.Width = newLogo.Width
	l.Height = newLogo.Height

	// Save to preferences
	if err := lp.state.SaveLogoLibrary(); err != nil {
		fmt.Printf("Warning: could not save logo library: %v\n", err)
	}

	// showLogoDetail will handle overlay display
	lp.showLogoDetail(lp.selectedIdx)

	fmt.Printf("Adjusted logo <%s> to (%d,%d)\n", l.Name, l.Bounds.X, l.Bounds.Y)
}

// ResizeCapture changes the capture size and re-captures.
// delta is in source image pixels.
func (lp *LogosPanel) ResizeCapture(delta int) {
	if lp.selectedIdx < 0 || lp.state.LogoLibrary == nil {
		fmt.Println("ResizeCapture: no selection or library")
		return
	}

	if lp.selectedIdx >= len(lp.state.LogoLibrary.Logos) {
		fmt.Println("ResizeCapture: selection out of range")
		return
	}

	l := lp.state.LogoLibrary.Logos[lp.selectedIdx]

	// Adjust the bounds size in source coordinates (keep center fixed)
	oldCenterX := l.Bounds.X + l.Bounds.Width/2
	oldCenterY := l.Bounds.Y + l.Bounds.Height/2

	l.Bounds.Width += delta
	l.Bounds.Height += delta

	if l.Bounds.Width < 8 {
		l.Bounds.Width = 8
	}
	if l.Bounds.Height < 8 {
		l.Bounds.Height = 8
	}

	// Re-center
	l.Bounds.X = oldCenterX - l.Bounds.Width/2
	l.Bounds.Y = oldCenterY - l.Bounds.Height/2

	// Re-quantize from canvas image
	img := lp.canvas.GetRenderedOutput()
	if img == nil {
		fmt.Println("ResizeCapture: GetRenderedOutput returned nil")
		return
	}

	// Convert source bounds to canvas coordinates for NewLogo
	zoom := lp.canvas.GetZoom()
	canvasBounds := geometry.RectInt{
		X:      int(float64(l.Bounds.X) * zoom),
		Y:      int(float64(l.Bounds.Y) * zoom),
		Width:  int(float64(l.Bounds.Width) * zoom),
		Height: int(float64(l.Bounds.Height) * zoom),
	}

	fmt.Printf("ResizeCapture: re-reading from image %dx%d, canvas bounds (%d,%d) %dx%d\n",
		img.Bounds().Dx(), img.Bounds().Dy(),
		canvasBounds.X, canvasBounds.Y, canvasBounds.Width, canvasBounds.Height)

	// Use stored quantized size, or default if not set
	qSize := l.QuantizedSize
	if qSize < 8 {
		qSize = 64 // Default for legacy logos
	}

	newLogo := logo.NewLogo(l.Name, img, canvasBounds, qSize)
	if newLogo == nil {
		fmt.Println("ResizeCapture: NewLogo returned nil")
		return
	}

	// Update logo in place with fresh data from image
	l.Bits = newLogo.Bits
	l.Width = newLogo.Width
	l.Height = newLogo.Height

	fmt.Printf("ResizeCapture: new bitmap %dx%d (%d bytes)\n", l.Width, l.Height, len(l.Bits))

	// Save to preferences
	if err := lp.state.SaveLogoLibrary(); err != nil {
		fmt.Printf("Warning: could not save logo library: %v\n", err)
	}

	// showLogoDetail will handle overlay display
	lp.showLogoDetail(lp.selectedIdx)

	fmt.Printf("Resized logo <%s> to source %dx%d -> bitmap %dx%d\n",
		l.Name, l.Bounds.Width, l.Bounds.Height, l.Width, l.Height)
}

// onKeyPressed handles keyboard events for logo editing.
// Arrow keys adjust position, +/- adjust size.
func (lp *LogosPanel) onKeyPressed(ev *fyne.KeyEvent) {
	fmt.Printf("LogosPanel.onKeyPressed: key=%s selectedIdx=%d\n", ev.Name, lp.selectedIdx)

	if lp.selectedIdx < 0 || lp.state.LogoLibrary == nil {
		fmt.Println("  -> no selection or no library")
		return
	}
	if lp.selectedIdx >= len(lp.state.LogoLibrary.Logos) {
		fmt.Println("  -> selection out of range")
		return
	}

	// Use 1 pixel steps for fine adjustment
	step := 1

	switch ev.Name {
	case fyne.KeyUp:
		lp.AdjustCaptureBounds(0, -step)
	case fyne.KeyDown:
		lp.AdjustCaptureBounds(0, step)
	case fyne.KeyLeft:
		lp.AdjustCaptureBounds(-step, 0)
	case fyne.KeyRight:
		lp.AdjustCaptureBounds(step, 0)
	case "Plus":
		lp.ResizeCapture(2) // Grow by 2 pixels
	case "Minus":
		lp.ResizeCapture(-2) // Shrink by 2 pixels
	default:
		fmt.Printf("  -> unhandled key: %s\n", ev.Name)
		return
	}
}
