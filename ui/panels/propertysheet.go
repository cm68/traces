// Package panels provides UI panels for the application.
package panels

import (
	"fmt"
	"path/filepath"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"pcb-tracer/internal/app"
	"pcb-tracer/internal/image"
	"pcb-tracer/pkg/geometry"
	"pcb-tracer/ui/canvas"
)

// PropertySheet displays and allows editing of all project properties.
type PropertySheet struct {
	state     *app.State
	canvas    *canvas.ImageCanvas
	container fyne.CanvasObject
	window    fyne.Window

	// Callbacks for refresh
	onUpdate func()

	// Project info entries
	dpiEntry *widget.Entry

	// Front file info - button with label and browse capability
	frontFileBtn   *widget.Button
	frontCropLabel *widget.Label

	// Front side entries
	frontOffsetXEntry      *widget.Entry
	frontOffsetYEntry      *widget.Entry
	frontRotationEntry     *widget.Entry
	frontShearTopXEntry    *widget.Entry
	frontShearBottomXEntry *widget.Entry
	frontShearLeftYEntry   *widget.Entry
	frontShearRightYEntry  *widget.Entry
	frontAutoRotEntry      *widget.Entry
	frontAutoScaleXEntry   *widget.Entry
	frontAutoScaleYEntry   *widget.Entry
	frontRotCenterXEntry   *widget.Entry
	frontRotCenterYEntry   *widget.Entry

	// Back file info - button with label and browse capability
	backFileBtn   *widget.Button
	backCropLabel *widget.Label

	// Back side entries
	backOffsetXEntry      *widget.Entry
	backOffsetYEntry      *widget.Entry
	backRotationEntry     *widget.Entry
	backShearTopXEntry    *widget.Entry
	backShearBottomXEntry *widget.Entry
	backShearLeftYEntry   *widget.Entry
	backShearRightYEntry  *widget.Entry
	backAutoRotEntry      *widget.Entry
	backAutoScaleXEntry   *widget.Entry
	backAutoScaleYEntry   *widget.Entry
	backRotCenterXEntry   *widget.Entry
	backRotCenterYEntry   *widget.Entry
}

// NewPropertySheet creates a new property sheet panel.
func NewPropertySheet(state *app.State, cvs *canvas.ImageCanvas, onUpdate func()) *PropertySheet {
	ps := &PropertySheet{
		state:    state,
		canvas:   cvs,
		onUpdate: onUpdate,
	}

	ps.buildUI()
	ps.refresh()

	// Subscribe to state changes
	state.On(app.EventImageLoaded, func(_ interface{}) { ps.refresh() })
	state.On(app.EventAlignmentComplete, func(_ interface{}) { ps.refresh() })
	state.On(app.EventProjectLoaded, func(_ interface{}) { ps.refresh() })

	return ps
}

// Container returns the panel's container.
func (ps *PropertySheet) Container() fyne.CanvasObject {
	return ps.container
}

// buildUI creates the property sheet UI.
func (ps *PropertySheet) buildUI() {
	// Project info section
	ps.dpiEntry = widget.NewEntry()
	ps.dpiEntry.OnChanged = ps.onDPIChanged

	projectInfo := widget.NewCard("Project", "",
		container.NewVBox(
			ps.makeFormRow("DPI:", ps.dpiEntry),
		),
	)

	// Front file info - clickable button to browse for file
	ps.frontFileBtn = widget.NewButton("(none)", ps.onBrowseFrontFile)
	ps.frontFileBtn.Alignment = widget.ButtonAlignLeading
	ps.frontCropLabel = widget.NewLabel("")

	frontInfo := widget.NewCard("Front Image", "",
		container.NewVBox(
			ps.makeButtonRow("File:", ps.frontFileBtn),
			ps.makeLabelRow("Crop:", ps.frontCropLabel),
		),
	)

	// Front alignment parameters (auto-alignment seeds these, all editable)
	ps.frontOffsetXEntry = widget.NewEntry()
	ps.frontOffsetXEntry.OnChanged = func(s string) { ps.onIntChanged(s, &ps.state.FrontManualOffset.X, true) }
	ps.frontOffsetYEntry = widget.NewEntry()
	ps.frontOffsetYEntry.OnChanged = func(s string) { ps.onIntChanged(s, &ps.state.FrontManualOffset.Y, true) }
	ps.frontRotationEntry = widget.NewEntry()
	ps.frontRotationEntry.OnChanged = func(s string) { ps.onFloatChanged(s, &ps.state.FrontManualRotation, true) }
	ps.frontShearTopXEntry = widget.NewEntry()
	ps.frontShearTopXEntry.OnChanged = func(s string) { ps.onFloatChanged(s, &ps.state.FrontShearTopX, true) }
	ps.frontShearBottomXEntry = widget.NewEntry()
	ps.frontShearBottomXEntry.OnChanged = func(s string) { ps.onFloatChanged(s, &ps.state.FrontShearBottomX, true) }
	ps.frontShearLeftYEntry = widget.NewEntry()
	ps.frontShearLeftYEntry.OnChanged = func(s string) { ps.onFloatChanged(s, &ps.state.FrontShearLeftY, true) }
	ps.frontShearRightYEntry = widget.NewEntry()
	ps.frontShearRightYEntry.OnChanged = func(s string) { ps.onFloatChanged(s, &ps.state.FrontShearRightY, true) }
	ps.frontAutoRotEntry = widget.NewEntry()
	ps.frontAutoRotEntry.OnChanged = func(s string) { ps.onFloatChanged(s, &ps.state.FrontAutoRotation, true) }
	ps.frontAutoScaleXEntry = widget.NewEntry()
	ps.frontAutoScaleXEntry.OnChanged = func(s string) { ps.onFloatChanged(s, &ps.state.FrontAutoScaleX, true) }
	ps.frontAutoScaleYEntry = widget.NewEntry()
	ps.frontAutoScaleYEntry.OnChanged = func(s string) { ps.onFloatChanged(s, &ps.state.FrontAutoScaleY, true) }
	ps.frontRotCenterXEntry = widget.NewEntry()
	ps.frontRotCenterXEntry.OnChanged = func(s string) { ps.onFloatChanged(s, &ps.state.FrontRotationCenter.X, true) }
	ps.frontRotCenterYEntry = widget.NewEntry()
	ps.frontRotCenterYEntry.OnChanged = func(s string) { ps.onFloatChanged(s, &ps.state.FrontRotationCenter.Y, true) }

	frontAlignment := widget.NewCard("Front Alignment", "",
		container.NewVBox(
			ps.makeFormRow("Offset X:", ps.frontOffsetXEntry),
			ps.makeFormRow("Offset Y:", ps.frontOffsetYEntry),
			ps.makeFormRow("Rotation:", ps.frontRotationEntry),
			ps.makeFormRow("Import Rot:", ps.frontAutoRotEntry),
			ps.makeFormRow("Rot Center X:", ps.frontRotCenterXEntry),
			ps.makeFormRow("Rot Center Y:", ps.frontRotCenterYEntry),
			ps.makeFormRow("Scale X:", ps.frontAutoScaleXEntry),
			ps.makeFormRow("Scale Y:", ps.frontAutoScaleYEntry),
			ps.makeFormRow("Shear Top X:", ps.frontShearTopXEntry),
			ps.makeFormRow("Shear Bot X:", ps.frontShearBottomXEntry),
			ps.makeFormRow("Shear Left Y:", ps.frontShearLeftYEntry),
			ps.makeFormRow("Shear Right Y:", ps.frontShearRightYEntry),
		),
	)

	// Back file info - clickable button to browse for file
	ps.backFileBtn = widget.NewButton("(none)", ps.onBrowseBackFile)
	ps.backFileBtn.Alignment = widget.ButtonAlignLeading
	ps.backCropLabel = widget.NewLabel("")

	backInfo := widget.NewCard("Back Image", "",
		container.NewVBox(
			ps.makeButtonRow("File:", ps.backFileBtn),
			ps.makeLabelRow("Crop:", ps.backCropLabel),
		),
	)

	// Back alignment parameters (auto-alignment seeds these, all editable)
	ps.backOffsetXEntry = widget.NewEntry()
	ps.backOffsetXEntry.OnChanged = func(s string) { ps.onIntChanged(s, &ps.state.BackManualOffset.X, false) }
	ps.backOffsetYEntry = widget.NewEntry()
	ps.backOffsetYEntry.OnChanged = func(s string) { ps.onIntChanged(s, &ps.state.BackManualOffset.Y, false) }
	ps.backRotationEntry = widget.NewEntry()
	ps.backRotationEntry.OnChanged = func(s string) { ps.onFloatChanged(s, &ps.state.BackManualRotation, false) }
	ps.backShearTopXEntry = widget.NewEntry()
	ps.backShearTopXEntry.OnChanged = func(s string) { ps.onFloatChanged(s, &ps.state.BackShearTopX, false) }
	ps.backShearBottomXEntry = widget.NewEntry()
	ps.backShearBottomXEntry.OnChanged = func(s string) { ps.onFloatChanged(s, &ps.state.BackShearBottomX, false) }
	ps.backShearLeftYEntry = widget.NewEntry()
	ps.backShearLeftYEntry.OnChanged = func(s string) { ps.onFloatChanged(s, &ps.state.BackShearLeftY, false) }
	ps.backShearRightYEntry = widget.NewEntry()
	ps.backShearRightYEntry.OnChanged = func(s string) { ps.onFloatChanged(s, &ps.state.BackShearRightY, false) }
	ps.backAutoRotEntry = widget.NewEntry()
	ps.backAutoRotEntry.OnChanged = func(s string) { ps.onFloatChanged(s, &ps.state.BackAutoRotation, false) }
	ps.backAutoScaleXEntry = widget.NewEntry()
	ps.backAutoScaleXEntry.OnChanged = func(s string) { ps.onFloatChanged(s, &ps.state.BackAutoScaleX, false) }
	ps.backAutoScaleYEntry = widget.NewEntry()
	ps.backAutoScaleYEntry.OnChanged = func(s string) { ps.onFloatChanged(s, &ps.state.BackAutoScaleY, false) }
	ps.backRotCenterXEntry = widget.NewEntry()
	ps.backRotCenterXEntry.OnChanged = func(s string) { ps.onFloatChanged(s, &ps.state.BackRotationCenter.X, false) }
	ps.backRotCenterYEntry = widget.NewEntry()
	ps.backRotCenterYEntry.OnChanged = func(s string) { ps.onFloatChanged(s, &ps.state.BackRotationCenter.Y, false) }

	backAlignment := widget.NewCard("Back Alignment", "",
		container.NewVBox(
			ps.makeFormRow("Offset X:", ps.backOffsetXEntry),
			ps.makeFormRow("Offset Y:", ps.backOffsetYEntry),
			ps.makeFormRow("Rotation:", ps.backRotationEntry),
			ps.makeFormRow("Import Rot:", ps.backAutoRotEntry),
			ps.makeFormRow("Rot Center X:", ps.backRotCenterXEntry),
			ps.makeFormRow("Rot Center Y:", ps.backRotCenterYEntry),
			ps.makeFormRow("Scale X:", ps.backAutoScaleXEntry),
			ps.makeFormRow("Scale Y:", ps.backAutoScaleYEntry),
			ps.makeFormRow("Shear Top X:", ps.backShearTopXEntry),
			ps.makeFormRow("Shear Bot X:", ps.backShearBottomXEntry),
			ps.makeFormRow("Shear Left Y:", ps.backShearLeftYEntry),
			ps.makeFormRow("Shear Right Y:", ps.backShearRightYEntry),
		),
	)

	// Buttons
	applyBtn := widget.NewButton("Apply All", ps.applyAll)
	clearBtn := widget.NewButton("Clear Manual", ps.clearManual)

	buttons := container.NewHBox(applyBtn, clearBtn)

	// Combine into scrollable container
	content := container.NewVBox(
		projectInfo,
		frontInfo,
		frontAlignment,
		backInfo,
		backAlignment,
		buttons,
	)

	ps.container = container.NewVScroll(content)
}

// makeFormRow creates a labeled form row with an editable entry.
func (ps *PropertySheet) makeFormRow(label string, entry *widget.Entry) fyne.CanvasObject {
	entry.Wrapping = fyne.TextWrapOff
	lbl := widget.NewLabel(label)
	lbl.Alignment = fyne.TextAlignTrailing
	return container.NewBorder(nil, nil, lbl, nil, entry)
}

// makeLabelRow creates a labeled row with a read-only label.
func (ps *PropertySheet) makeLabelRow(label string, value *widget.Label) fyne.CanvasObject {
	lbl := widget.NewLabel(label)
	lbl.Alignment = fyne.TextAlignTrailing
	return container.NewBorder(nil, nil, lbl, nil, value)
}

// makeButtonRow creates a labeled row with a button.
func (ps *PropertySheet) makeButtonRow(label string, btn *widget.Button) fyne.CanvasObject {
	lbl := widget.NewLabel(label)
	lbl.Alignment = fyne.TextAlignTrailing
	return container.NewBorder(nil, nil, lbl, nil, btn)
}

// refresh updates all entries from state.
func (ps *PropertySheet) refresh() {
	// Project info
	ps.dpiEntry.SetText(fmt.Sprintf("%.1f", ps.state.DPI))

	// Front file/crop info
	if ps.state.FrontImage != nil {
		ps.frontFileBtn.SetText(filepath.Base(ps.state.FrontImage.Path))
		crop := ps.state.FrontCropBounds
		if crop.Width > 0 && crop.Height > 0 {
			ps.frontCropLabel.SetText(fmt.Sprintf("%d,%d %dx%d", crop.X, crop.Y, crop.Width, crop.Height))
		} else {
			ps.frontCropLabel.SetText("(none)")
		}
	} else {
		ps.frontFileBtn.SetText("(none)")
		ps.frontCropLabel.SetText("(none)")
	}

	// Front alignment
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

	// Back file/crop info
	if ps.state.BackImage != nil {
		ps.backFileBtn.SetText(filepath.Base(ps.state.BackImage.Path))
		crop := ps.state.BackCropBounds
		if crop.Width > 0 && crop.Height > 0 {
			ps.backCropLabel.SetText(fmt.Sprintf("%d,%d %dx%d", crop.X, crop.Y, crop.Width, crop.Height))
		} else {
			ps.backCropLabel.SetText("(none)")
		}
	} else {
		ps.backFileBtn.SetText("(none)")
		ps.backCropLabel.SetText("(none)")
	}

	// Back alignment
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

// onDPIChanged handles DPI entry changes.
func (ps *PropertySheet) onDPIChanged(s string) {
	if v, err := strconv.ParseFloat(s, 64); err == nil && v > 0 {
		ps.state.DPI = v
		ps.state.SetModified(true)
	}
}

// onIntChanged handles integer entry changes.
func (ps *PropertySheet) onIntChanged(s string, target *int, isFront bool) {
	if v, err := strconv.Atoi(s); err == nil {
		*target = v
		ps.state.SetModified(true)
	}
}

// onFloatChanged handles float entry changes.
func (ps *PropertySheet) onFloatChanged(s string, target *float64, isFront bool) {
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		*target = v
		ps.state.SetModified(true)
	}
}

// clearManual resets all manual adjustment settings to defaults.
func (ps *PropertySheet) clearManual() {
	// Reset front manual settings
	ps.state.FrontManualOffset = geometry.PointInt{}
	ps.state.FrontManualRotation = 0
	ps.state.FrontShearTopX = 1.0
	ps.state.FrontShearBottomX = 1.0
	ps.state.FrontShearLeftY = 1.0
	ps.state.FrontShearRightY = 1.0

	// Reset back manual settings
	ps.state.BackManualOffset = geometry.PointInt{}
	ps.state.BackManualRotation = 0
	ps.state.BackShearTopX = 1.0
	ps.state.BackShearBottomX = 1.0
	ps.state.BackShearLeftY = 1.0
	ps.state.BackShearRightY = 1.0

	// Apply to layers
	ps.applyAll()

	// Refresh UI
	ps.refresh()
}

// applyAll applies all current values to the layers and refreshes the canvas.
func (ps *PropertySheet) applyAll() {
	// Apply to front image
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
		// Ensure defaults
		if ps.state.FrontImage.ShearTopX == 0 {
			ps.state.FrontImage.ShearTopX = 1.0
		}
		if ps.state.FrontImage.ShearBottomX == 0 {
			ps.state.FrontImage.ShearBottomX = 1.0
		}
		if ps.state.FrontImage.ShearLeftY == 0 {
			ps.state.FrontImage.ShearLeftY = 1.0
		}
		if ps.state.FrontImage.ShearRightY == 0 {
			ps.state.FrontImage.ShearRightY = 1.0
		}
		if ps.state.FrontImage.AutoScaleX == 0 {
			ps.state.FrontImage.AutoScaleX = 1.0
		}
		if ps.state.FrontImage.AutoScaleY == 0 {
			ps.state.FrontImage.AutoScaleY = 1.0
		}
	}

	// Apply to back image
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
		// Ensure defaults
		if ps.state.BackImage.ShearTopX == 0 {
			ps.state.BackImage.ShearTopX = 1.0
		}
		if ps.state.BackImage.ShearBottomX == 0 {
			ps.state.BackImage.ShearBottomX = 1.0
		}
		if ps.state.BackImage.ShearLeftY == 0 {
			ps.state.BackImage.ShearLeftY = 1.0
		}
		if ps.state.BackImage.ShearRightY == 0 {
			ps.state.BackImage.ShearRightY = 1.0
		}
		if ps.state.BackImage.AutoScaleX == 0 {
			ps.state.BackImage.AutoScaleX = 1.0
		}
		if ps.state.BackImage.AutoScaleY == 0 {
			ps.state.BackImage.AutoScaleY = 1.0
		}
	}

	ps.state.SetModified(true)
	ps.canvas.Refresh()

	if ps.onUpdate != nil {
		ps.onUpdate()
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

// SetWindow sets the parent window for file dialogs.
func (ps *PropertySheet) SetWindow(w fyne.Window) {
	ps.window = w
}

// onBrowseFrontFile opens a file dialog to select a front image.
// Uses ImportFrontImage to run auto-detection on the new image.
func (ps *PropertySheet) onBrowseFrontFile() {
	if ps.window == nil {
		return
	}

	fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil || reader == nil {
			return
		}
		reader.Close()

		path := reader.URI().Path()
		if !image.IsSupportedFormat(path) {
			dialog.ShowError(fmt.Errorf("unsupported image format"), ps.window)
			return
		}

		// Use ImportFrontImage to run auto-detection on the new image
		if err := ps.state.ImportFrontImage(path); err != nil {
			dialog.ShowError(err, ps.window)
			return
		}

		// Sync layers to canvas
		if ps.onUpdate != nil {
			ps.onUpdate()
		}
	}, ps.window)

	// Set file filter for images
	fd.SetFilter(storage.NewExtensionFileFilter(image.SupportedFormats()))
	fd.Show()
}

// onBrowseBackFile opens a file dialog to select a back image.
// Uses ImportBackImage to run auto-detection on the new image.
func (ps *PropertySheet) onBrowseBackFile() {
	if ps.window == nil {
		return
	}

	fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil || reader == nil {
			return
		}
		reader.Close()

		path := reader.URI().Path()
		if !image.IsSupportedFormat(path) {
			dialog.ShowError(fmt.Errorf("unsupported image format"), ps.window)
			return
		}

		// Use ImportBackImage to run auto-detection on the new image
		if err := ps.state.ImportBackImage(path); err != nil {
			dialog.ShowError(err, ps.window)
			return
		}

		// Sync layers to canvas
		if ps.onUpdate != nil {
			ps.onUpdate()
		}
	}, ps.window)

	// Set file filter for images
	fd.SetFilter(storage.NewExtensionFileFilter(image.SupportedFormats()))
	fd.Show()
}
