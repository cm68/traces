// Package panels provides UI panels for the application.
package panels

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"pcb-tracer/internal/alignment"
	"pcb-tracer/internal/app"
	"pcb-tracer/internal/board"
	pcbimage "pcb-tracer/internal/image"
	"pcb-tracer/internal/via"
	"pcb-tracer/pkg/colorutil"
	"pcb-tracer/pkg/geometry"
	"pcb-tracer/ui/canvas"
	"pcb-tracer/ui/dialogs"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// SidePanel provides the main side panel with tabbed sections.
type SidePanel struct {
	state     *app.State
	canvas    *canvas.ImageCanvas
	container *container.AppTabs

	// Tab content
	importPanel    *ImportPanel
	layersPanel    *LayersPanel
	componentsPanel *ComponentsPanel
	tracesPanel    *TracesPanel
}

// NewSidePanel creates a new side panel.
func NewSidePanel(state *app.State, canvas *canvas.ImageCanvas) *SidePanel {
	sp := &SidePanel{
		state:  state,
		canvas: canvas,
	}

	// Create individual panels
	sp.importPanel = NewImportPanel(state, canvas)
	sp.layersPanel = NewLayersPanel(state, canvas)
	sp.componentsPanel = NewComponentsPanel(state)
	sp.tracesPanel = NewTracesPanel(state, canvas)

	// Create tabbed container
	sp.container = container.NewAppTabs(
		container.NewTabItem("Import", sp.importPanel.Container()),
		container.NewTabItem("Layers", sp.layersPanel.Container()),
		container.NewTabItem("Components", sp.componentsPanel.Container()),
		container.NewTabItem("Traces", sp.tracesPanel.Container()),
	)

	return sp
}

// Container returns the panel container.
func (sp *SidePanel) Container() fyne.CanvasObject {
	return sp.container
}

// SyncLayers updates the canvas with layers from state.
func (sp *SidePanel) SyncLayers() {
	sp.layersPanel.SyncLayers()
	sp.importPanel.ApplyLayerSelection()
}

// SetWindow sets the parent window for dialogs.
func (sp *SidePanel) SetWindow(w fyne.Window) {
	sp.importPanel.SetWindow(w)
}

// AutoDetectAndAlign runs automatic contact detection on both images and aligns them.
// This is called on startup after restoring images from preferences.
func (sp *SidePanel) AutoDetectAndAlign() {
	sp.importPanel.AutoDetectAndAlign()
}

// ImportPanel handles image import and board selection.
type ImportPanel struct {
	state     *app.State
	canvas    *canvas.ImageCanvas
	window    fyne.Window
	container fyne.CanvasObject

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

	// Layout
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
			ip.alignButton,
			ip.alignStatus,
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
					Label:          fmt.Sprintf("%d", i+1),
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
						Label:  fmt.Sprintf("%d", i+1),
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
				Label: fmt.Sprintf("%d", i+1),
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

		// Create front overlay
		ip.createContactOverlay("front_contacts", frontResult.Contacts,
			color.RGBA{R: 255, G: 0, B: 0, A: 255})
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

		// Create back overlay
		ip.createContactOverlay("back_contacts", backResult.Contacts,
			color.RGBA{R: 0, G: 0, B: 255, A: 255})
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
			Label: fmt.Sprintf("%d", i+1),
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

// LayersPanel controls layer visibility and blending.
type LayersPanel struct {
	state     *app.State
	canvas    *canvas.ImageCanvas
	container fyne.CanvasObject

	frontCheck   *widget.Check
	backCheck    *widget.Check
	frontOpacity *widget.Slider
	backOpacity  *widget.Slider
}

// NewLayersPanel creates a new layers panel.
func NewLayersPanel(state *app.State, canvas *canvas.ImageCanvas) *LayersPanel {
	lp := &LayersPanel{
		state:  state,
		canvas: canvas,
	}

	// Visibility checkboxes
	lp.frontCheck = widget.NewCheck("Show Front Layer", func(checked bool) {
		if state.FrontImage != nil {
			state.FrontImage.Visible = checked
			lp.syncLayersToCanvas()
		}
	})
	lp.frontCheck.SetChecked(true)

	lp.backCheck = widget.NewCheck("Show Back Layer", func(checked bool) {
		if state.BackImage != nil {
			state.BackImage.Visible = checked
			lp.syncLayersToCanvas()
		}
	})
	lp.backCheck.SetChecked(true)

	// Opacity sliders
	lp.frontOpacity = widget.NewSlider(0, 100)
	lp.frontOpacity.SetValue(100)
	lp.frontOpacity.OnChanged = func(val float64) {
		if state.FrontImage != nil {
			state.FrontImage.Opacity = val / 100.0
			lp.syncLayersToCanvas()
		}
	}

	lp.backOpacity = widget.NewSlider(0, 100)
	lp.backOpacity.SetValue(100)
	lp.backOpacity.OnChanged = func(val float64) {
		if state.BackImage != nil {
			state.BackImage.Opacity = val / 100.0
			lp.syncLayersToCanvas()
		}
	}

	// Layout
	lp.container = container.NewVBox(
		widget.NewCard("Front Layer", "", container.NewVBox(
			lp.frontCheck,
			widget.NewLabel("Opacity:"),
			lp.frontOpacity,
		)),
		widget.NewCard("Back Layer", "", container.NewVBox(
			lp.backCheck,
			widget.NewLabel("Opacity:"),
			lp.backOpacity,
		)),
	)

	return lp
}

// Container returns the panel container.
func (lp *LayersPanel) Container() fyne.CanvasObject {
	return lp.container
}

// syncLayersToCanvas updates the canvas with the current state layers.
func (lp *LayersPanel) syncLayersToCanvas() {
	var layers []*pcbimage.Layer
	if lp.state.FrontImage != nil {
		layers = append(layers, lp.state.FrontImage)
	}
	if lp.state.BackImage != nil {
		layers = append(layers, lp.state.BackImage)
	}
	lp.canvas.SetLayers(layers)

	// Set board bounds overlays
	if lp.state.FrontBoardBounds != nil {
		bounds := lp.state.FrontBoardBounds
		lp.canvas.SetOverlay("front_board_bounds", &canvas.Overlay{
			Rectangles: []canvas.OverlayRect{{
				X: bounds.X, Y: bounds.Y, Width: bounds.Width, Height: bounds.Height,
			}},
			Color: color.RGBA{R: 0, G: 255, B: 0, A: 128}, // Green
		})
	}
	if lp.state.BackBoardBounds != nil {
		bounds := lp.state.BackBoardBounds
		lp.canvas.SetOverlay("back_board_bounds", &canvas.Overlay{
			Rectangles: []canvas.OverlayRect{{
				X: bounds.X, Y: bounds.Y, Width: bounds.Width, Height: bounds.Height,
			}},
			Color: color.RGBA{R: 0, G: 0, B: 255, A: 128}, // Blue
		})
	}
}

// SyncLayers is called externally to refresh layers from state.
func (lp *LayersPanel) SyncLayers() {
	lp.syncLayersToCanvas()
}

// ComponentsPanel displays and manages detected components.
type ComponentsPanel struct {
	state     *app.State
	container fyne.CanvasObject

	list       *widget.List
	detailCard *widget.Card
}

// NewComponentsPanel creates a new components panel.
func NewComponentsPanel(state *app.State) *ComponentsPanel {
	cp := &ComponentsPanel{
		state: state,
	}

	// Component list
	cp.list = widget.NewList(
		func() int {
			return len(state.Components)
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("Component")
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id < len(state.Components) {
				comp := state.Components[id]
				obj.(*widget.Label).SetText(fmt.Sprintf("%s - %s", comp.ID, comp.PartNumber))
			}
		},
	)

	cp.list.OnSelected = func(id widget.ListItemID) {
		if id < len(state.Components) {
			cp.showComponentDetail(state.Components[id])
		}
	}

	// Detail card (empty initially)
	cp.detailCard = widget.NewCard("Component Details", "", widget.NewLabel("Select a component"))

	// Buttons
	detectBtn := widget.NewButton("Detect Components", func() {
		// TODO: Implement component detection
	})

	ocrBtn := widget.NewButton("Run OCR", func() {
		// TODO: Implement OCR
	})

	// Layout
	cp.container = container.NewBorder(
		container.NewVBox(detectBtn, ocrBtn),
		cp.detailCard,
		nil, nil,
		cp.list,
	)

	return cp
}

// Container returns the panel container.
func (cp *ComponentsPanel) Container() fyne.CanvasObject {
	return cp.container
}

func (cp *ComponentsPanel) showComponentDetail(comp interface{}) {
	// TODO: Show component details
}

// TracesPanel displays and manages detected vias and traces.
type TracesPanel struct {
	state     *app.State
	canvas    *canvas.ImageCanvas
	container fyne.CanvasObject

	// Via detection UI
	viaLayerSelect    *widget.RadioGroup
	detectViasBtn     *widget.Button
	clearViasBtn      *widget.Button
	viaStatusLabel    *widget.Label
	viaCountLabel     *widget.Label
	trainingLabel     *widget.Label

	// Trace detection UI
	detectTracesBtn   *widget.Button
	traceStatusLabel  *widget.Label

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

	tp.viaStatusLabel = widget.NewLabel("")
	tp.viaStatusLabel.Wrapping = fyne.TextWrapWord

	tp.viaCountLabel = widget.NewLabel("No vias detected")

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

	// Layout
	tp.container = container.NewVBox(
		widget.NewCard("Via Detection", "", container.NewVBox(
			widget.NewLabel("Layer:"),
			tp.viaLayerSelect,
			container.NewHBox(tp.detectViasBtn, tp.clearViasBtn),
			tp.viaStatusLabel,
			tp.viaCountLabel,
		)),
		widget.NewCard("Training Data", "", container.NewVBox(
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
		tp.createViaOverlay(result.Vias, side)

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
func (tp *TracesPanel) createViaOverlay(vias []via.Via, side pcbimage.Side) {
	fmt.Printf("  createViaOverlay: %d vias for side=%v\n", len(vias), side)
	var overlayName string
	var overlayColor color.RGBA

	if side == pcbimage.SideFront {
		overlayName = "front_vias"
		overlayColor = colorutil.Cyan
	} else {
		overlayName = "back_vias"
		overlayColor = colorutil.Magenta
	}

	overlay := &canvas.Overlay{
		Color:      overlayColor,
		Rectangles: make([]canvas.OverlayRect, 0),
		Polygons:   make([]canvas.OverlayPolygon, 0),
	}

	for _, v := range vias {
		// Extract via number from ID for label
		// Handle both "via-001" (manual) and "via-f-001" (detected) formats
		label := v.ID
		var viaNum int
		if _, err := fmt.Sscanf(v.ID, "via-%d", &viaNum); err == nil {
			label = fmt.Sprintf("%d", viaNum)
		} else {
			// Try format with side letter: "via-f-001" or "via-b-001"
			var side string
			if _, err := fmt.Sscanf(v.ID, "via-%1s-%d", &side, &viaNum); err == nil {
				label = fmt.Sprintf("%d", viaNum)
			}
		}

		// Use polygon if we have boundary points, otherwise use rectangle
		if len(v.PadBoundary) >= 3 {
			fmt.Printf("    Via %s: POLYGON with %d points\n", v.ID, len(v.PadBoundary))
			// Print first few boundary points for debugging
			for i := 0; i < len(v.PadBoundary) && i < 3; i++ {
				p := v.PadBoundary[i]
				fmt.Printf("      pt[%d]: (%.1f, %.1f)\n", i, p.X, p.Y)
			}
			if len(v.PadBoundary) > 3 {
				fmt.Printf("      ... and %d more points\n", len(v.PadBoundary)-3)
			}
			overlay.Polygons = append(overlay.Polygons, canvas.OverlayPolygon{
				Points: v.PadBoundary,
				Label:  label,
				Filled: true,
			})
		} else {
			// Fall back to rectangle for circular vias without boundary
			bounds := v.Bounds()
			fmt.Printf("    Via %s: RECT at (%d,%d) %dx%d\n", v.ID, bounds.X, bounds.Y, bounds.Width, bounds.Height)

			// Determine fill based on detection method
			var fill canvas.FillPattern
			switch v.Method {
			case via.MethodManual:
				fill = canvas.FillTarget // Crosshair for manual vias
			case via.MethodHoughCircle:
				fill = canvas.FillSolid
			default:
				fill = canvas.FillStripe
			}

			overlay.Rectangles = append(overlay.Rectangles, canvas.OverlayRect{
				X:      bounds.X,
				Y:      bounds.Y,
				Width:  bounds.Width,
				Height: bounds.Height,
				Fill:   fill,
				Label:  label,
			})
		}
	}

	fmt.Printf("  Setting overlay '%s': %d rects, %d polygons\n", overlayName, len(overlay.Rectangles), len(overlay.Polygons))
	tp.canvas.SetOverlay(overlayName, overlay)
}

// onClearVias clears all detected vias.
func (tp *TracesPanel) onClearVias() {
	tp.state.FeaturesLayer.ClearVias()
	tp.canvas.ClearOverlay("front_vias")
	tp.canvas.ClearOverlay("back_vias")
	tp.viaCountLabel.SetText("No vias detected")
	tp.viaStatusLabel.SetText("Cleared")
	tp.state.Emit(app.EventFeaturesChanged, nil)
}

// Container returns the panel container.
func (tp *TracesPanel) Container() fyne.CanvasObject {
	return tp.container
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
func (tp *TracesPanel) onLeftClickVia(x, y float64) {
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

	// Add to features layer
	tp.state.FeaturesLayer.AddVia(newVia)

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

	// Update counts
	front, back := tp.state.FeaturesLayer.ViaCountBySide()
	tp.viaCountLabel.SetText(fmt.Sprintf("Vias: %d front, %d back", front, back))

	tp.viaStatusLabel.SetText(fmt.Sprintf("Added %s (r=%.0f)", newVia.ID, boundary.Radius))
	tp.state.Emit(app.EventFeaturesChanged, nil)
	fmt.Printf("  NEW VIA COMPLETE\n")
}

// onRightClickVia handles right-click to remove a via at the clicked location.
func (tp *TracesPanel) onRightClickVia(x, y float64) {
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

// refreshViaOverlay recreates the via overlay for the specified side.
func (tp *TracesPanel) refreshViaOverlay(side pcbimage.Side) {
	vias := tp.state.FeaturesLayer.GetViasBySide(side)
	tp.createViaOverlay(vias, side)
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
