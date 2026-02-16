// Package panels provides UI panels for the application.
package panels

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"pcb-tracer/internal/alignment"
	"pcb-tracer/internal/app"
	"pcb-tracer/internal/board"
	"pcb-tracer/internal/component"
	"pcb-tracer/internal/datecode"
	pcbimage "pcb-tracer/internal/image"
	"pcb-tracer/internal/logo"
	"pcb-tracer/internal/ocr"
	"pcb-tracer/internal/via"
	"pcb-tracer/pkg/colorutil"
	"pcb-tracer/pkg/geometry"
	"pcb-tracer/ui/canvas"
	"pcb-tracer/ui/dialogs"

	"fyne.io/fyne/v2"
	fynecanvas "fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"gocv.io/x/gocv"
)

// Panel names for ShowPanel method.
const (
	PanelImport     = "import"
	PanelComponents = "components"
	PanelTraces     = "traces"
	PanelProperties = "properties"
	PanelLogos      = "logos"
)

// SidePanel provides the main side panel with switchable views.
type SidePanel struct {
	state     *app.State
	canvas    *canvas.ImageCanvas
	container *fyne.Container

	// Individual panels
	importPanel      *ImportPanel
	componentsPanel  *ComponentsPanel
	tracesPanel      *TracesPanel
	propertiesPanel  *PropertySheet
	logosPanel       *LogosPanel

	// Currently visible panel name
	currentPanel string

	// Panel enable/disable (panels disabled until normalization complete)
	disabledPanels map[string]bool
}

// NewSidePanel creates a new side panel.
func NewSidePanel(state *app.State, canvas *canvas.ImageCanvas) *SidePanel {
	sp := &SidePanel{
		state:          state,
		canvas:         canvas,
		currentPanel:   PanelImport,
		disabledPanels: make(map[string]bool),
	}

	// Create individual panels
	sp.importPanel = NewImportPanel(state, canvas)
	sp.componentsPanel = NewComponentsPanel(state, canvas)
	sp.tracesPanel = NewTracesPanel(state, canvas)
	sp.propertiesPanel = NewPropertySheet(state, canvas, func() {
		// Callback when properties are applied - refresh import panel labels
		sp.importPanel.RefreshLabels()
	})
	sp.logosPanel = NewLogosPanel(state, canvas)

	// Give import panel a reference to the side panel for enabling/disabling
	sp.importPanel.sidePanel = sp

	// Create container showing import panel by default
	sp.container = container.NewStack(sp.importPanel.Container())

	// Start with non-alignment panels disabled
	sp.SetPanelEnabled(PanelComponents, false)
	sp.SetPanelEnabled(PanelTraces, false)
	sp.SetPanelEnabled(PanelProperties, false)
	sp.SetPanelEnabled(PanelLogos, false)

	// After project load, enable panels if normalized images exist
	state.On(app.EventProjectLoaded, func(_ interface{}) {
		sp.updatePanelEnableState()
		sp.importPanel.updateAlignmentUI()
	})

	return sp
}

// Container returns the panel container.
func (sp *SidePanel) Container() fyne.CanvasObject {
	return sp.container
}

// ShowPanel switches to the specified panel.
func (sp *SidePanel) ShowPanel(name string) {
	if name == sp.currentPanel {
		return
	}
	if sp.disabledPanels[name] {
		fmt.Printf("Panel %q is disabled (save aligned images first)\n", name)
		return
	}

	var panel fyne.CanvasObject
	switch name {
	case PanelImport:
		panel = sp.importPanel.Container()
	case PanelComponents:
		panel = sp.componentsPanel.Container()
	case PanelTraces:
		panel = sp.tracesPanel.Container()
	case PanelProperties:
		sp.propertiesPanel.Refresh()
		panel = sp.propertiesPanel.Container()
	case PanelLogos:
		sp.logosPanel.Refresh()
		panel = sp.logosPanel.Container()
	default:
		return
	}

	// Set up appropriate middle-click handler based on active panel
	switch name {
	case PanelLogos:
		sp.canvas.OnMiddleClick(sp.logosPanel.OnMiddleClick)
	case PanelComponents:
		sp.canvas.OnMiddleClick(sp.componentsPanel.OnMiddleClickFloodFill)
	default:
		// No middle-click handler for other panels
		sp.canvas.OnMiddleClick(nil)
	}

	sp.currentPanel = name
	sp.container.Objects = []fyne.CanvasObject{panel}
	sp.container.Refresh()
}

// CurrentPanel returns the name of the currently visible panel.
func (sp *SidePanel) CurrentPanel() string {
	return sp.currentPanel
}

// SavePreferences saves panel preferences (e.g., split positions) to app preferences.
func (sp *SidePanel) SavePreferences() {
	if sp.componentsPanel != nil {
		sp.componentsPanel.SaveSplitOffset()
	}
}

// LogosPanel returns the logos panel for external access (e.g., middle-click handling).
func (sp *SidePanel) LogosPanel() *LogosPanel {
	return sp.logosPanel
}

// SyncLayers updates the canvas with layers from state.
func (sp *SidePanel) SyncLayers() {
	// Update canvas with current layers
	var layers []*pcbimage.Layer
	if sp.state.FrontImage != nil {
		layers = append(layers, sp.state.FrontImage)
	}
	if sp.state.BackImage != nil {
		layers = append(layers, sp.state.BackImage)
	}
	sp.canvas.SetLayers(layers)

	// Set DPI for background grid
	dpi := sp.state.DPI
	if dpi == 0 && sp.state.FrontImage != nil && sp.state.FrontImage.DPI > 0 {
		dpi = sp.state.FrontImage.DPI
	}
	if dpi == 0 && sp.state.BackImage != nil && sp.state.BackImage.DPI > 0 {
		dpi = sp.state.BackImage.DPI
	}
	sp.canvas.SetDPI(dpi)

	// Set board bounds overlays
	if sp.state.FrontBoardBounds != nil {
		bounds := sp.state.FrontBoardBounds
		sp.canvas.SetOverlay("front_board_bounds", &canvas.Overlay{
			Rectangles: []canvas.OverlayRect{{
				X: bounds.X, Y: bounds.Y, Width: bounds.Width, Height: bounds.Height,
			}},
			Color: color.RGBA{R: 0, G: 255, B: 0, A: 128}, // Green
		})
	}
	if sp.state.BackBoardBounds != nil {
		bounds := sp.state.BackBoardBounds
		sp.canvas.SetOverlay("back_board_bounds", &canvas.Overlay{
			Rectangles: []canvas.OverlayRect{{
				X: bounds.X, Y: bounds.Y, Width: bounds.Width, Height: bounds.Height,
			}},
			Color: color.RGBA{R: 0, G: 0, B: 255, A: 128}, // Blue
		})
	}

	sp.importPanel.ApplyLayerSelection()
}

// SetWindow sets the parent window for dialogs.
func (sp *SidePanel) SetWindow(w fyne.Window) {
	sp.importPanel.SetWindow(w)
	sp.propertiesPanel.SetWindow(w)
	sp.componentsPanel.SetWindow(w)
	sp.logosPanel.SetWindow(w)
}

// AutoDetectAndAlign runs automatic contact detection on both images and aligns them.
// This is called on startup after restoring images from preferences.
func (sp *SidePanel) AutoDetectAndAlign() {
	sp.importPanel.AutoDetectAndAlign()
}

// SetTracesEnabled enables or disables the traces panel interactive widgets.
func (sp *SidePanel) SetTracesEnabled(enabled bool) {
	sp.tracesPanel.SetEnabled(enabled)
}

// updatePanelEnableState enables or disables panels based on whether
// normalized images exist. Called after project load and after Save Aligned.
func (sp *SidePanel) updatePanelEnableState() {
	enabled := sp.state.HasNormalizedImages()
	sp.SetPanelEnabled(PanelComponents, enabled)
	sp.SetPanelEnabled(PanelTraces, enabled)
	sp.SetPanelEnabled(PanelProperties, enabled)
	sp.SetPanelEnabled(PanelLogos, enabled)
}

// SetPanelEnabled enables or disables switching to a specific panel.
func (sp *SidePanel) SetPanelEnabled(name string, enabled bool) {
	if enabled {
		delete(sp.disabledPanels, name)
	} else {
		sp.disabledPanels[name] = true
	}
}

// IsPanelEnabled returns whether a panel is enabled.
func (sp *SidePanel) IsPanelEnabled(name string) bool {
	return !sp.disabledPanels[name]
}

// ImportPanel handles image import and board selection.
type ImportPanel struct {
	state     *app.State
	canvas    *canvas.ImageCanvas
	window    fyne.Window
	container fyne.CanvasObject
	sidePanel *SidePanel // Back-reference for panel enable/disable

	boardSelect    *widget.Select
	boardSpecLabel *widget.Label
	frontLabel     *widget.Label
	backLabel      *widget.Label
	dpiLabel       *widget.Label

	// Contact detection
	contactInfoLabel  *widget.Label
	layerSelect       *widget.RadioGroup
	detectButton      *widget.Button
	sampleButton      *widget.Button
	alignButton       *widget.Button
	alignStatus       *widget.Label

	// Manual alignment nudge
	offsetLabel   *widget.Label
	rotationLabel *widget.Label
	shearLabel    *widget.Label

	// Auto align button
	autoAlignButton *widget.Button

	// Save aligned / Re-align buttons
	saveAlignedBtn *widget.Button
	realignBtn     *widget.Button
	alignControls  *fyne.Container // Manual controls container (hidden after save)

	// Crop editing
	cropLabel *widget.Label
}

// NewImportPanel creates a new import panel.
func NewImportPanel(state *app.State, cvs *canvas.ImageCanvas) *ImportPanel {
	ip := &ImportPanel{
		state:  state,
		canvas: cvs,
	}

	// Initialize all labels first (before any callbacks can fire)
	ip.boardSpecLabel = widget.NewLabel("")
	ip.boardSpecLabel.Wrapping = fyne.TextWrapWord
	ip.frontLabel = widget.NewLabel("No front image loaded")
	ip.backLabel = widget.NewLabel("No back image loaded")
	ip.dpiLabel = widget.NewLabel("DPI: Unknown")
	ip.contactInfoLabel = widget.NewLabel("")
	ip.contactInfoLabel.Wrapping = fyne.TextWrapWord
	ip.alignStatus = widget.NewLabel("")
	ip.cropLabel = widget.NewLabel("Crop: (none)")

	// Board selection (callback may trigger updateBoardSpecInfo)
	boardNames := board.ListSpecs()
	ip.boardSelect = widget.NewSelect(boardNames, func(selected string) {
		if spec := board.GetSpec(selected); spec != nil {
			state.BoardSpec = spec
			ip.updateBoardSpecInfo()
		}
	})
	if state.BoardSpec != nil {
		ip.boardSelect.SetSelected(state.BoardSpec.Name())
	}

	editSpecButton := widget.NewButton("Edit Spec...", func() {
		ip.showBoardSpecDialog()
	})

	ip.layerSelect = widget.NewRadioGroup([]string{"Front", "Back"}, func(selected string) {
		// Raise the selected layer to the top
		if selected == "Front" {
			cvs.RaiseLayerBySide(pcbimage.SideFront)
		} else {
			cvs.RaiseLayerBySide(pcbimage.SideBack)
		}
		// Update labels to show selected layer's values
		ip.RefreshLabels()
	})
	ip.layerSelect.SetSelected("Front")
	ip.layerSelect.Horizontal = true

	ip.detectButton = widget.NewButton("Detect Contacts", func() {
		ip.onDetectContacts()
	})

	ip.sampleButton = widget.NewButton("Sample Contact", func() {
		ip.onSampleContact()
	})

	// Set up canvas selection callback for contact sampling
	cvs.OnSelect(func(x1, y1, x2, y2 float64) {
		ip.onSampleSelected(x1, y1, x2, y2)
	})

	ip.alignButton = widget.NewButton("Align Images", func() {
		ip.onAlignImages()
	})

	// Auto Align button - runs detection and alignment in one step
	ip.autoAlignButton = widget.NewButton("Auto Align", func() {
		ip.onAutoAlign()
	})
	ip.autoAlignButton.Importance = widget.HighImportance

	// Manual alignment nudge - compass rose
	ip.offsetLabel = widget.NewLabel("Offset: (0, 0)")
	compassRose := container.NewGridWithColumns(3,
		layout.NewSpacer(),
		widget.NewButton("N", func() { ip.onNudgeAlignment(0, -1) }),
		layout.NewSpacer(),
		widget.NewButton("W", func() { ip.onNudgeAlignment(-1, 0) }),
		layout.NewSpacer(),
		widget.NewButton("E", func() { ip.onNudgeAlignment(1, 0) }),
		layout.NewSpacer(),
		widget.NewButton("S", func() { ip.onNudgeAlignment(0, 1) }),
		layout.NewSpacer(),
	)

	// Rotation controls (0.01 degree increments for fine adjustment)
	ip.rotationLabel = widget.NewLabel("Rotation: 0.000°")
	rotationButtons := container.NewHBox(
		widget.NewButton("↺ CCW", func() { ip.onNudgeRotation(-0.01) }),
		widget.NewButton("CW ↻", func() { ip.onNudgeRotation(0.01) }),
	)

	// Shear controls - separate buttons for each edge
	// Top/Bottom control horizontal shear (X scale at each edge)
	// Left/Right control vertical shear (Y scale at each edge)
	ip.shearLabel = widget.NewLabel("Shear: T=1.000 B=1.000 L=1.000 R=1.000")
	shearTopButtons := container.NewHBox(
		widget.NewLabel("Top X:"),
		widget.NewButton("-", func() { ip.onNudgeShear("topX", -0.001) }),
		widget.NewButton("+", func() { ip.onNudgeShear("topX", 0.001) }),
	)
	shearBottomButtons := container.NewHBox(
		widget.NewLabel("Bot X:"),
		widget.NewButton("-", func() { ip.onNudgeShear("bottomX", -0.001) }),
		widget.NewButton("+", func() { ip.onNudgeShear("bottomX", 0.001) }),
	)
	shearLeftButtons := container.NewHBox(
		widget.NewLabel("Left Y:"),
		widget.NewButton("-", func() { ip.onNudgeShear("leftY", -0.001) }),
		widget.NewButton("+", func() { ip.onNudgeShear("leftY", 0.001) }),
	)
	shearRightButtons := container.NewHBox(
		widget.NewLabel("Right Y:"),
		widget.NewButton("-", func() { ip.onNudgeShear("rightY", -0.001) }),
		widget.NewButton("+", func() { ip.onNudgeShear("rightY", 0.001) }),
	)

	// Crop controls - position and size adjustment
	cropPosButtons := container.NewHBox(
		widget.NewLabel("Pos:"),
		widget.NewButton("←", func() { ip.onNudgeCrop("x", -10) }),
		widget.NewButton("→", func() { ip.onNudgeCrop("x", 10) }),
		widget.NewButton("↑", func() { ip.onNudgeCrop("y", -10) }),
		widget.NewButton("↓", func() { ip.onNudgeCrop("y", 10) }),
	)
	cropSizeButtons := container.NewHBox(
		widget.NewLabel("Size:"),
		widget.NewButton("W-", func() { ip.onNudgeCrop("w", -10) }),
		widget.NewButton("W+", func() { ip.onNudgeCrop("w", 10) }),
		widget.NewButton("H-", func() { ip.onNudgeCrop("h", -10) }),
		widget.NewButton("H+", func() { ip.onNudgeCrop("h", 10) }),
	)
	reImportButton := widget.NewButton("Re-import with Crop", func() { ip.onReImportWithCrop() })

	// Save Aligned button - bakes all transforms into normalized PNGs
	ip.saveAlignedBtn = widget.NewButton("Save Aligned", func() {
		ip.onSaveAligned()
	})
	ip.saveAlignedBtn.Importance = widget.HighImportance

	// Re-align button - reloads raw images for re-editing alignment
	ip.realignBtn = widget.NewButton("Re-align", func() {
		ip.onRealign()
	})

	// Layout
	// Background mode radio group
	bgRadio := widget.NewRadioGroup([]string{"Checkerboard", "Solid Black"}, func(selected string) {
		ip.canvas.SetSolidBlackBackground(selected == "Solid Black")
	})
	bgRadio.Horizontal = true
	bgRadio.SetSelected("Checkerboard")

	// Wrap manual alignment controls so they can be hidden after normalization
	ip.alignControls = container.NewVBox(
		ip.autoAlignButton,
		ip.alignButton,
		ip.alignStatus,
		widget.NewSeparator(),
		widget.NewLabel("Background:"),
		bgRadio,
		widget.NewSeparator(),
		widget.NewLabel("Manual Adjust (use Layer above):"),
		compassRose,
		ip.offsetLabel,
		widget.NewSeparator(),
		rotationButtons,
		ip.rotationLabel,
		widget.NewSeparator(),
		shearTopButtons,
		shearBottomButtons,
		shearLeftButtons,
		shearRightButtons,
		ip.shearLabel,
		widget.NewSeparator(),
		widget.NewLabel("Crop Bounds:"),
		cropPosButtons,
		cropSizeButtons,
		ip.cropLabel,
		reImportButton,
		widget.NewSeparator(),
		ip.saveAlignedBtn,
	)

	// If already normalized, show Re-align button; hide alignment controls
	// If not normalized, hide Re-align button; show alignment controls
	if state.HasNormalizedImages() {
		ip.alignControls.Hide()
		ip.realignBtn.Show()
	} else {
		ip.realignBtn.Hide()
	}

	ip.container = container.NewVBox(
		widget.NewCard("Board Type", "", container.NewVBox(
			ip.boardSelect,
			ip.boardSpecLabel,
			editSpecButton,
		)),
		widget.NewCard("Images", "", container.NewVBox(
			widget.NewLabel("Front (Component Side):"),
			ip.frontLabel,
			widget.NewLabel("Back (Solder Side):"),
			ip.backLabel,
			ip.dpiLabel,
		)),
		widget.NewCard("Contact Detection", "", container.NewVBox(
			ip.contactInfoLabel,
			widget.NewLabel("Layer:"),
			ip.layerSelect,
			container.NewHBox(ip.detectButton, ip.sampleButton),
		)),
		widget.NewCard("Alignment", "", container.NewVBox(
			ip.realignBtn,
			ip.alignControls,
		)),
	)

	// Register for events
	state.On(app.EventImageLoaded, func(data interface{}) {
		ip.updateImageStatus()
		// Clear all contact overlays - user must re-detect on the rotated image
		ip.canvas.ClearOverlay("front_contacts")
		ip.canvas.ClearOverlay("back_contacts")
		ip.canvas.ClearOverlay("front_expected")
		ip.canvas.ClearOverlay("back_expected")
		ip.canvas.ClearOverlay("front_search_area")
		ip.canvas.ClearOverlay("back_search_area")
	})

	// Initialize board spec info
	ip.updateBoardSpecInfo()

	return ip
}

// SetWindow sets the parent window for dialogs.
func (ip *ImportPanel) SetWindow(w fyne.Window) {
	ip.window = w
}

// RefreshLabels updates all labels from current state.
// updateAlignmentUI shows Re-align button or alignment controls
// based on whether normalized images exist.
func (ip *ImportPanel) updateAlignmentUI() {
	if ip.state.HasNormalizedImages() {
		ip.alignControls.Hide()
		ip.realignBtn.Show()
	} else {
		ip.alignControls.Show()
		ip.realignBtn.Hide()
	}
}

func (ip *ImportPanel) RefreshLabels() {
	ip.updateBoardSpecInfo()
	ip.updateCropLabel()
	ip.updateRotationCenterOverlay()

	// Update labels based on selected layer
	isFront := ip.layerSelect.Selected == "Front"
	var layer *pcbimage.Layer
	if isFront {
		layer = ip.state.FrontImage
	} else {
		layer = ip.state.BackImage
	}

	if layer != nil {
		ip.offsetLabel.SetText(fmt.Sprintf("Offset: (%d, %d)",
			layer.ManualOffsetX, layer.ManualOffsetY))
		ip.rotationLabel.SetText(fmt.Sprintf("Rotation: %.3f°", layer.ManualRotation))
		ip.shearLabel.SetText(fmt.Sprintf("Shear: T=%.3f B=%.3f L=%.3f R=%.3f",
			layer.ShearTopX, layer.ShearBottomX, layer.ShearLeftY, layer.ShearRightY))
	}
}

// updateRotationCenterOverlay shows a red dot at the rotation center for each image.
func (ip *ImportPanel) updateRotationCenterOverlay() {
	// Only show if we have rotation centers set
	frontCenter := ip.state.FrontRotationCenter
	backCenter := ip.state.BackRotationCenter

	// If both are zero, clear overlay
	if frontCenter.X == 0 && frontCenter.Y == 0 && backCenter.X == 0 && backCenter.Y == 0 {
		ip.canvas.ClearOverlay("rotation_center")
		return
	}

	// Create overlay with red circles at rotation centers
	overlay := &canvas.Overlay{
		Color: colorutil.Red,
	}

	// Add front rotation center (if set)
	if frontCenter.X != 0 || frontCenter.Y != 0 {
		overlay.Circles = append(overlay.Circles, canvas.OverlayCircle{
			X:      frontCenter.X,
			Y:      frontCenter.Y,
			Radius: 5, // 10 pixel diameter = 5 pixel radius
			Filled: true,
		})
	}

	// Add back rotation center (if set and different from front)
	// Apply back image offset so the dot appears at the correct position
	if backCenter.X != 0 || backCenter.Y != 0 {
		overlay.Circles = append(overlay.Circles, canvas.OverlayCircle{
			X:      backCenter.X + float64(ip.state.BackManualOffset.X),
			Y:      backCenter.Y + float64(ip.state.BackManualOffset.Y),
			Radius: 5,
			Filled: true,
		})
	}

	ip.canvas.SetOverlay("rotation_center", overlay)
}

func (ip *ImportPanel) updateBoardSpecInfo() {
	spec := ip.state.BoardSpec
	if spec == nil {
		ip.boardSpecLabel.SetText("No board selected")
		ip.contactInfoLabel.SetText("")
		return
	}

	w, h := spec.Dimensions()
	ip.boardSpecLabel.SetText(fmt.Sprintf("%.2f\" × %.2f\"", w, h))

	contacts := spec.ContactSpec()
	if contacts != nil && contacts.Detection != nil {
		det := contacts.Detection
		ip.contactInfoLabel.SetText(fmt.Sprintf(
			"%d contacts, %.3f\" pitch\nAspect: %.1f-%.1f\nHSV: H(%0.f-%0.f) S(%0.f-%0.f) V(%0.f-%0.f)",
			contacts.Count, contacts.PitchInches,
			det.AspectRatioMin, det.AspectRatioMax,
			det.Color.HueMin, det.Color.HueMax,
			det.Color.SatMin, det.Color.SatMax,
			det.Color.ValMin, det.Color.ValMax,
		))
	} else if contacts != nil {
		ip.contactInfoLabel.SetText(fmt.Sprintf(
			"%d contacts, %.3f\" pitch\nNo detection params",
			contacts.Count, contacts.PitchInches,
		))
	} else {
		ip.contactInfoLabel.SetText("No contact spec")
	}
}

func (ip *ImportPanel) showBoardSpecDialog() {
	if ip.window == nil || ip.state.BoardSpec == nil {
		return
	}

	// Get a mutable copy of the spec
	spec, ok := ip.state.BoardSpec.(*board.BaseSpec)
	if !ok {
		ip.alignStatus.SetText("Cannot edit this board type")
		return
	}

	dlg := dialogs.NewBoardSpecDialog(spec, ip.window, func(updated *board.BaseSpec) {
		ip.state.BoardSpec = updated
		ip.updateBoardSpecInfo()
	})
	dlg.Show()
}

func (ip *ImportPanel) onDetectContacts() {
	// Detect contacts on the selected layer only
	isFront := ip.layerSelect.Selected == "Front"

	var img *pcbimage.Layer
	var layerName, overlayName string
	var overlayColor color.RGBA

	if isFront {
		img = ip.state.FrontImage
		layerName = "Front"
		overlayName = "front_contacts"
		overlayColor = color.RGBA{R: 255, G: 0, B: 0, A: 255} // Red
	} else {
		img = ip.state.BackImage
		layerName = "Back"
		overlayName = "back_contacts"
		overlayColor = color.RGBA{R: 0, G: 0, B: 255, A: 255} // Blue
	}

	if img == nil || img.Image == nil {
		ip.alignStatus.SetText(fmt.Sprintf("No %s image loaded", layerName))
		return
	}

	ip.alignStatus.SetText(fmt.Sprintf("Detecting contacts on %s...", layerName))
	ip.detectButton.Disable()

	// Clear ALL overlays before detection to avoid confusion
	ip.canvas.ClearOverlay("front_contacts")
	ip.canvas.ClearOverlay("back_contacts")
	ip.canvas.ClearOverlay("front_expected")
	ip.canvas.ClearOverlay("back_expected")
	ip.canvas.ClearOverlay("front_search_area")
	ip.canvas.ClearOverlay("back_search_area")
	ip.canvas.ClearOverlay("front_ejectors")
	ip.canvas.ClearOverlay("back_ejectors")

	// Run detection in goroutine to keep UI responsive
	go func() {
		dpi := ip.state.DPI
		if dpi == 0 && img.DPI > 0 {
			dpi = img.DPI
		}

		// Get per-side color params if sampled
		var colorParams *alignment.DetectionParams
		var sampledParams *app.ColorParams
		if isFront {
			sampledParams = ip.state.FrontColorParams
		} else {
			sampledParams = ip.state.BackColorParams
		}
		if sampledParams != nil {
			colorParams = &alignment.DetectionParams{
				HueMin: sampledParams.HueMin,
				HueMax: sampledParams.HueMax,
				SatMin: sampledParams.SatMin,
				SatMax: sampledParams.SatMax,
				ValMin: sampledParams.ValMin,
				ValMax: sampledParams.ValMax,
			}
		}

		// Use top-edge-only detection since image should already be rotated
		// (back image is flipped at load time)
		result, err := alignment.DetectContactsOnTopEdge(img.Image, ip.state.BoardSpec, dpi, colorParams)

		var contactCount int
		if result != nil {
			contactCount = len(result.Contacts)

			// Store result
			if isFront {
				ip.state.FrontDetectionResult = result
			} else {
				ip.state.BackDetectionResult = result
			}

			// Update DPI if detected
			if result.DPI > 0 && ip.state.DPI == 0 {
				ip.state.DPI = result.DPI
				ip.dpiLabel.SetText(fmt.Sprintf("DPI: %.1f", result.DPI))
			}

			// Create overlay with fill pattern based on detection pass
			overlay := &canvas.Overlay{
				Color:      overlayColor,
				Rectangles: make([]canvas.OverlayRect, len(result.Contacts)),
			}
			for i, contact := range result.Contacts {
				// Determine fill pattern based on detection pass
				var fill canvas.FillPattern
				switch contact.Pass {
				case alignment.PassFirst:
					fill = canvas.FillSolid
				case alignment.PassBruteForce:
					fill = canvas.FillStripe
				case alignment.PassRescue:
					fill = canvas.FillCrosshatch
				default:
					fill = canvas.FillSolid
				}

				overlay.Rectangles[i] = canvas.OverlayRect{
					X: contact.Bounds.X, Y: contact.Bounds.Y,
					Width: contact.Bounds.Width, Height: contact.Bounds.Height,
					Fill:           fill,
					StripeInterval: contact.Bounds.Width, // Use contact width as stripe interval
				}
			}
			ip.canvas.SetOverlay(overlayName, overlay)

			// Create search area overlay (pink for front, magenta for back)
			var searchColor color.RGBA
			var searchOverlayName string
			if isFront {
				searchColor = color.RGBA{R: 255, G: 105, B: 180, A: 255} // Pink
				searchOverlayName = "front_search_area"
			} else {
				searchColor = colorutil.Magenta
				searchOverlayName = "back_search_area"
			}
			searchOverlay := &canvas.Overlay{
				Color: searchColor,
				Rectangles: []canvas.OverlayRect{{
					X:      result.SearchBounds.X,
					Y:      result.SearchBounds.Y,
					Width:  result.SearchBounds.Width,
					Height: result.SearchBounds.Height,
				}},
			}
			ip.canvas.SetOverlay(searchOverlayName, searchOverlay)

			// Create overlay for expected grid positions (open squares - no fill)
			fmt.Printf("Expected positions from result: %d\n", len(result.ExpectedPositions))
			if len(result.ExpectedPositions) > 0 {
				var expectedOverlayName string
				var expectedColor color.RGBA
				if isFront {
					expectedOverlayName = "front_expected"
					expectedColor = color.RGBA{R: 0, G: 200, B: 200, A: 255} // Cyan
				} else {
					expectedOverlayName = "back_expected"
					expectedColor = color.RGBA{R: 200, G: 200, B: 0, A: 255} // Yellow
				}
				expectedOverlay := &canvas.Overlay{
					Color:      expectedColor,
					Rectangles: make([]canvas.OverlayRect, len(result.ExpectedPositions)),
				}
				for i, pos := range result.ExpectedPositions {
					expectedOverlay.Rectangles[i] = canvas.OverlayRect{
						X:      pos.X,
						Y:      pos.Y,
						Width:  pos.Width,
						Height: pos.Height,
						Fill:   canvas.FillNone, // Open square (no fill)
					}
				}
				ip.canvas.SetOverlay(expectedOverlayName, expectedOverlay)
			}

			// Detect ejector registration marks at bottom corners
			ejectorDPI := dpi
			if ejectorDPI == 0 {
				ejectorDPI = result.DPI
			}
			if ejectorDPI > 0 {
				ejectorMarks := alignment.DetectEjectorMarksFromImage(img.Image, result.Contacts, ejectorDPI)
				if len(ejectorMarks) > 0 {
					fmt.Printf("Detected %d ejector marks:\n", len(ejectorMarks))
					for _, mark := range ejectorMarks {
						fmt.Printf("  %s: (%.1f, %.1f)\n", mark.Side, mark.Center.X, mark.Center.Y)
					}

					// Create overlay for ejector marks (green circles)
					var ejectorOverlayName string
					if isFront {
						ejectorOverlayName = "front_ejectors"
					} else {
						ejectorOverlayName = "back_ejectors"
					}
					ejectorOverlay := &canvas.Overlay{
						Color:      color.RGBA{R: 0, G: 255, B: 0, A: 255}, // Green
						Rectangles: make([]canvas.OverlayRect, len(ejectorMarks)),
					}
					markSize := int(0.25 * ejectorDPI) // 0.25" crosshair size
					for i, mark := range ejectorMarks {
						ejectorOverlay.Rectangles[i] = canvas.OverlayRect{
							X:      int(mark.Center.X) - markSize/2,
							Y:      int(mark.Center.Y) - markSize/2,
							Width:  markSize,
							Height: markSize,
							Fill:   canvas.FillTarget,
							Label:  mark.Side,
						}
					}
					ip.canvas.SetOverlay(ejectorOverlayName, ejectorOverlay)
				}
			}

			// Print contact statistics to stdout
			printContactStats(img.Image, result.Contacts, dpi, layerName)
		}

		ip.detectButton.Enable()

		// Calculate size and aspect ratio statistics
		var sizeInfo string
		if contactCount > 0 {
			minW, maxW := result.Contacts[0].Bounds.Width, result.Contacts[0].Bounds.Width
			minH, maxH := result.Contacts[0].Bounds.Height, result.Contacts[0].Bounds.Height
			minAspect, maxAspect := float64(maxH)/float64(maxW), float64(maxH)/float64(maxW)

			for _, c := range result.Contacts {
				w, h := c.Bounds.Width, c.Bounds.Height
				if w < minW {
					minW = w
				}
				if w > maxW {
					maxW = w
				}
				if h < minH {
					minH = h
				}
				if h > maxH {
					maxH = h
				}
				aspect := float64(h) / float64(w)
				if aspect < minAspect {
					minAspect = aspect
				}
				if aspect > maxAspect {
					maxAspect = aspect
				}
			}
			sizeInfo = fmt.Sprintf("\nSize: %d-%d x %d-%d px\nAspect: %.1f-%.1f",
				minW, maxW, minH, maxH, minAspect, maxAspect)
		}

		// Update status
		if err != nil {
			ip.alignStatus.SetText(fmt.Sprintf("%s: %d contacts%s\n%v", layerName, contactCount, sizeInfo, err))
		} else {
			ip.alignStatus.SetText(fmt.Sprintf("%s: %d contacts%s", layerName, contactCount, sizeInfo))
		}
	}()
}

// onNudgeAlignment adjusts the manual alignment offset for the selected layer.
func (ip *ImportPanel) onNudgeAlignment(dx, dy int) {
	isFront := ip.layerSelect.Selected == "Front"

	var layer *pcbimage.Layer
	if isFront {
		layer = ip.state.FrontImage
		ip.state.FrontManualOffset.X += dx
		ip.state.FrontManualOffset.Y += dy
	} else {
		layer = ip.state.BackImage
		ip.state.BackManualOffset.X += dx
		ip.state.BackManualOffset.Y += dy
	}

	if layer == nil {
		ip.alignStatus.SetText("No image loaded for " + ip.layerSelect.Selected)
		return
	}

	// Apply offset to layer for rendering
	layer.ManualOffsetX += dx
	layer.ManualOffsetY += dy

	ip.state.SetModified(true)
	ip.canvas.Refresh()

	// Update offset label
	ip.offsetLabel.SetText(fmt.Sprintf("Offset: (%d, %d)",
		layer.ManualOffsetX, layer.ManualOffsetY))
}

// onNudgeRotation adjusts the manual rotation for the selected layer.
func (ip *ImportPanel) onNudgeRotation(degrees float64) {
	isFront := ip.layerSelect.Selected == "Front"

	var layer *pcbimage.Layer
	var detectionResult *alignment.DetectionResult
	if isFront {
		layer = ip.state.FrontImage
		detectionResult = ip.state.FrontDetectionResult
		ip.state.FrontManualRotation += degrees
	} else {
		layer = ip.state.BackImage
		detectionResult = ip.state.BackDetectionResult
		ip.state.BackManualRotation += degrees
	}

	if layer == nil {
		ip.alignStatus.SetText("No image loaded for " + ip.layerSelect.Selected)
		return
	}

	// Set rotation center to board center (from contacts) if not already set
	if layer.RotationCenterX == 0 && layer.RotationCenterY == 0 {
		if detectionResult != nil && len(detectionResult.Contacts) > 0 {
			// Calculate center from contacts bounding box
			minX, minY := detectionResult.Contacts[0].Center.X, detectionResult.Contacts[0].Center.Y
			maxX, maxY := minX, minY
			for _, c := range detectionResult.Contacts {
				if c.Center.X < minX {
					minX = c.Center.X
				}
				if c.Center.X > maxX {
					maxX = c.Center.X
				}
				if c.Center.Y < minY {
					minY = c.Center.Y
				}
				if c.Center.Y > maxY {
					maxY = c.Center.Y
				}
			}
			// Board center is offset from contact center (contacts are at edge)
			// Use contact center X, but estimate board center Y from image
			layer.RotationCenterX = (minX + maxX) / 2
			if layer.Image != nil {
				// Board center Y is roughly middle of image
				layer.RotationCenterY = float64(layer.Image.Bounds().Dy()) / 2
			} else {
				layer.RotationCenterY = (minY + maxY) / 2
			}

			// Also update state
			if isFront {
				ip.state.FrontRotationCenter.X = layer.RotationCenterX
				ip.state.FrontRotationCenter.Y = layer.RotationCenterY
			} else {
				ip.state.BackRotationCenter.X = layer.RotationCenterX
				ip.state.BackRotationCenter.Y = layer.RotationCenterY
			}
		}
	}

	// Apply rotation to layer for rendering
	layer.ManualRotation += degrees

	ip.state.SetModified(true)
	ip.canvas.Refresh()

	// Update rotation label
	ip.rotationLabel.SetText(fmt.Sprintf("Rotation: %.3f°", layer.ManualRotation))
}

// onNudgeShear adjusts the shear for the selected layer.
// edge is one of: "topX", "bottomX", "leftY", "rightY"
func (ip *ImportPanel) onNudgeShear(edge string, delta float64) {
	isFront := ip.layerSelect.Selected == "Front"

	var layer *pcbimage.Layer
	if isFront {
		layer = ip.state.FrontImage
	} else {
		layer = ip.state.BackImage
	}

	if layer == nil {
		ip.alignStatus.SetText("No image loaded for " + ip.layerSelect.Selected)
		return
	}

	// Apply shear delta to the appropriate edge
	switch edge {
	case "topX":
		layer.ShearTopX += delta
		if isFront {
			ip.state.FrontShearTopX += delta
		} else {
			ip.state.BackShearTopX += delta
		}
	case "bottomX":
		layer.ShearBottomX += delta
		if isFront {
			ip.state.FrontShearBottomX += delta
		} else {
			ip.state.BackShearBottomX += delta
		}
	case "leftY":
		layer.ShearLeftY += delta
		if isFront {
			ip.state.FrontShearLeftY += delta
		} else {
			ip.state.BackShearLeftY += delta
		}
	case "rightY":
		layer.ShearRightY += delta
		if isFront {
			ip.state.FrontShearRightY += delta
		} else {
			ip.state.BackShearRightY += delta
		}
	}

	ip.state.SetModified(true)
	ip.canvas.Refresh()

	// Update shear label
	ip.shearLabel.SetText(fmt.Sprintf("Shear: T=%.3f B=%.3f L=%.3f R=%.3f",
		layer.ShearTopX, layer.ShearBottomX, layer.ShearLeftY, layer.ShearRightY))
}

// onNudgeCrop adjusts the crop bounds for the selected layer.
// dimension is one of: "x", "y", "w", "h"
func (ip *ImportPanel) onNudgeCrop(dimension string, delta int) {
	isFront := ip.layerSelect.Selected == "Front"

	var crop *geometry.RectInt
	if isFront {
		crop = &ip.state.FrontCropBounds
	} else {
		crop = &ip.state.BackCropBounds
	}

	// Apply delta to the appropriate dimension
	switch dimension {
	case "x":
		crop.X += delta
		if crop.X < 0 {
			crop.X = 0
		}
	case "y":
		crop.Y += delta
		if crop.Y < 0 {
			crop.Y = 0
		}
	case "w":
		crop.Width += delta
		if crop.Width < 10 {
			crop.Width = 10
		}
	case "h":
		crop.Height += delta
		if crop.Height < 10 {
			crop.Height = 10
		}
	}

	ip.updateCropLabel()
}

// onReImportWithCrop reloads the image with the updated crop bounds.
func (ip *ImportPanel) onReImportWithCrop() {
	isFront := ip.layerSelect.Selected == "Front"

	var layer *pcbimage.Layer
	var crop geometry.RectInt
	if isFront {
		layer = ip.state.FrontImage
		crop = ip.state.FrontCropBounds
	} else {
		layer = ip.state.BackImage
		crop = ip.state.BackCropBounds
	}

	if layer == nil || layer.Path == "" {
		ip.alignStatus.SetText("No image loaded for " + ip.layerSelect.Selected)
		return
	}

	// Validate crop bounds
	if crop.Width <= 0 || crop.Height <= 0 {
		ip.alignStatus.SetText("Invalid crop bounds")
		return
	}

	ip.alignStatus.SetText("Re-importing with new crop...")

	// Reload the image using the Load function (which applies saved crop bounds)
	var err error
	if isFront {
		err = ip.state.LoadFrontImage(layer.Path)
	} else {
		err = ip.state.LoadBackImage(layer.Path)
	}

	if err != nil {
		ip.alignStatus.SetText(fmt.Sprintf("Re-import failed: %v", err))
		return
	}

	ip.state.SetModified(true)
	ip.alignStatus.SetText("Re-imported with new crop bounds")
	ip.updateCropLabel()
}

// updateCropLabel updates the crop label from state.
func (ip *ImportPanel) updateCropLabel() {
	isFront := ip.layerSelect.Selected == "Front"

	var crop geometry.RectInt
	if isFront {
		crop = ip.state.FrontCropBounds
	} else {
		crop = ip.state.BackCropBounds
	}

	if crop.Width > 0 && crop.Height > 0 {
		ip.cropLabel.SetText(fmt.Sprintf("Crop: %d,%d %dx%d", crop.X, crop.Y, crop.Width, crop.Height))
	} else {
		ip.cropLabel.SetText("Crop: (none)")
	}
}

// onToggleStepEdgeViz enables/disables the step-edge striped visualization.
// This shows alternating 1cm horizontal stripes from front and back images
// in the step-edge region to help verify alignment precision.
// Also draws horizontal lines where the step edge detection thinks the edges are.
func (ip *ImportPanel) onToggleStepEdgeViz(enabled bool) {
	if !enabled {
		ip.canvas.SetStepEdgeViz(false, 0, 0)
		ip.canvas.ClearOverlay("front_step_edge")
		ip.canvas.ClearOverlay("back_step_edge")
		return
	}

	// Need DPI to calculate stripe width
	dpi := ip.state.DPI
	if dpi == 0 && ip.state.FrontDetectionResult != nil {
		dpi = ip.state.FrontDetectionResult.DPI
	}
	if dpi == 0 && ip.state.BackDetectionResult != nil {
		dpi = ip.state.BackDetectionResult.DPI
	}
	if dpi == 0 {
		dpi = 400 // Default fallback
	}

	// Calculate step-edge Y position from contacts for the stripe visualization
	var stepY float64
	if ip.state.FrontDetectionResult != nil && len(ip.state.FrontDetectionResult.Contacts) > 0 {
		var sumY float64
		for _, c := range ip.state.FrontDetectionResult.Contacts {
			sumY += c.Center.Y
		}
		avgContactY := sumY / float64(len(ip.state.FrontDetectionResult.Contacts))
		stepY = avgContactY + (0.3 * dpi)
	}

	ip.canvas.SetStepEdgeViz(true, stepY, dpi)

	// Now run actual step edge detection and draw horizontal lines
	var frontStepY, backStepY float64
	var frontDetected, backDetected bool

	// Get image width for the line
	imgWidth := 0
	if ip.state.FrontImage != nil && ip.state.FrontImage.Image != nil {
		imgWidth = ip.state.FrontImage.Image.Bounds().Dx()
	}

	// Detect front step edges
	if ip.state.FrontImage != nil && ip.state.FrontDetectionResult != nil {
		edges := alignment.DetectStepEdgesFromImage(ip.state.FrontImage.Image,
			ip.state.FrontDetectionResult.Contacts, dpi)
		if len(edges) > 0 {
			var sumY float64
			for _, e := range edges {
				sumY += e.EdgeY
			}
			frontStepY = sumY / float64(len(edges))
			frontDetected = true
			fmt.Printf("Front step edge detected at Y=%.1f\n", frontStepY)
		}
	}

	// Detect back step edges
	if ip.state.BackImage != nil && ip.state.BackDetectionResult != nil {
		edges := alignment.DetectStepEdgesFromImage(ip.state.BackImage.Image,
			ip.state.BackDetectionResult.Contacts, dpi)
		if len(edges) > 0 {
			var sumY float64
			for _, e := range edges {
				sumY += e.EdgeY
			}
			backStepY = sumY / float64(len(edges))
			backDetected = true
			fmt.Printf("Back step edge detected at Y=%.1f\n", backStepY)
		}
	}

	// Draw horizontal lines as overlays
	lineHeight := 3 // pixels thick

	if frontDetected && imgWidth > 0 {
		ip.canvas.SetOverlay("front_step_edge", &canvas.Overlay{
			Rectangles: []canvas.OverlayRect{{
				X: 0, Y: int(frontStepY) - lineHeight/2,
				Width: imgWidth, Height: lineHeight,
			}},
			Color: color.RGBA{R: 0, G: 0, B: 255, A: 200}, // Blue for front
		})
	}

	if backDetected && imgWidth > 0 {
		ip.canvas.SetOverlay("back_step_edge", &canvas.Overlay{
			Rectangles: []canvas.OverlayRect{{
				X: 0, Y: int(backStepY) - lineHeight/2,
				Width: imgWidth, Height: lineHeight,
			}},
			Color: color.RGBA{R: 255, G: 0, B: 0, A: 200}, // Red for back
		})
	}

	// Show status with detected positions
	status := fmt.Sprintf("Step-edge: front=%.0f back=%.0f (delta=%.1f px)",
		frontStepY, backStepY, backStepY-frontStepY)
	ip.alignStatus.SetText(status)
	fmt.Println(status)
}

// onAutoAlign runs detection on both images and then aligns them in one step.
func (ip *ImportPanel) onAutoAlign() {
	if ip.state.FrontImage == nil || ip.state.BackImage == nil {
		ip.alignStatus.SetText("Need both front and back images")
		return
	}

	ip.alignStatus.SetText("Auto-aligning: detecting contacts...")
	ip.autoAlignButton.Disable()
	ip.alignButton.Disable()

	go func() {
		dpi := ip.state.DPI
		if dpi == 0 && ip.state.FrontImage.DPI > 0 {
			dpi = ip.state.FrontImage.DPI
		}

		// Detect contacts on front image (use top edge detection since images are pre-rotated)
		frontResult, err := alignment.DetectContactsOnTopEdge(
			ip.state.FrontImage.Image,
			ip.state.BoardSpec,
			dpi,
			nil,
		)
		if err != nil || frontResult == nil || len(frontResult.Contacts) < 10 {
			ip.alignStatus.SetText("Auto-align failed: not enough front contacts")
			ip.autoAlignButton.Enable()
			ip.alignButton.Enable()
			return
		}
		ip.state.FrontDetectionResult = frontResult

		// Detect contacts on back image
		backResult, err := alignment.DetectContactsOnTopEdge(
			ip.state.BackImage.Image,
			ip.state.BoardSpec,
			dpi,
			nil,
		)
		if err != nil || backResult == nil || len(backResult.Contacts) < 10 {
			ip.alignStatus.SetText("Auto-align failed: not enough back contacts")
			ip.autoAlignButton.Enable()
			ip.alignButton.Enable()
			return
		}
		ip.state.BackDetectionResult = backResult

		ip.alignStatus.SetText(fmt.Sprintf("Detected %d/%d contacts, aligning...",
			len(frontResult.Contacts), len(backResult.Contacts)))

		// Now run alignment
		frontContacts := frontResult.Contacts
		backContacts := backResult.Contacts

		// Step 1: Coarse alignment using contact positions (translation only)
		var frontSumX, frontSumY, backSumX, backSumY float64
		minContacts := minInt(len(frontContacts), len(backContacts))

		for i := 0; i < minContacts; i++ {
			frontSumX += frontContacts[i].Center.X
			frontSumY += frontContacts[i].Center.Y
			backSumX += backContacts[i].Center.X
			backSumY += backContacts[i].Center.Y
		}

		frontAvgX := frontSumX / float64(minContacts)
		frontAvgY := frontSumY / float64(minContacts)
		backAvgX := backSumX / float64(minContacts)
		backAvgY := backSumY / float64(minContacts)

		// Calculate the offset to align back to front
		offsetX := int(frontAvgX - backAvgX)
		offsetY := int(frontAvgY - backAvgY)

		// Step 2: Rotate both images to make contacts horizontal
		// frontAngle/backAngle are the detected slope of contact lines
		// Negative rotation corrects the angle to horizontal
		frontAngle := frontResult.ContactAngle
		backAngle := backResult.ContactAngle

		// Apply front rotation to make front contacts horizontal
		ip.state.FrontManualRotation = -frontAngle
		ip.state.FrontRotationCenter = geometry.Point2D{X: frontAvgX, Y: frontAvgY}
		ip.state.FrontImage.ManualRotation = -frontAngle
		ip.state.FrontImage.RotationCenterX = frontAvgX
		ip.state.FrontImage.RotationCenterY = frontAvgY

		// Apply back rotation to make back contacts horizontal
		ip.state.BackManualRotation = -backAngle
		ip.state.BackRotationCenter = geometry.Point2D{X: backAvgX, Y: backAvgY}
		ip.state.BackImage.ManualRotation = -backAngle
		ip.state.BackImage.RotationCenterX = backAvgX
		ip.state.BackImage.RotationCenterY = backAvgY

		// Apply offset to back image (front stays at origin)
		ip.state.BackManualOffset.X = offsetX
		ip.state.BackManualOffset.Y = offsetY
		ip.state.BackImage.ManualOffsetX = offsetX
		ip.state.BackImage.ManualOffsetY = offsetY

		// Reset shear to 1.0 (no shear) - auto-align uses only rotation and translation
		ip.state.FrontShearTopX = 1.0
		ip.state.FrontShearBottomX = 1.0
		ip.state.FrontShearLeftY = 1.0
		ip.state.FrontShearRightY = 1.0
		ip.state.BackShearTopX = 1.0
		ip.state.BackShearBottomX = 1.0
		ip.state.BackShearLeftY = 1.0
		ip.state.BackShearRightY = 1.0
		ip.state.FrontImage.ShearTopX = 1.0
		ip.state.FrontImage.ShearBottomX = 1.0
		ip.state.FrontImage.ShearLeftY = 1.0
		ip.state.FrontImage.ShearRightY = 1.0
		ip.state.BackImage.ShearTopX = 1.0
		ip.state.BackImage.ShearBottomX = 1.0
		ip.state.BackImage.ShearLeftY = 1.0
		ip.state.BackImage.ShearRightY = 1.0

		ip.state.Aligned = true
		ip.state.SetModified(true)
		ip.state.Emit(app.EventAlignmentComplete, nil)

		// Clear all contact/connector overlays - don't show outlines on the image
		ip.canvas.ClearOverlay("front_contacts")
		ip.canvas.ClearOverlay("back_contacts")
		ip.canvas.ClearOverlay("front_expected")
		ip.canvas.ClearOverlay("back_expected")
		ip.canvas.ClearOverlay("front_search_area")
		ip.canvas.ClearOverlay("back_search_area")
		ip.canvas.ClearOverlay("front_ejectors")
		ip.canvas.ClearOverlay("back_ejectors")
		ip.canvas.ClearOverlay("connectors")

		ip.alignStatus.SetText(fmt.Sprintf("Aligned: front rot=%.3f°, back rot=%.3f°, offset=(%d,%d)",
			-frontAngle, -backAngle, offsetX, offsetY))
		ip.autoAlignButton.Enable()
		ip.alignButton.Enable()
		ip.RefreshLabels()
		ip.canvas.Refresh()
	}()
}

func (ip *ImportPanel) onAlignImages() {
	// Check that we have both images and detection results
	if ip.state.FrontImage == nil || ip.state.BackImage == nil {
		ip.alignStatus.SetText("Need both front and back images")
		return
	}
	if ip.state.FrontDetectionResult == nil || ip.state.BackDetectionResult == nil {
		ip.alignStatus.SetText("Detect contacts on both images first")
		return
	}

	frontContacts := ip.state.FrontDetectionResult.Contacts
	backContacts := ip.state.BackDetectionResult.Contacts

	if len(frontContacts) < 10 || len(backContacts) < 10 {
		ip.alignStatus.SetText("Need at least 10 contacts on each image")
		return
	}

	ip.alignStatus.SetText("Aligning images...")
	ip.alignButton.Disable()

	go func() {
		dpi := ip.state.DPI
		if dpi == 0 {
			dpi = ip.state.FrontDetectionResult.DPI
		}

		// Step 1: Coarse alignment using contact positions (translation only)
		var frontSumX, frontSumY, backSumX, backSumY float64
		minContacts := minInt(len(frontContacts), len(backContacts))

		for i := 0; i < minContacts; i++ {
			frontSumX += frontContacts[i].Center.X
			frontSumY += frontContacts[i].Center.Y
			backSumX += backContacts[i].Center.X
			backSumY += backContacts[i].Center.Y
		}

		frontAvgX := frontSumX / float64(minContacts)
		frontAvgY := frontSumY / float64(minContacts)
		backAvgX := backSumX / float64(minContacts)
		backAvgY := backSumY / float64(minContacts)

		deltaX := frontAvgX - backAvgX
		deltaY := frontAvgY - backAvgY

		// Apply initial translation
		translatedBack := translateImage(ip.state.BackImage.Image, int(deltaX), int(deltaY))

		// Step 2: Detect ejector marks on both images for fine alignment
		frontMarks := alignment.DetectEjectorMarksFromImage(ip.state.FrontImage.Image, frontContacts, dpi)

		// Translate back contacts for ejector detection on translated image
		translatedBackContacts := make([]alignment.Contact, len(backContacts))
		for i, c := range backContacts {
			translatedBackContacts[i] = c
			translatedBackContacts[i].Center.X += deltaX
			translatedBackContacts[i].Center.Y += deltaY
		}
		backMarks := alignment.DetectEjectorMarksFromImage(translatedBack, translatedBackContacts, dpi)

		fmt.Printf("Alignment: front ejector marks=%d, back ejector marks=%d\n", len(frontMarks), len(backMarks))

		var finalImage image.Image = translatedBack
		var alignInfo string

		// Step 3: If we have matching ejector marks, calculate affine transform
		if len(frontMarks) >= 2 && len(backMarks) >= 2 {
			// Find matching left/right marks
			var frontLeft, frontRight, backLeft, backRight *alignment.EjectorMark
			for i := range frontMarks {
				if frontMarks[i].Side == "left" {
					frontLeft = &frontMarks[i]
				} else if frontMarks[i].Side == "right" {
					frontRight = &frontMarks[i]
				}
			}
			for i := range backMarks {
				if backMarks[i].Side == "left" {
					backLeft = &backMarks[i]
				} else if backMarks[i].Side == "right" {
					backRight = &backMarks[i]
				}
			}

			if frontLeft != nil && frontRight != nil && backLeft != nil && backRight != nil {
				// Calculate shear transform that preserves the contact edge
				// The contact line (Y=contactY) stays completely fixed
				// Points below are sheared in X to align ejector marks
				contactY := frontAvgY // The Y coordinate of the contact line
				finalImage, alignInfo = applyShearAlignment(
					translatedBack,
					backLeft.Center, backRight.Center,
					frontLeft.Center, frontRight.Center,
					contactY,
				)
				fmt.Printf("Shear alignment: %s\n", alignInfo)
			} else {
				alignInfo = fmt.Sprintf("translated (%.1f, %.1f) px (missing ejector marks)", deltaX, deltaY)
			}
		} else {
			alignInfo = fmt.Sprintf("translated (%.1f, %.1f) px (no ejector marks)", deltaX, deltaY)
		}

		// Update the back image in state
		ip.state.BackImage.Image = finalImage
		ip.state.Aligned = true

		// Update overlays (use simple translation for now - full transform would require more work)
		alignedBackContacts := make([]alignment.Contact, len(backContacts))
		for i, c := range backContacts {
			alignedBackContacts[i] = alignment.Contact{
				Bounds: geometry.RectInt{
					X:      c.Bounds.X + int(deltaX),
					Y:      c.Bounds.Y + int(deltaY),
					Width:  c.Bounds.Width,
					Height: c.Bounds.Height,
				},
				Center: geometry.Point2D{
					X: c.Center.X + deltaX,
					Y: c.Center.Y + deltaY,
				},
				Pass: c.Pass,
			}
		}
		ip.state.BackDetectionResult.Contacts = alignedBackContacts

		// Update back contacts overlay
		overlay := &canvas.Overlay{
			Color:      color.RGBA{R: 0, G: 0, B: 255, A: 255},
			Rectangles: make([]canvas.OverlayRect, len(alignedBackContacts)),
		}
		for i, contact := range alignedBackContacts {
			overlay.Rectangles[i] = canvas.OverlayRect{
				X: contact.Bounds.X, Y: contact.Bounds.Y,
				Width: contact.Bounds.Width, Height: contact.Bounds.Height,
			}
		}
		ip.canvas.SetOverlay("back_contacts", overlay)

		ip.alignButton.Enable()
		ip.alignStatus.SetText("Aligned: " + alignInfo)

		// Clear debug overlays now that alignment is complete
		ip.canvas.ClearOverlay("front_contacts")
		ip.canvas.ClearOverlay("back_contacts")
		ip.canvas.ClearOverlay("front_expected")
		ip.canvas.ClearOverlay("back_expected")
		ip.canvas.ClearOverlay("front_search_area")
		ip.canvas.ClearOverlay("back_search_area")
		ip.canvas.ClearOverlay("front_ejectors")
		ip.canvas.ClearOverlay("back_ejectors")

		// Refresh canvas to show the aligned back image
		ip.canvas.Refresh()
		ip.state.Emit(app.EventAlignmentComplete, nil)
	}()
}

// applyShearAlignment applies a shear and scale transform that preserves the contact edge.
// The line Y=contactY remains completely fixed (no X movement).
// Points below contactY are sheared in X and scaled in Y to align the ejector marks.
func applyShearAlignment(img image.Image, backLeft, backRight, frontLeft, frontRight geometry.Point2D, contactY float64) (image.Image, string) {
	// Y distances from contact line to ejector marks
	backYDistLeft := backLeft.Y - contactY
	backYDistRight := backRight.Y - contactY
	frontYDistLeft := frontLeft.Y - contactY
	frontYDistRight := frontRight.Y - contactY

	// Average Y distances for scale calculation
	backYDist := (backYDistLeft + backYDistRight) / 2
	frontYDist := (frontYDistLeft + frontYDistRight) / 2

	if math.Abs(backYDist) < 1 {
		return img, "ejectors too close to contacts"
	}

	// Y scale factor: ratio of front to back Y distances from contact line
	yScale := frontYDist / backYDist

	// After Y scaling, calculate X shear needed
	// The back ejector positions after Y scaling
	scaledBackLeftY := contactY + backYDistLeft*yScale
	scaledBackRightY := contactY + backYDistRight*yScale

	// X shifts needed at ejector positions (after Y scaling)
	deltaLeftX := frontLeft.X - backLeft.X
	deltaRightX := frontRight.X - backRight.X

	// Shear factors: X shift per unit Y distance from contact line
	shearLeft := deltaLeftX / frontYDist
	shearRight := deltaRightX / frontYDist

	// X positions of ejectors for interpolation
	ejectorLeftX := backLeft.X
	ejectorRightX := backRight.X
	ejectorSpanX := ejectorRightX - ejectorLeftX

	if math.Abs(ejectorSpanX) < 1 {
		return img, "ejectors too close together"
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	result := image.NewRGBA(image.Rect(0, 0, w, h))

	// For each output pixel, find the corresponding input pixel
	for y := 0; y < h; y++ {
		// Distance from contact line in output (front) space
		outYDist := float64(y) - contactY

		// Inverse Y scale to find source Y distance
		srcYDist := outYDist / yScale
		srcY := contactY + srcYDist

		for x := 0; x < w; x++ {
			// Interpolate shear factor based on X position
			t := (float64(x) - ejectorLeftX) / ejectorSpanX
			if t < 0 {
				t = 0
			}
			if t > 1 {
				t = 1
			}
			shear := shearLeft*(1-t) + shearRight*t

			// Calculate X shift for this point (based on output Y distance)
			xShift := shear * outYDist

			// Source X coordinate (inverse shear)
			srcX := float64(x) - xShift

			// Sample from source (nearest neighbor)
			sx := int(srcX + 0.5)
			sy := int(srcY + 0.5)

			if sx >= 0 && sx < w && sy >= 0 && sy < h {
				result.Set(x, y, img.At(sx+bounds.Min.X, sy+bounds.Min.Y))
			}
		}
	}

	_ = scaledBackLeftY  // Suppress unused variable warning
	_ = scaledBackRightY // Suppress unused variable warning

	return result, fmt.Sprintf("yScale=%.4f, shear L=%.4f R=%.4f", yScale, shearLeft, shearRight)
}

// flipHorizontal flips an image horizontally.
func flipHorizontal(img image.Image) image.Image {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	flipped := image.NewRGBA(image.Rect(0, 0, w, h))

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			flipped.Set(w-1-x, y, img.At(x+bounds.Min.X, y+bounds.Min.Y))
		}
	}
	return flipped
}

// translateImage translates an image by (dx, dy) pixels.
func translateImage(img image.Image, dx, dy int) image.Image {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	// Create output image, potentially larger to accommodate translation
	newW := w + absInt(dx)
	newH := h + absInt(dy)
	translated := image.NewRGBA(image.Rect(0, 0, newW, newH))

	// Fill with black (or transparent)
	// Default is black (zero value)

	// Calculate where to place the source image
	offsetX := 0
	offsetY := 0
	if dx > 0 {
		offsetX = dx
	}
	if dy > 0 {
		offsetY = dy
	}

	// Copy the image to its new position
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			translated.Set(x+offsetX, y+offsetY, img.At(x+bounds.Min.X, y+bounds.Min.Y))
		}
	}

	return translated
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func (ip *ImportPanel) onSampleContact() {
	// Check if we have an image
	isFront := ip.layerSelect.Selected == "Front"
	var img *pcbimage.Layer
	if isFront {
		img = ip.state.FrontImage
	} else {
		img = ip.state.BackImage
	}

	if img == nil || img.Image == nil {
		ip.alignStatus.SetText("No image loaded for " + ip.layerSelect.Selected)
		return
	}

	ip.alignStatus.SetText("Draw a rectangle around a gold contact...")
	ip.canvas.EnableSelectMode()
}

func (ip *ImportPanel) onSampleSelected(x1, y1, x2, y2 float64) {
	// Safety check
	if ip.canvas == nil {
		ip.alignStatus.SetText("Canvas not available")
		return
	}

	// Get the rendered canvas output (what the user actually sees)
	canvasOutput := ip.canvas.GetRenderedOutput()
	if canvasOutput == nil {
		ip.alignStatus.SetText("No canvas output available")
		return
	}

	// Coordinates are in canvas space - clamp to canvas bounds
	bounds := canvasOutput.Bounds()
	if bounds.Empty() {
		ip.alignStatus.SetText("Canvas is empty")
		return
	}

	ix1 := maxInt(int(x1), bounds.Min.X)
	iy1 := maxInt(int(y1), bounds.Min.Y)
	ix2 := minInt(int(x2), bounds.Max.X-1)
	iy2 := minInt(int(y2), bounds.Max.Y-1)

	if ix2 <= ix1 || iy2 <= iy1 {
		ip.alignStatus.SetText("Invalid selection")
		return
	}

	// Extract HSV statistics from the canvas output (what user sees)
	hsvStats := extractHSVStats(canvasOutput, ix1, iy1, ix2, iy2)

	// Create color params from sample (mean ± 2 sigma for 95% coverage)
	colorParams := &app.ColorParams{
		HueMin: maxFloat(0, hsvStats.hueMean-2*hsvStats.hueStd),
		HueMax: minFloat(180, hsvStats.hueMean+2*hsvStats.hueStd),
		SatMin: maxFloat(0, hsvStats.satMean-2*hsvStats.satStd),
		SatMax: minFloat(255, hsvStats.satMean+2*hsvStats.satStd),
		ValMin: maxFloat(0, hsvStats.valMean-2*hsvStats.valStd),
		ValMax: minFloat(255, hsvStats.valMean+2*hsvStats.valStd),
	}

	// Store to the appropriate side
	isFront := ip.layerSelect.Selected == "Front"
	if isFront {
		ip.state.FrontColorParams = colorParams
	} else {
		ip.state.BackColorParams = colorParams
	}

	ip.alignStatus.SetText(fmt.Sprintf(
		"%s sampled: H(%.0f±%.0f) S(%.0f±%.0f) V(%.0f±%.0f)",
		ip.layerSelect.Selected,
		hsvStats.hueMean, hsvStats.hueStd,
		hsvStats.satMean, hsvStats.satStd,
		hsvStats.valMean, hsvStats.valStd,
	))
}

// hsvStats holds HSV statistics for a region (mean ± 1 sigma).
type hsvStats struct {
	hueMean, hueStd float64
	satMean, satStd float64
	valMean, valStd float64
}

// extractHSVStats extracts HSV statistics (mean ± 1 sigma) from a region of an image.
func extractHSVStats(img image.Image, x1, y1, x2, y2 int) hsvStats {
	var hues, sats, vals []float64

	bounds := img.Bounds()
	for y := y1; y <= y2; y++ {
		if y < bounds.Min.Y || y >= bounds.Max.Y {
			continue
		}
		for x := x1; x <= x2; x++ {
			if x < bounds.Min.X || x >= bounds.Max.X {
				continue
			}
			r, g, b, _ := img.At(x, y).RGBA()
			// Convert from 16-bit to 8-bit
			r8 := float64(r >> 8)
			g8 := float64(g >> 8)
			b8 := float64(b >> 8)

			h, s, v := colorutil.RGBToHSV(r8, g8, b8)
			hues = append(hues, h)
			sats = append(sats, s)
			vals = append(vals, v)
		}
	}

	return hsvStats{
		hueMean: mean(hues),
		hueStd:  stdDev(hues),
		satMean: mean(sats),
		satStd:  stdDev(sats),
		valMean: mean(vals),
		valStd:  stdDev(vals),
	}
}

// mean calculates the arithmetic mean of a slice.
func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// stdDev calculates the standard deviation of a slice.
func stdDev(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	m := mean(values)
	sumSq := 0.0
	for _, v := range values {
		diff := v - m
		sumSq += diff * diff
	}
	return math.Sqrt(sumSq / float64(len(values)))
}

// minInt returns the smaller of two ints.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// maxInt returns the larger of two ints.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// minFloat returns the smaller of two floats.
func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// maxFloat returns the larger of two floats.
func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// Container returns the panel container.
// onSaveAligned normalizes both images, saves them as PNGs, and enables other panels.
func (ip *ImportPanel) onSaveAligned() {
	if ip.state.ProjectPath == "" {
		dialog.ShowInformation("Save Project First",
			"Please save the project before saving aligned images.", ip.window)
		return
	}

	if ip.state.FrontImage == nil && ip.state.BackImage == nil {
		dialog.ShowInformation("No Images",
			"Load at least one image before saving alignment.", ip.window)
		return
	}

	projectDir := filepath.Dir(ip.state.ProjectPath)

	ip.saveAlignedBtn.Disable()
	ip.alignStatus.SetText("Normalizing images...")

	go func() {
		var errs []string

		if ip.state.FrontImage != nil {
			if err := ip.state.NormalizeFrontImage(projectDir); err != nil {
				errs = append(errs, "Front: "+err.Error())
			}
		}
		if ip.state.BackImage != nil {
			if err := ip.state.NormalizeBackImage(projectDir); err != nil {
				errs = append(errs, "Back: "+err.Error())
			}
		}

		// Save project to persist normalized paths
		if err := ip.state.SaveProject(ip.state.ProjectPath); err != nil {
			errs = append(errs, "Save: "+err.Error())
		}

		// Update UI on main thread
		ip.saveAlignedBtn.Enable()
		if len(errs) > 0 {
			ip.alignStatus.SetText("Errors: " + strings.Join(errs, "; "))
			return
		}

		ip.alignStatus.SetText("Aligned images saved")

		// Update UI state
		ip.updateAlignmentUI()
		if ip.sidePanel != nil {
			ip.sidePanel.updatePanelEnableState()
		}

		// Fire events so mainwindow can enable menu items and
		// component overlay gets refreshed
		ip.state.Emit(app.EventNormalizationComplete, nil)
		ip.state.Emit(app.EventComponentsChanged, nil)

		// Refresh canvas with normalized images
		if ip.sidePanel != nil {
			ip.sidePanel.SyncLayers()
		}
		ip.canvas.Refresh()
		ip.RefreshLabels()
	}()
}

// onRealign reloads raw images with saved transforms for re-editing alignment.
func (ip *ImportPanel) onRealign() {
	if ip.state.FrontImage == nil && ip.state.BackImage == nil {
		return
	}

	ip.alignStatus.SetText("Reloading raw images...")

	go func() {
		// Reload raw images (this applies crop + auto-rotation but NOT manual transforms)
		if ip.state.FrontImage != nil && ip.state.FrontImage.Path != "" {
			if err := ip.state.LoadFrontImage(ip.state.FrontImage.Path); err != nil {
				fmt.Printf("Error reloading front image: %v\n", err)
			}
		}
		if ip.state.BackImage != nil && ip.state.BackImage.Path != "" {
			if err := ip.state.LoadBackImage(ip.state.BackImage.Path); err != nil {
				fmt.Printf("Error reloading back image: %v\n", err)
			}
		}

		// Clear normalized state so HasNormalizedImages() returns false
		ip.state.FrontNormalizedPath = ""
		ip.state.BackNormalizedPath = ""

		// Restore manual transforms from state onto layers
		// (LoadFrontImage/LoadBackImage don't set manual transforms, but the state still has them)
		if ip.state.FrontImage != nil {
			ip.state.FrontImage.ManualOffsetX = ip.state.FrontManualOffset.X
			ip.state.FrontImage.ManualOffsetY = ip.state.FrontManualOffset.Y
			ip.state.FrontImage.ManualRotation = ip.state.FrontManualRotation
			ip.state.FrontImage.RotationCenterX = ip.state.FrontRotationCenter.X
			ip.state.FrontImage.RotationCenterY = ip.state.FrontRotationCenter.Y
			ip.state.FrontImage.ShearTopX = ip.state.FrontShearTopX
			ip.state.FrontImage.ShearBottomX = ip.state.FrontShearBottomX
			ip.state.FrontImage.ShearLeftY = ip.state.FrontShearLeftY
			ip.state.FrontImage.ShearRightY = ip.state.FrontShearRightY
			ip.state.FrontImage.IsNormalized = false
		}
		if ip.state.BackImage != nil {
			ip.state.BackImage.ManualOffsetX = ip.state.BackManualOffset.X
			ip.state.BackImage.ManualOffsetY = ip.state.BackManualOffset.Y
			ip.state.BackImage.ManualRotation = ip.state.BackManualRotation
			ip.state.BackImage.RotationCenterX = ip.state.BackRotationCenter.X
			ip.state.BackImage.RotationCenterY = ip.state.BackRotationCenter.Y
			ip.state.BackImage.ShearTopX = ip.state.BackShearTopX
			ip.state.BackImage.ShearBottomX = ip.state.BackShearBottomX
			ip.state.BackImage.ShearLeftY = ip.state.BackShearLeftY
			ip.state.BackImage.ShearRightY = ip.state.BackShearRightY
			ip.state.BackImage.IsNormalized = false
		}

		// Show alignment controls, hide Re-align button
		ip.updateAlignmentUI()

		// Disable other panels until re-aligned
		if ip.sidePanel != nil {
			ip.sidePanel.updatePanelEnableState()
		}

		ip.alignStatus.SetText("Re-alignment mode - adjust and Save Aligned")

		// Refresh
		if ip.sidePanel != nil {
			ip.sidePanel.SyncLayers()
		}
		ip.canvas.Refresh()
		ip.RefreshLabels()
	}()
}

func (ip *ImportPanel) Container() fyne.CanvasObject {
	return ip.container
}

// ApplyLayerSelection raises the currently selected layer to the top.
func (ip *ImportPanel) ApplyLayerSelection() {
	if ip.layerSelect.Selected == "Front" {
		ip.canvas.RaiseLayerBySide(pcbimage.SideFront)
	} else {
		ip.canvas.RaiseLayerBySide(pcbimage.SideBack)
	}
}

// AutoDetectAndAlign runs the full detection and alignment pipeline automatically.
// This is called on startup when images are restored from preferences.
// Runs synchronously to complete before UI is shown.
func (ip *ImportPanel) AutoDetectAndAlign() {
	// Only proceed if both images are loaded
	if ip.state.FrontImage == nil || ip.state.BackImage == nil {
		return
	}

	fmt.Println("Auto-detect: starting detection and alignment...")

	dpi := ip.state.DPI
	if dpi == 0 && ip.state.FrontImage.DPI > 0 {
		dpi = ip.state.FrontImage.DPI
	}

	// Step 1: Detect contacts on front
	fmt.Println("Auto-detect: detecting front contacts...")
	frontResult, frontErr := alignment.DetectContactsOnTopEdge(
		ip.state.FrontImage.Image, ip.state.BoardSpec, dpi, nil)
	if frontErr != nil {
		fmt.Printf("Auto-detect: front detection error: %v\n", frontErr)
	}
	if frontResult != nil {
		ip.state.FrontDetectionResult = frontResult
		if frontResult.DPI > 0 && ip.state.DPI == 0 {
			ip.state.DPI = frontResult.DPI
			dpi = frontResult.DPI
		}
		fmt.Printf("Auto-detect: found %d front contacts\n", len(frontResult.Contacts))
	}

	// Step 2: Detect contacts on back
	fmt.Println("Auto-detect: detecting back contacts...")
	backResult, backErr := alignment.DetectContactsOnTopEdge(
		ip.state.BackImage.Image, ip.state.BoardSpec, dpi, nil)
	if backErr != nil {
		fmt.Printf("Auto-detect: back detection error: %v\n", backErr)
	}
	if backResult != nil {
		ip.state.BackDetectionResult = backResult
		fmt.Printf("Auto-detect: found %d back contacts\n", len(backResult.Contacts))
	}

	// Update contact info
	frontCount := 0
	backCount := 0
	if frontResult != nil {
		frontCount = len(frontResult.Contacts)
	}
	if backResult != nil {
		backCount = len(backResult.Contacts)
	}
	ip.contactInfoLabel.SetText(fmt.Sprintf("Front: %d, Back: %d contacts", frontCount, backCount))

	// Step 3: Align if we have enough contacts on both sides
	if frontResult != nil && backResult != nil &&
		len(frontResult.Contacts) >= 10 && len(backResult.Contacts) >= 10 {
		fmt.Println("Auto-detect: aligning images...")
		ip.performAlignment(frontResult.Contacts, backResult.Contacts, dpi)
	} else {
		fmt.Printf("Auto-detect: insufficient contacts (F:%d B:%d) - manual alignment needed\n",
			frontCount, backCount)
	}

	fmt.Println("Auto-detect: complete")
}

// createContactOverlay creates an overlay for detected contacts.
func (ip *ImportPanel) createContactOverlay(name string, contacts []alignment.Contact, c color.RGBA) {
	overlay := &canvas.Overlay{
		Color:      c,
		Rectangles: make([]canvas.OverlayRect, len(contacts)),
	}
	for i, contact := range contacts {
		var fill canvas.FillPattern
		switch contact.Pass {
		case alignment.PassFirst:
			fill = canvas.FillSolid
		case alignment.PassBruteForce:
			fill = canvas.FillStripe
		case alignment.PassRescue:
			fill = canvas.FillCrosshatch
		default:
			fill = canvas.FillSolid
		}
		overlay.Rectangles[i] = canvas.OverlayRect{
			X: contact.Bounds.X, Y: contact.Bounds.Y,
			Width: contact.Bounds.Width, Height: contact.Bounds.Height,
			Fill:  fill,
		}
	}
	ip.canvas.SetOverlay(name, overlay)
}

// performAlignment aligns the back image to the front using contacts and ejector marks.
func (ip *ImportPanel) performAlignment(frontContacts, backContacts []alignment.Contact, dpi float64) {
	// Coarse alignment using contact positions (translation only)
	var frontSumX, frontSumY, backSumX, backSumY float64
	minContacts := minInt(len(frontContacts), len(backContacts))

	for i := 0; i < minContacts; i++ {
		frontSumX += frontContacts[i].Center.X
		frontSumY += frontContacts[i].Center.Y
		backSumX += backContacts[i].Center.X
		backSumY += backContacts[i].Center.Y
	}

	frontAvgX := frontSumX / float64(minContacts)
	frontAvgY := frontSumY / float64(minContacts)
	backAvgX := backSumX / float64(minContacts)
	backAvgY := backSumY / float64(minContacts)

	deltaX := frontAvgX - backAvgX
	deltaY := frontAvgY - backAvgY

	// Apply initial translation
	translatedBack := translateImage(ip.state.BackImage.Image, int(deltaX), int(deltaY))

	// Detect ejector marks for fine alignment
	frontMarks := alignment.DetectEjectorMarksFromImage(ip.state.FrontImage.Image, frontContacts, dpi)

	translatedBackContacts := make([]alignment.Contact, len(backContacts))
	for i, c := range backContacts {
		translatedBackContacts[i] = c
		translatedBackContacts[i].Center.X += deltaX
		translatedBackContacts[i].Center.Y += deltaY
	}
	backMarks := alignment.DetectEjectorMarksFromImage(translatedBack, translatedBackContacts, dpi)

	fmt.Printf("Auto-align: front ejector marks=%d, back ejector marks=%d\n", len(frontMarks), len(backMarks))

	// Draw ejector mark overlays
	ip.createEjectorOverlay("front_ejectors", frontMarks, color.RGBA{R: 255, G: 255, B: 0, A: 255})
	ip.createEjectorOverlay("back_ejectors", backMarks, colorutil.Cyan)

	var finalImage image.Image = translatedBack
	var alignInfo string

	// Apply shear transform if we have matching ejector marks
	if len(frontMarks) >= 2 && len(backMarks) >= 2 {
		var frontLeft, frontRight, backLeft, backRight *alignment.EjectorMark
		for i := range frontMarks {
			if frontMarks[i].Side == "left" {
				frontLeft = &frontMarks[i]
			} else if frontMarks[i].Side == "right" {
				frontRight = &frontMarks[i]
			}
		}
		for i := range backMarks {
			if backMarks[i].Side == "left" {
				backLeft = &backMarks[i]
			} else if backMarks[i].Side == "right" {
				backRight = &backMarks[i]
			}
		}

		if frontLeft != nil && frontRight != nil && backLeft != nil && backRight != nil {
			contactY := frontAvgY
			finalImage, alignInfo = applyShearAlignment(
				translatedBack,
				backLeft.Center, backRight.Center,
				frontLeft.Center, frontRight.Center,
				contactY,
			)
			fmt.Printf("Auto-align: shear alignment: %s\n", alignInfo)
		} else {
			alignInfo = fmt.Sprintf("translated (%.1f, %.1f) px (missing ejector marks)", deltaX, deltaY)
		}
	} else {
		alignInfo = fmt.Sprintf("translated (%.1f, %.1f) px (no ejector marks)", deltaX, deltaY)
	}

	// Update state
	ip.state.BackImage.Image = finalImage
	ip.state.Aligned = true

	// Update back contacts overlay with translated positions
	alignedBackContacts := make([]alignment.Contact, len(backContacts))
	for i, c := range backContacts {
		alignedBackContacts[i] = alignment.Contact{
			Bounds: geometry.RectInt{
				X: c.Bounds.X + int(deltaX), Y: c.Bounds.Y + int(deltaY),
				Width: c.Bounds.Width, Height: c.Bounds.Height,
			},
			Center: geometry.Point2D{
				X: c.Center.X + deltaX,
				Y: c.Center.Y + deltaY,
			},
			Pass: c.Pass,
		}
	}
	ip.state.BackDetectionResult.Contacts = alignedBackContacts
	ip.createContactOverlay("back_contacts", alignedBackContacts, color.RGBA{R: 0, G: 0, B: 255, A: 255})

	ip.alignStatus.SetText("Aligned: " + alignInfo)

	// Clear debug overlays now that alignment is complete
	ip.canvas.ClearOverlay("front_contacts")
	ip.canvas.ClearOverlay("back_contacts")
	ip.canvas.ClearOverlay("front_expected")
	ip.canvas.ClearOverlay("back_expected")
	ip.canvas.ClearOverlay("front_search_area")
	ip.canvas.ClearOverlay("back_search_area")
	ip.canvas.ClearOverlay("front_ejectors")
	ip.canvas.ClearOverlay("back_ejectors")

	ip.state.Emit(app.EventAlignmentComplete, nil)
}

// createEjectorOverlay creates an overlay for ejector marks with target crosshairs.
func (ip *ImportPanel) createEjectorOverlay(name string, marks []alignment.EjectorMark, c color.RGBA) {
	overlay := &canvas.Overlay{
		Color:      c,
		Rectangles: make([]canvas.OverlayRect, len(marks)),
	}
	markerSize := 40 // pixels
	for i, mark := range marks {
		overlay.Rectangles[i] = canvas.OverlayRect{
			X: int(mark.Center.X) - markerSize/2, Y: int(mark.Center.Y) - markerSize/2,
			Width: markerSize, Height: markerSize,
			Label: mark.Side,
			Fill:  canvas.FillTarget,
		}
	}
	ip.canvas.SetOverlay(name, overlay)
}

func (ip *ImportPanel) updateImageStatus() {
	var frontDPI, backDPI float64

	if ip.state.FrontImage != nil {
		ip.frontLabel.SetText(fmt.Sprintf("%dx%d pixels",
			ip.state.FrontImage.Width(), ip.state.FrontImage.Height()))
		frontDPI = ip.state.FrontImage.DPI
	} else {
		ip.frontLabel.SetText("No front image loaded")
	}

	if ip.state.BackImage != nil {
		ip.backLabel.SetText(fmt.Sprintf("%dx%d pixels",
			ip.state.BackImage.Width(), ip.state.BackImage.Height()))
		backDPI = ip.state.BackImage.DPI
	} else {
		ip.backLabel.SetText("No back image loaded")
	}

	// Check DPI consistency
	if frontDPI > 0 && backDPI > 0 && frontDPI != backDPI {
		ip.dpiLabel.SetText(fmt.Sprintf("DPI MISMATCH: %.0f vs %.0f", frontDPI, backDPI))
		ip.state.DPI = 0
	} else if frontDPI > 0 {
		ip.state.DPI = frontDPI
		ip.dpiLabel.SetText(fmt.Sprintf("DPI: %.0f", frontDPI))
	} else if backDPI > 0 {
		ip.state.DPI = backDPI
		ip.dpiLabel.SetText(fmt.Sprintf("DPI: %.0f", backDPI))
	} else {
		ip.state.DPI = 0
		ip.dpiLabel.SetText("DPI: Unknown")
	}
}

// ComponentsPanel displays and manages detected components.
type ComponentsPanel struct {
	state     *app.State
	canvas    *canvas.ImageCanvas
	container fyne.CanvasObject
	window    fyne.Window

	list          *widget.List
	detailCard    *widget.Card
	split         *container.Split // Draggable split between list and edit form
	hoveredIndex  int              // -1 when no component is hovered
	sortedIndices []int            // Indices into state.Components, sorted by Y then X

	// Inline edit form
	editingComp        *component.Component // Currently editing component (nil if none)
	editingIndex       int                  // Index in state.Components
	editForm           *fyne.Container      // The edit form container
	idEntry            *widget.Entry
	partNumberEntry    *widget.Entry
	packageEntry       *widget.Entry
	manufacturerEntry  *widget.Entry
	placeEntry         *widget.Entry
	dateCodeEntry      *widget.Entry
	revisionEntry      *widget.Entry
	speedGradeEntry    *widget.Entry
	descriptionEntry   *widget.Entry
	ocrTextEntry       *widget.Entry
	correctedTextEntry *widget.Entry
	ocrOrientation     *widget.RadioGroup
	ocrTrainingLabel   *widget.Label
}

// updateOCRTrainingLabel updates the training status label with sample counts.
func (cp *ComponentsPanel) updateOCRTrainingLabel() {
	if cp.ocrTrainingLabel == nil {
		return
	}
	if cp.state.GlobalOCRTraining == nil || len(cp.state.GlobalOCRTraining.Samples) == 0 {
		cp.ocrTrainingLabel.SetText("No training data")
		return
	}
	db := cp.state.GlobalOCRTraining
	orientCounts := make(map[string]int)
	for _, s := range db.Samples {
		orientCounts[s.Orientation]++
	}
	cp.ocrTrainingLabel.SetText(fmt.Sprintf("Trained: %d samples (N:%d S:%d E:%d W:%d)",
		len(db.Samples), orientCounts["N"], orientCounts["S"], orientCounts["E"], orientCounts["W"]))
}

// rebuildSortedIndices rebuilds the sorted indices using natural numeric sorting by component ID.
// This ensures A2 comes before A10, U1 before U2 before U10, etc.
func (cp *ComponentsPanel) rebuildSortedIndices() {
	n := len(cp.state.Components)
	cp.sortedIndices = make([]int, n)
	for i := range cp.sortedIndices {
		cp.sortedIndices[i] = i
	}

	// Sort by component ID using natural numeric ordering
	sort.Slice(cp.sortedIndices, func(i, j int) bool {
		ci := cp.state.Components[cp.sortedIndices[i]]
		cj := cp.state.Components[cp.sortedIndices[j]]
		return naturalLess(ci.ID, cj.ID)
	})
}

// naturalLess compares two strings using natural numeric ordering.
// "A2" < "A10", "U1" < "U2" < "U10", etc.
func naturalLess(a, b string) bool {
	// Split each string into chunks of letters and numbers
	chunksA := splitNatural(a)
	chunksB := splitNatural(b)

	// Compare chunk by chunk
	for i := 0; i < len(chunksA) && i < len(chunksB); i++ {
		ca, cb := chunksA[i], chunksB[i]

		// If both chunks are numeric, compare numerically
		if isNumeric(ca) && isNumeric(cb) {
			na := parseNum(ca)
			nb := parseNum(cb)
			if na != nb {
				return na < nb
			}
		} else {
			// Compare as strings (case-insensitive)
			cmp := strings.Compare(strings.ToUpper(ca), strings.ToUpper(cb))
			if cmp != 0 {
				return cmp < 0
			}
		}
	}

	// If all compared chunks are equal, shorter string comes first
	return len(chunksA) < len(chunksB)
}

// splitNatural splits a string into alternating chunks of letters and digits.
// "A10BC2" -> ["A", "10", "BC", "2"]
func splitNatural(s string) []string {
	var chunks []string
	var current strings.Builder

	wasDigit := false
	for i, r := range s {
		isDigit := r >= '0' && r <= '9'

		if i > 0 && isDigit != wasDigit {
			// Transition between letter and digit, save current chunk
			chunks = append(chunks, current.String())
			current.Reset()
		}

		current.WriteRune(r)
		wasDigit = isDigit
	}

	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}

	return chunks
}

// isNumeric checks if a string is all digits.
func isNumeric(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// parseNum parses a numeric string to int, returns 0 on error.
func parseNum(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n = n*10 + int(r-'0')
		}
	}
	return n
}

// focusableContainer wraps a container to receive keyboard events.
type focusableContainer struct {
	widget.BaseWidget
	content     fyne.CanvasObject
	onTypedKey  func(*fyne.KeyEvent)
	onTypedRune func(rune)
	focused     bool
}

func newFocusableContainer(content fyne.CanvasObject, onTypedKey func(*fyne.KeyEvent)) *focusableContainer {
	fc := &focusableContainer{
		content:    content,
		onTypedKey: onTypedKey,
	}
	fc.ExtendBaseWidget(fc)
	return fc
}

func (fc *focusableContainer) CreateRenderer() fyne.WidgetRenderer {
	return &focusableContainerRenderer{container: fc}
}

func (fc *focusableContainer) FocusGained() {
	fc.focused = true
}

func (fc *focusableContainer) FocusLost() {
	fc.focused = false
}

func (fc *focusableContainer) TypedRune(r rune) {
	// Handle +, -, *, / as special keys for component adjustment
	if fc.onTypedKey != nil {
		switch r {
		case '+', '=': // + or = (unshifted +)
			fc.onTypedKey(&fyne.KeyEvent{Name: "Plus"})
		case '-':
			fc.onTypedKey(&fyne.KeyEvent{Name: "Minus"})
		case '*':
			fc.onTypedKey(&fyne.KeyEvent{Name: "Asterisk"})
		case '/':
			fc.onTypedKey(&fyne.KeyEvent{Name: "Slash"})
		}
	}
}

func (fc *focusableContainer) TypedKey(ev *fyne.KeyEvent) {
	if fc.onTypedKey != nil {
		fc.onTypedKey(ev)
	}
}

func (fc *focusableContainer) Focused() bool {
	return fc.focused
}

type focusableContainerRenderer struct {
	container *focusableContainer
}

func (r *focusableContainerRenderer) Layout(size fyne.Size) {
	r.container.content.Resize(size)
}

func (r *focusableContainerRenderer) MinSize() fyne.Size {
	return r.container.content.MinSize()
}

func (r *focusableContainerRenderer) Refresh() {
	r.container.content.Refresh()
}

func (r *focusableContainerRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.container.content}
}

func (r *focusableContainerRenderer) Destroy() {}

// tappableListItem is a label that supports left-click selection and right-click for deletion.
type tappableListItem struct {
	widget.Label
	onTapped     func()
	onRightClick func()
	onMouseIn    func()
	onMouseOut   func()
	focusTarget  fyne.Focusable // Widget to focus on hover
}

func newTappableListItem(onRightClick func()) *tappableListItem {
	item := &tappableListItem{onRightClick: onRightClick}
	item.ExtendBaseWidget(item)
	return item
}

// Tapped implements fyne.Tappable for left-click selection.
func (t *tappableListItem) Tapped(_ *fyne.PointEvent) {
	if t.onTapped != nil {
		t.onTapped()
	}
}

// TappedSecondary implements fyne.SecondaryTappable for right-click.
func (t *tappableListItem) TappedSecondary(_ *fyne.PointEvent) {
	if t.onRightClick != nil {
		t.onRightClick()
	}
}

// MouseIn implements desktop.Hoverable for hover enter.
func (t *tappableListItem) MouseIn(_ *desktop.MouseEvent) {
	if t.onMouseIn != nil {
		t.onMouseIn()
	}
	// Request focus to enable keyboard input
	if t.focusTarget != nil {
		if c := fyne.CurrentApp().Driver().CanvasForObject(t); c != nil {
			c.Focus(t.focusTarget)
		}
	}
}

// MouseOut implements desktop.Hoverable for hover exit.
func (t *tappableListItem) MouseOut() {
	if t.onMouseOut != nil {
		t.onMouseOut()
	}
}

// MouseMoved implements desktop.Hoverable (required but unused).
func (t *tappableListItem) MouseMoved(_ *desktop.MouseEvent) {}

// NewComponentsPanel creates a new components panel.
func NewComponentsPanel(state *app.State, canv *canvas.ImageCanvas) *ComponentsPanel {
	cp := &ComponentsPanel{
		state:        state,
		canvas:       canv,
		hoveredIndex: -1,
	}

	// Build initial sorted indices
	cp.rebuildSortedIndices()

	// Create focusable wrapper for keyboard input (will be set up after list creation)
	var focusWrapper *focusableContainer

	// Component list with right-click delete support
	// Uses sortedIndices to display components sorted by Y, X
	cp.list = widget.NewList(
		func() int {
			return len(cp.sortedIndices)
		},
		func() fyne.CanvasObject {
			// Create a tappable item - onRightClick will be set in update
			return newTappableListItem(nil)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			item := obj.(*tappableListItem)
			if id < len(cp.sortedIndices) {
				compIdx := cp.sortedIndices[id]
				if compIdx < len(state.Components) {
					comp := state.Components[compIdx]
					// Mark unsaved components with asterisk prefix
					prefix := ""
					if !comp.Confirmed {
						prefix = "* "
					}
					// Show part number if available, otherwise package
					detail := comp.PartNumber
					if detail == "" {
						detail = comp.Package
					}
					item.SetText(fmt.Sprintf("%s%s %s", prefix, comp.ID, detail))
					// Set up click handlers for this item (use actual component index)
					actualIdx := compIdx
					item.onTapped = func() {
						cp.list.Select(id)
						cp.showEditDialog(actualIdx)
					}
					item.onRightClick = func() {
						cp.showEditDialog(actualIdx)
					}
					// Set up hover handlers for highlighting and keyboard focus
					item.onMouseIn = func() {
						cp.hoveredIndex = actualIdx
						cp.highlightComponent(actualIdx, false)
					}
					item.onMouseOut = func() {
						cp.hoveredIndex = -1
						cp.clearHighlight()
					}
					// Set focus target for keyboard input
					item.focusTarget = focusWrapper
				}
			}
		},
	)

	cp.list.OnSelected = func(id widget.ListItemID) {
		if id < len(cp.sortedIndices) {
			compIdx := cp.sortedIndices[id]
			if compIdx < len(state.Components) {
				cp.showComponentDetail(state.Components[compIdx])
			}
		}
	}

	// Create inline edit form entries
	cp.idEntry = widget.NewEntry()
	cp.idEntry.SetPlaceHolder("Component ID")

	cp.partNumberEntry = widget.NewEntry()
	cp.partNumberEntry.SetPlaceHolder("e.g., 74LS244")

	cp.packageEntry = widget.NewEntry()
	cp.packageEntry.SetPlaceHolder("e.g., DIP-20")

	cp.manufacturerEntry = widget.NewEntry()
	cp.manufacturerEntry.SetPlaceHolder("e.g., Texas Instruments")

	cp.placeEntry = widget.NewEntry()
	cp.placeEntry.SetPlaceHolder("e.g., Malaysia")

	cp.dateCodeEntry = widget.NewEntry()
	cp.dateCodeEntry.SetPlaceHolder("e.g., 8523")

	cp.revisionEntry = widget.NewEntry()
	cp.speedGradeEntry = widget.NewEntry()
	cp.speedGradeEntry.SetPlaceHolder("e.g., -25")

	cp.descriptionEntry = widget.NewEntry()
	cp.descriptionEntry.MultiLine = true
	cp.descriptionEntry.SetMinRowsVisible(2)

	cp.correctedTextEntry = widget.NewEntry()
	cp.correctedTextEntry.MultiLine = true
	cp.correctedTextEntry.SetMinRowsVisible(2)
	cp.correctedTextEntry.SetPlaceHolder("Corrected text (for training)")

	cp.ocrTextEntry = widget.NewEntry()
	cp.ocrTextEntry.MultiLine = true
	cp.ocrTextEntry.SetMinRowsVisible(2)
	cp.ocrTextEntry.SetPlaceHolder("OCR detected text")

	// OCR orientation selector
	cp.ocrOrientation = widget.NewRadioGroup([]string{"N", "S", "E", "W"}, nil)
	cp.ocrOrientation.Horizontal = true
	cp.ocrOrientation.SetSelected("N")

	// OCR buttons
	ocrBtn := widget.NewButton("OCR", func() {
		cp.runOCR()
	})
	trainBtn := widget.NewButton("Train", func() {
		cp.runOCRTraining()
	})

	// Save/Delete buttons for the form
	saveBtn := widget.NewButton("Save", func() {
		cp.saveEditingComponent()
	})
	saveBtn.Importance = widget.HighImportance

	deleteBtn := widget.NewButton("Delete", func() {
		cp.deleteEditingComponent()
	})
	deleteBtn.Importance = widget.DangerImportance

	// Build the edit form using compact layout
	formGrid := container.NewGridWithColumns(2,
		widget.NewLabel("ID:"), cp.idEntry,
		widget.NewLabel("Part #:"), cp.partNumberEntry,
		widget.NewLabel("Package:"), cp.packageEntry,
		widget.NewLabel("Mfr:"), cp.manufacturerEntry,
		widget.NewLabel("Place:"), cp.placeEntry,
		widget.NewLabel("Date:"), cp.dateCodeEntry,
		widget.NewLabel("Rev:"), cp.revisionEntry,
		widget.NewLabel("Speed:"), cp.speedGradeEntry,
	)

	cp.ocrTrainingLabel = widget.NewLabel("")
	cp.updateOCRTrainingLabel()

	ocrControls := container.NewHBox(
		ocrBtn, trainBtn,
		widget.NewLabel("Dir:"), cp.ocrOrientation,
	)

	formButtons := container.NewHBox(
		deleteBtn,
		layout.NewSpacer(),
		saveBtn,
	)

	// Combine into edit form
	cp.editForm = container.NewVBox(
		formGrid,
		widget.NewSeparator(),
		widget.NewLabel("Description:"),
		cp.descriptionEntry,
		widget.NewSeparator(),
		ocrControls,
		cp.ocrTrainingLabel,
		widget.NewLabel("Corrected:"),
		cp.correctedTextEntry,
		widget.NewLabel("OCR Result:"),
		cp.ocrTextEntry,
		widget.NewSeparator(),
		formButtons,
	)

	// Detail card containing the edit form (scrollable)
	editScroll := container.NewVScroll(cp.editForm)
	cp.detailCard = widget.NewCard("Component", "Select a component to edit", editScroll)

	// OCR Silkscreen button (batch OCR)
	ocrSilkscreenBtn := widget.NewButton("OCR All Silkscreen", func() {
		cp.onOCRSilkscreen()
	})

	// Set up right-click handler for deleting components on canvas
	cp.canvas.OnRightClick(cp.onRightClickDeleteComponent)

	// Set up left-click handler for resizing components
	cp.canvas.OnLeftClick(cp.onLeftClickResize)

	// Set up middle-click handler for flood fill component detection
	cp.canvas.OnMiddleClick(cp.OnMiddleClickFloodFill)

	// Wrap list in a scroll container
	listScroll := container.NewVScroll(cp.list)

	// Create focusable wrapper with key handler
	focusWrapper = newFocusableContainer(listScroll, cp.onKeyPressed)

	// Create VSplit between list and edit form with draggable divider
	cp.split = container.NewVSplit(focusWrapper, cp.detailCard)

	// Load saved split offset from preferences (default 0.5 = equal split)
	prefs := fyne.CurrentApp().Preferences()
	savedOffset := prefs.FloatWithFallback("components_split_offset", 0.5)
	cp.split.SetOffset(savedOffset)

	// Layout: OCR button at top, split fills remaining space
	cp.container = container.NewBorder(
		ocrSilkscreenBtn, // top: OCR button
		nil, nil, nil,
		cp.split, // center: draggable split between list and edit form
	)

	// Subscribe to component change events to refresh the list
	state.On(app.EventComponentsChanged, func(_ interface{}) {
		cp.rebuildSortedIndices()
		cp.list.Refresh()
		cp.updateComponentOverlay()
	})

	// Also subscribe to project loaded to handle startup case where
	// components are loaded before panel is created
	state.On(app.EventProjectLoaded, func(_ interface{}) {
		cp.rebuildSortedIndices()
		cp.list.Refresh()
		cp.updateComponentOverlay()
	})

	return cp
}

// Container returns the panel container.
func (cp *ComponentsPanel) Container() fyne.CanvasObject {
	return cp.container
}

// SetWindow sets the parent window for dialogs.
func (cp *ComponentsPanel) SetWindow(w fyne.Window) {
	cp.window = w
}

// SaveSplitOffset saves the current split position to preferences.
func (cp *ComponentsPanel) SaveSplitOffset() {
	if cp.split != nil {
		prefs := fyne.CurrentApp().Preferences()
		prefs.SetFloat("components_split_offset", cp.split.Offset)
	}
}

// showEditDialog populates the inline edit form for the given component index.
func (cp *ComponentsPanel) showEditDialog(index int) {
	if index < 0 || index >= len(cp.state.Components) {
		return
	}

	comp := cp.state.Components[index]
	cp.editingComp = comp
	cp.editingIndex = index

	// Populate form fields
	cp.idEntry.SetText(comp.ID)
	cp.partNumberEntry.SetText(comp.PartNumber)
	cp.packageEntry.SetText(comp.Package)
	cp.manufacturerEntry.SetText(comp.Manufacturer)
	cp.placeEntry.SetText(comp.Place)
	cp.dateCodeEntry.SetText(comp.DateCode)
	cp.revisionEntry.SetText(comp.Revision)
	cp.speedGradeEntry.SetText(comp.SpeedGrade)
	cp.descriptionEntry.SetText(comp.Description)
	cp.ocrTextEntry.SetText(comp.OCRText)
	cp.correctedTextEntry.SetText(comp.CorrectedText)

	// Set orientation - priority: component's saved orientation > sticky default > "N"
	if comp.OCROrientation != "" {
		cp.ocrOrientation.SetSelected(comp.OCROrientation)
	} else if cp.state.LastOCROrientation != "" {
		cp.ocrOrientation.SetSelected(cp.state.LastOCROrientation)
	} else {
		cp.ocrOrientation.SetSelected("N")
	}

	// Update card title
	cp.detailCard.SetTitle(comp.ID)
	cp.detailCard.SetSubTitle(fmt.Sprintf("%s - %s", comp.Package, comp.PartNumber))
	cp.detailCard.Refresh()

	// Highlight the component on canvas and scroll to it (initial selection)
	cp.highlightComponent(index, true)
}

func (cp *ComponentsPanel) showComponentDetail(c interface{}) {
	if comp, ok := c.(*component.Component); ok {
		// Find the index of this component
		for i, stateComp := range cp.state.Components {
			if stateComp == comp {
				cp.showEditDialog(i)
				return
			}
		}
	}
}

// saveEditingComponent saves changes from the inline form to the editing component.
func (cp *ComponentsPanel) saveEditingComponent() {
	if cp.editingComp == nil {
		return
	}

	// Apply changes from form
	cp.editingComp.ID = cp.idEntry.Text
	cp.editingComp.PartNumber = cp.partNumberEntry.Text
	cp.editingComp.Package = cp.packageEntry.Text
	cp.editingComp.Manufacturer = cp.manufacturerEntry.Text
	cp.editingComp.Place = cp.placeEntry.Text
	cp.editingComp.DateCode = cp.dateCodeEntry.Text
	cp.editingComp.Revision = cp.revisionEntry.Text
	cp.editingComp.SpeedGrade = cp.speedGradeEntry.Text
	cp.editingComp.Description = cp.descriptionEntry.Text
	cp.editingComp.OCRText = cp.ocrTextEntry.Text
	cp.editingComp.CorrectedText = cp.correctedTextEntry.Text
	cp.editingComp.OCROrientation = cp.ocrOrientation.Selected

	// Update sticky orientation
	cp.state.LastOCROrientation = cp.ocrOrientation.Selected

	// Add corrected text as training sample if we have both OCR and corrected text
	corrected := cp.correctedTextEntry.Text
	ocrText := cp.ocrTextEntry.Text
	orientation := cp.ocrOrientation.Selected
	if strings.TrimSpace(corrected) != "" && strings.TrimSpace(ocrText) != "" {
		score := ocr.TextSimilarity(ocrText, corrected)
		// Use orientation-specific params if available, otherwise recommended
		var params ocr.OCRParams
		if cp.state.GlobalOCRTraining != nil {
			if p, ok := cp.state.GlobalOCRTraining.GetParamsForOrientation(orientation); ok {
				params = p
			} else {
				params = cp.state.GlobalOCRTraining.GetRecommendedParams()
			}
		} else {
			params = ocr.DefaultOCRParams()
		}
		if score >= 0.7 {
			cp.state.AddOCRTrainingSample(corrected, ocrText, score, orientation, params)
			cp.updateOCRTrainingLabel()
			fmt.Printf("[Save] Added training sample: score=%.1f%% orientation=%s\n", score*100, orientation)
		} else {
			fmt.Printf("[Save] Score too low for training: %.1f%% orientation=%s\n", score*100, orientation)
		}
	}

	cp.rebuildSortedIndices()
	cp.list.Refresh()
	cp.updateComponentOverlay()

	// Update card title
	cp.detailCard.SetTitle(cp.editingComp.ID)
	cp.detailCard.SetSubTitle(fmt.Sprintf("%s - %s", cp.editingComp.Package, cp.editingComp.PartNumber))

	// Save split offset while we're saving
	cp.SaveSplitOffset()

	// Persist to disk
	if cp.state.ProjectPath != "" {
		if err := cp.state.SaveProject(cp.state.ProjectPath); err != nil {
			fmt.Printf("Error saving project: %v\n", err)
		}
	} else {
		cp.state.SetModified(true)
	}

	fmt.Printf("Saved component %s\n", cp.editingComp.ID)
}

// deleteEditingComponent deletes the currently editing component.
func (cp *ComponentsPanel) deleteEditingComponent() {
	if cp.editingComp == nil || cp.editingIndex < 0 {
		return
	}

	cp.deleteComponent(cp.editingIndex)

	// Clear the form
	cp.editingComp = nil
	cp.editingIndex = -1
	cp.clearEditForm()
}

// clearEditForm clears all form fields and the highlight.
func (cp *ComponentsPanel) clearEditForm() {
	cp.idEntry.SetText("")
	cp.partNumberEntry.SetText("")
	cp.packageEntry.SetText("")
	cp.manufacturerEntry.SetText("")
	cp.placeEntry.SetText("")
	cp.dateCodeEntry.SetText("")
	cp.revisionEntry.SetText("")
	cp.speedGradeEntry.SetText("")
	cp.descriptionEntry.SetText("")
	cp.ocrTextEntry.SetText("")
	cp.correctedTextEntry.SetText("")
	cp.ocrOrientation.SetSelected("N")

	cp.detailCard.SetTitle("Component")
	cp.detailCard.SetSubTitle("Select a component to edit")
	cp.detailCard.Refresh()

	// Clear the highlight overlay since no component is selected
	cp.canvas.SetOverlay("component_highlight", nil)
	cp.canvas.Refresh()
}

// getComponentImage returns the source image for the currently editing component.
func (cp *ComponentsPanel) getComponentImage() image.Image {
	if cp.editingComp == nil {
		return nil
	}

	// Get the original layer image for OCR (not the zoomed canvas output)
	var img image.Image
	switch cp.editingComp.Layer {
	case pcbimage.SideBack:
		if cp.state.BackImage != nil {
			img = cp.state.BackImage.Image
		}
	default: // SideFront or unset
		if cp.state.FrontImage != nil {
			img = cp.state.FrontImage.Image
		}
	}
	if img == nil {
		img = cp.canvas.GetRenderedOutput()
	}
	return img
}

// runOCR performs OCR on the currently editing component.
func (cp *ComponentsPanel) runOCR() {
	if cp.editingComp == nil {
		fmt.Println("[OCR] No component selected")
		return
	}

	img := cp.getComponentImage()
	if img == nil {
		fmt.Println("[OCR] No image available")
		return
	}

	// Extract the component region from the image
	bounds := cp.editingComp.Bounds
	x, y := int(bounds.X), int(bounds.Y)
	w, h := int(bounds.Width), int(bounds.Height)
	fmt.Printf("[OCR] Component bounds: (%d,%d) %dx%d\n", x, y, w, h)

	// Clamp to image bounds
	imgBounds := img.Bounds()
	if x < imgBounds.Min.X {
		x = imgBounds.Min.X
	}
	if y < imgBounds.Min.Y {
		y = imgBounds.Min.Y
	}
	w = min(w, imgBounds.Max.X-x)
	h = min(h, imgBounds.Max.Y-y)

	if w <= 0 || h <= 0 {
		fmt.Println("[OCR] Invalid bounds")
		return
	}

	// Create cropped region
	cropped := image.NewRGBA(image.Rect(0, 0, w, h))
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			cropped.Set(dx, dy, img.At(x+dx, y+dy))
		}
	}

	// Get orientation and logo rotation
	orientation := cp.ocrOrientation.Selected
	logoRotation := orientationToRotation(orientation)

	// Rotate for OCR first (preview shows the rotated view)
	rotated := rotateForOCR(cropped, orientation)

	// Create a copy for logo masking (keep rotated as the clean original)
	rotBounds := rotated.Bounds()
	masked := image.NewRGBA(rotBounds)
	copy(masked.Pix, rotated.Pix)

	// Detect logos on the masked copy and fill them
	var detectedLogos []logo.LogoMatch
	if cp.state.LogoLibrary != nil && len(cp.state.LogoLibrary.Logos) > 0 {
		mw, mh := rotBounds.Dx(), rotBounds.Dy()
		searchBounds := geometry.RectInt{X: 0, Y: 0, Width: mw, Height: mh}
		detectedLogos = cp.state.LogoLibrary.DetectLogos(masked, searchBounds, 0.75, logoRotation)
		if len(detectedLogos) > 0 {
			fmt.Printf("[OCR] Detected %d logos\n", len(detectedLogos))
			bgColor := calculateBackgroundColor(masked)
			compArea := mw * mh
			for _, m := range detectedLogos {
				logoArea := m.Bounds.Width * m.Bounds.Height
				pct := logoArea * 100 / compArea
				fmt.Printf("[OCR Logo] name=%q score=%.3f rot=%d scale=%.2f bounds=(%d,%d %dx%d) area=%d%% of component\n",
					m.Logo.Name, m.Score, m.Rotation, m.ScaleFactor,
					m.Bounds.X, m.Bounds.Y, m.Bounds.Width, m.Bounds.Height, pct)
				if logoArea > compArea/4 {
					fmt.Printf("[OCR Logo] SKIP: too large (%d%% > 25%%)\n", pct)
					continue
				}
				fmt.Printf("[OCR Logo] MASK: filling (%d,%d)-(%d,%d) with bg=(%d,%d,%d)\n",
					m.Bounds.X, m.Bounds.Y,
					m.Bounds.X+m.Bounds.Width, m.Bounds.Y+m.Bounds.Height,
					bgColor.R, bgColor.G, bgColor.B)
				maskRegion(masked, m.Bounds, bgColor)
			}
		}
	}

	// Show OCR preview dialog: raw → B&W → logo-masked
	cp.showOCRPreview(rotated, masked, orientation)

	// OCR runs on the masked image (logos removed), binarized and despeckled
	ocrGray, mw, mh := rgbaToGray(masked)
	ocrThresh := robustOtsu(ocrGray, mw, mh)

	ocrBW := make([]bool, mw*mh)
	for i := 0; i < mw*mh; i++ {
		ocrBW[i] = ocrGray[i] > ocrThresh
	}
	despeckleBW(ocrBW, mw, mh)

	// Build grayscale image bytes (0 or 255) for gocv
	ocrBytes := make([]byte, mw*mh)
	for i := 0; i < mw*mh; i++ {
		if ocrBW[i] {
			ocrBytes[i] = 255
		}
	}

	grayMat, err := gocv.NewMatFromBytes(mh, mw, gocv.MatTypeCV8UC1, ocrBytes)
	if err != nil {
		fmt.Printf("[OCR] Mat conversion failed: %v\n", err)
		return
	}
	defer grayMat.Close()

	// Convert grayscale to BGR for OCR engine
	bgr := gocv.NewMat()
	defer bgr.Close()
	gocv.CvtColor(grayMat, &bgr, gocv.ColorGrayToBGR)

	// Create OCR engine
	engine, err := ocr.NewEngine()
	if err != nil {
		fmt.Printf("[OCR] Engine creation failed: %v\n", err)
		return
	}
	defer engine.Close()

	// Run OCR - use best available params:
	// 1. Global training database params (orientation-specific, then generic)
	// 2. Default params
	var text string
	var params ocr.OCRParams
	paramsSource := "default"

	if cp.state.GlobalOCRTraining != nil && len(cp.state.GlobalOCRTraining.Samples) >= 5 {
		if orientParams, ok := cp.state.GlobalOCRTraining.GetParamsForOrientation(orientation); ok {
			params = orientParams
			paramsSource = fmt.Sprintf("global/%s (%d samples)", orientation, len(cp.state.GlobalOCRTraining.Samples))
		} else {
			params = cp.state.GlobalOCRTraining.GetRecommendedParams()
			paramsSource = fmt.Sprintf("global (%d samples)", len(cp.state.GlobalOCRTraining.Samples))
		}
	} else {
		params = ocr.DefaultOCRParams()
	}

	fmt.Printf("[OCR] Using %s params\n", paramsSource)
	text, err = engine.RecognizeWithParams(bgr, params)

	if err != nil {
		fmt.Printf("[OCR] Failed: %v\n", err)
		return
	}

	// Image is already binarized+despeckled, no histogram fallback needed

	// Fix common OCR misreads in part numbers (4↔A, 5↔S ambiguity)
	text = fixOCRPartNumbers(text)

	// Prepend detected logos
	var detectedManufacturer string
	if len(detectedLogos) > 0 {
		var logoNames []string
		for _, m := range detectedLogos {
			logoNames = append(logoNames, fmt.Sprintf("<%s>", m.Logo.Name))
			if detectedManufacturer == "" && m.Logo.ManufacturerID != "" {
				detectedManufacturer = m.Logo.ManufacturerID
			}
		}
		text = strings.Join(logoNames, " ") + "\n" + text
	}

	// Update form fields
	cp.ocrTextEntry.SetText(text)
	cp.editingComp.OCRText = text

	// Parse component info
	info := parseComponentInfo(text)
	if info.PartNumber != "" && cp.partNumberEntry.Text == "" {
		cp.partNumberEntry.SetText(info.PartNumber)
	}
	if detectedManufacturer != "" && cp.manufacturerEntry.Text == "" {
		cp.manufacturerEntry.SetText(detectedManufacturer)
	} else if info.Manufacturer != "" && cp.manufacturerEntry.Text == "" {
		cp.manufacturerEntry.SetText(info.Manufacturer)
	}
	if cp.dateCodeEntry.Text == "" {
		if code, decoded := datecode.ExtractDateCode(text, 1990); decoded != nil {
			cp.dateCodeEntry.SetText(code)
			fmt.Printf("[OCR] Decoded date: %s -> %s\n", code, decoded.String())
		} else if info.DateCode != "" {
			cp.dateCodeEntry.SetText(info.DateCode)
		}
	}
	if info.Place != "" && cp.placeEntry.Text == "" {
		cp.placeEntry.SetText(info.Place)
	}

	// Auto-fill package from part database
	partNum := cp.partNumberEntry.Text
	if partNum == "" {
		partNum = info.PartNumber
	}
	if partNum != "" && cp.packageEntry.Text == "" {
		if pkgInfo := component.LookupPartPackage(partNum); pkgInfo != nil {
			cp.packageEntry.SetText(pkgInfo.Package)
			cp.editingComp.Package = pkgInfo.Package
			fmt.Printf("[OCR] Package lookup: %s → %s (%d pins)\n", partNum, pkgInfo.Package, pkgInfo.PinCount)
		}
	}

	// Update sticky orientation
	cp.state.LastOCROrientation = orientation

	fmt.Printf("[OCR] Complete: %s\n", text)
}

// robustOtsu computes an Otsu threshold that is resistant to small bright/dark
// artifacts. It divides the grayscale image into 8 spatial regions (2x4 grid),
// ranks them by mean brightness, discards the 2 brightest and 2 darkest, and
// computes Otsu on the combined histogram of the remaining 4 regions.
func robustOtsu(gray []uint8, w, h int) uint8 {
	// Divide into 2 columns x 4 rows = 8 regions
	cols, rows := 2, 4
	type region struct {
		mean float64
		hist [256]int
		n    int
	}
	regions := make([]region, cols*rows)

	for ry := 0; ry < rows; ry++ {
		y0 := ry * h / rows
		y1 := (ry + 1) * h / rows
		for rx := 0; rx < cols; rx++ {
			x0 := rx * w / cols
			x1 := (rx + 1) * w / cols
			r := &regions[ry*cols+rx]
			var sum int64
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					v := gray[y*w+x]
					r.hist[v]++
					sum += int64(v)
					r.n++
				}
			}
			if r.n > 0 {
				r.mean = float64(sum) / float64(r.n)
			}
		}
	}

	// Sort by mean brightness
	sort.Slice(regions, func(i, j int) bool {
		return regions[i].mean < regions[j].mean
	})

	// Discard 2 darkest and 2 brightest, keep middle 4
	var hist [256]int
	total := 0
	for _, r := range regions[2:6] {
		for i := 0; i < 256; i++ {
			hist[i] += r.hist[i]
		}
		total += r.n
	}

	fmt.Printf("[Otsu] Region means: ")
	for i, r := range regions {
		mark := " "
		if i < 2 || i >= 6 {
			mark = "x"
		}
		fmt.Printf("%.0f%s ", r.mean, mark)
	}
	fmt.Println()

	// Standard Otsu on the combined histogram
	var sum float64
	for i := 0; i < 256; i++ {
		sum += float64(i) * float64(hist[i])
	}
	var sumB float64
	var wB, wF int
	var maxVar float64
	thresh := uint8(128)
	for t := 0; t < 256; t++ {
		wB += hist[t]
		if wB == 0 {
			continue
		}
		wF = total - wB
		if wF == 0 {
			break
		}
		sumB += float64(t) * float64(hist[t])
		mB := sumB / float64(wB)
		mF := (sum - sumB) / float64(wF)
		variance := float64(wB) * float64(wF) * (mB - mF) * (mB - mF)
		if variance > maxVar {
			maxVar = variance
			thresh = uint8(t)
		}
	}

	// Ensure minimum white pixel ratio using the trimmed histogram.
	// For faded printing, Otsu may set the threshold too high.
	// Count from the trimmed histogram (middle 4 regions) so bright/dark
	// outlier regions don't skew the ratio.
	minWhiteRatio := 0.10
	countWhite := func(t uint8) int {
		n := 0
		for i := int(t) + 1; i < 256; i++ {
			n += hist[i]
		}
		return n
	}
	whiteCount := countWhite(thresh)
	whiteRatio := float64(whiteCount) / float64(total)
	fmt.Printf("[Otsu] thresh=%d white=%.1f%%\n", thresh, whiteRatio*100)

	if whiteRatio < minWhiteRatio {
		// Binary search for a threshold that gives at least minWhiteRatio white
		lo, hi := uint8(0), thresh
		for lo < hi {
			mid := (lo + hi) / 2
			ratio := float64(countWhite(mid)) / float64(total)
			if ratio < minWhiteRatio {
				hi = mid
			} else {
				lo = mid + 1
			}
		}
		if lo > 0 {
			lo--
		}
		newRatio := float64(countWhite(lo)) / float64(total)
		fmt.Printf("[Otsu] Adjusted for faded: %d → %d (white %.1f%% → %.1f%%)\n",
			thresh, lo, whiteRatio*100, newRatio*100)
		thresh = lo
	}

	return thresh
}

// rgbaToGray converts an RGBA image to a grayscale byte slice.
func rgbaToGray(src *image.RGBA) ([]uint8, int, int) {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	srcPix := src.Pix
	srcStride := src.Stride
	gray := make([]uint8, sw*sh)
	for y := 0; y < sh; y++ {
		for x := 0; x < sw; x++ {
			si := y*srcStride + x*4
			gray[y*sw+x] = uint8((299*uint32(srcPix[si]) + 587*uint32(srcPix[si+1]) + 114*uint32(srcPix[si+2])) / 1000)
		}
	}
	return gray, sw, sh
}

// despeckleBW clears isolated set bits with no 4-connected neighbors.
func despeckleBW(bw []bool, w, h int) {
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if !bw[y*w+x] {
				continue
			}
			hasNeighbor := false
			for _, d := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
				nx, ny := x+d[0], y+d[1]
				if nx >= 0 && nx < w && ny >= 0 && ny < h && bw[ny*w+nx] {
					hasNeighbor = true
					break
				}
			}
			if !hasNeighbor {
				bw[y*w+x] = false
			}
		}
	}
}

// showOCRPreview displays a dialog with three processing phases:
// 1. Raw color input (rotated), 2. B&W Otsu threshold, 3. Logo-masked.
func (cp *ComponentsPanel) showOCRPreview(raw, masked *image.RGBA, orientation string) {
	w, h := raw.Bounds().Dx(), raw.Bounds().Dy()
	fmt.Printf("[OCR Preview] %dx%d orientation=%s\n", w, h, orientation)

	// Helper: scale an RGBA image to 2x NRGBA for display
	scale2x := func(src *image.RGBA) *image.NRGBA {
		sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
		out := image.NewNRGBA(image.Rect(0, 0, sw*2, sh*2))
		srcPix := src.Pix
		srcStride := src.Stride
		dstPix := out.Pix
		dstStride := out.Stride
		for y := 0; y < sh; y++ {
			for x := 0; x < sw; x++ {
				si := y*srcStride + x*4
				r, g, b, a := srcPix[si], srcPix[si+1], srcPix[si+2], srcPix[si+3]
				for _, dy := range [2]int{0, 1} {
					for _, dx := range [2]int{0, 1} {
						di := (y*2+dy)*dstStride + (x*2+dx)*4
						dstPix[di] = r
						dstPix[di+1] = g
						dstPix[di+2] = b
						dstPix[di+3] = a
					}
				}
			}
		}
		return out
	}

	// Helper: Otsu threshold an RGBA image to 2x B&W NRGBA
	otsuBW := func(src *image.RGBA) (*image.NRGBA, uint8) {
		gray, sw, sh := rgbaToGray(src)
		thresh := robustOtsu(gray, sw, sh)

		bw := make([]bool, sw*sh)
		for i := 0; i < sw*sh; i++ {
			bw[i] = gray[i] > thresh
		}
		despeckleBW(bw, sw, sh)

		out := image.NewNRGBA(image.Rect(0, 0, sw*2, sh*2))
		outPix := out.Pix
		outStride := out.Stride
		for y := 0; y < sh; y++ {
			for x := 0; x < sw; x++ {
				var v byte
				if bw[y*sw+x] {
					v = 255
				}
				for _, dy := range [2]int{0, 1} {
					for _, dx := range [2]int{0, 1} {
						di := (y*2+dy)*outStride + (x*2+dx)*4
						outPix[di] = v
						outPix[di+1] = v
						outPix[di+2] = v
						outPix[di+3] = 255
					}
				}
			}
		}
		return out, thresh
	}

	// 1. Raw color at 2x
	rawScaled := scale2x(raw)

	// 2. B&W of raw image
	rawBW, rawThresh := otsuBW(raw)
	fmt.Printf("[OCR Preview] Raw Otsu threshold: %d\n", rawThresh)

	// 3. B&W of logo-masked image (what OCR sees)
	maskedBW, maskedThresh := otsuBW(masked)
	fmt.Printf("[OCR Preview] Masked Otsu threshold: %d\n", maskedThresh)

	// Build Fyne widgets
	mkImg := func(img *image.NRGBA) *fynecanvas.Image {
		fi := fynecanvas.NewImageFromImage(img)
		fi.FillMode = fynecanvas.ImageFillOriginal
		fi.ScaleMode = fynecanvas.ImageScalePixels
		return fi
	}

	content := container.NewVBox(
		widget.NewLabel("Color"),
		mkImg(rawScaled),
		widget.NewLabel(fmt.Sprintf("B&W (Otsu %d)", rawThresh)),
		mkImg(rawBW),
		widget.NewLabel(fmt.Sprintf("Logo Masked B&W (Otsu %d)", maskedThresh)),
		mkImg(maskedBW),
		widget.NewLabel(fmt.Sprintf("%dx%d", w, h)),
	)

	title := fmt.Sprintf("OCR Preview — %s (%s)", cp.editingComp.ID, orientation)
	previewWin := fyne.CurrentApp().NewWindow(title)
	previewWin.SetContent(container.NewScroll(content))
	previewWin.Resize(fyne.NewSize(float32(w*2+60), float32(h*6+280)))
	previewWin.Show()

	// Set always-on-top via wmctrl (Linux)
	go func() {
		time.Sleep(200 * time.Millisecond)
		exec.Command("wmctrl", "-r", title, "-b", "add,above").Run()
	}()
}

// runOCRWithHistogramThreshold uses histogram analysis for better OCR.
func (cp *ComponentsPanel) runOCRWithHistogramThreshold(bgr gocv.Mat, engine *ocr.Engine) string {
	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(bgr, &gray, gocv.ColorBGRToGray)

	// Build histogram
	hist := make([]int, 256)
	totalPixels := gray.Rows() * gray.Cols()
	for y := 0; y < gray.Rows(); y++ {
		for x := 0; x < gray.Cols(); x++ {
			v := gray.GetUCharAt(y, x)
			hist[v]++
		}
	}

	// Find threshold for brightest 5%
	targetPixels := totalPixels * 5 / 100
	cumulative := 0
	threshold := 255
	for i := 255; i >= 0; i-- {
		cumulative += hist[i]
		if cumulative >= targetPixels {
			threshold = i
			break
		}
	}
	if threshold < 128 {
		threshold = 128
	}

	// Create binary image
	binary := gocv.NewMat()
	defer binary.Close()
	gocv.Threshold(gray, &binary, float32(threshold), 255, gocv.ThresholdBinary)

	// Invert
	inverted := gocv.NewMat()
	defer inverted.Close()
	gocv.BitwiseNot(binary, &inverted)

	// Convert back to BGR
	bgrOut := gocv.NewMat()
	defer bgrOut.Close()
	gocv.CvtColor(inverted, &bgrOut, gocv.ColorGrayToBGR)

	text, err := engine.RecognizeImage(bgrOut)
	if err != nil {
		return ""
	}
	return text
}

// runOCRTraining runs parameter annealing to find the best OCR settings.
// Runs in a background goroutine so the UI stays responsive.
func (cp *ComponentsPanel) runOCRTraining() {
	if cp.editingComp == nil {
		fmt.Println("[OCR Train] No component selected")
		return
	}

	groundTruth := cp.correctedTextEntry.Text
	if strings.TrimSpace(groundTruth) == "" {
		fmt.Println("[OCR Train] No ground truth provided")
		return
	}

	img := cp.getComponentImage()
	if img == nil {
		fmt.Println("[OCR Train] No image available")
		return
	}

	// Extract component region
	bounds := cp.editingComp.Bounds
	x, y := int(bounds.X), int(bounds.Y)
	w, h := int(bounds.Width), int(bounds.Height)

	imgBounds := img.Bounds()
	if x < imgBounds.Min.X {
		x = imgBounds.Min.X
	}
	if y < imgBounds.Min.Y {
		y = imgBounds.Min.Y
	}
	w = min(w, imgBounds.Max.X-x)
	h = min(h, imgBounds.Max.Y-y)

	if w <= 0 || h <= 0 {
		return
	}

	// Create cropped region
	cropped := image.NewRGBA(image.Rect(0, 0, w, h))
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			cropped.Set(dx, dy, img.At(x+dx, y+dy))
		}
	}

	// Logo training
	orientation := cp.ocrOrientation.Selected
	logoRotation := orientationToRotation(orientation)
	cp.trainLogoDetection(cropped, w, h, groundTruth, logoRotation)

	// Capture references needed for the goroutine
	comp := cp.editingComp
	compID := comp.ID

	fmt.Printf("[OCR Train] Starting background training for %s (orientation %s)...\n", compID, orientation)

	go func() {
		// Rotate for OCR
		rotated := rotateForOCR(cropped, orientation)

		// Convert to gocv.Mat
		rotBounds := rotated.Bounds()
		mat, err := gocv.NewMatFromBytes(rotBounds.Dy(), rotBounds.Dx(), gocv.MatTypeCV8UC4, rotated.Pix)
		if err != nil {
			fmt.Printf("[OCR Train] %s: failed to convert image: %v\n", compID, err)
			return
		}
		defer mat.Close()

		bgr := gocv.NewMat()
		defer bgr.Close()
		gocv.CvtColor(mat, &bgr, gocv.ColorRGBAToBGR)

		engine, err := ocr.NewEngine()
		if err != nil {
			fmt.Printf("[OCR Train] %s: failed to create engine: %v\n", compID, err)
			return
		}
		defer engine.Close()

		// Run parameter search
		bestParams, bestScore, bestText := engine.AnnealOCRParams(bgr, groundTruth, 5000)
		fmt.Printf("[OCR Train] %s: score=%.1f%% text=%s\n", compID, bestScore*100, bestText)

		// Save to global training database if good enough (score >= 0.7)
		if bestScore >= 0.7 {
			cp.state.AddOCRTrainingSample(groundTruth, bestText, bestScore, orientation, bestParams)
			fmt.Printf("[OCR Train] %s: added to global training database\n", compID)
		}

		// Update UI on main thread — only if we're still editing the same component
		cp.updateOCRTrainingLabel()
		if cp.editingComp == comp {
			cp.ocrTextEntry.SetText(bestText)
			comp.OCRText = bestText
		}

		fmt.Printf("[OCR Train] %s: complete\n", compID)
	}()

	// Parse ground truth into form fields and component properties
	info := parseComponentInfo(groundTruth)

	// Part number: use parsed value if form is empty
	if info.PartNumber != "" && cp.partNumberEntry.Text == "" {
		cp.partNumberEntry.SetText(info.PartNumber)
		cp.editingComp.PartNumber = info.PartNumber
	}

	// Manufacturer: use parsed value if form is empty
	if info.Manufacturer != "" && cp.manufacturerEntry.Text == "" {
		cp.manufacturerEntry.SetText(info.Manufacturer)
		cp.editingComp.Manufacturer = info.Manufacturer
	}

	// Date code: any 4 consecutive digits
	if info.DateCode != "" && cp.dateCodeEntry.Text == "" {
		cp.dateCodeEntry.SetText(info.DateCode)
		cp.editingComp.DateCode = info.DateCode
	}

	// Place: manufacturing location
	if info.Place != "" && cp.placeEntry.Text == "" {
		cp.placeEntry.SetText(info.Place)
		cp.editingComp.Place = info.Place
	}

	// Store corrected text as ground truth
	cp.editingComp.CorrectedText = groundTruth

	// Update sticky orientation
	cp.state.LastOCROrientation = orientation
	cp.state.SetModified(true)

	fmt.Printf("[OCR Train] Parsed: part=%q mfr=%q date=%q place=%q\n",
		info.PartNumber, info.Manufacturer, info.DateCode, info.Place)
}

// trainLogoDetection compares detected logos to ground truth.
func (cp *ComponentsPanel) trainLogoDetection(cropped *image.RGBA, w, h int, groundTruth string, rotation int) {
	if cp.state.LogoLibrary == nil || len(cp.state.LogoLibrary.Logos) == 0 {
		return
	}

	// Extract expected logos from ground truth
	expectedLogos := extractLogoNames(groundTruth)
	if len(expectedLogos) == 0 {
		return
	}
	fmt.Printf("[Logo Train] Expected: %v\n", expectedLogos)

	// Detect logos
	searchBounds := geometry.RectInt{X: 0, Y: 0, Width: w, Height: h}
	detectedMatches := cp.state.LogoLibrary.DetectLogos(cropped, searchBounds, 0.70, rotation)

	detectedLogos := make(map[string]logo.LogoMatch)
	for _, m := range detectedMatches {
		detectedLogos[m.Logo.Name] = m
	}

	// Report results
	for _, expected := range expectedLogos {
		if _, found := detectedLogos[expected]; found {
			fmt.Printf("[Logo Train] %s: detected\n", expected)
		} else {
			fmt.Printf("[Logo Train] %s: MISSED\n", expected)
		}
	}

	expectedSet := make(map[string]bool)
	for _, e := range expectedLogos {
		expectedSet[e] = true
	}
	for name, m := range detectedLogos {
		if !expectedSet[name] {
			fmt.Printf("[Logo Train] %s: FALSE POSITIVE (score=%.2f)\n", name, m.Score)
		}
	}
}

// Helper functions for OCR

func orientationToRotation(orientation string) int {
	switch orientation {
	case "S":
		return 180
	case "E":
		return 90
	case "W":
		return 270
	default:
		return 0
	}
}

func rotateForOCR(img *image.RGBA, orientation string) *image.RGBA {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	srcPix := img.Pix
	srcStride := img.Stride

	switch orientation {
	case "S": // 180° — reverse all rows and pixels within each row
		out := image.NewRGBA(image.Rect(0, 0, w, h))
		dstPix := out.Pix
		for y := 0; y < h; y++ {
			srcRow := y * srcStride
			dstRow := (h - 1 - y) * out.Stride
			for x := 0; x < w; x++ {
				si := srcRow + x*4
				di := dstRow + (w-1-x)*4
				dstPix[di] = srcPix[si]
				dstPix[di+1] = srcPix[si+1]
				dstPix[di+2] = srcPix[si+2]
				dstPix[di+3] = srcPix[si+3]
			}
		}
		return out

	case "E": // 90° CCW — transpose then flip vertical
		out := image.NewRGBA(image.Rect(0, 0, h, w))
		dstPix := out.Pix
		dstStride := out.Stride
		for y := 0; y < h; y++ {
			srcRow := y * srcStride
			for x := 0; x < w; x++ {
				si := srcRow + x*4
				di := (w-1-x)*dstStride + y*4
				dstPix[di] = srcPix[si]
				dstPix[di+1] = srcPix[si+1]
				dstPix[di+2] = srcPix[si+2]
				dstPix[di+3] = srcPix[si+3]
			}
		}
		return out

	case "W": // 90° CW — transpose then flip horizontal
		out := image.NewRGBA(image.Rect(0, 0, h, w))
		dstPix := out.Pix
		dstStride := out.Stride
		for y := 0; y < h; y++ {
			srcRow := y * srcStride
			for x := 0; x < w; x++ {
				si := srcRow + x*4
				di := x*dstStride + (h-1-y)*4
				dstPix[di] = srcPix[si]
				dstPix[di+1] = srcPix[si+1]
				dstPix[di+2] = srcPix[si+2]
				dstPix[di+3] = srcPix[si+3]
			}
		}
		return out

	default:
		return img
	}
}

func calculateBackgroundColor(img *image.RGBA) color.RGBA {
	bounds := img.Bounds()
	var r, g, b, count uint64

	// Sample edge pixels
	for x := bounds.Min.X; x < bounds.Max.X; x++ {
		c := img.RGBAAt(x, bounds.Min.Y)
		r += uint64(c.R)
		g += uint64(c.G)
		b += uint64(c.B)
		count++
		c = img.RGBAAt(x, bounds.Max.Y-1)
		r += uint64(c.R)
		g += uint64(c.G)
		b += uint64(c.B)
		count++
	}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		c := img.RGBAAt(bounds.Min.X, y)
		r += uint64(c.R)
		g += uint64(c.G)
		b += uint64(c.B)
		count++
		c = img.RGBAAt(bounds.Max.X-1, y)
		r += uint64(c.R)
		g += uint64(c.G)
		b += uint64(c.B)
		count++
	}

	if count == 0 {
		return color.RGBA{A: 255}
	}
	return color.RGBA{
		R: uint8(r / count),
		G: uint8(g / count),
		B: uint8(b / count),
		A: 255,
	}
}

func maskRegion(img *image.RGBA, bounds geometry.RectInt, c color.RGBA) {
	imgBounds := img.Bounds()
	x0 := max(bounds.X, imgBounds.Min.X)
	y0 := max(bounds.Y, imgBounds.Min.Y)
	x1 := min(bounds.X+bounds.Width, imgBounds.Max.X)
	y1 := min(bounds.Y+bounds.Height, imgBounds.Max.Y)

	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func extractLogoNames(text string) []string {
	re := regexp.MustCompile(`<([A-Za-z0-9]+)>`)
	matches := re.FindAllStringSubmatch(text, -1)
	var names []string
	for _, m := range matches {
		if len(m) >= 2 {
			names = append(names, strings.ToUpper(m[1]))
		}
	}
	return names
}

// fixOCRPartNumbers corrects common OCR misreads where 4↔A and 5↔S are
// confused in 74/54-series logic part numbers. For example:
//
//	7AL500 → 74LS00, 7ALS00N → 74LS00N, 5A138 → 54138
//
// The approach: find tokens that look like mangled 74/54-series parts, then
// fix the prefix (7A→74, 5A→54), the family code (L5→LS, AL5→ALS, etc.),
// and digits after the family (S→5, A→4).
func fixOCRPartNumbers(text string) string {
	// Known 74/54-series logic families
	families := []string{
		"ALS", "ALS", "AS", "LS", "S", "F",
		"HC", "HCT", "AC", "ACT",
		"LV", "LVC", "LVT", "ABT",
		"BCT", "FCT", "GTL", "GTLP",
	}

	// Build a map of mangled family → correct family
	// Only fix cases where S→5 or 4→A creates ambiguity
	familyFixes := map[string]string{
		"L5":   "LS",
		"AL5":  "ALS",
		"A5":   "AS",
		"": "", // no family is valid (e.g., 7400)
	}
	// Also add all correct families as identity mappings
	for _, f := range families {
		familyFixes[f] = f
	}

	// Common manufacturer prefix misreads: 0↔D, 5↔S
	prefixFixes := map[string]string{
		"0M": "DM", "OM": "DM", "JM": "DM", // National Semiconductor
		"5N": "SN", // Texas Instruments
		"0S": "DS", // National Semiconductor
		"MC": "MC", // Motorola (identity)
		"SN": "SN", "DM": "DM", "DS": "DS",
		"H0": "HD", // Hitachi
		"HD": "HD", "HA": "HA",
		"TC": "TC", "MB": "MB",
		"UA": "UA", "CA": "CA", "CD": "CD",
	}

	// Match tokens that look like (optional manufacturer prefix)(7A|74|5A|54)(family?)(digits)(suffix?)
	// Prefix pattern includes alphanumeric to catch 0M, 5N etc.
	pat := regexp.MustCompile(`(?i)\b([A-Z0-9]{0,3})([7T][A4]|[5S][A4])([A-Z]{0,4})([0-9OSA]{1,4})([A-Z]{0,3})\b`)

	result := pat.ReplaceAllStringFunc(text, func(match string) string {
		sub := pat.FindStringSubmatch(match)
		if sub == nil {
			return match
		}
		prefix := sub[1]   // e.g., "SN", "0M", ""
		series := sub[2]   // e.g., "7A", "74", "5A", "54"
		family := sub[3]   // e.g., "L5", "LS", "HC", ""
		digits := sub[4]   // e.g., "00", "245"
		suffix := sub[5]   // e.g., "N", "D", ""

		// Fix prefix: 0M→DM, 5N→SN, H0→HD
		prefixUpper := strings.ToUpper(prefix)
		if fixed, ok := prefixFixes[prefixUpper]; ok {
			prefix = fixed
		} else {
			prefix = prefixUpper
		}

		// Fix series: 7A/TA/T4→74, 5A/SA/S4→54
		seriesUpper := strings.ToUpper(series)
		switch seriesUpper[0] {
		case '7', 'T':
			series = "74"
		case '5', 'S':
			series = "54"
		}

		// Fix family: L5→LS, AL5→ALS, A5→AS
		// Reject if family code is present but not a known/mangled logic family
		familyUpper := strings.ToUpper(family)
		if familyUpper != "" {
			if fixed, ok := familyFixes[familyUpper]; ok {
				family = fixed
			} else {
				// Unknown family like LVAC — not a real part number, skip
				return match
			}
		}

		// Fix digits: S→5, A→4 in the digit string (shouldn't have letters but OCR mangles them)
		fixedDigits := strings.Map(func(r rune) rune {
			switch r {
			case 'S', 's':
				return '5'
			case 'A', 'a':
				return '4'
			case 'O', 'o':
				return '0'
			default:
				return r
			}
		}, digits)

		fixed := prefix + series + family + fixedDigits + suffix
		fmt.Printf("[OCR Fix] %q → %q (prefix=%s series=%s family=%s digits=%s suffix=%s)\n",
			match, fixed, sub[1], sub[2], sub[3], sub[4], sub[5])
		return fixed
	})

	return result
}

// componentInfo holds parsed component information from OCR text.
type componentInfo struct {
	PartNumber   string
	Manufacturer string
	DateCode     string
	Place        string
}

func parseComponentInfo(text string) componentInfo {
	var info componentInfo
	upperText := strings.ToUpper(text)
	lines := strings.Split(strings.TrimSpace(text), "\n")

	// Extract logos from text (e.g., <TI> <MOTOROLA>)
	logoPattern := regexp.MustCompile(`<([A-Za-z0-9]+)>`)
	logoMatches := logoPattern.FindAllStringSubmatch(text, -1)

	// Location names to skip when looking for part numbers
	locationNames := []string{
		"MALAYSIA", "PHILIPPINES", "SINGAPORE", "INDONESIA", "THAILAND",
		"VIETNAM", "IRELAND", "GERMANY", "ENGLAND", "SCOTLAND", "CANADA",
		"BRAZIL", "TAIWAN", "SALVADOR", "MEXICO", "KOREA", "JAPAN",
		"CHINA", "INDIA", "HONGKONG", "CAMBODIA", "PORTUGAL", "FRANCE",
		"ITALY", "SPAIN", "AUSTRIA", "SWEDEN", "ISRAEL",
	}
	isLocationLine := func(s string) bool {
		upper := strings.ToUpper(s)
		// Strip digits and punctuation for fuzzy check
		lettersOnly := strings.Map(func(r rune) rune {
			if r >= 'A' && r <= 'Z' {
				return r
			}
			return -1
		}, upper)
		for _, loc := range locationNames {
			if strings.Contains(lettersOnly, loc) {
				return true
			}
		}
		return false
	}

	// Find the best part number line: prefer lines with letters+digits (actual part numbers)
	// over short all-digit lines (likely date codes or noise)
	partNumPattern := regexp.MustCompile(`[A-Za-z]`)
	var fallbackPartNum string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Skip lines that are only logos
		withoutLogos := logoPattern.ReplaceAllString(line, "")
		withoutLogos = strings.TrimSpace(withoutLogos)
		if withoutLogos == "" {
			continue
		}
		// Strip trailing punctuation (OCR artifacts like trailing -)
		withoutLogos = strings.TrimRight(withoutLogos, "-_.,;:!/ ")
		if withoutLogos == "" {
			continue
		}
		// Skip lines that are manufacturing locations (e.g., MALAYSIA8A)
		if isLocationLine(withoutLogos) {
			continue
		}
		// Prefer lines containing letters (real part numbers) over all-digit lines
		if partNumPattern.MatchString(withoutLogos) && len(withoutLogos) >= 3 {
			info.PartNumber = withoutLogos
			break
		}
		// Remember first all-digit line as fallback
		if fallbackPartNum == "" {
			fallbackPartNum = withoutLogos
		}
	}
	if info.PartNumber == "" && fallbackPartNum != "" {
		info.PartNumber = fallbackPartNum
	}

	// Check for 74/54-series part numbers with manufacturer prefixes (e.g., SN7438, DM74LS244)
	logic74Pattern := regexp.MustCompile(`(?i)\b([A-Z]{1,3})?((?:74|54)[A-Z]{0,4}\d{1,4})[A-Z]{0,3}\b`)
	if matches := logic74Pattern.FindStringSubmatch(upperText); len(matches) > 0 {
		prefix := matches[1]
		corePart := strings.ToUpper(matches[2]) // e.g., "74LS244", "7438"

		// Use the core part (without manufacturer prefix or suffix) as the part number
		if corePart != "" {
			info.PartNumber = corePart
		}

		if prefix != "" && info.Manufacturer == "" {
			// Map manufacturer prefixes
			prefixToMfr := map[string]string{
				"SN": "Texas Instruments", "TL": "Texas Instruments", "UC": "Texas Instruments",
				"DM": "National Semiconductor", "LM": "National Semiconductor", "DS": "National Semiconductor",
				"MC": "Motorola", "MJ": "Motorola",
				"UA": "Fairchild", "9N": "Fairchild",
				"AM": "AMD",
				"CD": "RCA", "CA": "RCA",
				"HD": "Hitachi", "HA": "Hitachi",
				"TC": "Toshiba",
				"MB": "Fujitsu",
				"N": "Signetics", "NE": "Signetics",
			}
			if mfr, ok := prefixToMfr[strings.ToUpper(prefix)]; ok {
				info.Manufacturer = mfr
			}
		}
	}

	// Any 4 consecutive digits is a date code (YYWW format is most common)
	datePattern := regexp.MustCompile(`\b([0-9]{4})\b`)
	if matches := datePattern.FindStringSubmatch(upperText); len(matches) >= 2 {
		info.DateCode = matches[1]
	}

	// Map common logo names to manufacturers
	logoToManufacturer := map[string]string{
		"TI":        "Texas Instruments",
		"MOTOROLA":  "Motorola",
		"MOT":       "Motorola",
		"NATIONAL":  "National Semiconductor",
		"NSC":       "National Semiconductor",
		"NS":        "National Semiconductor",
		"FAIRCHILD": "Fairchild",
		"SIGNETICS": "Signetics",
		"AMD":       "AMD",
		"INTEL":     "Intel",
		"ZILOG":     "Zilog",
		"NEC":       "NEC",
		"HITACHI":   "Hitachi",
		"TOSHIBA":   "Toshiba",
		"FUJITSU":   "Fujitsu",
		"MITSUBISHI": "Mitsubishi",
		"SAMSUNG":   "Samsung",
		"PHILIPS":   "Philips",
		"SIEMENS":   "Siemens",
		"SGS":       "SGS-Thomson",
		"ST":        "STMicroelectronics",
		"MOSTEK":    "Mostek",
		"RCA":       "RCA",
		"CYPRESS":   "Cypress",
		"LATTICE":   "Lattice",
		"XILINX":    "Xilinx",
		"ALTERA":    "Altera",
		"MICROCHIP": "Microchip",
		"ATMEL":     "Atmel",
		"MAXIM":     "Maxim",
		"ANALOG":    "Analog Devices",
		"LINEAR":    "Linear Technology",
		"BURRBROWN": "Burr-Brown",
		"HARRIS":    "Harris",
		"IDT":       "IDT",
		"MICRON":    "Micron",
		"HYUNDAI":   "Hyundai",
		"HYNIX":     "Hynix",
		"ELPIDA":    "Elpida",
		"INFINEON":  "Infineon",
		"RENESAS":   "Renesas",
		"SHARP":     "Sharp",
		"SANYO":     "Sanyo",
		"SONY":      "Sony",
		"PANASONIC": "Panasonic",
		"ROHM":      "Rohm",
		"MURATA":    "Murata",
		"TDK":       "TDK",
		"VISHAY":    "Vishay",
		"ONSEMI":    "ON Semiconductor",
		"DIODES":    "Diodes Inc",
		"NEXPERIA":  "Nexperia",
	}

	// Use first recognized logo as manufacturer
	for _, match := range logoMatches {
		if len(match) >= 2 {
			logoName := strings.ToUpper(match[1])
			if mfr, ok := logoToManufacturer[logoName]; ok {
				info.Manufacturer = mfr
				break
			}
		}
	}

	// Look for manufacturing locations
	locations := []struct {
		pattern string
		place   string
	}{
		{"PHILIPPINES", "Philippines"},
		{"SINGAPORE", "Singapore"},
		{"INDONESIA", "Indonesia"},
		{"MALAYSIA", "Malaysia"},
		{"THAILAND", "Thailand"},
		{"VIETNAM", "Vietnam"},
		{"HONG KONG", "Hong Kong"},
		{"IRELAND", "Ireland"},
		{"GERMANY", "Germany"},
		{"ENGLAND", "UK"},
		{"SCOTLAND", "UK"},
		{"CANADA", "Canada"},
		{"BRAZIL", "Brazil"},
		{"TAIWAN", "Taiwan"},
		{"EL SALVADOR", "El Salvador"},
		{"MEXICO", "Mexico"},
		{"KOREA", "Korea"},
		{"JAPAN", "Japan"},
		{"CHINA", "China"},
		{"INDIA", "India"},
		{"S'PORE", "Singapore"},
		{"SPORE", "Singapore"},
		{"M'SIA", "Malaysia"},
		{"MSIA", "Malaysia"},
		{"HONGKONG", "Hong Kong"},
		{"H.K.", "Hong Kong"},
		{"R.O.C", "Taiwan"},
		{"ROC", "Taiwan"},
		{"P.R.C", "China"},
		{"PRC", "China"},
		{"USA", "USA"},
		{" UK ", "UK"},
		{" HK ", "Hong Kong"},
	}
	for _, loc := range locations {
		if strings.Contains(upperText, loc.pattern) {
			info.Place = loc.place
			break
		}
	}

	// Fuzzy match if exact match failed — handles OCR mangling like ELSALVADOR,
	// MA1AYSIA, THAI1AND, PH1LIPPINES, etc.
	if info.Place == "" {
		// Strip spaces/punctuation from the OCR text for comparison
		stripped := strings.Map(func(r rune) rune {
			if r >= 'A' && r <= 'Z' {
				return r
			}
			return -1
		}, upperText)

		// Canonical names to fuzzy-match (no spaces, all uppercase)
		fuzzyLocations := []struct {
			canonical string
			place     string
		}{
			{"ELSALVADOR", "El Salvador"},
			{"PHILIPPINES", "Philippines"},
			{"SINGAPORE", "Singapore"},
			{"INDONESIA", "Indonesia"},
			{"MALAYSIA", "Malaysia"},
			{"THAILAND", "Thailand"},
			{"VIETNAM", "Vietnam"},
			{"HONGKONG", "Hong Kong"},
			{"IRELAND", "Ireland"},
			{"GERMANY", "Germany"},
			{"ENGLAND", "UK"},
			{"SCOTLAND", "UK"},
			{"CANADA", "Canada"},
			{"BRAZIL", "Brazil"},
			{"TAIWAN", "Taiwan"},
			{"MEXICO", "Mexico"},
			{"KOREA", "Korea"},
			{"JAPAN", "Japan"},
			{"CHINA", "China"},
			{"INDIA", "India"},
		}

		for _, loc := range fuzzyLocations {
			if fuzzyContains(stripped, loc.canonical, len(loc.canonical)/5) {
				fmt.Printf("[OCR Place] Fuzzy matched %q in %q → %s\n", loc.canonical, stripped, loc.place)
				info.Place = loc.place
				break
			}
		}
	}

	return info
}

// fuzzyContains checks if haystack contains a substring within maxErrors
// edit distance of needle. Uses a simple sliding window approach comparing
// each window of len(needle) characters with allowed substitutions.
func fuzzyContains(haystack, needle string, maxErrors int) bool {
	nLen := len(needle)
	if nLen == 0 {
		return true
	}
	if len(haystack) < nLen {
		return false
	}

	// Common OCR substitution groups: characters that look alike
	sameGroup := func(a, b byte) bool {
		if a == b {
			return true
		}
		groups := [][2]byte{
			{'0', 'O'}, {'0', 'D'}, {'O', 'D'},
			{'1', 'I'}, {'1', 'L'}, {'I', 'L'},
			{'5', 'S'}, {'4', 'A'},
			{'8', 'B'}, {'6', 'G'},
			{'2', 'Z'},
		}
		for _, g := range groups {
			if (a == g[0] && b == g[1]) || (a == g[1] && b == g[0]) {
				return true
			}
		}
		return false
	}

	for i := 0; i <= len(haystack)-nLen; i++ {
		errors := 0
		for j := 0; j < nLen; j++ {
			if !sameGroup(haystack[i+j], needle[j]) {
				errors++
				if errors > maxErrors {
					break
				}
			}
		}
		if errors <= maxErrors {
			return true
		}
	}
	return false
}

// deleteComponent removes a component by index.
func (cp *ComponentsPanel) deleteComponent(index int) {
	if index < 0 || index >= len(cp.state.Components) {
		return
	}

	comp := cp.state.Components[index]
	fmt.Printf("Deleting component %s (%s)\n", comp.ID, comp.Package)

	// Remove from slice
	cp.state.Components = append(cp.state.Components[:index], cp.state.Components[index+1:]...)
	cp.state.SetModified(true)

	// Refresh UI
	cp.list.Refresh()
	cp.updateComponentOverlay()
}

// highlightComponent shows a highlight overlay for the component at the given index.
// If scrollToView is true, the canvas will scroll to center the component.
func (cp *ComponentsPanel) highlightComponent(index int, scrollToView bool) {
	if index < 0 || index >= len(cp.state.Components) {
		return
	}

	comp := cp.state.Components[index]

	// Create a highlight overlay with magenta outline (no fill)
	highlight := &canvas.Overlay{
		Color: color.RGBA{R: 255, G: 0, B: 255, A: 255}, // Magenta outline
		Rectangles: []canvas.OverlayRect{
			{
				X:      int(comp.Bounds.X),
				Y:      int(comp.Bounds.Y),
				Width:  int(comp.Bounds.Width),
				Height: int(comp.Bounds.Height),
				Fill:   canvas.FillNone, // Outline only
			},
		},
		Layer: canvas.LayerFront,
	}

	cp.canvas.SetOverlay("component_highlight", highlight)
	cp.canvas.Refresh()

	// Only scroll if explicitly requested (e.g., initial selection, not during editing)
	if scrollToView {
		cp.canvas.ScrollToRegion(int(comp.Bounds.X), int(comp.Bounds.Y),
			int(comp.Bounds.Width), int(comp.Bounds.Height))
	}
}

// clearHighlight removes the component highlight overlay.
// If a component is being edited, it re-highlights that component instead of clearing.
func (cp *ComponentsPanel) clearHighlight() {
	// If we're editing a component, keep its highlight visible
	if cp.editingComp != nil && cp.editingIndex >= 0 {
		cp.highlightComponent(cp.editingIndex, false)
		return
	}
	cp.canvas.SetOverlay("component_highlight", nil)
	cp.canvas.Refresh()
}

// onKeyPressed handles keyboard input for component adjustment.
// +/- adjust length (height), * // adjust width, arrows move the component.
// Granularity is 0.1mm.
func (cp *ComponentsPanel) onKeyPressed(ev *fyne.KeyEvent) {
	if cp.hoveredIndex < 0 || cp.hoveredIndex >= len(cp.state.Components) {
		return
	}

	// Calculate 0.1mm in pixels
	dpi := cp.state.DPI
	if dpi <= 0 {
		dpi = 1200 // Default
	}
	// 0.1mm = 0.00394 inches
	step := dpi * 0.00394

	comp := cp.state.Components[cp.hoveredIndex]

	switch ev.Name {
	case "Plus": // + increases length (height)
		comp.Bounds.Height += step
	case "Minus": // - decreases length (height)
		if comp.Bounds.Height > step {
			comp.Bounds.Height -= step
		}
	case "Asterisk": // * increases width
		comp.Bounds.Width += step
	case "Slash": // / decreases width
		if comp.Bounds.Width > step {
			comp.Bounds.Width -= step
		}
	case fyne.KeyUp:
		comp.Bounds.Y -= step
	case fyne.KeyDown:
		comp.Bounds.Y += step
	case fyne.KeyLeft:
		comp.Bounds.X -= step
	case fyne.KeyRight:
		comp.Bounds.X += step
	default:
		return // Unknown key, don't update
	}

	cp.state.SetModified(true)
	cp.updateComponentOverlay()
	cp.highlightComponent(cp.hoveredIndex, false)
	cp.list.Refresh()

	fmt.Printf("Adjusted component %s: pos=(%.1f,%.1f) size=%.1fx%.1f\n",
		comp.ID, comp.Bounds.X, comp.Bounds.Y, comp.Bounds.Width, comp.Bounds.Height)
}

// onRightClickDeleteComponent handles right-click to delete components on the canvas.
func (cp *ComponentsPanel) onRightClickDeleteComponent(x, y float64) {
	// Find component at click position
	for i, comp := range cp.state.Components {
		if x >= comp.Bounds.X && x <= comp.Bounds.X+comp.Bounds.Width &&
			y >= comp.Bounds.Y && y <= comp.Bounds.Y+comp.Bounds.Height {
			cp.deleteComponent(i)
			return
		}
	}
}

// onLeftClickResize handles left-click to resize component bounds.
// Clicking near an edge shrinks/expands that edge toward/away from the click.
func (cp *ComponentsPanel) onLeftClickResize(x, y float64) {
	const edgeThreshold = 10.0 // pixels from edge to trigger resize

	// Find component at click position
	for i, comp := range cp.state.Components {
		// Check if click is near the edges of this component
		left := comp.Bounds.X
		right := comp.Bounds.X + comp.Bounds.Width
		top := comp.Bounds.Y
		bottom := comp.Bounds.Y + comp.Bounds.Height

		// Must be within vertical range
		if y < top-edgeThreshold || y > bottom+edgeThreshold {
			continue
		}
		// Must be within horizontal range
		if x < left-edgeThreshold || x > right+edgeThreshold {
			continue
		}

		// Check which edge is closest
		distLeft := abs64(x - left)
		distRight := abs64(x - right)
		distTop := abs64(y - top)
		distBottom := abs64(y - bottom)

		minDist := distLeft
		edge := "left"
		if distRight < minDist {
			minDist = distRight
			edge = "right"
		}
		if distTop < minDist {
			minDist = distTop
			edge = "top"
		}
		if distBottom < minDist {
			minDist = distBottom
			edge = "bottom"
		}

		// Only trigger if near an edge
		if minDist > edgeThreshold {
			continue
		}

		// Move the edge to the click position
		switch edge {
		case "left":
			delta := x - left
			cp.state.Components[i].Bounds.X = x
			cp.state.Components[i].Bounds.Width -= delta
		case "right":
			cp.state.Components[i].Bounds.Width = x - left
		case "top":
			delta := y - top
			cp.state.Components[i].Bounds.Y = y
			cp.state.Components[i].Bounds.Height -= delta
		case "bottom":
			cp.state.Components[i].Bounds.Height = y - top
		}

		cp.state.SetModified(true)
		cp.updateComponentOverlay()
		fmt.Printf("Resized component %s %s edge to %.0f\n", comp.ID, edge, map[string]float64{
			"left": x, "right": x, "top": y, "bottom": y,
		}[edge])
		return
	}
}

// abs64 returns the absolute value of a float64.
func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// OnMiddleClickFloodFill handles middle-click for flood fill component detection.
func (cp *ComponentsPanel) OnMiddleClickFloodFill(x, y float64) {
	// Get the rendered canvas output (composited, aligned, stretched)
	img := cp.canvas.GetRenderedOutput()
	if img == nil {
		fmt.Println("No rendered image available for flood fill")
		return
	}

	clickX, clickY := int(x), int(y)

	// Color tolerance - how different can pixels be and still match
	// Higher values find larger regions, lower values are more precise
	const colorTolerance = 25

	fmt.Printf("Middle-click flood fill at canvas (%d, %d)\n", clickX, clickY)

	result, err := component.FloodFillDetect(img, clickX, clickY, colorTolerance)
	if err != nil {
		fmt.Printf("Flood fill failed: %v\n", err)
		return
	}

	// Trim the bounds by scoring a grid and removing low-scoring edges
	// This removes green PCB areas and metallic pins from the edges
	const gridStep = 3      // Sample every 3 pixels
	const minScore = 0.25   // Require 25% matching pixels (trim if >75% miss)

	// Get grid scores for debug visualization
	gridScores := component.GetGridScores(img, result.Bounds, result.SeedColor, colorTolerance, gridStep, minScore)
	trimmedBounds := gridScores.TrimBounds

	// Create debug overlay showing grid points
	if false {
		cp.showGridDebugOverlay(gridScores)
	}

	// Get zoom for coordinate conversion
	zoom := cp.canvas.GetZoom()

	// Create a new component from the trimmed result
	// Use grid-based ID suggestion if existing components have grid IDs
	centerX := float64(trimmedBounds.X+trimmedBounds.Width/2) / zoom
	centerY := float64(trimmedBounds.Y+trimmedBounds.Height/2) / zoom
	compID := component.SuggestComponentID(cp.state.Components, centerX, centerY, 100, "U")

	// Classify package based on dimensions
	// Canvas pixels = source pixels * zoom, so effective DPI = source DPI * zoom
	dpi := cp.state.DPI
	if dpi <= 0 {
		dpi = 1200 // Default
	}
	effectiveDPI := dpi * zoom
	mmToPixels := effectiveDPI / 25.4

	widthMM := float64(trimmedBounds.Width) / mmToPixels
	heightMM := float64(trimmedBounds.Height) / mmToPixels

	pkgType := "UNKNOWN"
	// Check if it matches DIP dimensions
	if component.IsValidDIPWidth(widthMM) || component.IsValidDIPWidth(heightMM) {
		// Estimate pin count from length
		dipLength := heightMM
		if component.IsValidDIPWidth(heightMM) {
			dipLength = widthMM
		}
		pinCount := int(dipLength/2.54) * 2
		if pinCount >= 8 && pinCount <= 40 {
			pkgType = fmt.Sprintf("DIP-%d", pinCount)
		}
	}

	// Convert canvas coordinates to image coordinates (divide by zoom)
	// The overlay drawing will multiply by zoom again
	newComp := &component.Component{
		ID:      compID,
		Package: pkgType,
		Bounds: geometry.Rect{
			X:      float64(trimmedBounds.X) / zoom,
			Y:      float64(trimmedBounds.Y) / zoom,
			Width:  float64(trimmedBounds.Width) / zoom,
			Height: float64(trimmedBounds.Height) / zoom,
		},
		Confirmed: true, // User-selected components are confirmed
	}

	cp.state.Components = append(cp.state.Components, newComp)
	cp.state.SetModified(true)
	cp.rebuildSortedIndices()
	cp.list.Refresh()

	fmt.Printf("Created component %s (%s) at (%.0f,%.0f) size %.0fx%.0f (%.1fx%.1f mm)\n",
		compID, pkgType, newComp.Bounds.X, newComp.Bounds.Y,
		newComp.Bounds.Width, newComp.Bounds.Height, widthMM, heightMM)

	// Select the new component for editing
	newCompIdx := len(cp.state.Components) - 1
	for sortedPos, compIdx := range cp.sortedIndices {
		if compIdx == newCompIdx {
			cp.list.Select(sortedPos)
			cp.showEditDialog(newCompIdx)
			break
		}
	}

	// Update overlay
	cp.updateComponentOverlay()
}

// showGridDebugOverlay creates an overlay showing grid scoring points for debug.
// Green circles = matching color, Red circles = non-matching color.
func (cp *ComponentsPanel) showGridDebugOverlay(gridScores *component.GridScoreResult) {
	if gridScores == nil || len(gridScores.Points) == 0 {
		return
	}

	zoom := cp.canvas.GetZoom()

	// Create two overlays - one for matching points (green) and one for non-matching (red)
	matchOverlay := &canvas.Overlay{
		Color:   color.RGBA{R: 0, G: 255, B: 0, A: 200}, // Green
		Circles: make([]canvas.OverlayCircle, 0),
	}
	noMatchOverlay := &canvas.Overlay{
		Color:   color.RGBA{R: 255, G: 0, B: 0, A: 200}, // Red
		Circles: make([]canvas.OverlayCircle, 0),
	}

	// Also show points in trimmed-out regions with different styling
	trimmedOutOverlay := &canvas.Overlay{
		Color:   color.RGBA{R: 128, G: 128, B: 128, A: 150}, // Gray for trimmed-out areas
		Circles: make([]canvas.OverlayCircle, 0),
	}

	radius := 1.5 // Small circles for grid points

	for _, pt := range gridScores.Points {
		// Convert canvas coordinates to image coordinates (divide by zoom)
		// The overlay drawing will multiply by zoom again
		imgX := float64(pt.X) / zoom
		imgY := float64(pt.Y) / zoom

		circle := canvas.OverlayCircle{
			X:      imgX,
			Y:      imgY,
			Radius: radius,
			Filled: true,
		}

		// Check if point is in the trimmed-out region
		inTrimmed := pt.X >= gridScores.TrimBounds.X &&
			pt.X < gridScores.TrimBounds.X+gridScores.TrimBounds.Width &&
			pt.Y >= gridScores.TrimBounds.Y &&
			pt.Y < gridScores.TrimBounds.Y+gridScores.TrimBounds.Height

		if !inTrimmed {
			// Point is in trimmed-out region
			trimmedOutOverlay.Circles = append(trimmedOutOverlay.Circles, circle)
		} else if pt.Matches {
			matchOverlay.Circles = append(matchOverlay.Circles, circle)
		} else {
			noMatchOverlay.Circles = append(noMatchOverlay.Circles, circle)
		}
	}

	// Set all overlays
	cp.canvas.SetOverlay("grid_match", matchOverlay)
	cp.canvas.SetOverlay("grid_nomatch", noMatchOverlay)
	cp.canvas.SetOverlay("grid_trimmed", trimmedOutOverlay)

	fmt.Printf("Grid debug: %d match, %d no-match, %d trimmed-out points (grid=%dpx, minScore=%.0f%%)\n",
		len(matchOverlay.Circles), len(noMatchOverlay.Circles), len(trimmedOutOverlay.Circles),
		gridScores.GridStep, gridScores.MinScore*100)
}

// updateComponentOverlay refreshes the component overlay on the canvas.
func (cp *ComponentsPanel) updateComponentOverlay() {
	if len(cp.state.Components) == 0 {
		cp.canvas.SetOverlay("components", nil)
		cp.canvas.Refresh()
		return
	}

	overlay := component.CreateOverlay(cp.state.Components)
	overlay.Layer = canvas.LayerFront // Associate with front layer
	cp.canvas.SetOverlay("components", overlay)
	cp.canvas.Refresh()
}

// onOCRSilkscreen runs OCR on the silkscreen to find component labels and coordinates.
func (cp *ComponentsPanel) onOCRSilkscreen() {
	if cp.state.FrontImage == nil || cp.state.FrontImage.Image == nil {
		fmt.Println("No front image loaded for OCR")
		return
	}

	fmt.Println("Starting silkscreen OCR...")

	// Create OCR engine
	engine, err := ocr.NewEngine()
	if err != nil {
		fmt.Printf("Failed to create OCR engine: %v\n", err)
		return
	}
	defer engine.Close()

	// Convert Go image to gocv.Mat
	img := cp.state.FrontImage.Image
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	// Create RGBA image
	rgba := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			rgba.Set(x, y, img.At(x, y))
		}
	}

	// Convert to gocv.Mat
	mat, err := gocv.NewMatFromBytes(h, w, gocv.MatTypeCV8UC4, rgba.Pix)
	if err != nil {
		fmt.Printf("Failed to convert image: %v\n", err)
		return
	}
	defer mat.Close()

	// Convert RGBA to BGR
	bgr := gocv.NewMat()
	defer bgr.Close()
	gocv.CvtColor(mat, &bgr, gocv.ColorRGBAToBGR)

	// Run silkscreen detection
	result, err := engine.DetectSilkscreen(bgr)
	if err != nil {
		fmt.Printf("Silkscreen OCR error: %v\n", err)
		return
	}

	// Display results
	fmt.Printf("\n=== Silkscreen OCR Results ===\n")
	fmt.Printf("Found %d component designators:\n", len(result.Designators))

	// Group by type
	counts := result.GetDesignatorCounts()
	for prefix, count := range counts {
		fmt.Printf("  %s: %d\n", prefix, count)
		designators := result.GetDesignatorsByType(prefix)
		for _, d := range designators {
			fmt.Printf("    %s at (%d,%d)\n", d.Text, d.Bounds.X, d.Bounds.Y)
		}
	}

	if result.XAxis != nil {
		fmt.Printf("\nX-Axis detected: %d markers\n", len(result.XAxis.Markers))
		for _, m := range result.XAxis.Markers {
			fmt.Printf("  %s at X=%d\n", m.Text, m.Bounds.X)
		}
	}

	if result.YAxis != nil {
		fmt.Printf("\nY-Axis detected: %d markers\n", len(result.YAxis.Markers))
		for _, m := range result.YAxis.Markers {
			fmt.Printf("  %s at Y=%d\n", m.Text, m.Bounds.Y)
		}
	}

	fmt.Printf("\nTotal text items found: %d\n", len(result.AllText))
	fmt.Printf("==============================\n")

	// Create overlay for detected text
	cp.updateOCROverlay(result)
}

// updateOCROverlay shows detected silkscreen text on the canvas.
func (cp *ComponentsPanel) updateOCROverlay(result *ocr.SilkscreenResult) {
	if result == nil || len(result.AllText) == 0 {
		cp.canvas.SetOverlay("ocr", nil)
		cp.canvas.Refresh()
		return
	}

	// Create overlay - cyan for component designators, yellow for coordinates
	// Associate with front layer so overlay follows layer offset adjustments
	overlay := &canvas.Overlay{
		Color: color.RGBA{R: 0, G: 255, B: 255, A: 255}, // Cyan
		Layer: canvas.LayerFront,
	}

	// Add rectangles for designators
	for _, d := range result.Designators {
		rect := canvas.OverlayRect{
			X:      d.Bounds.X,
			Y:      d.Bounds.Y,
			Width:  d.Bounds.Width,
			Height: d.Bounds.Height,
			Label:  d.Text,
			Fill:   canvas.FillNone,
		}
		overlay.Rectangles = append(overlay.Rectangles, rect)
	}

	cp.canvas.SetOverlay("ocr", overlay)
	cp.canvas.Refresh()
}

// Overlay name constants for via and connector overlays.
const (
	OverlayFrontVias     = "front_vias"
	OverlayBackVias      = "back_vias"
	OverlayConfirmedVias = "confirmed_vias"
	OverlayConnectors    = "connectors"
)

// TracesPanel displays and manages detected vias and traces.
type TracesPanel struct {
	state     *app.State
	canvas    *canvas.ImageCanvas
	container fyne.CanvasObject

	// Via detection UI
	viaLayerSelect      *widget.RadioGroup
	detectViasBtn       *widget.Button
	clearViasBtn        *widget.Button
	matchViasBtn        *widget.Button
	viaStatusLabel      *widget.Label
	viaCountLabel       *widget.Label
	confirmedCountLabel *widget.Label
	trainingLabel       *widget.Label

	// Via edit mode (when enabled, clicks add/remove vias)
	viaEditMode      bool
	viaEditModeCheck *widget.Check

	// Trace detection UI
	detectTracesBtn  *widget.Button
	traceStatusLabel *widget.Label

	// Default via radius for manual addition (pixels at current DPI)
	defaultViaRadius float64
}

// NewTracesPanel creates a new traces panel.
func NewTracesPanel(state *app.State, cvs *canvas.ImageCanvas) *TracesPanel {
	tp := &TracesPanel{
		state:            state,
		canvas:           cvs,
		defaultViaRadius: 15, // Default radius in pixels, will be updated based on DPI
	}

	// Via detection UI
	tp.viaLayerSelect = widget.NewRadioGroup([]string{"Front", "Back"}, func(selected string) {
		// Raise the selected layer to the top
		if selected == "Front" {
			cvs.RaiseLayerBySide(pcbimage.SideFront)
		} else {
			cvs.RaiseLayerBySide(pcbimage.SideBack)
		}
	})
	tp.viaLayerSelect.SetSelected("Front")
	tp.viaLayerSelect.Horizontal = true

	tp.detectViasBtn = widget.NewButton("Detect Vias", func() {
		tp.onDetectVias()
	})

	tp.clearViasBtn = widget.NewButton("Clear", func() {
		tp.onClearVias()
	})

	tp.matchViasBtn = widget.NewButton("Match Vias", func() {
		tp.tryMatchVias()
	})

	tp.viaStatusLabel = widget.NewLabel("")
	tp.viaStatusLabel.Wrapping = fyne.TextWrapWord

	tp.viaCountLabel = widget.NewLabel("No vias detected")
	tp.confirmedCountLabel = widget.NewLabel("Confirmed: 0")

	tp.trainingLabel = widget.NewLabel("Training: 0 pos, 0 neg")

	// Trace detection UI (stub for now)
	tp.detectTracesBtn = widget.NewButton("Detect Traces", func() {
		tp.traceStatusLabel.SetText("Trace detection not yet implemented")
	})
	tp.detectTracesBtn.Disable() // Disable until implemented

	tp.traceStatusLabel = widget.NewLabel("")

	// Set up click handlers for manual via annotation
	cvs.OnLeftClick(func(x, y float64) {
		tp.onLeftClickVia(x, y)
	})
	cvs.OnRightClick(func(x, y float64) {
		tp.onRightClickVia(x, y)
	})

	// Load training set from default location
	tp.loadTrainingSet()
	tp.updateTrainingLabel()

	// Auto-match vias and create connectors when alignment completes
	state.On(app.EventAlignmentComplete, func(data interface{}) {
		// Create connectors from detected contacts (but don't show overlay)
		tp.state.CreateConnectorsFromAlignment()
		connCount := tp.state.FeaturesLayer.ConnectorCount()
		fmt.Printf("Created %d connectors from alignment contacts\n", connCount)
		// Don't create connector overlay - user doesn't want contacts outlined

		// Auto-match vias if both sides have vias
		frontVias := tp.state.FeaturesLayer.GetViasBySide(pcbimage.SideFront)
		backVias := tp.state.FeaturesLayer.GetViasBySide(pcbimage.SideBack)
		if len(frontVias) > 0 && len(backVias) > 0 {
			fmt.Printf("Auto-matching vias after alignment complete...\n")
			tp.tryMatchVias()
		}
	})

	// Via edit mode checkbox
	tp.viaEditModeCheck = widget.NewCheck("Enable via edit mode", func(checked bool) {
		tp.viaEditMode = checked
		if checked {
			tp.viaStatusLabel.SetText("Edit mode: click to add, right-click to remove")
		} else {
			tp.viaStatusLabel.SetText("")
		}
	})

	// Layout
	tp.container = container.NewVBox(
		widget.NewCard("Via Detection", "", container.NewVBox(
			widget.NewLabel("Layer:"),
			tp.viaLayerSelect,
			container.NewHBox(tp.detectViasBtn, tp.clearViasBtn),
			tp.matchViasBtn,
			tp.viaStatusLabel,
			tp.viaCountLabel,
			tp.confirmedCountLabel,
		)),
		widget.NewCard("Manual Via Editing", "", container.NewVBox(
			tp.viaEditModeCheck,
			widget.NewLabel("Left-click: add via"),
			widget.NewLabel("Right-click: remove via"),
			tp.trainingLabel,
		)),
		widget.NewCard("Trace Detection", "", container.NewVBox(
			tp.detectTracesBtn,
			tp.traceStatusLabel,
		)),
	)

	return tp
}

// onDetectVias runs via detection on the selected layer.
func (tp *TracesPanel) onDetectVias() {
	isFront := tp.viaLayerSelect.Selected == "Front"

	var img *pcbimage.Layer
	var side pcbimage.Side
	var layerName string

	if isFront {
		img = tp.state.FrontImage
		side = pcbimage.SideFront
		layerName = "Front"
	} else {
		img = tp.state.BackImage
		side = pcbimage.SideBack
		layerName = "Back"
	}

	if img == nil || img.Image == nil {
		tp.viaStatusLabel.SetText(fmt.Sprintf("No %s image loaded", layerName))
		return
	}

	// Get DPI
	dpi := tp.state.DPI
	if dpi == 0 && img.DPI > 0 {
		dpi = img.DPI
	}
	if dpi == 0 {
		tp.viaStatusLabel.SetText("DPI unknown - load a TIFF with DPI metadata")
		return
	}

	tp.viaStatusLabel.SetText(fmt.Sprintf("Detecting vias on %s...", layerName))
	tp.detectViasBtn.Disable()

	go func() {
		// Set up detection parameters
		params := via.DefaultParams().WithDPI(dpi)

		// Run detection
		result, err := via.DetectViasFromImage(img.Image, side, params)

		tp.detectViasBtn.Enable()

		if err != nil {
			tp.viaStatusLabel.SetText(fmt.Sprintf("Error: %v", err))
			return
		}

		// Post-process: detect metal boundaries for each via (parallel)
		// This gives us polygon boundaries instead of just circles
		numVias := len(result.Vias)
		fmt.Printf("Post-processing %d detected vias to find metal boundaries (parallel)...\n", numVias)
		maxRadius := 0.030 * dpi // 30 mil search radius

		startTime := time.Now()
		numWorkers := runtime.NumCPU()
		if numWorkers > numVias {
			numWorkers = numVias
		}
		if numWorkers < 1 {
			numWorkers = 1
		}

		var wg sync.WaitGroup
		viaChan := make(chan int, numVias)

		// Start workers
		for w := 0; w < numWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range viaChan {
					v := &result.Vias[i]
					boundary := via.DetectMetalBoundary(img.Image, v.Center.X, v.Center.Y, maxRadius)
					v.PadBoundary = boundary.Boundary
					v.Center = boundary.Center
					v.Radius = boundary.Radius
				}
			}()
		}

		// Send work
		for i := range result.Vias {
			viaChan <- i
		}
		close(viaChan)

		wg.Wait()
		elapsed := time.Since(startTime)
		fmt.Printf("Post-processing complete (%d workers, %.1fms)\n", numWorkers, float64(elapsed.Microseconds())/1000)

		// Add to features layer
		tp.state.FeaturesLayer.AddVias(result.Vias)

		// Create overlay for visualization
		tp.createViaOverlay(result.Vias, side, false)

		// Update counts
		front, back := tp.state.FeaturesLayer.ViaCountBySide()
		tp.viaCountLabel.SetText(fmt.Sprintf("Vias: %d front, %d back", front, back))

		// Count by method
		houghCount := 0
		contourCount := 0
		for _, v := range result.Vias {
			switch v.Method {
			case via.MethodHoughCircle:
				houghCount++
			case via.MethodContourFit:
				contourCount++
			}
		}

		tp.viaStatusLabel.SetText(fmt.Sprintf("%s: %d vias (%d Hough, %d contour)",
			layerName, len(result.Vias), houghCount, contourCount))

		// Emit event
		tp.state.Emit(app.EventFeaturesChanged, nil)
	}()
}

// createViaOverlay creates a canvas overlay to visualize detected vias.
// Uses polygons for vias with boundary points, rectangles for circular vias.
// If skipMatched is true, vias with BothSidesConfirmed are excluded (they're in confirmed overlay).
func (tp *TracesPanel) createViaOverlay(vias []via.Via, side pcbimage.Side, skipMatched bool) {
	fmt.Printf("  createViaOverlay: %d vias for side=%v skipMatched=%v\n", len(vias), side, skipMatched)
	var overlayName string
	var overlayColor color.RGBA

	if side == pcbimage.SideFront {
		overlayName = OverlayFrontVias
		overlayColor = colorutil.Cyan
	} else {
		overlayName = OverlayBackVias
		overlayColor = colorutil.Magenta
	}

	overlay := &canvas.Overlay{
		Color:      overlayColor,
		Rectangles: make([]canvas.OverlayRect, 0),
		Polygons:   make([]canvas.OverlayPolygon, 0),
	}

	for _, v := range vias {
		// Skip matched vias if they're shown in the confirmed overlay
		if skipMatched && v.BothSidesConfirmed {
			continue
		}

		// Unmatched (half) vias: no label, outline only
		// Only confirmed vias get IDs and filled rendering

		// Use polygon if we have boundary points, otherwise use rectangle
		if len(v.PadBoundary) >= 3 {
			fmt.Printf("    Via %s: POLYGON outline with %d points\n", v.ID, len(v.PadBoundary))
			overlay.Polygons = append(overlay.Polygons, canvas.OverlayPolygon{
				Points: v.PadBoundary,
				Label:  "", // No label for unmatched vias
				Filled: false, // Outline only
			})
		} else {
			// Fall back to rectangle for circular vias without boundary
			bounds := v.Bounds()
			fmt.Printf("    Via %s: RECT outline at (%d,%d) %dx%d\n", v.ID, bounds.X, bounds.Y, bounds.Width, bounds.Height)

			overlay.Rectangles = append(overlay.Rectangles, canvas.OverlayRect{
				X:      bounds.X,
				Y:      bounds.Y,
				Width:  bounds.Width,
				Height: bounds.Height,
				Fill:   canvas.FillNone, // Outline only
				Label:  "", // No label for unmatched vias
			})
		}
	}

	fmt.Printf("  Setting overlay '%s': %d rects, %d polygons\n", overlayName, len(overlay.Rectangles), len(overlay.Polygons))
	tp.canvas.SetOverlay(overlayName, overlay)
}

// onClearVias clears all detected vias.
func (tp *TracesPanel) onClearVias() {
	tp.state.FeaturesLayer.ClearVias()
	tp.state.FeaturesLayer.ClearConfirmedVias()
	tp.canvas.ClearOverlay(OverlayFrontVias)
	tp.canvas.ClearOverlay(OverlayBackVias)
	tp.canvas.ClearOverlay(OverlayConfirmedVias)
	tp.viaCountLabel.SetText("No vias detected")
	tp.confirmedCountLabel.SetText("Confirmed: 0")
	tp.viaStatusLabel.SetText("Cleared")
	tp.state.Emit(app.EventFeaturesChanged, nil)
}

// Container returns the panel container.
func (tp *TracesPanel) Container() fyne.CanvasObject {
	return tp.container
}

// SetEnabled enables or disables the panel's interactive widgets.
func (tp *TracesPanel) SetEnabled(enabled bool) {
	if enabled {
		tp.detectViasBtn.Enable()
		tp.clearViasBtn.Enable()
		tp.matchViasBtn.Enable()
		tp.detectTracesBtn.Enable()
	} else {
		tp.detectViasBtn.Disable()
		tp.clearViasBtn.Disable()
		tp.matchViasBtn.Disable()
		tp.detectTracesBtn.Disable()
	}
}

// loadTrainingSet loads the training set from the default location.
func (tp *TracesPanel) loadTrainingSet() {
	// Use project directory if available, otherwise use current directory
	trainingPath := "via_training.json"
	if tp.state.ProjectPath != "" {
		trainingPath = filepath.Join(filepath.Dir(tp.state.ProjectPath), "via_training.json")
	}

	ts, err := via.LoadTrainingSet(trainingPath)
	if err != nil {
		fmt.Printf("Warning: failed to load training set: %v\n", err)
		ts = via.NewTrainingSet()
	}
	ts.SetFilePath(trainingPath)
	tp.state.ViaTrainingSet = ts
}

// updateTrainingLabel updates the training label with current counts.
func (tp *TracesPanel) updateTrainingLabel() {
	if tp.state.ViaTrainingSet == nil {
		tp.trainingLabel.SetText("Training: not loaded")
		return
	}
	pos := tp.state.ViaTrainingSet.PositiveCount()
	neg := tp.state.ViaTrainingSet.NegativeCount()
	tp.trainingLabel.SetText(fmt.Sprintf("Training: %d pos, %d neg", pos, neg))
}

// onLeftClickVia handles left-click to add a via at the clicked location.
// If the click is near an existing via, the new boundary is merged with it.
// If the click is inside a confirmed via, the underlying via on the selected side is expanded.
func (tp *TracesPanel) onLeftClickVia(x, y float64) {
	// Only process clicks when via edit mode is enabled
	if !tp.viaEditMode {
		return
	}
	fmt.Printf("\n=== LEFT CLICK VIA at (%.1f, %.1f) ===\n", x, y)

	// Determine which side based on current layer selection
	isFront := tp.viaLayerSelect.Selected == "Front"
	var side pcbimage.Side
	var img *pcbimage.Layer
	if isFront {
		side = pcbimage.SideFront
		img = tp.state.FrontImage
		fmt.Printf("  Side: FRONT\n")
	} else {
		side = pcbimage.SideBack
		img = tp.state.BackImage
		fmt.Printf("  Side: BACK\n")
	}

	if img == nil {
		fmt.Printf("  ERROR: No image loaded for this side\n")
		tp.viaStatusLabel.SetText("No image loaded for this side")
		return
	}

	// Calculate max search radius based on DPI (typical via is ~0.050" max diameter)
	maxRadius := tp.defaultViaRadius
	if tp.state.DPI > 0 {
		maxRadius = 0.030 * tp.state.DPI // 30 mil search radius
	}
	fmt.Printf("  Max search radius: %.1f px (DPI=%.0f)\n", maxRadius, tp.state.DPI)

	// First, check if click is inside a confirmed via
	confirmedVia := tp.state.FeaturesLayer.HitTestConfirmedVia(x, y)
	if confirmedVia != nil {
		fmt.Printf("  HIT CONFIRMED VIA: %s\n", confirmedVia.ID)
		tp.expandConfirmedVia(confirmedVia, x, y, side, img, maxRadius)
		return
	}

	// Detect metal boundary around the clicked point
	fmt.Printf("  Calling DetectMetalBoundary...\n")
	boundary := via.DetectMetalBoundary(img.Image, x, y, maxRadius)
	fmt.Printf("  Boundary result: center=(%.1f,%.1f) radius=%.1f isCircle=%v boundaryPts=%d\n",
		boundary.Center.X, boundary.Center.Y, boundary.Radius, boundary.IsCircle, len(boundary.Boundary))

	// Check if click is near an existing via - if so, merge boundaries
	// Use 1mm tolerance (1mm = ~0.03937 inches)
	mergeTolerance := 15.0 // Default 15 pixels
	if tp.state.DPI > 0 {
		mergeTolerance = 0.03937 * tp.state.DPI // 1mm in pixels
	}
	fmt.Printf("  Checking for nearby vias (tolerance=%.1f px)...\n", mergeTolerance)
	vias := tp.state.FeaturesLayer.GetViasBySide(side)
	fmt.Printf("  Existing vias on this side: %d\n", len(vias))
	var nearestVia *via.Via
	nearestDist := mergeTolerance * mergeTolerance

	for i := range vias {
		v := &vias[i]
		dx := v.Center.X - x
		dy := v.Center.Y - y
		dist := dx*dx + dy*dy
		if dist < nearestDist {
			nearestDist = dist
			nearestVia = v
		}
	}

	if nearestVia != nil {
		fmt.Printf("  MERGE: Found nearby via %s at dist=%.1f\n", nearestVia.ID, math.Sqrt(nearestDist))
		// Merge with existing via
		existingBoundary := via.BoundaryResult{
			Center:   nearestVia.Center,
			Radius:   nearestVia.Radius,
			Boundary: nearestVia.PadBoundary,
			IsCircle: len(nearestVia.PadBoundary) == 0,
		}
		fmt.Printf("  Existing boundary: pts=%d radius=%.1f\n", len(existingBoundary.Boundary), existingBoundary.Radius)

		fmt.Printf("  Calling MergeBoundaries...\n")
		merged := via.MergeBoundaries(existingBoundary, boundary)
		fmt.Printf("  Merged result: center=(%.1f,%.1f) radius=%.1f pts=%d\n",
			merged.Center.X, merged.Center.Y, merged.Radius, len(merged.Boundary))

		// Filter out any green points from the merged boundary
		merged.Boundary = via.FilterGreenPoints(img.Image, merged.Boundary)
		fmt.Printf("  After green filter: %d pts\n", len(merged.Boundary))

		// Debug: dump pixel colors and PNG for merged boundary
		if false {
			via.DumpBoundaryPixels(img.Image, merged.Boundary, nearestVia.ID+"-merged")
			via.DumpBoundaryPNG(img.Image, merged.Boundary, nearestVia.ID+"-merged")
		}

		// Update the existing via with merged boundary
		tp.state.FeaturesLayer.RemoveVia(nearestVia.ID)
		updatedVia := via.Via{
			ID:          nearestVia.ID,
			Center:      merged.Center,
			Radius:      merged.Radius,
			Side:        side,
			Confidence:  1.0,
			Method:      via.MethodManual,
			Circularity: 1.0,
			PadBoundary: merged.Boundary,
		}
		tp.state.FeaturesLayer.AddVia(updatedVia)
		fmt.Printf("  Updated via %s with %d boundary points\n", updatedVia.ID, len(updatedVia.PadBoundary))

		fmt.Printf("  Calling refreshViaOverlay...\n")
		tp.refreshViaOverlay(side)
		front, back := tp.state.FeaturesLayer.ViaCountBySide()
		tp.viaCountLabel.SetText(fmt.Sprintf("Vias: %d front, %d back", front, back))
		tp.viaStatusLabel.SetText(fmt.Sprintf("Expanded %s (r=%.0f)", nearestVia.ID, merged.Radius))
		tp.state.Emit(app.EventFeaturesChanged, nil)
		fmt.Printf("  MERGE COMPLETE\n")
		return
	}

	// No nearby via - create a new one
	fmt.Printf("  NEW VIA: No nearby via found, creating new one\n")
	viaNum := tp.state.FeaturesLayer.NextViaNumber()

	// Create manual via with detected boundary
	newVia := via.Via{
		ID:          fmt.Sprintf("via-%03d", viaNum),
		Center:      boundary.Center,
		Radius:      boundary.Radius,
		Side:        side,
		Confidence:  1.0, // Manual = high confidence
		Method:      via.MethodManual,
		Circularity: 1.0,
		PadBoundary: boundary.Boundary,
	}
	fmt.Printf("  Created via %s with %d boundary points\n", newVia.ID, len(newVia.PadBoundary))

	// Debug: dump pixel colors and PNG for manual vias
	if false {
		via.DumpBoundaryPixels(img.Image, boundary.Boundary, newVia.ID)
		via.DumpBoundaryPNG(img.Image, boundary.Boundary, newVia.ID)
	}

	// Add to features layer
	tp.state.FeaturesLayer.AddVia(newVia)

	// Quick-match: check if there's a matching via on the opposite side
	var matchedVia *via.Via
	var statusMsg string
	if tp.state.Aligned {
		matchedVia = tp.tryQuickMatchVia(&newVia)
		if matchedVia != nil {
			statusMsg = fmt.Sprintf("Added %s + matched %s → confirmed", newVia.ID, matchedVia.ID)
		} else {
			statusMsg = fmt.Sprintf("Added %s (r=%.0f)", newVia.ID, boundary.Radius)
		}
	} else {
		statusMsg = fmt.Sprintf("Added %s (r=%.0f)", newVia.ID, boundary.Radius)
	}

	// Add positive training sample
	if tp.state.ViaTrainingSet != nil {
		tp.state.ViaTrainingSet.AddPositive(boundary.Center, boundary.Radius, side, "manual")
		if err := tp.state.ViaTrainingSet.Save(); err != nil {
			fmt.Printf("Warning: failed to save training set: %v\n", err)
		}
		tp.updateTrainingLabel()
	}

	// Update overlay
	fmt.Printf("  Calling refreshViaOverlay...\n")
	tp.refreshViaOverlay(side)
	if matchedVia != nil {
		// Also refresh the other side and confirmed vias overlay
		if side == pcbimage.SideFront {
			tp.refreshViaOverlay(pcbimage.SideBack)
		} else {
			tp.refreshViaOverlay(pcbimage.SideFront)
		}
		tp.refreshConfirmedViaOverlay()
	}

	// Update counts
	front, back := tp.state.FeaturesLayer.ViaCountBySide()
	tp.viaCountLabel.SetText(fmt.Sprintf("Vias: %d front, %d back", front, back))

	tp.viaStatusLabel.SetText(statusMsg)
	tp.state.Emit(app.EventFeaturesChanged, nil)
	if matchedVia != nil {
		tp.state.Emit(app.EventConfirmedViasChanged, nil)
	}
	fmt.Printf("  NEW VIA COMPLETE\n")
}

// onRightClickVia handles right-click to remove a via at the clicked location.
func (tp *TracesPanel) onRightClickVia(x, y float64) {
	// Only process clicks when via edit mode is enabled
	if !tp.viaEditMode {
		return
	}
	// Determine which side based on current layer selection
	isFront := tp.viaLayerSelect.Selected == "Front"
	var side pcbimage.Side
	if isFront {
		side = pcbimage.SideFront
	} else {
		side = pcbimage.SideBack
	}

	// Find tolerance based on DPI (click within ~0.030" of via center)
	tolerance := 20.0 // Default pixels
	if tp.state.DPI > 0 {
		tolerance = 0.030 * tp.state.DPI
	}

	center := geometry.Point2D{X: x, Y: y}

	// Find and remove the closest via within tolerance
	vias := tp.state.FeaturesLayer.GetViasBySide(side)
	var closestVia *via.Via
	closestDist := tolerance * tolerance

	for i := range vias {
		v := &vias[i]
		dx := v.Center.X - x
		dy := v.Center.Y - y
		dist := dx*dx + dy*dy
		if dist < closestDist {
			closestDist = dist
			closestVia = v
		}
	}

	if closestVia == nil {
		tp.viaStatusLabel.SetText(fmt.Sprintf("No via near (%.0f, %.0f)", x, y))
		return
	}

	// Remove from features layer
	tp.state.FeaturesLayer.RemoveVia(closestVia.ID)

	// Add negative training sample (this location is NOT a via)
	if tp.state.ViaTrainingSet != nil {
		tp.state.ViaTrainingSet.AddNegative(center, closestVia.Radius, side, "rejected")
		if err := tp.state.ViaTrainingSet.Save(); err != nil {
			fmt.Printf("Warning: failed to save training set: %v\n", err)
		}
		tp.updateTrainingLabel()
	}

	// Update overlay
	tp.refreshViaOverlay(side)

	// Update counts
	front, back := tp.state.FeaturesLayer.ViaCountBySide()
	tp.viaCountLabel.SetText(fmt.Sprintf("Vias: %d front, %d back", front, back))

	tp.viaStatusLabel.SetText(fmt.Sprintf("Removed via %s", closestVia.ID))
	tp.state.Emit(app.EventFeaturesChanged, nil)
}

// expandConfirmedVia handles expanding a confirmed via when it's clicked.
// The underlying via on the selected side is expanded, then the intersection is recomputed.
func (tp *TracesPanel) expandConfirmedVia(cv *via.ConfirmedVia, x, y float64, side pcbimage.Side, img *pcbimage.Layer, maxRadius float64) {
	fmt.Printf("  expandConfirmedVia: %s on %s side\n", cv.ID, side.String())

	// Detect metal boundary at click point
	boundary := via.DetectMetalBoundary(img.Image, x, y, maxRadius)
	fmt.Printf("  Detected boundary: center=(%.1f,%.1f) radius=%.1f pts=%d\n",
		boundary.Center.X, boundary.Center.Y, boundary.Radius, len(boundary.Boundary))

	// Get the underlying via on the selected side
	var underlyingViaID string
	if side == pcbimage.SideFront {
		underlyingViaID = cv.FrontViaID
	} else {
		underlyingViaID = cv.BackViaID
	}

	underlyingVia := tp.state.FeaturesLayer.GetViaByID(underlyingViaID)
	if underlyingVia == nil {
		fmt.Printf("  ERROR: Could not find underlying via %s\n", underlyingViaID)
		tp.viaStatusLabel.SetText(fmt.Sprintf("Error: via %s not found", underlyingViaID))
		return
	}
	fmt.Printf("  Underlying via: %s center=(%.1f,%.1f) radius=%.1f pts=%d\n",
		underlyingVia.ID, underlyingVia.Center.X, underlyingVia.Center.Y,
		underlyingVia.Radius, len(underlyingVia.PadBoundary))

	// Merge the new boundary with the existing via boundary
	existingBoundary := via.BoundaryResult{
		Center:   underlyingVia.Center,
		Radius:   underlyingVia.Radius,
		Boundary: underlyingVia.PadBoundary,
		IsCircle: len(underlyingVia.PadBoundary) == 0,
	}
	merged := via.MergeBoundaries(existingBoundary, boundary)
	fmt.Printf("  Merged boundary: center=(%.1f,%.1f) radius=%.1f pts=%d\n",
		merged.Center.X, merged.Center.Y, merged.Radius, len(merged.Boundary))

	// Filter out any green points from the merged boundary
	merged.Boundary = via.FilterGreenPoints(img.Image, merged.Boundary)
	fmt.Printf("  After green filter: %d pts\n", len(merged.Boundary))

	// Debug: dump pixel colors inside the merged boundary
	if false {
		via.DumpBoundaryPixels(img.Image, merged.Boundary, underlyingVia.ID+"-expanded")
	}

	// Update the underlying via
	updatedVia := *underlyingVia
	updatedVia.Center = merged.Center
	updatedVia.Radius = merged.Radius
	updatedVia.PadBoundary = merged.Boundary
	tp.state.FeaturesLayer.UpdateVia(updatedVia)

	// Get both front and back vias for intersection update
	frontVia := tp.state.FeaturesLayer.GetViaByID(cv.FrontViaID)
	backVia := tp.state.FeaturesLayer.GetViaByID(cv.BackViaID)
	if frontVia != nil && backVia != nil {
		// Recompute the intersection
		cv.UpdateIntersection(frontVia, backVia)
		fmt.Printf("  Updated intersection: center=(%.1f,%.1f) pts=%d\n",
			cv.Center.X, cv.Center.Y, len(cv.IntersectionBoundary))
	}

	// Refresh all overlays to show the updated boundaries
	tp.refreshAllViaOverlays()

	tp.viaStatusLabel.SetText(fmt.Sprintf("Expanded %s via %s (r=%.0f)", cv.ID, underlyingViaID, merged.Radius))
	tp.state.Emit(app.EventFeaturesChanged, nil)
	tp.state.Emit(app.EventConfirmedViasChanged, nil)
	fmt.Printf("  expandConfirmedVia COMPLETE\n")
}

// refreshViaOverlay recreates the via overlay for the specified side.
func (tp *TracesPanel) refreshViaOverlay(side pcbimage.Side) {
	vias := tp.state.FeaturesLayer.GetViasBySide(side)
	// Don't skip matched vias if there are no confirmed vias
	skipMatched := tp.state.FeaturesLayer.ConfirmedViaCount() > 0
	tp.createViaOverlay(vias, side, skipMatched)
}

// refreshConfirmedViaOverlay recreates just the confirmed via overlay.
func (tp *TracesPanel) refreshConfirmedViaOverlay() {
	confirmedVias := tp.state.FeaturesLayer.GetConfirmedVias()
	tp.createConfirmedViaOverlay(confirmedVias)
	tp.confirmedCountLabel.SetText(fmt.Sprintf("Confirmed: %d", len(confirmedVias)))
}

// refreshAllViaOverlays recreates all via overlays (front, back, and confirmed).
func (tp *TracesPanel) refreshAllViaOverlays() {
	// Create confirmed via overlay first (so we know which vias to skip)
	confirmedVias := tp.state.FeaturesLayer.GetConfirmedVias()
	tp.createConfirmedViaOverlay(confirmedVias)

	// Create front and back overlays, skipping matched vias
	skipMatched := len(confirmedVias) > 0
	frontVias := tp.state.FeaturesLayer.GetViasBySide(pcbimage.SideFront)
	tp.createViaOverlay(frontVias, pcbimage.SideFront, skipMatched)

	backVias := tp.state.FeaturesLayer.GetViasBySide(pcbimage.SideBack)
	tp.createViaOverlay(backVias, pcbimage.SideBack, skipMatched)

	// Update count labels
	front, back := tp.state.FeaturesLayer.ViaCountBySide()
	tp.viaCountLabel.SetText(fmt.Sprintf("Vias: %d front, %d back", front, back))
	tp.confirmedCountLabel.SetText(fmt.Sprintf("Confirmed: %d", len(confirmedVias)))
}

// createConfirmedViaOverlay creates a canvas overlay for confirmed vias (blue).
func (tp *TracesPanel) createConfirmedViaOverlay(confirmedVias []*via.ConfirmedVia) {
	fmt.Printf("  createConfirmedViaOverlay: %d confirmed vias\n", len(confirmedVias))

	overlay := &canvas.Overlay{
		Color:      colorutil.Blue,
		Rectangles: make([]canvas.OverlayRect, 0),
		Polygons:   make([]canvas.OverlayPolygon, 0),
	}

	for _, cv := range confirmedVias {
		// Extract via number from ID for label
		label := cv.ID
		var viaNum int
		if _, err := fmt.Sscanf(cv.ID, "cvia-%d", &viaNum); err == nil {
			label = fmt.Sprintf("%d", viaNum)
		}

		if len(cv.IntersectionBoundary) >= 3 {
			fmt.Printf("    Confirmed %s: POLYGON with %d points\n", cv.ID, len(cv.IntersectionBoundary))
			overlay.Polygons = append(overlay.Polygons, canvas.OverlayPolygon{
				Points: cv.IntersectionBoundary,
				Label:  label,
				Filled: true,
			})
		} else {
			// Fall back to rectangle for confirmed vias without boundary
			bounds := cv.Bounds()
			fmt.Printf("    Confirmed %s: RECT at (%d,%d) %dx%d\n", cv.ID, bounds.X, bounds.Y, bounds.Width, bounds.Height)
			overlay.Rectangles = append(overlay.Rectangles, canvas.OverlayRect{
				X:      bounds.X,
				Y:      bounds.Y,
				Width:  bounds.Width,
				Height: bounds.Height,
				Fill:   canvas.FillSolid,
				Label:  label,
			})
		}
	}

	fmt.Printf("  Setting overlay '%s': %d rects, %d polygons\n", OverlayConfirmedVias, len(overlay.Rectangles), len(overlay.Polygons))
	tp.canvas.SetOverlay(OverlayConfirmedVias, overlay)
}

// createConnectorOverlay creates a canvas overlay for board edge connectors.
// Connectors are displayed as green rectangles with signal name labels.
// Labels are drawn rotated -90 degrees and fade with the layer opacity.
func (tp *TracesPanel) createConnectorOverlay() {
	connectors := tp.state.FeaturesLayer.GetConnectors()
	fmt.Printf("  createConnectorOverlay: %d connectors\n", len(connectors))

	overlay := &canvas.Overlay{
		Color:      colorutil.Green,
		Rectangles: make([]canvas.OverlayRect, 0, len(connectors)),
		Polygons:   make([]canvas.OverlayPolygon, 0),
	}

	// Build connector labels for opacity-aware rendering
	labels := make([]canvas.ConnectorLabel, 0, len(connectors))

	for _, c := range connectors {
		// Use signal name as label, or pin number if no signal name
		label := c.SignalName
		if label == "" {
			label = fmt.Sprintf("P%d", c.PinNumber)
		}

		// Add rectangle overlay (no label - labels are drawn separately with opacity)
		overlay.Rectangles = append(overlay.Rectangles, canvas.OverlayRect{
			X:      c.Bounds.X,
			Y:      c.Bounds.Y,
			Width:  c.Bounds.Width,
			Height: c.Bounds.Height,
			Fill:   canvas.FillNone,
		})

		// Add connector label for opacity-aware rendering
		labels = append(labels, canvas.ConnectorLabel{
			Label:   label,
			CenterX: c.Center.X,
			CenterY: c.Center.Y,
			Side:    c.Side,
		})
	}

	fmt.Printf("  Setting overlay '%s': %d rects, %d labels\n", OverlayConnectors, len(overlay.Rectangles), len(labels))
	tp.canvas.SetOverlay(OverlayConnectors, overlay)
	tp.canvas.SetConnectorLabels(labels)
}

// tryMatchVias attempts to match front and back vias to create confirmed vias.
func (tp *TracesPanel) tryMatchVias() {
	frontVias := tp.state.FeaturesLayer.GetViasBySide(pcbimage.SideFront)
	backVias := tp.state.FeaturesLayer.GetViasBySide(pcbimage.SideBack)

	if len(frontVias) == 0 || len(backVias) == 0 {
		tp.viaStatusLabel.SetText("Need vias on both sides to match")
		return
	}

	if !tp.state.Aligned {
		tp.viaStatusLabel.SetText("Images must be aligned before matching")
		return
	}

	tp.viaStatusLabel.SetText("Matching vias...")

	// Clear existing confirmed vias
	tp.state.FeaturesLayer.ClearConfirmedVias()

	// Calculate matching tolerance based on DPI
	tolerance := via.SuggestMatchTolerance(tp.state.DPI)
	fmt.Printf("tryMatchVias: %d front, %d back, tolerance=%.1f px\n", len(frontVias), len(backVias), tolerance)

	// Match vias
	result := via.MatchViasAcrossSides(frontVias, backVias, tolerance)

	// Add confirmed vias to features layer
	for _, cv := range result.ConfirmedVias {
		tp.state.FeaturesLayer.AddConfirmedVia(cv)
	}

	// Update the underlying vias with match info (they were modified in place)
	// We need to update them in the features layer
	for _, v := range frontVias {
		tp.state.FeaturesLayer.UpdateVia(v)
	}
	for _, v := range backVias {
		tp.state.FeaturesLayer.UpdateVia(v)
	}

	// Refresh all overlays
	tp.refreshAllViaOverlays()

	tp.viaStatusLabel.SetText(fmt.Sprintf("Matched %d vias (avg err: %.1f px)", result.Matched, result.AvgError))
	tp.state.Emit(app.EventConfirmedViasChanged, nil)
}

// tryQuickMatchVia checks if a newly added via has a match on the opposite side.
// If found, creates a confirmed via immediately and returns the matched via.
// Returns nil if no match found.
func (tp *TracesPanel) tryQuickMatchVia(newVia *via.Via) *via.Via {
	// Determine opposite side
	var oppositeSide pcbimage.Side
	if newVia.Side == pcbimage.SideFront {
		oppositeSide = pcbimage.SideBack
	} else {
		oppositeSide = pcbimage.SideFront
	}

	// Get vias on opposite side
	oppositeVias := tp.state.FeaturesLayer.GetViasBySide(oppositeSide)
	if len(oppositeVias) == 0 {
		return nil
	}

	// Calculate matching tolerance
	tolerance := via.SuggestMatchTolerance(tp.state.DPI)

	// Find closest unmatched via within tolerance
	var bestMatch *via.Via
	bestDist := tolerance + 1

	for i := range oppositeVias {
		v := &oppositeVias[i]
		// Skip already matched vias
		if v.BothSidesConfirmed {
			continue
		}
		dist := newVia.Center.Distance(v.Center)
		if dist <= tolerance && dist < bestDist {
			bestDist = dist
			bestMatch = v
		}
	}

	if bestMatch == nil {
		return nil
	}

	fmt.Printf("  Quick-match: %s <-> %s (dist=%.1f px)\n", newVia.ID, bestMatch.ID, bestDist)

	// Mark both vias as matched
	newVia.MatchedViaID = bestMatch.ID
	newVia.BothSidesConfirmed = true
	bestMatch.MatchedViaID = newVia.ID
	bestMatch.BothSidesConfirmed = true

	// Update both vias in features layer
	tp.state.FeaturesLayer.UpdateVia(*newVia)
	tp.state.FeaturesLayer.UpdateVia(*bestMatch)

	// Create confirmed via
	cvNum := tp.state.FeaturesLayer.NextConfirmedViaNumber()
	cvID := fmt.Sprintf("cvia-%03d", cvNum)

	var cv *via.ConfirmedVia
	if newVia.Side == pcbimage.SideFront {
		cv = via.NewConfirmedVia(cvID, newVia, bestMatch)
	} else {
		cv = via.NewConfirmedVia(cvID, bestMatch, newVia)
	}

	tp.state.FeaturesLayer.AddConfirmedVia(cv)
	fmt.Printf("  Created confirmed via %s\n", cvID)

	return bestMatch
}

// printContactStats prints contact statistics to stdout.
func printContactStats(img image.Image, contacts []alignment.Contact, dpi float64, layerName string) {
	if len(contacts) == 0 {
		return
	}

	fmt.Printf("\n=== Contact Statistics for %s ===\n", layerName)
	fmt.Printf("%-4s %12s %12s %12s %12s %20s %20s %20s\n",
		"#", "W (px)", "H (px)", "W (in)", "H (in)", "Avg R/G/B", "StdDev R/G/B", "Aspect")

	bounds := img.Bounds()

	for i, contact := range contacts {
		b := contact.Bounds
		widthPx := b.Width
		heightPx := b.Height

		// Convert to inches if DPI is known
		widthIn := 0.0
		heightIn := 0.0
		if dpi > 0 {
			widthIn = float64(widthPx) / dpi
			heightIn = float64(heightPx) / dpi
		}

		// Sample all pixels in the contact rectangle
		var sumR, sumG, sumB float64
		var sumR2, sumG2, sumB2 float64
		var count int

		for y := b.Y; y < b.Y+b.Height && y < bounds.Max.Y; y++ {
			for x := b.X; x < b.X+b.Width && x < bounds.Max.X; x++ {
				if x < bounds.Min.X || y < bounds.Min.Y {
					continue
				}
				r, g, b, _ := img.At(x, y).RGBA()
				// Convert from 16-bit to 8-bit
				rf := float64(r >> 8)
				gf := float64(g >> 8)
				bf := float64(b >> 8)

				sumR += rf
				sumG += gf
				sumB += bf
				sumR2 += rf * rf
				sumG2 += gf * gf
				sumB2 += bf * bf
				count++
			}
		}

		if count > 0 {
			n := float64(count)
			avgR := sumR / n
			avgG := sumG / n
			avgB := sumB / n

			// Standard deviation: sqrt(E[X^2] - E[X]^2)
			stdR := math.Sqrt(sumR2/n - avgR*avgR)
			stdG := math.Sqrt(sumG2/n - avgG*avgG)
			stdB := math.Sqrt(sumB2/n - avgB*avgB)

			aspect := float64(heightPx) / float64(widthPx)

			fmt.Printf("%-4d %12d %12d %12.4f %12.4f %6.1f/%5.1f/%5.1f %6.1f/%5.1f/%5.1f %12.2f\n",
				i+1, widthPx, heightPx, widthIn, heightIn,
				avgR, avgG, avgB, stdR, stdG, stdB, aspect)
		}
	}
	fmt.Println()
}
