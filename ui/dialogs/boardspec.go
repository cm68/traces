// Package dialogs provides application dialogs.
package dialogs

import (
	"fmt"
	"image/color"
	"math"
	"strconv"

	"pcb-tracer/internal/board"

	"github.com/gotk3/gotk3/gtk"
)

// BoardSpecDialog provides a dialog for editing board specifications.
type BoardSpecDialog struct {
	spec *board.BaseSpec
	win  *gtk.Window

	// Board dimensions
	nameEntry   *gtk.Entry
	widthEntry  *gtk.Entry
	heightEntry *gtk.Entry

	// Contact spec
	contactEdge   *gtk.ComboBoxText
	contactCount  *gtk.Entry
	contactPitch  *gtk.Entry
	contactWidth  *gtk.Entry
	contactHeight *gtk.Entry
	contactMargin *gtk.Entry

	// Detection params
	hueMinEntry    *gtk.Entry
	hueMaxEntry    *gtk.Entry
	satMinEntry    *gtk.Entry
	satMaxEntry    *gtk.Entry
	valMinEntry    *gtk.Entry
	valMaxEntry    *gtk.Entry
	aspectMinEntry *gtk.Entry
	aspectMaxEntry *gtk.Entry
	areaMinEntry   *gtk.Entry
	areaMaxEntry   *gtk.Entry

	// Color swatches
	colorSwatchMin *gtk.DrawingArea
	colorSwatchMax *gtk.DrawingArea
	swatchMinColor color.RGBA
	swatchMaxColor color.RGBA

	// Callback
	onSave func(*board.BaseSpec)
}

// NewBoardSpecDialog creates a new board specification dialog.
func NewBoardSpecDialog(spec *board.BaseSpec, win *gtk.Window, onSave func(*board.BaseSpec)) *BoardSpecDialog {
	return &BoardSpecDialog{
		spec:   spec,
		win:    win,
		onSave: onSave,
	}
}

// Show displays the dialog.
func (d *BoardSpecDialog) Show() {
	dlg, _ := gtk.DialogNewWithButtons("Board Specification: "+d.spec.SpecName, d.win,
		gtk.DIALOG_MODAL|gtk.DIALOG_DESTROY_WITH_PARENT,
		[]interface{}{"Cancel", gtk.RESPONSE_CANCEL},
		[]interface{}{"Save", gtk.RESPONSE_OK})
	dlg.SetDefaultSize(500, 700)

	contentArea, _ := dlg.GetContentArea()

	// Build content inside a scrolled window
	scrollWin, _ := gtk.ScrolledWindowNew(nil, nil)
	scrollWin.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)

	contentBox, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4)
	contentBox.SetMarginStart(8)
	contentBox.SetMarginEnd(8)
	contentBox.SetMarginTop(4)
	contentBox.SetMarginBottom(4)

	d.buildContent(contentBox)

	scrollWin.Add(contentBox)
	contentArea.PackStart(scrollWin, true, true, 0)
	dlg.ShowAll()

	response := dlg.Run()
	if response == gtk.RESPONSE_OK {
		d.applyChanges()
		if d.onSave != nil {
			d.onSave(d.spec)
		}
	}
	dlg.Destroy()
}

func (d *BoardSpecDialog) buildContent(box *gtk.Box) {
	addFrame := func(label string) (*gtk.Frame, *gtk.Box) {
		frame, _ := gtk.FrameNew(label)
		inner, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 2)
		inner.SetMarginStart(4)
		inner.SetMarginEnd(4)
		inner.SetMarginTop(4)
		inner.SetMarginBottom(4)
		frame.Add(inner)
		box.PackStart(frame, false, false, 2)
		return frame, inner
	}

	addRow := func(parent *gtk.Box, label string, entry *gtk.Entry) {
		row, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
		lbl, _ := gtk.LabelNew(label)
		lbl.SetWidthChars(20)
		lbl.SetXAlign(1.0)
		row.PackStart(lbl, false, false, 0)
		row.PackStart(entry, true, true, 0)
		parent.PackStart(row, false, false, 0)
	}

	newEntry := func(text string) *gtk.Entry {
		e, _ := gtk.EntryNew()
		e.SetText(text)
		return e
	}

	// Board Dimensions
	_, dimBox := addFrame("Board Dimensions")
	d.nameEntry = newEntry(d.spec.SpecName)
	d.widthEntry = newEntry(fmt.Sprintf("%.4f", d.spec.WidthInches))
	d.heightEntry = newEntry(fmt.Sprintf("%.4f", d.spec.HeightInches))
	addRow(dimBox, "Name:", d.nameEntry)
	addRow(dimBox, "Width (inches):", d.widthEntry)
	addRow(dimBox, "Height (inches):", d.heightEntry)

	// Edge Contacts
	_, contactBox := addFrame("Edge Contacts")
	d.contactEdge, _ = gtk.ComboBoxTextNew()
	edges := []string{string(board.EdgeTop), string(board.EdgeBottom), string(board.EdgeLeft), string(board.EdgeRight)}
	for _, e := range edges {
		d.contactEdge.AppendText(e)
	}

	edgeRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	edgeLbl, _ := gtk.LabelNew("Edge:")
	edgeLbl.SetWidthChars(20)
	edgeLbl.SetXAlign(1.0)
	edgeRow.PackStart(edgeLbl, false, false, 0)
	edgeRow.PackStart(d.contactEdge, true, true, 0)
	contactBox.PackStart(edgeRow, false, false, 0)

	if c := d.spec.Contacts; c != nil {
		for i, e := range edges {
			if e == string(c.Edge) {
				d.contactEdge.SetActive(i)
				break
			}
		}
		d.contactCount = newEntry(fmt.Sprintf("%d", c.Count))
		d.contactPitch = newEntry(fmt.Sprintf("%.4f", c.PitchInches))
		d.contactWidth = newEntry(fmt.Sprintf("%.4f", c.WidthInches))
		d.contactHeight = newEntry(fmt.Sprintf("%.4f", c.HeightInches))
		d.contactMargin = newEntry(fmt.Sprintf("%.4f", c.MarginInches))
	} else {
		d.contactEdge.SetActive(0)
		d.contactCount = newEntry("50")
		d.contactPitch = newEntry("0.125")
		d.contactWidth = newEntry("0.0625")
		d.contactHeight = newEntry("0.375")
		d.contactMargin = newEntry("2.0")
	}

	addRow(contactBox, "Count:", d.contactCount)
	addRow(contactBox, "Pitch (inches):", d.contactPitch)
	addRow(contactBox, "Width (inches):", d.contactWidth)
	addRow(contactBox, "Height (inches):", d.contactHeight)
	addRow(contactBox, "Margin (inches):", d.contactMargin)

	// Contact Color (HSV)
	_, hsvBox := addFrame("Contact Color (HSV)")

	if c := d.spec.Contacts; c != nil && c.Detection != nil {
		det := c.Detection
		d.hueMinEntry = newEntry(fmt.Sprintf("%.0f", det.Color.HueMin))
		d.hueMaxEntry = newEntry(fmt.Sprintf("%.0f", det.Color.HueMax))
		d.satMinEntry = newEntry(fmt.Sprintf("%.0f", det.Color.SatMin))
		d.satMaxEntry = newEntry(fmt.Sprintf("%.0f", det.Color.SatMax))
		d.valMinEntry = newEntry(fmt.Sprintf("%.0f", det.Color.ValMin))
		d.valMaxEntry = newEntry(fmt.Sprintf("%.0f", det.Color.ValMax))
	} else {
		d.hueMinEntry = newEntry("15")
		d.hueMaxEntry = newEntry("35")
		d.satMinEntry = newEntry("80")
		d.satMaxEntry = newEntry("255")
		d.valMinEntry = newEntry("120")
		d.valMaxEntry = newEntry("255")
	}

	addHSVRow := func(label string, minEntry, maxEntry *gtk.Entry, rangeHint string) {
		row, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
		lbl, _ := gtk.LabelNew(label)
		lbl.SetWidthChars(6)
		lbl.SetXAlign(1.0)
		toLbl, _ := gtk.LabelNew("to")
		hint, _ := gtk.LabelNew(rangeHint)
		row.PackStart(lbl, false, false, 0)
		row.PackStart(minEntry, true, true, 0)
		row.PackStart(toLbl, false, false, 0)
		row.PackStart(maxEntry, true, true, 0)
		row.PackStart(hint, false, false, 0)
		hsvBox.PackStart(row, false, false, 0)
	}

	addHSVRow("Hue:", d.hueMinEntry, d.hueMaxEntry, "(0-180)")
	addHSVRow("Sat:", d.satMinEntry, d.satMaxEntry, "(0-255)")
	addHSVRow("Val:", d.valMinEntry, d.valMaxEntry, "(0-255)")

	// Color swatches
	swatchRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	swatchLbl, _ := gtk.LabelNew("Color Range:")
	swatchRow.PackStart(swatchLbl, false, false, 0)

	d.colorSwatchMin, _ = gtk.DrawingAreaNew()
	d.colorSwatchMin.SetSizeRequest(40, 24)
	d.colorSwatchMin.Connect("draw", func(da *gtk.DrawingArea, cr interface{}) {
		// Simple fill with swatch color
	})

	d.colorSwatchMax, _ = gtk.DrawingAreaNew()
	d.colorSwatchMax.SetSizeRequest(40, 24)

	toLbl2, _ := gtk.LabelNew("to")
	swatchRow.PackStart(d.colorSwatchMin, false, false, 0)
	swatchRow.PackStart(toLbl2, false, false, 0)
	swatchRow.PackStart(d.colorSwatchMax, false, false, 0)
	hsvBox.PackStart(swatchRow, false, false, 0)

	d.updateColorSwatches()

	// Update swatches on any HSV change
	updateCb := func() { d.updateColorSwatches() }
	for _, entry := range []*gtk.Entry{
		d.hueMinEntry, d.hueMaxEntry,
		d.satMinEntry, d.satMaxEntry,
		d.valMinEntry, d.valMaxEntry,
	} {
		entry.Connect("changed", updateCb)
	}

	// Contact Detection
	_, detBox := addFrame("Contact Detection")

	if c := d.spec.Contacts; c != nil && c.Detection != nil {
		det := c.Detection
		d.aspectMinEntry = newEntry(fmt.Sprintf("%.1f", det.AspectRatioMin))
		d.aspectMaxEntry = newEntry(fmt.Sprintf("%.1f", det.AspectRatioMax))
		d.areaMinEntry = newEntry(fmt.Sprintf("%d", det.MinAreaPixels))
		d.areaMaxEntry = newEntry(fmt.Sprintf("%d", det.MaxAreaPixels))
	} else {
		d.aspectMinEntry = newEntry("4.0")
		d.aspectMaxEntry = newEntry("8.0")
		d.areaMinEntry = newEntry("2000")
		d.areaMaxEntry = newEntry("20000")
	}

	addRow(detBox, "Aspect Ratio Min:", d.aspectMinEntry)
	addRow(detBox, "Aspect Ratio Max:", d.aspectMaxEntry)
	addRow(detBox, "Min Area (px @600dpi):", d.areaMinEntry)
	addRow(detBox, "Max Area (px @600dpi):", d.areaMaxEntry)
}

func (d *BoardSpecDialog) applyChanges() {
	getText := func(e *gtk.Entry) string {
		t, _ := e.GetText()
		return t
	}

	d.spec.SpecName = getText(d.nameEntry)
	if v, err := strconv.ParseFloat(getText(d.widthEntry), 64); err == nil {
		d.spec.WidthInches = v
	}
	if v, err := strconv.ParseFloat(getText(d.heightEntry), 64); err == nil {
		d.spec.HeightInches = v
	}

	if d.spec.Contacts == nil {
		d.spec.Contacts = &board.ContactSpec{}
	}

	d.spec.Contacts.Edge = board.Edge(d.contactEdge.GetActiveText())
	if v, err := strconv.Atoi(getText(d.contactCount)); err == nil {
		d.spec.Contacts.Count = v
	}
	if v, err := strconv.ParseFloat(getText(d.contactPitch), 64); err == nil {
		d.spec.Contacts.PitchInches = v
	}
	if v, err := strconv.ParseFloat(getText(d.contactWidth), 64); err == nil {
		d.spec.Contacts.WidthInches = v
	}
	if v, err := strconv.ParseFloat(getText(d.contactHeight), 64); err == nil {
		d.spec.Contacts.HeightInches = v
	}
	if v, err := strconv.ParseFloat(getText(d.contactMargin), 64); err == nil {
		d.spec.Contacts.MarginInches = v
	}

	if d.spec.Contacts.Detection == nil {
		d.spec.Contacts.Detection = &board.ContactDetectionParams{}
	}

	det := d.spec.Contacts.Detection
	if v, err := strconv.ParseFloat(getText(d.hueMinEntry), 64); err == nil {
		det.Color.HueMin = v
	}
	if v, err := strconv.ParseFloat(getText(d.hueMaxEntry), 64); err == nil {
		det.Color.HueMax = v
	}
	if v, err := strconv.ParseFloat(getText(d.satMinEntry), 64); err == nil {
		det.Color.SatMin = v
	}
	if v, err := strconv.ParseFloat(getText(d.satMaxEntry), 64); err == nil {
		det.Color.SatMax = v
	}
	if v, err := strconv.ParseFloat(getText(d.valMinEntry), 64); err == nil {
		det.Color.ValMin = v
	}
	if v, err := strconv.ParseFloat(getText(d.valMaxEntry), 64); err == nil {
		det.Color.ValMax = v
	}
	if v, err := strconv.ParseFloat(getText(d.aspectMinEntry), 64); err == nil {
		det.AspectRatioMin = v
	}
	if v, err := strconv.ParseFloat(getText(d.aspectMaxEntry), 64); err == nil {
		det.AspectRatioMax = v
	}
	if v, err := strconv.Atoi(getText(d.areaMinEntry)); err == nil {
		det.MinAreaPixels = v
	}
	if v, err := strconv.Atoi(getText(d.areaMaxEntry)); err == nil {
		det.MaxAreaPixels = v
	}
}

func (d *BoardSpecDialog) updateColorSwatches() {
	getText := func(e *gtk.Entry) float64 {
		t, _ := e.GetText()
		v, _ := strconv.ParseFloat(t, 64)
		return v
	}

	hMin := getText(d.hueMinEntry)
	hMax := getText(d.hueMaxEntry)
	sMin := getText(d.satMinEntry)
	sMax := getText(d.satMaxEntry)
	vMin := getText(d.valMinEntry)
	vMax := getText(d.valMaxEntry)

	d.swatchMinColor = hsvToRGB(hMin, sMin, vMin)
	d.swatchMaxColor = hsvToRGB(hMax, sMax, vMax)

	if d.colorSwatchMin != nil {
		d.colorSwatchMin.QueueDraw()
	}
	if d.colorSwatchMax != nil {
		d.colorSwatchMax.QueueDraw()
	}
}

// hsvToRGB converts HSV (OpenCV convention: H 0-180, S 0-255, V 0-255) to RGB.
func hsvToRGB(h, s, v float64) color.RGBA {
	h = h * 2 // OpenCV uses 0-180, convert to 0-360
	s = s / 255.0
	v = v / 255.0

	if s == 0 {
		gray := uint8(v * 255)
		return color.RGBA{R: gray, G: gray, B: gray, A: 255}
	}

	h = math.Mod(h, 360)
	h = h / 60
	i := int(h)
	f := h - float64(i)
	p := v * (1 - s)
	q := v * (1 - s*f)
	t := v * (1 - s*(1-f))

	var r, g, b float64
	switch i {
	case 0:
		r, g, b = v, t, p
	case 1:
		r, g, b = q, v, p
	case 2:
		r, g, b = p, v, t
	case 3:
		r, g, b = p, q, v
	case 4:
		r, g, b = t, p, v
	default:
		r, g, b = v, p, q
	}

	return color.RGBA{
		R: uint8(r * 255),
		G: uint8(g * 255),
		B: uint8(b * 255),
		A: 255,
	}
}
