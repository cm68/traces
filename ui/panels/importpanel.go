package panels

import (
	"fmt"
	"image"
	"image/color"
	"path/filepath"
	"strings"

	"pcb-tracer/internal/alignment"
	"pcb-tracer/internal/app"
	"pcb-tracer/internal/board"
	pcbimage "pcb-tracer/internal/image"
	"pcb-tracer/pkg/colorutil"
	"pcb-tracer/pkg/geometry"
	"pcb-tracer/ui/canvas"
	"pcb-tracer/ui/dialogs"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

// ImportPanel handles image import, board selection, and alignment.
type ImportPanel struct {
	state     *app.State
	canvas    *canvas.ImageCanvas
	win       *gtk.Window
	sidePanel *SidePanel
	widget    *gtk.Box

	// Board selection
	boardSelect    *gtk.ComboBoxText
	boardSpecLabel *gtk.Label

	// Image info
	frontLabel *gtk.Label
	backLabel  *gtk.Label
	dpiLabel   *gtk.Label

	// Contact detection
	contactInfoLabel *gtk.Label
	layerFrontRadio  *gtk.RadioButton
	layerBackRadio   *gtk.RadioButton
	detectButton     *gtk.Button
	sampleButton     *gtk.Button
	alignButton      *gtk.Button
	alignStatus      *gtk.Label

	// Manual alignment
	offsetLabel   *gtk.Label
	rotationLabel *gtk.Label
	shearLabel    *gtk.Label
	cropLabel     *gtk.Label

	// Auto align
	autoAlignButton *gtk.Button

	// Save / Re-align
	saveAlignedBtn *gtk.Button
	realignBtn     *gtk.Button
	alignControls  *gtk.Box // Manual controls (hidden after normalization)
}

// NewImportPanel creates a new import panel.
func NewImportPanel(state *app.State, cvs *canvas.ImageCanvas, win *gtk.Window, sp *SidePanel) *ImportPanel {
	ip := &ImportPanel{
		state:     state,
		canvas:    cvs,
		win:       win,
		sidePanel: sp,
	}

	// Initialize labels
	ip.boardSpecLabel, _ = gtk.LabelNew("")
	ip.boardSpecLabel.SetLineWrap(true)
	ip.frontLabel, _ = gtk.LabelNew("No front image loaded")
	ip.backLabel, _ = gtk.LabelNew("No back image loaded")
	ip.dpiLabel, _ = gtk.LabelNew("DPI: Unknown")
	ip.contactInfoLabel, _ = gtk.LabelNew("")
	ip.contactInfoLabel.SetLineWrap(true)
	ip.alignStatus, _ = gtk.LabelNew("")
	ip.alignStatus.SetLineWrap(true)
	ip.cropLabel, _ = gtk.LabelNew("Crop: (none)")
	ip.offsetLabel, _ = gtk.LabelNew("Offset: (0, 0)")
	ip.rotationLabel, _ = gtk.LabelNew("Rotation: 0.000°")
	ip.shearLabel, _ = gtk.LabelNew("Shear: T=1.000 B=1.000 L=1.000 R=1.000")

	// Board selection
	ip.boardSelect, _ = gtk.ComboBoxTextNew()
	specNames := board.ListSpecs()
	for _, name := range specNames {
		ip.boardSelect.AppendText(name)
	}
	ip.boardSelect.Connect("changed", func() {
		selected := ip.boardSelect.GetActiveText()
		if spec := board.GetSpec(selected); spec != nil {
			state.BoardSpec = spec
			ip.updateBoardSpecInfo()
		}
	})
	if state.BoardSpec != nil {
		for i, name := range specNames {
			if name == state.BoardSpec.Name() {
				ip.boardSelect.SetActive(i)
				break
			}
		}
	}

	// Layer radio buttons
	ip.layerFrontRadio, _ = gtk.RadioButtonNewWithLabel(nil, "Front")
	ip.layerBackRadio, _ = gtk.RadioButtonNewWithLabelFromWidget(ip.layerFrontRadio, "Back")
	ip.layerFrontRadio.SetActive(true)
	ip.layerFrontRadio.Connect("toggled", func() {
		if ip.layerFrontRadio.GetActive() {
			cvs.RaiseLayerBySide(pcbimage.SideFront)
			ip.RefreshLabels()
		}
	})
	ip.layerBackRadio.Connect("toggled", func() {
		if ip.layerBackRadio.GetActive() {
			cvs.RaiseLayerBySide(pcbimage.SideBack)
			ip.RefreshLabels()
		}
	})

	// Buttons
	ip.detectButton, _ = gtk.ButtonNewWithLabel("Detect Contacts")
	ip.detectButton.Connect("clicked", func() { ip.onDetectContacts() })

	ip.sampleButton, _ = gtk.ButtonNewWithLabel("Sample Contact")
	ip.sampleButton.Connect("clicked", func() { ip.onSampleContact() })

	ip.alignButton, _ = gtk.ButtonNewWithLabel("Align Images")
	ip.alignButton.Connect("clicked", func() { ip.onAlignImages() })

	ip.autoAlignButton, _ = gtk.ButtonNewWithLabel("Auto Align")
	ip.autoAlignButton.Connect("clicked", func() { ip.onAutoAlign() })

	ip.saveAlignedBtn, _ = gtk.ButtonNewWithLabel("Save Aligned")
	ip.saveAlignedBtn.Connect("clicked", func() { ip.onSaveAligned() })

	ip.realignBtn, _ = gtk.ButtonNewWithLabel("Re-align")
	ip.realignBtn.Connect("clicked", func() { ip.onRealign() })

	// Selection callback
	cvs.OnSelect(func(x1, y1, x2, y2 float64) {
		ip.onSampleSelected(x1, y1, x2, y2)
	})

	// Compass rose for manual alignment nudge
	compassGrid, _ := gtk.GridNew()
	compassGrid.SetColumnHomogeneous(true)
	compassGrid.SetRowHomogeneous(true)
	btnN, _ := gtk.ButtonNewWithLabel("N")
	btnN.Connect("clicked", func() { ip.onNudgeAlignment(0, -1) })
	btnW, _ := gtk.ButtonNewWithLabel("W")
	btnW.Connect("clicked", func() { ip.onNudgeAlignment(-1, 0) })
	btnE, _ := gtk.ButtonNewWithLabel("E")
	btnE.Connect("clicked", func() { ip.onNudgeAlignment(1, 0) })
	btnS, _ := gtk.ButtonNewWithLabel("S")
	btnS.Connect("clicked", func() { ip.onNudgeAlignment(0, 1) })
	compassGrid.Attach(btnN, 1, 0, 1, 1)
	compassGrid.Attach(btnW, 0, 1, 1, 1)
	compassGrid.Attach(btnE, 2, 1, 1, 1)
	compassGrid.Attach(btnS, 1, 2, 1, 1)

	// Rotation buttons
	rotCCW, _ := gtk.ButtonNewWithLabel("CCW")
	rotCCW.Connect("clicked", func() { ip.onNudgeRotation(-0.01) })
	rotCW, _ := gtk.ButtonNewWithLabel("CW")
	rotCW.Connect("clicked", func() { ip.onNudgeRotation(0.01) })
	rotBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	rotBox.PackStart(rotCCW, true, true, 0)
	rotBox.PackStart(rotCW, true, true, 0)

	// Shear buttons
	shearBox, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 2)
	for _, entry := range []struct {
		label string
		edge  string
	}{
		{"Top X", "topX"},
		{"Bot X", "bottomX"},
		{"Left Y", "leftY"},
		{"Right Y", "rightY"},
	} {
		row, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
		lbl, _ := gtk.LabelNew(entry.label + ":")
		row.PackStart(lbl, false, false, 0)
		edge := entry.edge
		minus, _ := gtk.ButtonNewWithLabel("-")
		minus.Connect("clicked", func() { ip.onNudgeShear(edge, -0.001) })
		plus, _ := gtk.ButtonNewWithLabel("+")
		plus.Connect("clicked", func() { ip.onNudgeShear(edge, 0.001) })
		row.PackStart(minus, true, true, 0)
		row.PackStart(plus, true, true, 0)
		shearBox.PackStart(row, false, false, 0)
	}

	// Crop buttons
	cropPosBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	cropPosLbl, _ := gtk.LabelNew("Pos:")
	cropPosBox.PackStart(cropPosLbl, false, false, 0)
	for _, entry := range []struct {
		label string
		dim   string
		delta int
	}{
		{"←", "x", -10}, {"→", "x", 10}, {"↑", "y", -10}, {"↓", "y", 10},
	} {
		dim, delta := entry.dim, entry.delta
		btn, _ := gtk.ButtonNewWithLabel(entry.label)
		btn.Connect("clicked", func() { ip.onNudgeCrop(dim, delta) })
		cropPosBox.PackStart(btn, true, true, 0)
	}

	cropSizeBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	cropSizeLbl, _ := gtk.LabelNew("Size:")
	cropSizeBox.PackStart(cropSizeLbl, false, false, 0)
	for _, entry := range []struct {
		label string
		dim   string
		delta int
	}{
		{"W-", "w", -10}, {"W+", "w", 10}, {"H-", "h", -10}, {"H+", "h", 10},
	} {
		dim, delta := entry.dim, entry.delta
		btn, _ := gtk.ButtonNewWithLabel(entry.label)
		btn.Connect("clicked", func() { ip.onNudgeCrop(dim, delta) })
		cropSizeBox.PackStart(btn, true, true, 0)
	}

	reImportBtn, _ := gtk.ButtonNewWithLabel("Re-import with Crop")
	reImportBtn.Connect("clicked", func() { ip.onReImportWithCrop() })

	// Background mode
	bgCheckRadio, _ := gtk.RadioButtonNewWithLabel(nil, "Checkerboard")
	bgBlackRadio, _ := gtk.RadioButtonNewWithLabelFromWidget(bgCheckRadio, "Solid Black")
	bgCheckRadio.Connect("toggled", func() {
		if bgCheckRadio.GetActive() {
			ip.canvas.SetSolidBlackBackground(false)
		}
	})
	bgBlackRadio.Connect("toggled", func() {
		if bgBlackRadio.GetActive() {
			ip.canvas.SetSolidBlackBackground(true)
		}
	})
	bgBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	bgBox.PackStart(bgCheckRadio, false, false, 0)
	bgBox.PackStart(bgBlackRadio, false, false, 0)

	// Manual alignment controls container
	ip.alignControls, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4)
	ip.alignControls.SetMarginStart(4)
	ip.alignControls.SetMarginEnd(4)

	addToBox(ip.alignControls, ip.autoAlignButton)
	addToBox(ip.alignControls, ip.alignButton)
	addToBox(ip.alignControls, ip.alignStatus)
	addSep(ip.alignControls)
	addLabel(ip.alignControls, "Background:")
	addToBox(ip.alignControls, bgBox)
	addSep(ip.alignControls)
	addLabel(ip.alignControls, "Manual Adjust (use Layer above):")
	addToBox(ip.alignControls, compassGrid)
	addToBox(ip.alignControls, ip.offsetLabel)
	addSep(ip.alignControls)
	addToBox(ip.alignControls, rotBox)
	addToBox(ip.alignControls, ip.rotationLabel)
	addSep(ip.alignControls)
	addToBox(ip.alignControls, shearBox)
	addToBox(ip.alignControls, ip.shearLabel)
	addSep(ip.alignControls)
	addLabel(ip.alignControls, "Crop Bounds:")
	addToBox(ip.alignControls, cropPosBox)
	addToBox(ip.alignControls, cropSizeBox)
	addToBox(ip.alignControls, ip.cropLabel)
	addToBox(ip.alignControls, reImportBtn)
	addSep(ip.alignControls)
	addToBox(ip.alignControls, ip.saveAlignedBtn)

	// If already normalized, hide alignment controls and show Re-align
	if state.HasNormalizedImages() {
		ip.alignControls.SetVisible(false)
		ip.realignBtn.SetVisible(true)
	} else {
		ip.realignBtn.SetVisible(false)
	}

	// Layer radio box
	layerBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 8)
	layerBox.PackStart(ip.layerFrontRadio, false, false, 0)
	layerBox.PackStart(ip.layerBackRadio, false, false, 0)

	// Detect/Sample buttons row
	detectBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	detectBox.PackStart(ip.detectButton, true, true, 0)
	detectBox.PackStart(ip.sampleButton, true, true, 0)

	// Main layout
	ip.widget, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4)
	ip.widget.SetMarginStart(8)
	ip.widget.SetMarginEnd(8)
	ip.widget.SetMarginTop(4)

	editSpecBtn, _ := gtk.ButtonNewWithLabel("Edit Spec...")
	editSpecBtn.Connect("clicked", func() { ip.showBoardSpecDialog() })

	addFrame(ip.widget, "Board Type", func(box *gtk.Box) {
		addToBox(box, ip.boardSelect)
		addToBox(box, ip.boardSpecLabel)
		addToBox(box, editSpecBtn)
	})

	addFrame(ip.widget, "Images", func(box *gtk.Box) {
		addLabel(box, "Front (Component Side):")
		addToBox(box, ip.frontLabel)
		addLabel(box, "Back (Solder Side):")
		addToBox(box, ip.backLabel)
		addToBox(box, ip.dpiLabel)
	})

	addFrame(ip.widget, "Contact Detection", func(box *gtk.Box) {
		addToBox(box, ip.contactInfoLabel)
		addLabel(box, "Layer:")
		addToBox(box, layerBox)
		addToBox(box, detectBox)
	})

	addFrame(ip.widget, "Alignment", func(box *gtk.Box) {
		addToBox(box, ip.realignBtn)
		addToBox(box, ip.alignControls)
	})

	// Register for events
	state.On(app.EventImageLoaded, func(data interface{}) {
		ip.updateImageStatus()
		ip.canvas.ClearOverlay("front_contacts")
		ip.canvas.ClearOverlay("back_contacts")
		ip.canvas.ClearOverlay("front_expected")
		ip.canvas.ClearOverlay("back_expected")
		ip.canvas.ClearOverlay("front_search_area")
		ip.canvas.ClearOverlay("back_search_area")
	})

	ip.updateBoardSpecInfo()

	return ip
}

// Widget returns the panel widget for embedding in layouts.
func (ip *ImportPanel) Widget() gtk.IWidget {
	return ip.widget
}

// selectedLayer returns "Front" or "Back".
func (ip *ImportPanel) selectedLayer() string {
	if ip.layerFrontRadio.GetActive() {
		return "Front"
	}
	return "Back"
}

// syncBoardSelection updates the board combo to match state.BoardSpec.
func (ip *ImportPanel) syncBoardSelection() {
	if ip.state.BoardSpec == nil {
		return
	}
	specName := ip.state.BoardSpec.Name()
	for i, name := range board.ListSpecs() {
		if name == specName {
			ip.boardSelect.SetActive(i)
			break
		}
	}
	ip.updateBoardSpecInfo()
}

// updateAlignmentUI shows/hides alignment controls based on normalization state.
func (ip *ImportPanel) updateAlignmentUI() {
	if ip.state.HasNormalizedImages() {
		ip.alignControls.SetVisible(false)
		ip.realignBtn.SetVisible(true)
	} else {
		ip.alignControls.SetVisible(true)
		ip.realignBtn.SetVisible(false)
	}
}

// RefreshLabels updates all labels from current state.
func (ip *ImportPanel) RefreshLabels() {
	ip.updateBoardSpecInfo()
	ip.updateCropLabel()
	ip.updateRotationCenterOverlay()

	isFront := ip.selectedLayer() == "Front"
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

func (ip *ImportPanel) updateRotationCenterOverlay() {
	frontCenter := ip.state.FrontRotationCenter
	backCenter := ip.state.BackRotationCenter

	if frontCenter.X == 0 && frontCenter.Y == 0 && backCenter.X == 0 && backCenter.Y == 0 {
		ip.canvas.ClearOverlay("rotation_center")
		return
	}

	overlay := &canvas.Overlay{
		Color: colorutil.Red,
	}

	if frontCenter.X != 0 || frontCenter.Y != 0 {
		overlay.Circles = append(overlay.Circles, canvas.OverlayCircle{
			X: frontCenter.X, Y: frontCenter.Y, Radius: 5, Filled: true,
		})
	}

	if backCenter.X != 0 || backCenter.Y != 0 {
		overlay.Circles = append(overlay.Circles, canvas.OverlayCircle{
			X:      backCenter.X + float64(ip.state.BackManualOffset.X),
			Y:      backCenter.Y + float64(ip.state.BackManualOffset.Y),
			Radius: 5, Filled: true,
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
	if ip.win == nil || ip.state.BoardSpec == nil {
		return
	}

	spec, ok := ip.state.BoardSpec.(*board.BaseSpec)
	if !ok {
		fmt.Println("Board spec is not a BaseSpec, cannot edit")
		return
	}

	dlg := dialogs.NewBoardSpecDialog(spec, ip.win, func(updated *board.BaseSpec) {
		ip.state.BoardSpec = updated
		ip.updateBoardSpecInfo()
		ip.state.SetModified(true)
		fmt.Println("Board spec updated")
	})
	dlg.Show()
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

	if frontDPI > 0 && backDPI > 0 && frontDPI != backDPI {
		ip.dpiLabel.SetText(fmt.Sprintf("DPI MISMATCH: %.0f vs %.0f", frontDPI, backDPI))
		ip.state.DPI = 0
	} else if frontDPI > 0 {
		ip.state.DPI = frontDPI
		ip.dpiLabel.SetText(fmt.Sprintf("DPI: %.0f", frontDPI))
	} else if backDPI > 0 {
		ip.state.DPI = backDPI
		ip.dpiLabel.SetText(fmt.Sprintf("DPI: %.0f", backDPI))
	} else if ip.state.DPI > 0 {
		ip.dpiLabel.SetText(fmt.Sprintf("DPI: %.0f", ip.state.DPI))
	} else {
		ip.dpiLabel.SetText("DPI: Unknown")
	}
}

func (ip *ImportPanel) updateCropLabel() {
	isFront := ip.selectedLayer() == "Front"

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

// ApplyLayerSelection raises the currently selected layer to the top.
func (ip *ImportPanel) ApplyLayerSelection() {
	if ip.selectedLayer() == "Front" {
		ip.canvas.RaiseLayerBySide(pcbimage.SideFront)
	} else {
		ip.canvas.RaiseLayerBySide(pcbimage.SideBack)
	}
}

// --- Detection & Alignment ---

func (ip *ImportPanel) onDetectContacts() {
	isFront := ip.selectedLayer() == "Front"

	var img *pcbimage.Layer
	var layerName string

	if isFront {
		img = ip.state.FrontImage
		layerName = "Front"
	} else {
		img = ip.state.BackImage
		layerName = "Back"
	}

	if img == nil || img.Image == nil {
		ip.alignStatus.SetText(fmt.Sprintf("No %s image loaded", layerName))
		return
	}

	ip.alignStatus.SetText(fmt.Sprintf("Detecting contacts on %s...", layerName))
	ip.detectButton.SetSensitive(false)

	ip.canvas.ClearOverlay("front_contacts")
	ip.canvas.ClearOverlay("back_contacts")
	ip.canvas.ClearOverlay("front_expected")
	ip.canvas.ClearOverlay("back_expected")
	ip.canvas.ClearOverlay("front_search_area")
	ip.canvas.ClearOverlay("back_search_area")
	ip.canvas.ClearOverlay("front_ejectors")
	ip.canvas.ClearOverlay("back_ejectors")

	go func() {
		dpi := ip.state.DPI
		if dpi == 0 && img.DPI > 0 {
			dpi = img.DPI
		}

		var colorParams *alignment.DetectionParams
		var sampledParams *app.ColorParams
		if isFront {
			sampledParams = ip.state.FrontColorParams
		} else {
			sampledParams = ip.state.BackColorParams
		}
		if sampledParams != nil {
			colorParams = &alignment.DetectionParams{
				HueMin: sampledParams.HueMin, HueMax: sampledParams.HueMax,
				SatMin: sampledParams.SatMin, SatMax: sampledParams.SatMax,
				ValMin: sampledParams.ValMin, ValMax: sampledParams.ValMax,
			}
		}

		result, err := alignment.DetectContactsOnTopEdge(img.Image, ip.state.BoardSpec, dpi, colorParams)

		var contactCount int
		if result != nil {
			contactCount = len(result.Contacts)

			if isFront {
				ip.state.FrontDetectionResult = result
			} else {
				ip.state.BackDetectionResult = result
			}

			glib.IdleAdd(func() {
				if result.DPI > 0 && ip.state.DPI == 0 {
					ip.state.DPI = result.DPI
					ip.dpiLabel.SetText(fmt.Sprintf("DPI: %.1f", result.DPI))
				}

				// Rebuild connectors from current detection results
				// (this creates labeled features overlays that replace the old diagnostic overlays)
				ip.state.CreateConnectorsFromAlignment()

				// Ejector marks
				overlayLayer := canvas.LayerFront
				if !isFront {
					overlayLayer = canvas.LayerBack
				}
				ejectorDPI := dpi
				if ejectorDPI == 0 {
					ejectorDPI = result.DPI
				}
				if ejectorDPI > 0 {
					ejectorMarks := alignment.DetectEjectorMarksFromImage(img.Image, result.Contacts, ejectorDPI)
					if len(ejectorMarks) > 0 {
						var ejectorName string
						if isFront {
							ejectorName = "front_ejectors"
						} else {
							ejectorName = "back_ejectors"
						}
						ejectorOverlay := &canvas.Overlay{
							Color:      color.RGBA{R: 0, G: 255, B: 0, A: 255},
							Layer:      overlayLayer,
							Rectangles: make([]canvas.OverlayRect, len(ejectorMarks)),
						}
						markSize := int(0.25 * ejectorDPI)
						for i, mark := range ejectorMarks {
							ejectorOverlay.Rectangles[i] = canvas.OverlayRect{
								X: int(mark.Center.X) - markSize/2, Y: int(mark.Center.Y) - markSize/2,
								Width: markSize, Height: markSize,
								Fill: canvas.FillTarget, Label: mark.Side,
							}
						}
						ip.canvas.SetOverlay(ejectorName, ejectorOverlay)
					}
				}

				printContactStats(img.Image, result.Contacts, dpi, layerName)
			})
		}

		// Build size/aspect info
		var sizeInfo string
		if contactCount > 0 {
			minW, maxW := result.Contacts[0].Bounds.Width, result.Contacts[0].Bounds.Width
			minH, maxH := result.Contacts[0].Bounds.Height, result.Contacts[0].Bounds.Height
			minAspect := float64(maxH) / float64(maxW)
			maxAspect := minAspect
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

		glib.IdleAdd(func() {
			ip.detectButton.SetSensitive(true)
			if err != nil {
				ip.alignStatus.SetText(fmt.Sprintf("%s: %d contacts%s\n%v", layerName, contactCount, sizeInfo, err))
			} else {
				ip.alignStatus.SetText(fmt.Sprintf("%s: %d contacts%s", layerName, contactCount, sizeInfo))
			}
		})
	}()
}

func (ip *ImportPanel) onNudgeAlignment(dx, dy int) {
	isFront := ip.selectedLayer() == "Front"

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
		ip.alignStatus.SetText("No image loaded for " + ip.selectedLayer())
		return
	}

	layer.ManualOffsetX += dx
	layer.ManualOffsetY += dy
	ip.state.SetModified(true)
	ip.canvas.Refresh()
	ip.offsetLabel.SetText(fmt.Sprintf("Offset: (%d, %d)", layer.ManualOffsetX, layer.ManualOffsetY))
}

func (ip *ImportPanel) onNudgeRotation(degrees float64) {
	isFront := ip.selectedLayer() == "Front"

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
		ip.alignStatus.SetText("No image loaded for " + ip.selectedLayer())
		return
	}

	if layer.RotationCenterX == 0 && layer.RotationCenterY == 0 {
		if detectionResult != nil && len(detectionResult.Contacts) > 0 {
			minX, maxX := detectionResult.Contacts[0].Center.X, detectionResult.Contacts[0].Center.X
			for _, c := range detectionResult.Contacts {
				if c.Center.X < minX {
					minX = c.Center.X
				}
				if c.Center.X > maxX {
					maxX = c.Center.X
				}
			}
			layer.RotationCenterX = (minX + maxX) / 2
			if layer.Image != nil {
				layer.RotationCenterY = float64(layer.Image.Bounds().Dy()) / 2
			}
			if isFront {
				ip.state.FrontRotationCenter.X = layer.RotationCenterX
				ip.state.FrontRotationCenter.Y = layer.RotationCenterY
			} else {
				ip.state.BackRotationCenter.X = layer.RotationCenterX
				ip.state.BackRotationCenter.Y = layer.RotationCenterY
			}
		}
	}

	layer.ManualRotation += degrees
	ip.state.SetModified(true)
	ip.canvas.Refresh()
	ip.rotationLabel.SetText(fmt.Sprintf("Rotation: %.3f°", layer.ManualRotation))
}

func (ip *ImportPanel) onNudgeShear(edge string, delta float64) {
	isFront := ip.selectedLayer() == "Front"

	var layer *pcbimage.Layer
	if isFront {
		layer = ip.state.FrontImage
	} else {
		layer = ip.state.BackImage
	}

	if layer == nil {
		ip.alignStatus.SetText("No image loaded for " + ip.selectedLayer())
		return
	}

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
	ip.shearLabel.SetText(fmt.Sprintf("Shear: T=%.3f B=%.3f L=%.3f R=%.3f",
		layer.ShearTopX, layer.ShearBottomX, layer.ShearLeftY, layer.ShearRightY))
}

func (ip *ImportPanel) onNudgeCrop(dimension string, delta int) {
	isFront := ip.selectedLayer() == "Front"

	var crop *geometry.RectInt
	if isFront {
		crop = &ip.state.FrontCropBounds
	} else {
		crop = &ip.state.BackCropBounds
	}

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

func (ip *ImportPanel) onReImportWithCrop() {
	isFront := ip.selectedLayer() == "Front"

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
		ip.alignStatus.SetText("No image loaded for " + ip.selectedLayer())
		return
	}

	if crop.Width <= 0 || crop.Height <= 0 {
		ip.alignStatus.SetText("Invalid crop bounds")
		return
	}

	ip.alignStatus.SetText("Re-importing with new crop...")

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

func (ip *ImportPanel) onSampleContact() {
	isFront := ip.selectedLayer() == "Front"
	var img *pcbimage.Layer
	if isFront {
		img = ip.state.FrontImage
	} else {
		img = ip.state.BackImage
	}

	if img == nil || img.Image == nil {
		ip.alignStatus.SetText("No image loaded for " + ip.selectedLayer())
		return
	}

	ip.alignStatus.SetText("Draw a rectangle around a gold contact...")
	ip.canvas.EnableSelectMode()
}

func (ip *ImportPanel) onSampleSelected(x1, y1, x2, y2 float64) {
	canvasOutput := ip.canvas.GetRenderedOutput()
	if canvasOutput == nil {
		ip.alignStatus.SetText("No canvas output available")
		return
	}

	bounds := canvasOutput.Bounds()
	if bounds.Empty() {
		ip.alignStatus.SetText("Canvas is empty")
		return
	}

	ix1 := max(int(x1), bounds.Min.X)
	iy1 := max(int(y1), bounds.Min.Y)
	ix2 := min(int(x2), bounds.Max.X-1)
	iy2 := min(int(y2), bounds.Max.Y-1)

	if ix2 <= ix1 || iy2 <= iy1 {
		ip.alignStatus.SetText("Invalid selection")
		return
	}

	stats := extractHSVStats(canvasOutput, ix1, iy1, ix2, iy2)

	colorParams := &app.ColorParams{
		HueMin: max(0, stats.hueMean-2*stats.hueStd),
		HueMax: min(180, stats.hueMean+2*stats.hueStd),
		SatMin: max(0, stats.satMean-2*stats.satStd),
		SatMax: min(255, stats.satMean+2*stats.satStd),
		ValMin: max(0, stats.valMean-2*stats.valStd),
		ValMax: min(255, stats.valMean+2*stats.valStd),
	}

	if ip.selectedLayer() == "Front" {
		ip.state.FrontColorParams = colorParams
	} else {
		ip.state.BackColorParams = colorParams
	}

	ip.alignStatus.SetText(fmt.Sprintf(
		"%s sampled: H(%.0f±%.0f) S(%.0f±%.0f) V(%.0f±%.0f)",
		ip.selectedLayer(),
		stats.hueMean, stats.hueStd,
		stats.satMean, stats.satStd,
		stats.valMean, stats.valStd,
	))
}

func (ip *ImportPanel) onAutoAlign() {
	if ip.state.FrontImage == nil || ip.state.BackImage == nil {
		ip.alignStatus.SetText("Need both front and back images")
		return
	}

	ip.alignStatus.SetText("Auto-aligning: detecting contacts...")
	ip.autoAlignButton.SetSensitive(false)
	ip.alignButton.SetSensitive(false)

	go func() {
		dpi := ip.state.DPI
		if dpi == 0 && ip.state.FrontImage.DPI > 0 {
			dpi = ip.state.FrontImage.DPI
		}

		frontResult, err := alignment.DetectContactsOnTopEdge(ip.state.FrontImage.Image, ip.state.BoardSpec, dpi, nil)
		if err != nil || frontResult == nil || len(frontResult.Contacts) < 10 {
			glib.IdleAdd(func() {
				ip.alignStatus.SetText("Auto-align failed: not enough front contacts")
				ip.autoAlignButton.SetSensitive(true)
				ip.alignButton.SetSensitive(true)
			})
			return
		}
		ip.state.FrontDetectionResult = frontResult

		backResult, err := alignment.DetectContactsOnTopEdge(ip.state.BackImage.Image, ip.state.BoardSpec, dpi, nil)
		if err != nil || backResult == nil || len(backResult.Contacts) < 10 {
			glib.IdleAdd(func() {
				ip.alignStatus.SetText("Auto-align failed: not enough back contacts")
				ip.autoAlignButton.SetSensitive(true)
				ip.alignButton.SetSensitive(true)
			})
			return
		}
		ip.state.BackDetectionResult = backResult

		frontContacts := frontResult.Contacts
		backContacts := backResult.Contacts

		var frontSumX, frontSumY, backSumX, backSumY float64
		minC := min(len(frontContacts), len(backContacts))
		for i := 0; i < minC; i++ {
			frontSumX += frontContacts[i].Center.X
			frontSumY += frontContacts[i].Center.Y
			backSumX += backContacts[i].Center.X
			backSumY += backContacts[i].Center.Y
		}

		frontAvgX := frontSumX / float64(minC)
		frontAvgY := frontSumY / float64(minC)
		backAvgX := backSumX / float64(minC)
		backAvgY := backSumY / float64(minC)

		offsetX := int(frontAvgX - backAvgX)
		offsetY := int(frontAvgY - backAvgY)

		frontAngle := frontResult.ContactAngle
		backAngle := backResult.ContactAngle

		ip.state.FrontManualRotation = -frontAngle
		ip.state.FrontRotationCenter = geometry.Point2D{X: frontAvgX, Y: frontAvgY}
		ip.state.FrontImage.ManualRotation = -frontAngle
		ip.state.FrontImage.RotationCenterX = frontAvgX
		ip.state.FrontImage.RotationCenterY = frontAvgY

		ip.state.BackManualRotation = -backAngle
		ip.state.BackRotationCenter = geometry.Point2D{X: backAvgX, Y: backAvgY}
		ip.state.BackImage.ManualRotation = -backAngle
		ip.state.BackImage.RotationCenterX = backAvgX
		ip.state.BackImage.RotationCenterY = backAvgY

		ip.state.BackManualOffset.X = offsetX
		ip.state.BackManualOffset.Y = offsetY
		ip.state.BackImage.ManualOffsetX = offsetX
		ip.state.BackImage.ManualOffsetY = offsetY

		// Reset shear
		for _, v := range []*float64{
			&ip.state.FrontShearTopX, &ip.state.FrontShearBottomX,
			&ip.state.FrontShearLeftY, &ip.state.FrontShearRightY,
			&ip.state.BackShearTopX, &ip.state.BackShearBottomX,
			&ip.state.BackShearLeftY, &ip.state.BackShearRightY,
		} {
			*v = 1.0
		}
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

		glib.IdleAdd(func() {
			ip.canvas.ClearOverlay("front_contacts")
			ip.canvas.ClearOverlay("back_contacts")
			ip.canvas.ClearOverlay("front_expected")
			ip.canvas.ClearOverlay("back_expected")
			ip.canvas.ClearOverlay("front_search_area")
			ip.canvas.ClearOverlay("back_search_area")
			ip.canvas.ClearOverlay("front_ejectors")
			ip.canvas.ClearOverlay("back_ejectors")

			ip.alignStatus.SetText(fmt.Sprintf("Aligned: front rot=%.3f°, back rot=%.3f°, offset=(%d,%d)",
				-frontAngle, -backAngle, offsetX, offsetY))
			ip.autoAlignButton.SetSensitive(true)
			ip.alignButton.SetSensitive(true)
			ip.RefreshLabels()
			ip.canvas.Refresh()
		})
	}()
}

func (ip *ImportPanel) onAlignImages() {
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
	ip.alignButton.SetSensitive(false)

	go func() {
		dpi := ip.state.DPI
		if dpi == 0 {
			dpi = ip.state.FrontDetectionResult.DPI
		}

		var frontSumX, frontSumY, backSumX, backSumY float64
		minC := min(len(frontContacts), len(backContacts))
		for i := 0; i < minC; i++ {
			frontSumX += frontContacts[i].Center.X
			frontSumY += frontContacts[i].Center.Y
			backSumX += backContacts[i].Center.X
			backSumY += backContacts[i].Center.Y
		}

		frontAvgX := frontSumX / float64(minC)
		frontAvgY := frontSumY / float64(minC)
		backAvgX := backSumX / float64(minC)
		backAvgY := backSumY / float64(minC)

		deltaX := frontAvgX - backAvgX
		deltaY := frontAvgY - backAvgY

		translatedBack := translateImage(ip.state.BackImage.Image, int(deltaX), int(deltaY))

		frontMarks := alignment.DetectEjectorMarksFromImage(ip.state.FrontImage.Image, frontContacts, dpi)

		translatedBackContacts := make([]alignment.Contact, len(backContacts))
		for i, c := range backContacts {
			translatedBackContacts[i] = c
			translatedBackContacts[i].Center.X += deltaX
			translatedBackContacts[i].Center.Y += deltaY
		}
		backMarks := alignment.DetectEjectorMarksFromImage(translatedBack, translatedBackContacts, dpi)

		var finalImage image.Image = translatedBack
		var alignInfo string

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
			} else {
				alignInfo = fmt.Sprintf("translated (%.1f, %.1f) px (missing ejector marks)", deltaX, deltaY)
			}
		} else {
			alignInfo = fmt.Sprintf("translated (%.1f, %.1f) px (no ejector marks)", deltaX, deltaY)
		}

		ip.state.BackImage.Image = finalImage
		ip.state.Aligned = true

		alignedBackContacts := make([]alignment.Contact, len(backContacts))
		for i, c := range backContacts {
			alignedBackContacts[i] = alignment.Contact{
				Bounds: geometry.RectInt{
					X: c.Bounds.X + int(deltaX), Y: c.Bounds.Y + int(deltaY),
					Width: c.Bounds.Width, Height: c.Bounds.Height,
				},
				Center: geometry.Point2D{X: c.Center.X + deltaX, Y: c.Center.Y + deltaY},
				Pass:   c.Pass,
			}
		}
		ip.state.BackDetectionResult.Contacts = alignedBackContacts

		glib.IdleAdd(func() {
			ip.createContactOverlay("back_contacts", alignedBackContacts, color.RGBA{R: 0, G: 0, B: 255, A: 255}, canvas.LayerBack)

			ip.alignButton.SetSensitive(true)
			ip.alignStatus.SetText("Aligned: " + alignInfo)

			ip.canvas.ClearOverlay("front_contacts")
			ip.canvas.ClearOverlay("back_contacts")
			ip.canvas.ClearOverlay("front_expected")
			ip.canvas.ClearOverlay("back_expected")
			ip.canvas.ClearOverlay("front_search_area")
			ip.canvas.ClearOverlay("back_search_area")
			ip.canvas.ClearOverlay("front_ejectors")
			ip.canvas.ClearOverlay("back_ejectors")

			ip.canvas.Refresh()
			ip.state.Emit(app.EventAlignmentComplete, nil)
		})
	}()
}

func (ip *ImportPanel) onSaveAligned() {
	if ip.state.ProjectPath == "" {
		dlg := gtk.MessageDialogNew(ip.win, gtk.DIALOG_MODAL, gtk.MESSAGE_INFO, gtk.BUTTONS_OK,
			"Please save the project before saving aligned images.")
		dlg.SetTitle("Save Project First")
		dlg.Run()
		dlg.Destroy()
		return
	}

	if ip.state.FrontImage == nil && ip.state.BackImage == nil {
		dlg := gtk.MessageDialogNew(ip.win, gtk.DIALOG_MODAL, gtk.MESSAGE_INFO, gtk.BUTTONS_OK,
			"Load at least one image before saving alignment.")
		dlg.SetTitle("No Images")
		dlg.Run()
		dlg.Destroy()
		return
	}

	projectDir := filepath.Dir(ip.state.ProjectPath)

	ip.saveAlignedBtn.SetSensitive(false)
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

		if err := ip.state.SaveProject(ip.state.ProjectPath); err != nil {
			errs = append(errs, "Save: "+err.Error())
		}

		glib.IdleAdd(func() {
			ip.saveAlignedBtn.SetSensitive(true)
			if len(errs) > 0 {
				ip.alignStatus.SetText("Errors: " + strings.Join(errs, "; "))
				return
			}

			ip.alignStatus.SetText("Aligned images saved")
			ip.updateAlignmentUI()
			if ip.sidePanel != nil {
				ip.sidePanel.updatePanelEnableState()
			}

			ip.state.Emit(app.EventNormalizationComplete, nil)
			ip.state.Emit(app.EventComponentsChanged, nil)

			if ip.sidePanel != nil {
				ip.sidePanel.SyncLayers()
			}
			ip.canvas.Refresh()
			ip.RefreshLabels()
		})
	}()
}

func (ip *ImportPanel) onRealign() {
	if ip.state.FrontImage == nil && ip.state.BackImage == nil {
		return
	}

	ip.alignStatus.SetText("Reloading raw images...")

	go func() {
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

		ip.state.FrontNormalizedPath = ""
		ip.state.BackNormalizedPath = ""

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

		glib.IdleAdd(func() {
			ip.updateAlignmentUI()
			if ip.sidePanel != nil {
				ip.sidePanel.updatePanelEnableState()
			}
			ip.alignStatus.SetText("Re-alignment mode - adjust and Save Aligned")
			if ip.sidePanel != nil {
				ip.sidePanel.SyncLayers()
			}
			ip.canvas.Refresh()
			ip.RefreshLabels()
		})
	}()
}

func (ip *ImportPanel) createContactOverlay(name string, contacts []alignment.Contact, c color.RGBA, layer canvas.LayerRef) {
	overlay := &canvas.Overlay{
		Color:      c,
		Layer:      layer,
		Rectangles: make([]canvas.OverlayRect, len(contacts)),
	}
	for i, contact := range contacts {
		overlay.Rectangles[i] = canvas.OverlayRect{
			X: contact.Bounds.X, Y: contact.Bounds.Y,
			Width: contact.Bounds.Width, Height: contact.Bounds.Height,
			Fill: canvas.FillSolid,
		}
	}
	ip.canvas.SetOverlay(name, overlay)
}

func (ip *ImportPanel) createEjectorOverlay(name string, marks []alignment.EjectorMark, c color.RGBA, layer canvas.LayerRef) {
	overlay := &canvas.Overlay{
		Color:      c,
		Layer:      layer,
		Rectangles: make([]canvas.OverlayRect, len(marks)),
	}
	markerSize := 40
	for i, mark := range marks {
		overlay.Rectangles[i] = canvas.OverlayRect{
			X: int(mark.Center.X) - markerSize/2, Y: int(mark.Center.Y) - markerSize/2,
			Width: markerSize, Height: markerSize,
			Label: mark.Side, Fill: canvas.FillTarget,
		}
	}
	ip.canvas.SetOverlay(name, overlay)
}

// AutoDetectAndAlign runs full detection and alignment automatically.
func (ip *ImportPanel) AutoDetectAndAlign() {
	if ip.state.FrontImage == nil || ip.state.BackImage == nil {
		return
	}

	fmt.Println("Auto-detect: starting detection and alignment...")

	dpi := ip.state.DPI
	if dpi == 0 && ip.state.FrontImage.DPI > 0 {
		dpi = ip.state.FrontImage.DPI
	}

	fmt.Println("Auto-detect: detecting front contacts...")
	frontResult, frontErr := alignment.DetectContactsOnTopEdge(ip.state.FrontImage.Image, ip.state.BoardSpec, dpi, nil)
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

	fmt.Println("Auto-detect: detecting back contacts...")
	backResult, backErr := alignment.DetectContactsOnTopEdge(ip.state.BackImage.Image, ip.state.BoardSpec, dpi, nil)
	if backErr != nil {
		fmt.Printf("Auto-detect: back detection error: %v\n", backErr)
	}
	if backResult != nil {
		ip.state.BackDetectionResult = backResult
		fmt.Printf("Auto-detect: found %d back contacts\n", len(backResult.Contacts))
	}

	frontCount := 0
	backCount := 0
	if frontResult != nil {
		frontCount = len(frontResult.Contacts)
	}
	if backResult != nil {
		backCount = len(backResult.Contacts)
	}
	ip.contactInfoLabel.SetText(fmt.Sprintf("Front: %d, Back: %d contacts", frontCount, backCount))

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

func (ip *ImportPanel) performAlignment(frontContacts, backContacts []alignment.Contact, dpi float64) {
	var frontSumX, frontSumY, backSumX, backSumY float64
	minC := min(len(frontContacts), len(backContacts))

	for i := 0; i < minC; i++ {
		frontSumX += frontContacts[i].Center.X
		frontSumY += frontContacts[i].Center.Y
		backSumX += backContacts[i].Center.X
		backSumY += backContacts[i].Center.Y
	}

	frontAvgX := frontSumX / float64(minC)
	frontAvgY := frontSumY / float64(minC)
	backAvgX := backSumX / float64(minC)
	backAvgY := backSumY / float64(minC)

	deltaX := frontAvgX - backAvgX
	deltaY := frontAvgY - backAvgY

	translatedBack := translateImage(ip.state.BackImage.Image, int(deltaX), int(deltaY))

	frontMarks := alignment.DetectEjectorMarksFromImage(ip.state.FrontImage.Image, frontContacts, dpi)

	translatedBackContacts := make([]alignment.Contact, len(backContacts))
	for i, c := range backContacts {
		translatedBackContacts[i] = c
		translatedBackContacts[i].Center.X += deltaX
		translatedBackContacts[i].Center.Y += deltaY
	}
	backMarks := alignment.DetectEjectorMarksFromImage(translatedBack, translatedBackContacts, dpi)

	fmt.Printf("Auto-align: front ejector marks=%d, back ejector marks=%d\n", len(frontMarks), len(backMarks))

	ip.createEjectorOverlay("front_ejectors", frontMarks, color.RGBA{R: 255, G: 255, B: 0, A: 255}, canvas.LayerFront)
	ip.createEjectorOverlay("back_ejectors", backMarks, colorutil.Cyan, canvas.LayerBack)

	var finalImage image.Image = translatedBack
	var alignInfo string

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

	ip.state.BackImage.Image = finalImage
	ip.state.Aligned = true

	alignedBackContacts := make([]alignment.Contact, len(backContacts))
	for i, c := range backContacts {
		alignedBackContacts[i] = alignment.Contact{
			Bounds: geometry.RectInt{
				X: c.Bounds.X + int(deltaX), Y: c.Bounds.Y + int(deltaY),
				Width: c.Bounds.Width, Height: c.Bounds.Height,
			},
			Center: geometry.Point2D{X: c.Center.X + deltaX, Y: c.Center.Y + deltaY},
			Pass:   c.Pass,
		}
	}
	ip.state.BackDetectionResult.Contacts = alignedBackContacts
	ip.createContactOverlay("back_contacts", alignedBackContacts, color.RGBA{R: 0, G: 0, B: 255, A: 255}, canvas.LayerBack)

	ip.alignStatus.SetText("Aligned: " + alignInfo)

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

// --- Layout helpers ---

func addToBox(box *gtk.Box, w gtk.IWidget) {
	box.PackStart(w, false, false, 0)
}

func addLabel(box *gtk.Box, text string) {
	lbl, _ := gtk.LabelNew(text)
	lbl.SetHAlign(gtk.ALIGN_START)
	box.PackStart(lbl, false, false, 0)
}

func addSep(box *gtk.Box) {
	sep, _ := gtk.SeparatorNew(gtk.ORIENTATION_HORIZONTAL)
	box.PackStart(sep, false, false, 4)
}

func addFrame(parent *gtk.Box, title string, build func(box *gtk.Box)) {
	frame, _ := gtk.FrameNew(title)
	box, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4)
	box.SetMarginStart(6)
	box.SetMarginEnd(6)
	box.SetMarginTop(4)
	box.SetMarginBottom(4)
	build(box)
	frame.Add(box)
	parent.PackStart(frame, false, false, 4)
}
