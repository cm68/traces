// Package dialogs provides application dialogs.
package dialogs

import (
	"fmt"
	"image/color"
	"math"
	"strconv"

	"pcb-tracer/internal/board"

	"fyne.io/fyne/v2"
	fynecanvas "fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// BoardSpecDialog provides a property sheet for editing board specifications.
type BoardSpecDialog struct {
	spec   *board.BaseSpec
	window fyne.Window

	// Board dimensions
	nameEntry   *widget.Entry
	widthEntry  *widget.Entry
	heightEntry *widget.Entry

	// Contact spec
	contactEdge   *widget.Select
	contactCount  *widget.Entry
	contactPitch  *widget.Entry
	contactWidth  *widget.Entry
	contactHeight *widget.Entry
	contactMargin *widget.Entry

	// Detection params
	hueMinEntry    *widget.Entry
	hueMaxEntry    *widget.Entry
	satMinEntry    *widget.Entry
	satMaxEntry    *widget.Entry
	valMinEntry    *widget.Entry
	valMaxEntry    *widget.Entry
	aspectMinEntry *widget.Entry
	aspectMaxEntry *widget.Entry
	areaMinEntry   *widget.Entry
	areaMaxEntry   *widget.Entry

	// Color swatches
	colorSwatchMin *fynecanvas.Rectangle
	colorSwatchMax *fynecanvas.Rectangle

	// Callback
	onSave func(*board.BaseSpec)
}

// NewBoardSpecDialog creates a new board specification dialog.
func NewBoardSpecDialog(spec *board.BaseSpec, window fyne.Window, onSave func(*board.BaseSpec)) *BoardSpecDialog {
	d := &BoardSpecDialog{
		spec:   spec,
		window: window,
		onSave: onSave,
	}
	return d
}

// Show displays the dialog.
func (d *BoardSpecDialog) Show() {
	content := d.createContent()

	dlg := dialog.NewCustomConfirm(
		"Board Specification: "+d.spec.SpecName,
		"Save",
		"Cancel",
		content,
		func(save bool) {
			if save {
				d.applyChanges()
				if d.onSave != nil {
					d.onSave(d.spec)
				}
			}
		},
		d.window,
	)
	dlg.Resize(fyne.NewSize(500, 700))
	dlg.Show()
}

func (d *BoardSpecDialog) createContent() fyne.CanvasObject {
	// Board dimensions section
	d.nameEntry = widget.NewEntry()
	d.nameEntry.SetText(d.spec.SpecName)

	d.widthEntry = widget.NewEntry()
	d.widthEntry.SetText(fmt.Sprintf("%.4f", d.spec.WidthInches))

	d.heightEntry = widget.NewEntry()
	d.heightEntry.SetText(fmt.Sprintf("%.4f", d.spec.HeightInches))

	dimensionsForm := widget.NewForm(
		widget.NewFormItem("Name", d.nameEntry),
		widget.NewFormItem("Width (inches)", d.widthEntry),
		widget.NewFormItem("Height (inches)", d.heightEntry),
	)

	// Contact spec section
	edges := []string{string(board.EdgeTop), string(board.EdgeBottom), string(board.EdgeLeft), string(board.EdgeRight)}
	d.contactEdge = widget.NewSelect(edges, nil)

	d.contactCount = widget.NewEntry()
	d.contactPitch = widget.NewEntry()
	d.contactWidth = widget.NewEntry()
	d.contactHeight = widget.NewEntry()
	d.contactMargin = widget.NewEntry()

	if c := d.spec.Contacts; c != nil {
		d.contactEdge.SetSelected(string(c.Edge))
		d.contactCount.SetText(fmt.Sprintf("%d", c.Count))
		d.contactPitch.SetText(fmt.Sprintf("%.4f", c.PitchInches))
		d.contactWidth.SetText(fmt.Sprintf("%.4f", c.WidthInches))
		d.contactHeight.SetText(fmt.Sprintf("%.4f", c.HeightInches))
		d.contactMargin.SetText(fmt.Sprintf("%.4f", c.MarginInches))
	} else {
		d.contactEdge.SetSelected(string(board.EdgeTop))
		d.contactCount.SetText("50")
		d.contactPitch.SetText("0.125")
		d.contactWidth.SetText("0.0625")
		d.contactHeight.SetText("0.375")
		d.contactMargin.SetText("2.0")
	}

	contactsForm := widget.NewForm(
		widget.NewFormItem("Edge", d.contactEdge),
		widget.NewFormItem("Count", d.contactCount),
		widget.NewFormItem("Pitch (inches)", d.contactPitch),
		widget.NewFormItem("Width (inches)", d.contactWidth),
		widget.NewFormItem("Height (inches)", d.contactHeight),
		widget.NewFormItem("Margin (inches)", d.contactMargin),
	)

	// Detection params section
	d.hueMinEntry = widget.NewEntry()
	d.hueMaxEntry = widget.NewEntry()
	d.satMinEntry = widget.NewEntry()
	d.satMaxEntry = widget.NewEntry()
	d.valMinEntry = widget.NewEntry()
	d.valMaxEntry = widget.NewEntry()
	d.aspectMinEntry = widget.NewEntry()
	d.aspectMaxEntry = widget.NewEntry()
	d.areaMinEntry = widget.NewEntry()
	d.areaMaxEntry = widget.NewEntry()

	if c := d.spec.Contacts; c != nil && c.Detection != nil {
		det := c.Detection
		d.hueMinEntry.SetText(fmt.Sprintf("%.0f", det.Color.HueMin))
		d.hueMaxEntry.SetText(fmt.Sprintf("%.0f", det.Color.HueMax))
		d.satMinEntry.SetText(fmt.Sprintf("%.0f", det.Color.SatMin))
		d.satMaxEntry.SetText(fmt.Sprintf("%.0f", det.Color.SatMax))
		d.valMinEntry.SetText(fmt.Sprintf("%.0f", det.Color.ValMin))
		d.valMaxEntry.SetText(fmt.Sprintf("%.0f", det.Color.ValMax))
		d.aspectMinEntry.SetText(fmt.Sprintf("%.1f", det.AspectRatioMin))
		d.aspectMaxEntry.SetText(fmt.Sprintf("%.1f", det.AspectRatioMax))
		d.areaMinEntry.SetText(fmt.Sprintf("%d", det.MinAreaPixels))
		d.areaMaxEntry.SetText(fmt.Sprintf("%d", det.MaxAreaPixels))
	} else {
		// Default gold color values
		d.hueMinEntry.SetText("15")
		d.hueMaxEntry.SetText("35")
		d.satMinEntry.SetText("80")
		d.satMaxEntry.SetText("255")
		d.valMinEntry.SetText("120")
		d.valMaxEntry.SetText("255")
		d.aspectMinEntry.SetText("4.0")
		d.aspectMaxEntry.SetText("8.0")
		d.areaMinEntry.SetText("2000")
		d.areaMaxEntry.SetText("20000")
	}

	// Create color swatches
	d.colorSwatchMin = fynecanvas.NewRectangle(color.RGBA{R: 128, G: 128, B: 128, A: 255})
	d.colorSwatchMin.SetMinSize(fyne.NewSize(40, 24))
	d.colorSwatchMax = fynecanvas.NewRectangle(color.RGBA{R: 128, G: 128, B: 128, A: 255})
	d.colorSwatchMax.SetMinSize(fyne.NewSize(40, 24))

	// Update swatches when any HSV entry changes
	updateSwatches := func(s string) {
		d.updateColorSwatches()
	}
	for _, entry := range []*widget.Entry{
		d.hueMinEntry, d.hueMaxEntry,
		d.satMinEntry, d.satMaxEntry,
		d.valMinEntry, d.valMaxEntry,
	} {
		entry.OnChanged = updateSwatches
	}

	// Initialize swatch colors
	d.updateColorSwatches()

	// HSV color form with labels and swatches
	hsvForm := container.NewVBox(
		container.NewGridWithColumns(5,
			widget.NewLabel("Hue:"),
			d.hueMinEntry,
			widget.NewLabel("to"),
			d.hueMaxEntry,
			widget.NewLabel("(0-180)"),
		),
		container.NewGridWithColumns(5,
			widget.NewLabel("Sat:"),
			d.satMinEntry,
			widget.NewLabel("to"),
			d.satMaxEntry,
			widget.NewLabel("(0-255)"),
		),
		container.NewGridWithColumns(5,
			widget.NewLabel("Val:"),
			d.valMinEntry,
			widget.NewLabel("to"),
			d.valMaxEntry,
			widget.NewLabel("(0-255)"),
		),
		container.NewHBox(
			widget.NewLabel("Color Range:"),
			d.colorSwatchMin,
			widget.NewLabel("to"),
			d.colorSwatchMax,
		),
	)

	detectionForm := widget.NewForm(
		widget.NewFormItem("Aspect Ratio Min", d.aspectMinEntry),
		widget.NewFormItem("Aspect Ratio Max", d.aspectMaxEntry),
		widget.NewFormItem("Min Area (px @600dpi)", d.areaMinEntry),
		widget.NewFormItem("Max Area (px @600dpi)", d.areaMaxEntry),
	)

	// Assemble cards
	return container.NewVBox(
		widget.NewCard("Board Dimensions", "", dimensionsForm),
		widget.NewCard("Edge Contacts", "", contactsForm),
		widget.NewCard("Contact Color (HSV)", "", hsvForm),
		widget.NewCard("Contact Detection", "", detectionForm),
	)
}

func (d *BoardSpecDialog) applyChanges() {
	// Board dimensions
	d.spec.SpecName = d.nameEntry.Text
	if v, err := strconv.ParseFloat(d.widthEntry.Text, 64); err == nil {
		d.spec.WidthInches = v
	}
	if v, err := strconv.ParseFloat(d.heightEntry.Text, 64); err == nil {
		d.spec.HeightInches = v
	}

	// Ensure contacts struct exists
	if d.spec.Contacts == nil {
		d.spec.Contacts = &board.ContactSpec{}
	}

	// Contact spec
	d.spec.Contacts.Edge = board.Edge(d.contactEdge.Selected)
	if v, err := strconv.Atoi(d.contactCount.Text); err == nil {
		d.spec.Contacts.Count = v
	}
	if v, err := strconv.ParseFloat(d.contactPitch.Text, 64); err == nil {
		d.spec.Contacts.PitchInches = v
	}
	if v, err := strconv.ParseFloat(d.contactWidth.Text, 64); err == nil {
		d.spec.Contacts.WidthInches = v
	}
	if v, err := strconv.ParseFloat(d.contactHeight.Text, 64); err == nil {
		d.spec.Contacts.HeightInches = v
	}
	if v, err := strconv.ParseFloat(d.contactMargin.Text, 64); err == nil {
		d.spec.Contacts.MarginInches = v
	}

	// Ensure detection struct exists
	if d.spec.Contacts.Detection == nil {
		d.spec.Contacts.Detection = &board.ContactDetectionParams{}
	}

	// Detection params
	det := d.spec.Contacts.Detection
	if v, err := strconv.ParseFloat(d.hueMinEntry.Text, 64); err == nil {
		det.Color.HueMin = v
	}
	if v, err := strconv.ParseFloat(d.hueMaxEntry.Text, 64); err == nil {
		det.Color.HueMax = v
	}
	if v, err := strconv.ParseFloat(d.satMinEntry.Text, 64); err == nil {
		det.Color.SatMin = v
	}
	if v, err := strconv.ParseFloat(d.satMaxEntry.Text, 64); err == nil {
		det.Color.SatMax = v
	}
	if v, err := strconv.ParseFloat(d.valMinEntry.Text, 64); err == nil {
		det.Color.ValMin = v
	}
	if v, err := strconv.ParseFloat(d.valMaxEntry.Text, 64); err == nil {
		det.Color.ValMax = v
	}
	if v, err := strconv.ParseFloat(d.aspectMinEntry.Text, 64); err == nil {
		det.AspectRatioMin = v
	}
	if v, err := strconv.ParseFloat(d.aspectMaxEntry.Text, 64); err == nil {
		det.AspectRatioMax = v
	}
	if v, err := strconv.Atoi(d.areaMinEntry.Text); err == nil {
		det.MinAreaPixels = v
	}
	if v, err := strconv.Atoi(d.areaMaxEntry.Text); err == nil {
		det.MaxAreaPixels = v
	}
}

// updateColorSwatches updates the color swatches based on current HSV values.
func (d *BoardSpecDialog) updateColorSwatches() {
	hMin, _ := strconv.ParseFloat(d.hueMinEntry.Text, 64)
	hMax, _ := strconv.ParseFloat(d.hueMaxEntry.Text, 64)
	sMin, _ := strconv.ParseFloat(d.satMinEntry.Text, 64)
	sMax, _ := strconv.ParseFloat(d.satMaxEntry.Text, 64)
	vMin, _ := strconv.ParseFloat(d.valMinEntry.Text, 64)
	vMax, _ := strconv.ParseFloat(d.valMaxEntry.Text, 64)

	// Min swatch: min hue, min sat, min val
	d.colorSwatchMin.FillColor = hsvToRGB(hMin, sMin, vMin)

	// Max swatch: max hue, max sat, max val
	d.colorSwatchMax.FillColor = hsvToRGB(hMax, sMax, vMax)

	// Force canvas refresh
	fynecanvas.Refresh(d.colorSwatchMin)
	fynecanvas.Refresh(d.colorSwatchMax)
}

// hsvToRGB converts HSV (OpenCV convention: H 0-180, S 0-255, V 0-255) to RGB.
func hsvToRGB(h, s, v float64) color.RGBA {
	// Normalize to standard ranges
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
