package panels

import (
	"fmt"
	"image"
	"image/color"

	"pcb-tracer/internal/app"
	"pcb-tracer/internal/logo"
	"pcb-tracer/pkg/geometry"
	"pcb-tracer/ui/canvas"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/gtk"
)

// LogosPanel displays and manages logo templates.
type LogosPanel struct {
	state  *app.State
	canvas *canvas.ImageCanvas
	win    *gtk.Window
	box    *gtk.Box

	listBox          *gtk.ListBox
	detailFrame      *gtk.Frame
	nameEntry        *gtk.Entry
	manufacturerEntry *gtk.Entry
	selectedIdx      int

	rawImage      *gtk.Image
	quantizedImage *gtk.Image

	// Logo capture state
	capturing        bool
	captureSize      int
	bitmapResolution int
	captureBounds    geometry.RectInt

	// Size/resolution controls
	sizeLabel *gtk.Label
	resLabel  *gtk.Label
}

// NewLogosPanel creates a new logos panel.
func NewLogosPanel(state *app.State, cvs *canvas.ImageCanvas, win *gtk.Window) *LogosPanel {
	lp := &LogosPanel{
		state:            state,
		canvas:           cvs,
		win:              win,
		selectedIdx:      -1,
		captureSize:      64,
		bitmapResolution: 128,
	}

	lp.box, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4)
	lp.box.SetMarginStart(4)
	lp.box.SetMarginEnd(4)
	lp.box.SetMarginTop(4)
	lp.box.SetMarginBottom(4)

	// --- Capture Controls ---
	captureFrame, _ := gtk.FrameNew("Capture")
	captureBox, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4)
	captureBox.SetMarginStart(4)
	captureBox.SetMarginEnd(4)
	captureBox.SetMarginTop(4)
	captureBox.SetMarginBottom(4)

	lp.nameEntry, _ = gtk.EntryNew()
	lp.nameEntry.SetPlaceholderText("Logo name (e.g., ST, NS, TI)")
	captureBox.PackStart(lp.nameEntry, false, false, 0)

	// Size slider
	sizeRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	lp.sizeLabel, _ = gtk.LabelNew(fmt.Sprintf("Region: %dpx", lp.captureSize))
	sizeSlider, _ := gtk.ScaleNewWithRange(gtk.ORIENTATION_HORIZONTAL, 16, 256, 1)
	sizeSlider.SetValue(float64(lp.captureSize))
	sizeSlider.SetDrawValue(false)
	sizeSlider.Connect("value-changed", func() {
		lp.captureSize = int(sizeSlider.GetValue())
		lp.sizeLabel.SetText(fmt.Sprintf("Region: %dpx", lp.captureSize))
	})
	sizeRow.PackStart(lp.sizeLabel, false, false, 0)
	sizeRow.PackStart(sizeSlider, true, true, 0)
	captureBox.PackStart(sizeRow, false, false, 0)

	// Resolution slider
	resRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	lp.resLabel, _ = gtk.LabelNew(fmt.Sprintf("Resolution: %d", lp.bitmapResolution))
	resSlider, _ := gtk.ScaleNewWithRange(gtk.ORIENTATION_HORIZONTAL, 32, 256, 1)
	resSlider.SetValue(float64(lp.bitmapResolution))
	resSlider.SetDrawValue(false)
	resSlider.Connect("value-changed", func() {
		lp.bitmapResolution = int(resSlider.GetValue())
		lp.resLabel.SetText(fmt.Sprintf("Resolution: %d", lp.bitmapResolution))
	})
	resRow.PackStart(lp.resLabel, false, false, 0)
	resRow.PackStart(resSlider, true, true, 0)
	captureBox.PackStart(resRow, false, false, 0)

	// Buttons
	btnRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	captureBtn, _ := gtk.ButtonNewWithLabel("Capture Logo")
	captureBtn.Connect("clicked", func() { lp.startCapture() })
	deleteBtn, _ := gtk.ButtonNewWithLabel("Delete")
	deleteBtn.Connect("clicked", func() { lp.deleteSelected() })
	saveBtn, _ := gtk.ButtonNewWithLabel("Save Library")
	saveBtn.Connect("clicked", func() {
		if err := lp.state.SaveLogoLibrary(); err != nil {
			fmt.Printf("Error saving logo library: %v\n", err)
		} else {
			fmt.Println("Logo library saved")
		}
	})
	btnRow.PackStart(captureBtn, false, false, 0)
	btnRow.PackStart(deleteBtn, false, false, 0)
	btnRow.PackStart(saveBtn, false, false, 0)
	captureBox.PackStart(btnRow, false, false, 0)

	statusLabel, _ := gtk.LabelNew("Middle-click to capture. Arrow keys adjust, +/- resize.")
	statusLabel.SetHAlign(gtk.ALIGN_START)
	statusLabel.SetLineWrap(true)
	captureBox.PackStart(statusLabel, false, false, 0)

	captureFrame.Add(captureBox)
	lp.box.PackStart(captureFrame, false, false, 0)

	// --- Logo List ---
	listScroll, _ := gtk.ScrolledWindowNew(nil, nil)
	listScroll.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)
	listScroll.SetSizeRequest(-1, 150)

	lp.listBox, _ = gtk.ListBoxNew()
	lp.listBox.Connect("row-activated", func(lb *gtk.ListBox, row *gtk.ListBoxRow) {
		if row == nil {
			return
		}
		idx := row.GetIndex()
		lp.selectedIdx = idx
		lp.showLogoDetail(idx)
	})
	listScroll.Add(lp.listBox)
	lp.box.PackStart(listScroll, true, true, 0)

	// --- Detail Panel ---
	lp.detailFrame, _ = gtk.FrameNew("Selected Logo")
	detailBox, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4)
	detailBox.SetMarginStart(4)
	detailBox.SetMarginEnd(4)
	detailBox.SetMarginTop(4)
	detailBox.SetMarginBottom(4)

	// Image previews side by side
	imgRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 8)

	rawBox, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 2)
	rawLabel, _ := gtk.LabelNew("Raw Capture:")
	lp.rawImage, _ = gtk.ImageNew()
	rawBox.PackStart(rawLabel, false, false, 0)
	rawBox.PackStart(lp.rawImage, false, false, 0)

	quantBox, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 2)
	quantLabel, _ := gtk.LabelNew("Quantized:")
	lp.quantizedImage, _ = gtk.ImageNew()
	quantBox.PackStart(quantLabel, false, false, 0)
	quantBox.PackStart(lp.quantizedImage, false, false, 0)

	imgRow.PackStart(rawBox, true, true, 0)
	imgRow.PackStart(quantBox, true, true, 0)
	detailBox.PackStart(imgRow, false, false, 0)

	mfrLabel, _ := gtk.LabelNew("Manufacturer:")
	mfrLabel.SetHAlign(gtk.ALIGN_START)
	detailBox.PackStart(mfrLabel, false, false, 0)

	lp.manufacturerEntry, _ = gtk.EntryNew()
	lp.manufacturerEntry.SetPlaceholderText("Manufacturer (e.g., National Semiconductor)")
	lp.manufacturerEntry.Connect("changed", func() {
		t, _ := lp.manufacturerEntry.GetText()
		lp.updateSelectedManufacturer(t)
	})
	detailBox.PackStart(lp.manufacturerEntry, false, false, 0)

	lp.detailFrame.Add(detailBox)
	lp.box.PackStart(lp.detailFrame, false, false, 0)

	lp.refreshList()

	return lp
}

// Widget returns the panel widget for embedding.
func (lp *LogosPanel) Widget() gtk.IWidget {
	return lp.box
}

// OnKeyPressed handles keyboard events for logo editing.
func (lp *LogosPanel) OnKeyPressed(ev *gdk.EventKey) bool {
	if lp.selectedIdx < 0 || lp.state.LogoLibrary == nil {
		return false
	}
	if lp.selectedIdx >= len(lp.state.LogoLibrary.Logos) {
		return false
	}

	step := 1
	keyval := ev.KeyVal()
	switch keyval {
	case gdk.KEY_Up:
		lp.AdjustCaptureBounds(0, -step)
	case gdk.KEY_Down:
		lp.AdjustCaptureBounds(0, step)
	case gdk.KEY_Left:
		lp.AdjustCaptureBounds(-step, 0)
	case gdk.KEY_Right:
		lp.AdjustCaptureBounds(step, 0)
	case gdk.KEY_plus, gdk.KEY_equal, gdk.KEY_KP_Add:
		lp.ResizeCapture(2)
	case gdk.KEY_minus, gdk.KEY_KP_Subtract:
		lp.ResizeCapture(-2)
	default:
		return false
	}
	return true
}

// refreshList rebuilds the list from the logo library.
func (lp *LogosPanel) refreshList() {
	children := lp.listBox.GetChildren()
	children.Foreach(func(item interface{}) {
		w := item.(*gtk.Widget)
		lp.listBox.Remove(w)
	})

	if lp.state.LogoLibrary == nil {
		return
	}

	for _, l := range lp.state.LogoLibrary.Logos {
		mfr := l.ManufacturerID
		if mfr == "" {
			mfr = "-"
		}
		text := fmt.Sprintf("<%s> %s %dx%d", l.Name, mfr, l.Width, l.Height)
		label, _ := gtk.LabelNew(text)
		label.SetHAlign(gtk.ALIGN_START)
		lp.listBox.Add(label)
	}
	lp.listBox.ShowAll()
}

// startCapture initiates logo capture mode.
func (lp *LogosPanel) startCapture() {
	name, _ := lp.nameEntry.GetText()
	if name == "" {
		fmt.Println("Logo capture: please enter a logo name first")
		return
	}

	lp.capturing = true
	fmt.Printf("Logo capture started: middle-click on a logo (name=%s, size=%d)\n", name, lp.captureSize)
}

// OnMiddleClick handles middle-click for logo capture.
func (lp *LogosPanel) OnMiddleClick(x, y float64) {
	if !lp.capturing {
		return
	}

	name, _ := lp.nameEntry.GetText()
	if name == "" {
		return
	}

	img := lp.canvas.GetRenderedOutput()
	if img == nil {
		fmt.Println("No image available for logo capture")
		return
	}

	halfSize := lp.captureSize / 2
	bounds := geometry.RectInt{
		X:      int(x) - halfSize,
		Y:      int(y) - halfSize,
		Width:  lp.captureSize,
		Height: lp.captureSize,
	}

	imgBounds := img.Bounds()
	if int(x) < imgBounds.Min.X || int(x) >= imgBounds.Max.X ||
		int(y) < imgBounds.Min.Y || int(y) >= imgBounds.Max.Y {
		fmt.Printf("Logo capture: click position (%.0f,%.0f) outside image bounds\n", x, y)
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

	if bounds.Width < 8 || bounds.Height < 8 {
		fmt.Printf("Logo capture: region too small (%dx%d)\n", bounds.Width, bounds.Height)
		return
	}

	lp.captureBounds = bounds

	zoom := lp.canvas.GetZoom()
	sourceBounds := geometry.RectInt{
		X:      int(float64(bounds.X) / zoom),
		Y:      int(float64(bounds.Y) / zoom),
		Width:  int(float64(bounds.Width) / zoom),
		Height: int(float64(bounds.Height) / zoom),
	}

	newLogo := logo.NewLogo(name, img, bounds, lp.bitmapResolution)
	if newLogo != nil {
		newLogo.Bounds = sourceBounds
	}
	if newLogo == nil {
		fmt.Println("Failed to create logo")
		return
	}

	lp.state.LogoLibrary.Add(newLogo)
	if err := lp.state.SaveLogoLibrary(); err != nil {
		fmt.Printf("Warning: could not save logo library: %v\n", err)
	}

	fmt.Printf("Captured logo <%s> at (%d,%d) %dx%d -> %dx%d quantized\n",
		name, bounds.X, bounds.Y, bounds.Width, bounds.Height,
		newLogo.Width, newLogo.Height)

	lp.refreshList()
	lp.capturing = false
	lp.nameEntry.SetText("")

	// Select newly added logo
	newIdx := 0
	for i, l := range lp.state.LogoLibrary.Logos {
		if l.Name == name {
			newIdx = i
			break
		}
	}
	lp.selectedIdx = newIdx
	lp.listBox.SelectRow(lp.listBox.GetRowAtIndex(newIdx))
	lp.showLogoDetail(newIdx)
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
		Color: color.RGBA{R: 255, G: 0, B: 255, A: 200},
	})
}

// showLogoDetail displays both raw captured region and quantized image.
func (lp *LogosPanel) showLogoDetail(idx int) {
	if lp.state.LogoLibrary == nil || idx < 0 || idx >= len(lp.state.LogoLibrary.Logos) {
		return
	}

	l := lp.state.LogoLibrary.Logos[idx]

	zoom := lp.canvas.GetZoom()
	canvasBounds := geometry.RectInt{
		X:      int(float64(l.Bounds.X) * zoom),
		Y:      int(float64(l.Bounds.Y) * zoom),
		Width:  int(float64(l.Bounds.Width) * zoom),
		Height: int(float64(l.Bounds.Height) * zoom),
	}

	canvasImg := lp.canvas.GetRenderedOutput()
	if canvasImg != nil {
		rawRegion := lp.extractRawRegion(canvasImg, canvasBounds)
		if rawRegion != nil {
			pixbuf := rgbaToPixbuf(rawRegion)
			if pixbuf != nil {
				lp.rawImage.SetFromPixbuf(pixbuf)
			}
		} else {
			lp.rawImage.Clear()
		}
	}

	scaled := l.ToScaledImage(1)
	if scaled != nil {
		rgba := image.NewRGBA(scaled.Bounds())
		for y := 0; y < scaled.Bounds().Dy(); y++ {
			for x := 0; x < scaled.Bounds().Dx(); x++ {
				gray := scaled.GrayAt(x, y)
				rgba.Set(x, y, color.RGBA{R: gray.Y, G: gray.Y, B: gray.Y, A: 255})
			}
		}
		pixbuf := rgbaToPixbuf(rgba)
		if pixbuf != nil {
			lp.quantizedImage.SetFromPixbuf(pixbuf)
		}
	}

	lp.detailFrame.SetLabel(fmt.Sprintf("<%s>", l.Name))
	lp.manufacturerEntry.SetText(l.ManufacturerID)

	lp.showCaptureOverlay(canvasBounds)
	lp.canvas.ScrollToRegion(l.Bounds.X, l.Bounds.Y, l.Bounds.Width, l.Bounds.Height)
}

// rgbaToPixbuf converts an RGBA image to a GDK Pixbuf.
func rgbaToPixbuf(img *image.RGBA) *gdk.Pixbuf {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}

	// Convert RGBA to RGB for pixbuf (drop alpha)
	rgb := make([]byte, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := img.RGBAAt(x+bounds.Min.X, y+bounds.Min.Y)
			off := (y*w + x) * 3
			rgb[off] = c.R
			rgb[off+1] = c.G
			rgb[off+2] = c.B
		}
	}

	pixbuf, err := gdk.PixbufNewFromData(rgb, gdk.COLORSPACE_RGB, false, 8, w, h, w*3)
	if err != nil {
		return nil
	}
	return pixbuf
}

// extractRawRegion extracts a region from the canvas image.
func (lp *LogosPanel) extractRawRegion(img *image.RGBA, bounds geometry.RectInt) *image.RGBA {
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
		return
	}

	l.ManufacturerID = manufacturer
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

	dlg := gtk.MessageDialogNew(lp.win, gtk.DIALOG_MODAL, gtk.MESSAGE_QUESTION, gtk.BUTTONS_YES_NO,
		"Delete logo <%s>?", name)
	response := dlg.Run()
	dlg.Destroy()

	if response == gtk.RESPONSE_YES {
		lp.state.LogoLibrary.Remove(name)
		if err := lp.state.SaveLogoLibrary(); err != nil {
			fmt.Printf("Warning: could not save logo library: %v\n", err)
		}
		lp.selectedIdx = -1
		lp.rawImage.Clear()
		lp.quantizedImage.Clear()
		lp.detailFrame.SetLabel("Selected Logo")
		lp.canvas.ClearOverlay("logo_capture")
		lp.refreshList()
		fmt.Printf("Deleted logo <%s>\n", name)
	}
}

// IsCapturing returns true if logo capture mode is active.
func (lp *LogosPanel) IsCapturing() bool {
	return lp.capturing
}

// AdjustCaptureBounds adjusts the last capture bounds and re-captures.
func (lp *LogosPanel) AdjustCaptureBounds(dx, dy int) {
	if lp.selectedIdx < 0 || lp.state.LogoLibrary == nil {
		return
	}
	if lp.selectedIdx >= len(lp.state.LogoLibrary.Logos) {
		return
	}

	l := lp.state.LogoLibrary.Logos[lp.selectedIdx]
	l.Bounds.X += dx
	l.Bounds.Y += dy

	img := lp.canvas.GetRenderedOutput()
	if img == nil {
		return
	}

	zoom := lp.canvas.GetZoom()
	canvasBounds := geometry.RectInt{
		X:      int(float64(l.Bounds.X) * zoom),
		Y:      int(float64(l.Bounds.Y) * zoom),
		Width:  int(float64(l.Bounds.Width) * zoom),
		Height: int(float64(l.Bounds.Height) * zoom),
	}

	qSize := l.QuantizedSize
	if qSize < 8 {
		qSize = 64
	}

	newLogo := logo.NewLogo(l.Name, img, canvasBounds, qSize)
	if newLogo == nil {
		return
	}

	l.Bits = newLogo.Bits
	l.Width = newLogo.Width
	l.Height = newLogo.Height

	if err := lp.state.SaveLogoLibrary(); err != nil {
		fmt.Printf("Warning: could not save logo library: %v\n", err)
	}

	lp.showLogoDetail(lp.selectedIdx)
	fmt.Printf("Adjusted logo <%s> to (%d,%d)\n", l.Name, l.Bounds.X, l.Bounds.Y)
}

// ResizeCapture changes the capture size and re-captures.
func (lp *LogosPanel) ResizeCapture(delta int) {
	if lp.selectedIdx < 0 || lp.state.LogoLibrary == nil {
		return
	}
	if lp.selectedIdx >= len(lp.state.LogoLibrary.Logos) {
		return
	}

	l := lp.state.LogoLibrary.Logos[lp.selectedIdx]

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

	l.Bounds.X = oldCenterX - l.Bounds.Width/2
	l.Bounds.Y = oldCenterY - l.Bounds.Height/2

	img := lp.canvas.GetRenderedOutput()
	if img == nil {
		return
	}

	zoom := lp.canvas.GetZoom()
	canvasBounds := geometry.RectInt{
		X:      int(float64(l.Bounds.X) * zoom),
		Y:      int(float64(l.Bounds.Y) * zoom),
		Width:  int(float64(l.Bounds.Width) * zoom),
		Height: int(float64(l.Bounds.Height) * zoom),
	}

	qSize := l.QuantizedSize
	if qSize < 8 {
		qSize = 64
	}

	newLogo := logo.NewLogo(l.Name, img, canvasBounds, qSize)
	if newLogo == nil {
		return
	}

	l.Bits = newLogo.Bits
	l.Width = newLogo.Width
	l.Height = newLogo.Height

	if err := lp.state.SaveLogoLibrary(); err != nil {
		fmt.Printf("Warning: could not save logo library: %v\n", err)
	}

	lp.showLogoDetail(lp.selectedIdx)
	fmt.Printf("Resized logo <%s> to source %dx%d -> bitmap %dx%d\n",
		l.Name, l.Bounds.Width, l.Bounds.Height, l.Width, l.Height)
}
