package panels

import (
	"fmt"
	"path/filepath"
	"strconv"

	"pcb-tracer/internal/app"
	pcbimage "pcb-tracer/internal/image"
	"pcb-tracer/pkg/geometry"
	"pcb-tracer/ui/canvas"

	"github.com/gotk3/gotk3/gtk"
)

// PropertySheet displays and allows editing of all project properties.
type PropertySheet struct {
	state  *app.State
	canvas *canvas.ImageCanvas
	win    *gtk.Window
	box    *gtk.Box

	onUpdate func()

	dpiEntry *gtk.Entry

	frontFileBtn   *gtk.Button
	frontCropLabel *gtk.Label

	frontOffsetXEntry      *gtk.Entry
	frontOffsetYEntry      *gtk.Entry
	frontRotationEntry     *gtk.Entry
	frontShearTopXEntry    *gtk.Entry
	frontShearBottomXEntry *gtk.Entry
	frontShearLeftYEntry   *gtk.Entry
	frontShearRightYEntry  *gtk.Entry
	frontAutoRotEntry      *gtk.Entry
	frontAutoScaleXEntry   *gtk.Entry
	frontAutoScaleYEntry   *gtk.Entry
	frontRotCenterXEntry   *gtk.Entry
	frontRotCenterYEntry   *gtk.Entry

	backFileBtn   *gtk.Button
	backCropLabel *gtk.Label

	backOffsetXEntry      *gtk.Entry
	backOffsetYEntry      *gtk.Entry
	backRotationEntry     *gtk.Entry
	backShearTopXEntry    *gtk.Entry
	backShearBottomXEntry *gtk.Entry
	backShearLeftYEntry   *gtk.Entry
	backShearRightYEntry  *gtk.Entry
	backAutoRotEntry      *gtk.Entry
	backAutoScaleXEntry   *gtk.Entry
	backAutoScaleYEntry   *gtk.Entry
	backRotCenterXEntry   *gtk.Entry
	backRotCenterYEntry   *gtk.Entry
}

// NewPropertySheet creates a new property sheet panel.
func NewPropertySheet(state *app.State, cvs *canvas.ImageCanvas, win *gtk.Window, onUpdate func()) *PropertySheet {
	ps := &PropertySheet{
		state:    state,
		canvas:   cvs,
		win:      win,
		onUpdate: onUpdate,
	}

	ps.buildUI()
	ps.refresh()

	state.On(app.EventImageLoaded, func(_ interface{}) { ps.refresh() })
	state.On(app.EventAlignmentComplete, func(_ interface{}) { ps.refresh() })
	state.On(app.EventProjectLoaded, func(_ interface{}) { ps.refresh() })

	return ps
}

// Widget returns the panel widget for embedding.
func (ps *PropertySheet) Widget() gtk.IWidget {
	return ps.box
}

func (ps *PropertySheet) buildUI() {
	// Outer scrolled window
	scrollWin, _ := gtk.ScrolledWindowNew(nil, nil)
	scrollWin.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)

	content, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4)
	content.SetMarginStart(4)
	content.SetMarginEnd(4)
	content.SetMarginTop(4)
	content.SetMarginBottom(4)

	newEntry := func() *gtk.Entry {
		e, _ := gtk.EntryNew()
		return e
	}

	addFrame := func(label string) *gtk.Box {
		frame, _ := gtk.FrameNew(label)
		inner, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 2)
		inner.SetMarginStart(4)
		inner.SetMarginEnd(4)
		inner.SetMarginTop(4)
		inner.SetMarginBottom(4)
		frame.Add(inner)
		content.PackStart(frame, false, false, 2)
		return inner
	}

	addRow := func(parent *gtk.Box, label string, entry *gtk.Entry) {
		row, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
		lbl, _ := gtk.LabelNew(label)
		lbl.SetWidthChars(14)
		lbl.SetXAlign(1.0)
		row.PackStart(lbl, false, false, 0)
		row.PackStart(entry, true, true, 0)
		parent.PackStart(row, false, false, 0)
	}

	addLabelRow := func(parent *gtk.Box, label string, value *gtk.Label) {
		row, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
		lbl, _ := gtk.LabelNew(label)
		lbl.SetWidthChars(14)
		lbl.SetXAlign(1.0)
		row.PackStart(lbl, false, false, 0)
		row.PackStart(value, true, true, 0)
		parent.PackStart(row, false, false, 0)
	}

	addBtnRow := func(parent *gtk.Box, label string, btn *gtk.Button) {
		row, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
		lbl, _ := gtk.LabelNew(label)
		lbl.SetWidthChars(14)
		lbl.SetXAlign(1.0)
		row.PackStart(lbl, false, false, 0)
		row.PackStart(btn, true, true, 0)
		parent.PackStart(row, false, false, 0)
	}

	// Project info
	projBox := addFrame("Project")
	ps.dpiEntry = newEntry()
	ps.dpiEntry.Connect("changed", func() { ps.onDPIChanged() })
	addRow(projBox, "DPI:", ps.dpiEntry)

	// Front Image info
	frontInfoBox := addFrame("Front Image")
	ps.frontFileBtn, _ = gtk.ButtonNewWithLabel("(none)")
	ps.frontFileBtn.Connect("clicked", func() { ps.onBrowseFrontFile() })
	ps.frontCropLabel, _ = gtk.LabelNew("")
	ps.frontCropLabel.SetHAlign(gtk.ALIGN_START)
	addBtnRow(frontInfoBox, "File:", ps.frontFileBtn)
	addLabelRow(frontInfoBox, "Crop:", ps.frontCropLabel)

	// Front Alignment
	frontAlignBox := addFrame("Front Alignment")
	ps.frontOffsetXEntry = newEntry()
	ps.frontOffsetXEntry.Connect("changed", func() {
		t, _ := ps.frontOffsetXEntry.GetText()
		ps.onIntChanged(t, &ps.state.FrontManualOffset.X)
	})
	ps.frontOffsetYEntry = newEntry()
	ps.frontOffsetYEntry.Connect("changed", func() {
		t, _ := ps.frontOffsetYEntry.GetText()
		ps.onIntChanged(t, &ps.state.FrontManualOffset.Y)
	})
	ps.frontRotationEntry = newEntry()
	ps.frontRotationEntry.Connect("changed", func() {
		t, _ := ps.frontRotationEntry.GetText()
		ps.onFloatChanged(t, &ps.state.FrontManualRotation)
	})
	ps.frontAutoRotEntry = newEntry()
	ps.frontAutoRotEntry.Connect("changed", func() {
		t, _ := ps.frontAutoRotEntry.GetText()
		ps.onFloatChanged(t, &ps.state.FrontAutoRotation)
	})
	ps.frontRotCenterXEntry = newEntry()
	ps.frontRotCenterXEntry.Connect("changed", func() {
		t, _ := ps.frontRotCenterXEntry.GetText()
		ps.onFloatChanged(t, &ps.state.FrontRotationCenter.X)
	})
	ps.frontRotCenterYEntry = newEntry()
	ps.frontRotCenterYEntry.Connect("changed", func() {
		t, _ := ps.frontRotCenterYEntry.GetText()
		ps.onFloatChanged(t, &ps.state.FrontRotationCenter.Y)
	})
	ps.frontAutoScaleXEntry = newEntry()
	ps.frontAutoScaleXEntry.Connect("changed", func() {
		t, _ := ps.frontAutoScaleXEntry.GetText()
		ps.onFloatChanged(t, &ps.state.FrontAutoScaleX)
	})
	ps.frontAutoScaleYEntry = newEntry()
	ps.frontAutoScaleYEntry.Connect("changed", func() {
		t, _ := ps.frontAutoScaleYEntry.GetText()
		ps.onFloatChanged(t, &ps.state.FrontAutoScaleY)
	})
	ps.frontShearTopXEntry = newEntry()
	ps.frontShearTopXEntry.Connect("changed", func() {
		t, _ := ps.frontShearTopXEntry.GetText()
		ps.onFloatChanged(t, &ps.state.FrontShearTopX)
	})
	ps.frontShearBottomXEntry = newEntry()
	ps.frontShearBottomXEntry.Connect("changed", func() {
		t, _ := ps.frontShearBottomXEntry.GetText()
		ps.onFloatChanged(t, &ps.state.FrontShearBottomX)
	})
	ps.frontShearLeftYEntry = newEntry()
	ps.frontShearLeftYEntry.Connect("changed", func() {
		t, _ := ps.frontShearLeftYEntry.GetText()
		ps.onFloatChanged(t, &ps.state.FrontShearLeftY)
	})
	ps.frontShearRightYEntry = newEntry()
	ps.frontShearRightYEntry.Connect("changed", func() {
		t, _ := ps.frontShearRightYEntry.GetText()
		ps.onFloatChanged(t, &ps.state.FrontShearRightY)
	})

	addRow(frontAlignBox, "Offset X:", ps.frontOffsetXEntry)
	addRow(frontAlignBox, "Offset Y:", ps.frontOffsetYEntry)
	addRow(frontAlignBox, "Rotation:", ps.frontRotationEntry)
	addRow(frontAlignBox, "Import Rot:", ps.frontAutoRotEntry)
	addRow(frontAlignBox, "Rot Center X:", ps.frontRotCenterXEntry)
	addRow(frontAlignBox, "Rot Center Y:", ps.frontRotCenterYEntry)
	addRow(frontAlignBox, "Scale X:", ps.frontAutoScaleXEntry)
	addRow(frontAlignBox, "Scale Y:", ps.frontAutoScaleYEntry)
	addRow(frontAlignBox, "Shear Top X:", ps.frontShearTopXEntry)
	addRow(frontAlignBox, "Shear Bot X:", ps.frontShearBottomXEntry)
	addRow(frontAlignBox, "Shear Left Y:", ps.frontShearLeftYEntry)
	addRow(frontAlignBox, "Shear Right Y:", ps.frontShearRightYEntry)

	// Back Image info
	backInfoBox := addFrame("Back Image")
	ps.backFileBtn, _ = gtk.ButtonNewWithLabel("(none)")
	ps.backFileBtn.Connect("clicked", func() { ps.onBrowseBackFile() })
	ps.backCropLabel, _ = gtk.LabelNew("")
	ps.backCropLabel.SetHAlign(gtk.ALIGN_START)
	addBtnRow(backInfoBox, "File:", ps.backFileBtn)
	addLabelRow(backInfoBox, "Crop:", ps.backCropLabel)

	// Back Alignment
	backAlignBox := addFrame("Back Alignment")
	ps.backOffsetXEntry = newEntry()
	ps.backOffsetXEntry.Connect("changed", func() {
		t, _ := ps.backOffsetXEntry.GetText()
		ps.onIntChanged(t, &ps.state.BackManualOffset.X)
	})
	ps.backOffsetYEntry = newEntry()
	ps.backOffsetYEntry.Connect("changed", func() {
		t, _ := ps.backOffsetYEntry.GetText()
		ps.onIntChanged(t, &ps.state.BackManualOffset.Y)
	})
	ps.backRotationEntry = newEntry()
	ps.backRotationEntry.Connect("changed", func() {
		t, _ := ps.backRotationEntry.GetText()
		ps.onFloatChanged(t, &ps.state.BackManualRotation)
	})
	ps.backAutoRotEntry = newEntry()
	ps.backAutoRotEntry.Connect("changed", func() {
		t, _ := ps.backAutoRotEntry.GetText()
		ps.onFloatChanged(t, &ps.state.BackAutoRotation)
	})
	ps.backRotCenterXEntry = newEntry()
	ps.backRotCenterXEntry.Connect("changed", func() {
		t, _ := ps.backRotCenterXEntry.GetText()
		ps.onFloatChanged(t, &ps.state.BackRotationCenter.X)
	})
	ps.backRotCenterYEntry = newEntry()
	ps.backRotCenterYEntry.Connect("changed", func() {
		t, _ := ps.backRotCenterYEntry.GetText()
		ps.onFloatChanged(t, &ps.state.BackRotationCenter.Y)
	})
	ps.backAutoScaleXEntry = newEntry()
	ps.backAutoScaleXEntry.Connect("changed", func() {
		t, _ := ps.backAutoScaleXEntry.GetText()
		ps.onFloatChanged(t, &ps.state.BackAutoScaleX)
	})
	ps.backAutoScaleYEntry = newEntry()
	ps.backAutoScaleYEntry.Connect("changed", func() {
		t, _ := ps.backAutoScaleYEntry.GetText()
		ps.onFloatChanged(t, &ps.state.BackAutoScaleY)
	})
	ps.backShearTopXEntry = newEntry()
	ps.backShearTopXEntry.Connect("changed", func() {
		t, _ := ps.backShearTopXEntry.GetText()
		ps.onFloatChanged(t, &ps.state.BackShearTopX)
	})
	ps.backShearBottomXEntry = newEntry()
	ps.backShearBottomXEntry.Connect("changed", func() {
		t, _ := ps.backShearBottomXEntry.GetText()
		ps.onFloatChanged(t, &ps.state.BackShearBottomX)
	})
	ps.backShearLeftYEntry = newEntry()
	ps.backShearLeftYEntry.Connect("changed", func() {
		t, _ := ps.backShearLeftYEntry.GetText()
		ps.onFloatChanged(t, &ps.state.BackShearLeftY)
	})
	ps.backShearRightYEntry = newEntry()
	ps.backShearRightYEntry.Connect("changed", func() {
		t, _ := ps.backShearRightYEntry.GetText()
		ps.onFloatChanged(t, &ps.state.BackShearRightY)
	})

	addRow(backAlignBox, "Offset X:", ps.backOffsetXEntry)
	addRow(backAlignBox, "Offset Y:", ps.backOffsetYEntry)
	addRow(backAlignBox, "Rotation:", ps.backRotationEntry)
	addRow(backAlignBox, "Import Rot:", ps.backAutoRotEntry)
	addRow(backAlignBox, "Rot Center X:", ps.backRotCenterXEntry)
	addRow(backAlignBox, "Rot Center Y:", ps.backRotCenterYEntry)
	addRow(backAlignBox, "Scale X:", ps.backAutoScaleXEntry)
	addRow(backAlignBox, "Scale Y:", ps.backAutoScaleYEntry)
	addRow(backAlignBox, "Shear Top X:", ps.backShearTopXEntry)
	addRow(backAlignBox, "Shear Bot X:", ps.backShearBottomXEntry)
	addRow(backAlignBox, "Shear Left Y:", ps.backShearLeftYEntry)
	addRow(backAlignBox, "Shear Right Y:", ps.backShearRightYEntry)

	// Buttons
	btnBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	applyBtn, _ := gtk.ButtonNewWithLabel("Apply All")
	applyBtn.Connect("clicked", func() { ps.applyAll() })
	clearBtn, _ := gtk.ButtonNewWithLabel("Clear Manual")
	clearBtn.Connect("clicked", func() { ps.clearManual() })
	btnBox.PackStart(applyBtn, false, false, 0)
	btnBox.PackStart(clearBtn, false, false, 0)
	content.PackStart(btnBox, false, false, 4)

	scrollWin.Add(content)

	// Wrap in a box so Widget() returns a consistent type
	ps.box, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	ps.box.PackStart(scrollWin, true, true, 0)
}

func (ps *PropertySheet) refresh() {
	ps.dpiEntry.SetText(fmt.Sprintf("%.1f", ps.state.DPI))

	if ps.state.FrontImage != nil {
		ps.frontFileBtn.SetLabel(filepath.Base(ps.state.FrontImage.Path))
		crop := ps.state.FrontCropBounds
		if crop.Width > 0 && crop.Height > 0 {
			ps.frontCropLabel.SetText(fmt.Sprintf("%d,%d %dx%d", crop.X, crop.Y, crop.Width, crop.Height))
		} else {
			ps.frontCropLabel.SetText("(none)")
		}
	} else {
		ps.frontFileBtn.SetLabel("(none)")
		ps.frontCropLabel.SetText("(none)")
	}

	ps.frontOffsetXEntry.SetText(strconv.Itoa(ps.state.FrontManualOffset.X))
	ps.frontOffsetYEntry.SetText(strconv.Itoa(ps.state.FrontManualOffset.Y))
	ps.frontRotationEntry.SetText(fmt.Sprintf("%.4f", ps.state.FrontManualRotation))
	ps.frontAutoRotEntry.SetText(fmt.Sprintf("%.4f", ps.state.FrontAutoRotation))
	ps.frontRotCenterXEntry.SetText(fmt.Sprintf("%.1f", ps.state.FrontRotationCenter.X))
	ps.frontRotCenterYEntry.SetText(fmt.Sprintf("%.1f", ps.state.FrontRotationCenter.Y))
	ps.frontAutoScaleXEntry.SetText(fmt.Sprintf("%.6f", ps.state.FrontAutoScaleX))
	ps.frontAutoScaleYEntry.SetText(fmt.Sprintf("%.6f", ps.state.FrontAutoScaleY))
	ps.frontShearTopXEntry.SetText(fmt.Sprintf("%.6f", ps.state.FrontShearTopX))
	ps.frontShearBottomXEntry.SetText(fmt.Sprintf("%.6f", ps.state.FrontShearBottomX))
	ps.frontShearLeftYEntry.SetText(fmt.Sprintf("%.6f", ps.state.FrontShearLeftY))
	ps.frontShearRightYEntry.SetText(fmt.Sprintf("%.6f", ps.state.FrontShearRightY))

	if ps.state.BackImage != nil {
		ps.backFileBtn.SetLabel(filepath.Base(ps.state.BackImage.Path))
		crop := ps.state.BackCropBounds
		if crop.Width > 0 && crop.Height > 0 {
			ps.backCropLabel.SetText(fmt.Sprintf("%d,%d %dx%d", crop.X, crop.Y, crop.Width, crop.Height))
		} else {
			ps.backCropLabel.SetText("(none)")
		}
	} else {
		ps.backFileBtn.SetLabel("(none)")
		ps.backCropLabel.SetText("(none)")
	}

	ps.backOffsetXEntry.SetText(strconv.Itoa(ps.state.BackManualOffset.X))
	ps.backOffsetYEntry.SetText(strconv.Itoa(ps.state.BackManualOffset.Y))
	ps.backRotationEntry.SetText(fmt.Sprintf("%.4f", ps.state.BackManualRotation))
	ps.backAutoRotEntry.SetText(fmt.Sprintf("%.4f", ps.state.BackAutoRotation))
	ps.backRotCenterXEntry.SetText(fmt.Sprintf("%.1f", ps.state.BackRotationCenter.X))
	ps.backRotCenterYEntry.SetText(fmt.Sprintf("%.1f", ps.state.BackRotationCenter.Y))
	ps.backAutoScaleXEntry.SetText(fmt.Sprintf("%.6f", ps.state.BackAutoScaleX))
	ps.backAutoScaleYEntry.SetText(fmt.Sprintf("%.6f", ps.state.BackAutoScaleY))
	ps.backShearTopXEntry.SetText(fmt.Sprintf("%.6f", ps.state.BackShearTopX))
	ps.backShearBottomXEntry.SetText(fmt.Sprintf("%.6f", ps.state.BackShearBottomX))
	ps.backShearLeftYEntry.SetText(fmt.Sprintf("%.6f", ps.state.BackShearLeftY))
	ps.backShearRightYEntry.SetText(fmt.Sprintf("%.6f", ps.state.BackShearRightY))
}

func (ps *PropertySheet) onDPIChanged() {
	t, _ := ps.dpiEntry.GetText()
	if v, err := strconv.ParseFloat(t, 64); err == nil && v > 0 {
		ps.state.DPI = v
		ps.state.SetModified(true)
	}
}

func (ps *PropertySheet) onIntChanged(s string, target *int) {
	if v, err := strconv.Atoi(s); err == nil {
		*target = v
		ps.state.SetModified(true)
	}
}

func (ps *PropertySheet) onFloatChanged(s string, target *float64) {
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		*target = v
		ps.state.SetModified(true)
	}
}

func (ps *PropertySheet) clearManual() {
	ps.state.FrontManualOffset = geometry.PointInt{}
	ps.state.FrontManualRotation = 0
	ps.state.FrontShearTopX = 1.0
	ps.state.FrontShearBottomX = 1.0
	ps.state.FrontShearLeftY = 1.0
	ps.state.FrontShearRightY = 1.0

	ps.state.BackManualOffset = geometry.PointInt{}
	ps.state.BackManualRotation = 0
	ps.state.BackShearTopX = 1.0
	ps.state.BackShearBottomX = 1.0
	ps.state.BackShearLeftY = 1.0
	ps.state.BackShearRightY = 1.0

	ps.applyAll()
	ps.refresh()
}

func (ps *PropertySheet) applyAll() {
	if ps.state.FrontImage != nil {
		ps.state.FrontImage.ManualOffsetX = ps.state.FrontManualOffset.X
		ps.state.FrontImage.ManualOffsetY = ps.state.FrontManualOffset.Y
		ps.state.FrontImage.ManualRotation = ps.state.FrontManualRotation
		ps.state.FrontImage.ShearTopX = ps.state.FrontShearTopX
		ps.state.FrontImage.ShearBottomX = ps.state.FrontShearBottomX
		ps.state.FrontImage.ShearLeftY = ps.state.FrontShearLeftY
		ps.state.FrontImage.ShearRightY = ps.state.FrontShearRightY
		ps.state.FrontImage.AutoRotation = ps.state.FrontAutoRotation
		ps.state.FrontImage.AutoScaleX = ps.state.FrontAutoScaleX
		ps.state.FrontImage.AutoScaleY = ps.state.FrontAutoScaleY
		ps.state.FrontImage.RotationCenterX = ps.state.FrontRotationCenter.X
		ps.state.FrontImage.RotationCenterY = ps.state.FrontRotationCenter.Y
		ensureShearDefaults(ps.state.FrontImage)
	}

	if ps.state.BackImage != nil {
		ps.state.BackImage.ManualOffsetX = ps.state.BackManualOffset.X
		ps.state.BackImage.ManualOffsetY = ps.state.BackManualOffset.Y
		ps.state.BackImage.ManualRotation = ps.state.BackManualRotation
		ps.state.BackImage.ShearTopX = ps.state.BackShearTopX
		ps.state.BackImage.ShearBottomX = ps.state.BackShearBottomX
		ps.state.BackImage.ShearLeftY = ps.state.BackShearLeftY
		ps.state.BackImage.ShearRightY = ps.state.BackShearRightY
		ps.state.BackImage.AutoRotation = ps.state.BackAutoRotation
		ps.state.BackImage.AutoScaleX = ps.state.BackAutoScaleX
		ps.state.BackImage.AutoScaleY = ps.state.BackAutoScaleY
		ps.state.BackImage.RotationCenterX = ps.state.BackRotationCenter.X
		ps.state.BackImage.RotationCenterY = ps.state.BackRotationCenter.Y
		ensureShearDefaults(ps.state.BackImage)
	}

	ps.state.SetModified(true)
	ps.canvas.Refresh()

	if ps.onUpdate != nil {
		ps.onUpdate()
	}
}

func ensureShearDefaults(img *pcbimage.Layer) {
	if img.ShearTopX == 0 {
		img.ShearTopX = 1.0
	}
	if img.ShearBottomX == 0 {
		img.ShearBottomX = 1.0
	}
	if img.ShearLeftY == 0 {
		img.ShearLeftY = 1.0
	}
	if img.ShearRightY == 0 {
		img.ShearRightY = 1.0
	}
	if img.AutoScaleX == 0 {
		img.AutoScaleX = 1.0
	}
	if img.AutoScaleY == 0 {
		img.AutoScaleY = 1.0
	}
}

// Refresh re-reads values from state.
func (ps *PropertySheet) Refresh() {
	ps.refresh()
}

// SetRotationCenter sets the rotation center for a side.
func (ps *PropertySheet) SetRotationCenter(isFront bool, center geometry.Point2D) {
	if isFront {
		ps.state.FrontRotationCenter = center
		ps.frontRotCenterXEntry.SetText(fmt.Sprintf("%.1f", center.X))
		ps.frontRotCenterYEntry.SetText(fmt.Sprintf("%.1f", center.Y))
	} else {
		ps.state.BackRotationCenter = center
		ps.backRotCenterXEntry.SetText(fmt.Sprintf("%.1f", center.X))
		ps.backRotCenterYEntry.SetText(fmt.Sprintf("%.1f", center.Y))
	}
}

func (ps *PropertySheet) onBrowseFrontFile() {
	dlg, _ := gtk.FileChooserDialogNewWith2Buttons("Open Front Image", ps.win,
		gtk.FILE_CHOOSER_ACTION_OPEN,
		"Cancel", gtk.RESPONSE_CANCEL,
		"Open", gtk.RESPONSE_ACCEPT)

	filter, _ := gtk.FileFilterNew()
	filter.SetName("Images")
	for _, ext := range pcbimage.SupportedFormats() {
		filter.AddPattern("*" + ext)
	}
	dlg.AddFilter(filter)

	response := dlg.Run()
	if response == gtk.RESPONSE_ACCEPT {
		path := dlg.GetFilename()
		if pcbimage.IsSupportedFormat(path) {
			if err := ps.state.ImportFrontImage(path); err != nil {
				fmt.Printf("Error importing front image: %v\n", err)
			} else if ps.onUpdate != nil {
				ps.onUpdate()
			}
		}
	}
	dlg.Destroy()
}

func (ps *PropertySheet) onBrowseBackFile() {
	dlg, _ := gtk.FileChooserDialogNewWith2Buttons("Open Back Image", ps.win,
		gtk.FILE_CHOOSER_ACTION_OPEN,
		"Cancel", gtk.RESPONSE_CANCEL,
		"Open", gtk.RESPONSE_ACCEPT)

	filter, _ := gtk.FileFilterNew()
	filter.SetName("Images")
	for _, ext := range pcbimage.SupportedFormats() {
		filter.AddPattern("*" + ext)
	}
	dlg.AddFilter(filter)

	response := dlg.Run()
	if response == gtk.RESPONSE_ACCEPT {
		path := dlg.GetFilename()
		if pcbimage.IsSupportedFormat(path) {
			if err := ps.state.ImportBackImage(path); err != nil {
				fmt.Printf("Error importing back image: %v\n", err)
			} else if ps.onUpdate != nil {
				ps.onUpdate()
			}
		}
	}
	dlg.Destroy()
}
