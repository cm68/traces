// Package dialogs provides application dialogs.
package dialogs

import (
	"fmt"
	"image"
	goimage "image"
	"regexp"
	"strings"

	"pcb-tracer/internal/component"
	"pcb-tracer/internal/ocr"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"gocv.io/x/gocv"
)

// ComponentEditDialog provides a dialog for editing component properties.
type ComponentEditDialog struct {
	comp   *component.Component
	window fyne.Window
	img    goimage.Image // Image for OCR (the rendered canvas output)

	// Form entries
	idEntry           *widget.Entry
	partNumberEntry   *widget.Entry
	packageEntry      *widget.Entry
	manufacturerEntry *widget.Entry
	placeEntry        *widget.Entry
	dateCodeEntry     *widget.Entry
	revisionEntry     *widget.Entry
	speedGradeEntry   *widget.Entry
	descriptionEntry  *widget.Entry
	ocrTextEntry      *widget.Entry      // Raw OCR result from detection
	correctedTextEntry *widget.Entry     // User-corrected text for training

	// OCR orientation selector (N/S/E/W - indicates where text bottom is)
	ocrOrientation *widget.RadioGroup

	// OCR training
	learnedParams   *ocr.LearnedParams
	onParamsUpdated func(*ocr.LearnedParams) // Callback when params are updated

	// Callbacks
	onSave   func(*component.Component)
	onDelete func(*component.Component)
}

// NewComponentEditDialog creates a new component edit dialog.
func NewComponentEditDialog(comp *component.Component, window fyne.Window, img goimage.Image,
	onSave func(*component.Component), onDelete func(*component.Component)) *ComponentEditDialog {
	return &ComponentEditDialog{
		comp:     comp,
		window:   window,
		img:      img,
		onSave:   onSave,
		onDelete: onDelete,
	}
}

// SetOCRTraining sets the OCR training params and callback for parameter updates.
func (d *ComponentEditDialog) SetOCRTraining(params *ocr.LearnedParams, onUpdate func(*ocr.LearnedParams)) {
	d.learnedParams = params
	d.onParamsUpdated = onUpdate
}

// Show displays the dialog.
func (d *ComponentEditDialog) Show() {
	content := d.createContent()

	// Create custom dialog with three buttons
	dlg := dialog.NewCustomWithoutButtons(
		"Edit Component: "+d.comp.ID,
		content,
		d.window,
	)

	// Create buttons
	saveBtn := widget.NewButton("Save", func() {
		d.applyChanges()
		if d.onSave != nil {
			d.onSave(d.comp)
		}
		dlg.Hide()
	})
	saveBtn.Importance = widget.HighImportance

	cancelBtn := widget.NewButton("Cancel", func() {
		dlg.Hide()
	})

	deleteBtn := widget.NewButton("Delete", func() {
		dialog.ShowConfirm("Delete Component",
			fmt.Sprintf("Delete component %s?", d.comp.ID),
			func(confirmed bool) {
				if confirmed {
					if d.onDelete != nil {
						d.onDelete(d.comp)
					}
					dlg.Hide()
				}
			}, d.window)
	})
	deleteBtn.Importance = widget.DangerImportance

	// Layout buttons
	buttons := container.NewHBox(
		deleteBtn,
		container.NewHBox(), // spacer
		cancelBtn,
		saveBtn,
	)

	// Wrap content with buttons at bottom
	fullContent := container.NewBorder(nil, buttons, nil, nil, content)

	dlg = dialog.NewCustomWithoutButtons(
		"Edit Component: "+d.comp.ID,
		fullContent,
		d.window,
	)
	dlg.Resize(fyne.NewSize(500, 700))
	dlg.Show()
}

func (d *ComponentEditDialog) createContent() fyne.CanvasObject {
	// Create form entries
	d.idEntry = widget.NewEntry()
	d.idEntry.SetText(d.comp.ID)

	d.partNumberEntry = widget.NewEntry()
	d.partNumberEntry.SetText(d.comp.PartNumber)
	d.partNumberEntry.SetPlaceHolder("e.g., 74LS244")

	d.packageEntry = widget.NewEntry()
	d.packageEntry.SetText(d.comp.Package)
	d.packageEntry.SetPlaceHolder("e.g., DIP-20")

	d.manufacturerEntry = widget.NewEntry()
	d.manufacturerEntry.SetText(d.comp.Manufacturer)
	d.manufacturerEntry.SetPlaceHolder("e.g., Texas Instruments")

	d.placeEntry = widget.NewEntry()
	d.placeEntry.SetText(d.comp.Place)
	d.placeEntry.SetPlaceHolder("e.g., Malaysia")

	d.dateCodeEntry = widget.NewEntry()
	d.dateCodeEntry.SetText(d.comp.DateCode)
	d.dateCodeEntry.SetPlaceHolder("e.g., 8523")

	d.revisionEntry = widget.NewEntry()
	d.revisionEntry.SetText(d.comp.Revision)

	d.speedGradeEntry = widget.NewEntry()
	d.speedGradeEntry.SetText(d.comp.SpeedGrade)
	d.speedGradeEntry.SetPlaceHolder("e.g., -25")

	d.descriptionEntry = widget.NewEntry()
	d.descriptionEntry.SetText(d.comp.Description)
	d.descriptionEntry.MultiLine = true
	d.descriptionEntry.SetMinRowsVisible(2)

	// Corrected text entry - user's verified truth for training
	d.correctedTextEntry = widget.NewEntry()
	d.correctedTextEntry.SetText(d.comp.CorrectedText)
	d.correctedTextEntry.MultiLine = true
	d.correctedTextEntry.SetMinRowsVisible(3)
	d.correctedTextEntry.SetPlaceHolder("Your corrected text (for training)")

	// OCR detected text entry - raw result from OCR
	d.ocrTextEntry = widget.NewEntry()
	d.ocrTextEntry.SetText(d.comp.OCRText)
	d.ocrTextEntry.MultiLine = true
	d.ocrTextEntry.SetMinRowsVisible(3)
	d.ocrTextEntry.SetPlaceHolder("OCR detected text")

	// OCR orientation selector - indicates which direction text bottom faces
	// N = normal (bottom at bottom), S = upside down, E = bottom at right, W = bottom at left
	d.ocrOrientation = widget.NewRadioGroup([]string{"N", "S", "E", "W"}, nil)
	d.ocrOrientation.Horizontal = true
	// Restore saved orientation or default to "N"
	if d.comp.OCROrientation != "" {
		d.ocrOrientation.SetSelected(d.comp.OCROrientation)
	} else {
		d.ocrOrientation.SetSelected("N")
	}

	// OCR button
	ocrBtn := widget.NewButton("OCR", func() {
		d.runOCR()
	})

	// Train OCR button - runs annealing to find best params
	trainBtn := widget.NewButton("Train OCR", func() {
		d.runOCRTraining()
	})

	// Main form
	form := widget.NewForm(
		widget.NewFormItem("ID", d.idEntry),
		widget.NewFormItem("Part Number", d.partNumberEntry),
		widget.NewFormItem("Package", d.packageEntry),
		widget.NewFormItem("Manufacturer", d.manufacturerEntry),
		widget.NewFormItem("Place", d.placeEntry),
		widget.NewFormItem("Date Code", d.dateCodeEntry),
		widget.NewFormItem("Revision", d.revisionEntry),
		widget.NewFormItem("Speed Grade", d.speedGradeEntry),
	)

	// Description section
	descCard := widget.NewCard("Description", "", d.descriptionEntry)

	// OCR section with orientation selector and two text areas
	// Layout: [OCR button] [Train button] [N S E W orientation]
	ocrControls := container.NewHBox(
		ocrBtn,
		trainBtn,
		widget.NewLabel("Text bottom:"),
		d.ocrOrientation,
	)

	// Two text areas: corrected (user truth) and detected (OCR result)
	ocrTextAreas := container.NewVBox(
		widget.NewLabel("Corrected Text (your edits):"),
		d.correctedTextEntry,
		widget.NewLabel("OCR Detected:"),
		d.ocrTextEntry,
	)

	ocrCard := widget.NewCard("OCR", "", container.NewBorder(
		ocrControls, nil, nil, nil,
		ocrTextAreas,
	))

	return container.NewVBox(
		form,
		descCard,
		ocrCard,
	)
}

func (d *ComponentEditDialog) applyChanges() {
	d.comp.ID = d.idEntry.Text
	d.comp.PartNumber = d.partNumberEntry.Text
	d.comp.Package = d.packageEntry.Text
	d.comp.Manufacturer = d.manufacturerEntry.Text
	d.comp.Place = d.placeEntry.Text
	d.comp.DateCode = d.dateCodeEntry.Text
	d.comp.Revision = d.revisionEntry.Text
	d.comp.SpeedGrade = d.speedGradeEntry.Text
	d.comp.Description = d.descriptionEntry.Text
	d.comp.OCRText = d.ocrTextEntry.Text
	d.comp.CorrectedText = d.correctedTextEntry.Text
	d.comp.OCROrientation = d.ocrOrientation.Selected
}

// runOCR performs OCR on the component's bounding rectangle.
func (d *ComponentEditDialog) runOCR() {
	if d.img == nil {
		fmt.Println("No image available for OCR")
		return
	}

	// Extract the component region from the image
	bounds := d.comp.Bounds
	x := int(bounds.X)
	y := int(bounds.Y)
	w := int(bounds.Width)
	h := int(bounds.Height)

	// Clamp to image bounds
	imgBounds := d.img.Bounds()
	if x < imgBounds.Min.X {
		x = imgBounds.Min.X
	}
	if y < imgBounds.Min.Y {
		y = imgBounds.Min.Y
	}
	if x+w > imgBounds.Max.X {
		w = imgBounds.Max.X - x
	}
	if y+h > imgBounds.Max.Y {
		h = imgBounds.Max.Y - y
	}

	if w <= 0 || h <= 0 {
		fmt.Println("Invalid component bounds for OCR")
		return
	}

	// Create cropped region
	cropped := image.NewRGBA(image.Rect(0, 0, w, h))
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			cropped.Set(dx, dy, d.img.At(x+dx, y+dy))
		}
	}

	// Apply rotation based on selected orientation
	// The orientation indicates where the text bottom is facing
	orientation := d.ocrOrientation.Selected
	rotated := rotateForOCR(cropped, orientation)

	// Convert to gocv.Mat
	rotBounds := rotated.Bounds()
	mat, err := gocv.NewMatFromBytes(rotBounds.Dy(), rotBounds.Dx(), gocv.MatTypeCV8UC4, rotated.Pix)
	if err != nil {
		fmt.Printf("Failed to convert image for OCR: %v\n", err)
		return
	}
	defer mat.Close()

	// Convert RGBA to BGR for OpenCV
	bgr := gocv.NewMat()
	defer bgr.Close()
	gocv.CvtColor(mat, &bgr, gocv.ColorRGBAToBGR)

	// Create OCR engine and run
	engine, err := ocr.NewEngine()
	if err != nil {
		fmt.Printf("Failed to create OCR engine: %v\n", err)
		return
	}
	defer engine.Close()

	// Run OCR on the component region
	fmt.Printf("OCR with orientation %s on region %dx%d\n", orientation, rotBounds.Dx(), rotBounds.Dy())

	var text string

	// Use learned params if available and they have good training data
	if d.learnedParams != nil && len(d.learnedParams.Samples) > 0 && d.learnedParams.AvgScore > 0.5 {
		fmt.Printf("OCR: using learned params (avg score %.2f from %d samples)\n",
			d.learnedParams.AvgScore, len(d.learnedParams.Samples))
		text, err = engine.RecognizeWithParams(bgr, d.learnedParams.BestParams)
	} else {
		text, err = engine.RecognizeImage(bgr)
	}

	if err != nil {
		fmt.Printf("OCR failed: %v\n", err)
		return
	}

	// If no text found, try enhanced preprocessing with histogram-based thresholding
	if strings.TrimSpace(text) == "" {
		fmt.Println("No text found, trying histogram-based enhancement...")
		text = d.runOCRWithHistogramThreshold(bgr, engine)
	}

	// Update the OCR text field
	d.ocrTextEntry.SetText(text)
	d.comp.OCRText = text

	// Try to parse component info from OCR text
	info := parseComponentInfo(text)
	if info.PartNumber != "" && d.partNumberEntry.Text == "" {
		d.partNumberEntry.SetText(info.PartNumber)
	}
	if info.Manufacturer != "" && d.manufacturerEntry.Text == "" {
		d.manufacturerEntry.SetText(info.Manufacturer)
	}
	if info.DateCode != "" && d.dateCodeEntry.Text == "" {
		d.dateCodeEntry.SetText(info.DateCode)
	}
	if info.Place != "" && d.placeEntry.Text == "" {
		d.placeEntry.SetText(info.Place)
	}

	fmt.Printf("OCR result for %s: %s\n", d.comp.ID, text)
}

// runOCRWithHistogramThreshold uses histogram analysis to find the whitest pixels (text)
// and creates a high-contrast binary image for better OCR detection.
func (d *ComponentEditDialog) runOCRWithHistogramThreshold(bgr gocv.Mat, engine *ocr.Engine) string {
	// Convert to grayscale
	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(bgr, &gray, gocv.ColorBGRToGray)

	// Build histogram to find the brightest pixels
	hist := make([]int, 256)
	totalPixels := gray.Rows() * gray.Cols()

	for y := 0; y < gray.Rows(); y++ {
		for x := 0; x < gray.Cols(); x++ {
			val := gray.GetUCharAt(y, x)
			hist[val]++
		}
	}

	// Find the threshold that captures the brightest ~5-15% of pixels (likely text)
	// Start from the brightest and work down until we have enough pixels
	targetPixels := totalPixels * 10 / 100 // Target 10% of pixels as text
	cumulative := 0
	threshold := 255

	for v := 255; v >= 0; v-- {
		cumulative += hist[v]
		if cumulative >= targetPixels {
			threshold = v
			break
		}
	}

	// Ensure minimum threshold to avoid capturing too much background
	if threshold < 128 {
		threshold = 128
	}

	fmt.Printf("  Histogram threshold: %d (capturing ~%d%% of pixels)\n", threshold, cumulative*100/totalPixels)

	// Create binary image: bright pixels become white, dark pixels become black
	binary := gocv.NewMat()
	defer binary.Close()
	gocv.Threshold(gray, &binary, float32(threshold), 255, gocv.ThresholdBinary)

	// Invert so dark text on light background (standard for OCR)
	// IC text is typically light on dark, so after threshold we have light text on black
	// OCR expects dark on light, so invert
	inverted := gocv.NewMat()
	defer inverted.Close()
	gocv.BitwiseNot(binary, &inverted)

	// Convert back to BGR for OCR engine
	bgrOut := gocv.NewMat()
	defer bgrOut.Close()
	gocv.CvtColor(inverted, &bgrOut, gocv.ColorGrayToBGR)

	// Try OCR on enhanced image
	text, err := engine.RecognizeImage(bgrOut)
	if err != nil {
		fmt.Printf("  Enhanced OCR failed: %v\n", err)
		return ""
	}

	if strings.TrimSpace(text) != "" {
		fmt.Printf("  Enhanced OCR found text: %s\n", strings.TrimSpace(text))
	}

	return text
}

// runOCRTraining runs parameter annealing to find the best OCR settings.
// Requires corrected text in the correctedTextEntry field as ground truth.
func (d *ComponentEditDialog) runOCRTraining() {
	if d.img == nil {
		fmt.Println("No image available for OCR training")
		return
	}

	// Get ground truth from corrected text
	groundTruth := d.correctedTextEntry.Text
	if strings.TrimSpace(groundTruth) == "" {
		fmt.Println("OCR Training: need corrected text as ground truth")
		dialog.ShowInformation("Training Required",
			"Please enter the corrected text first.\nThis will be used as ground truth for training.",
			d.window)
		return
	}

	// Extract the component region from the image
	bounds := d.comp.Bounds
	x := int(bounds.X)
	y := int(bounds.Y)
	w := int(bounds.Width)
	h := int(bounds.Height)

	// Clamp to image bounds
	imgBounds := d.img.Bounds()
	if x < imgBounds.Min.X {
		x = imgBounds.Min.X
	}
	if y < imgBounds.Min.Y {
		y = imgBounds.Min.Y
	}
	if x+w > imgBounds.Max.X {
		w = imgBounds.Max.X - x
	}
	if y+h > imgBounds.Max.Y {
		h = imgBounds.Max.Y - y
	}

	if w <= 0 || h <= 0 {
		fmt.Println("Invalid component bounds for OCR training")
		return
	}

	// Create cropped region
	cropped := image.NewRGBA(image.Rect(0, 0, w, h))
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			cropped.Set(dx, dy, d.img.At(x+dx, y+dy))
		}
	}

	// Apply rotation based on selected orientation
	orientation := d.ocrOrientation.Selected
	rotated := rotateForOCR(cropped, orientation)

	// Convert to gocv.Mat
	rotBounds := rotated.Bounds()
	mat, err := gocv.NewMatFromBytes(rotBounds.Dy(), rotBounds.Dx(), gocv.MatTypeCV8UC4, rotated.Pix)
	if err != nil {
		fmt.Printf("Failed to convert image for OCR training: %v\n", err)
		return
	}
	defer mat.Close()

	// Convert RGBA to BGR for OpenCV
	bgr := gocv.NewMat()
	defer bgr.Close()
	gocv.CvtColor(mat, &bgr, gocv.ColorRGBAToBGR)

	// Create OCR engine
	engine, err := ocr.NewEngine()
	if err != nil {
		fmt.Printf("Failed to create OCR engine: %v\n", err)
		return
	}
	defer engine.Close()

	fmt.Printf("OCR Training: starting annealing for %s (ground truth: %q)\n", d.comp.ID, groundTruth)

	// Run annealing - try up to 500 parameter combinations
	bestParams, bestScore, bestText := engine.AnnealOCRParams(bgr, groundTruth, 500)

	fmt.Printf("OCR Training: best score=%.3f text=%q\n", bestScore, bestText)

	// Update the OCR text field with the best result
	d.ocrTextEntry.SetText(bestText)
	d.comp.OCRText = bestText

	// Save the training sample to learned params
	if d.learnedParams != nil {
		sample := ocr.TrainingSample{
			GroundTruth: groundTruth,
			Orientation: orientation,
			BestParams:  bestParams,
			BestScore:   bestScore,
		}
		d.learnedParams.UpdateLearnedParams(sample)

		// Notify that params were updated
		if d.onParamsUpdated != nil {
			d.onParamsUpdated(d.learnedParams)
		}
	}

	// Show result dialog
	resultMsg := fmt.Sprintf("Best score: %.1f%%\nDetected: %s", bestScore*100, bestText)
	if bestScore >= 0.9 {
		resultMsg += "\n\nExcellent match! Parameters saved for future use."
	} else if bestScore >= 0.7 {
		resultMsg += "\n\nGood match. Parameters saved."
	} else if bestScore >= 0.5 {
		resultMsg += "\n\nPartial match. Try adjusting orientation or improving image quality."
	} else {
		resultMsg += "\n\nPoor match. The image may need better preprocessing."
	}

	dialog.ShowInformation("OCR Training Complete", resultMsg, d.window)
}

// componentInfo holds parsed component information from OCR text.
type componentInfo struct {
	PartNumber   string
	Manufacturer string
	DateCode     string
	Place        string
}

// Manufacturer prefixes for IC part numbers
var manufacturerPrefixes = map[string]string{
	// Texas Instruments
	"SN": "Texas Instruments",
	"TL": "Texas Instruments",
	"TM": "Texas Instruments",
	"UC": "Texas Instruments",
	"UL": "Texas Instruments",

	// National Semiconductor
	"DM": "National Semiconductor",
	"LM": "National Semiconductor",
	"DS": "National Semiconductor",
	"MM": "National Semiconductor",

	// Motorola / ON Semi
	"MC": "Motorola",
	"MJ": "Motorola",
	"MK": "Motorola",

	// Fairchild
	"UA": "Fairchild",
	"9N": "Fairchild", // 9Nxxx series

	// AMD
	"AM": "AMD",
	"PAL": "AMD",

	// Intel
	"P":  "Intel",
	"D":  "Intel",
	"iC": "Intel",

	// Signetics / Philips
	"N":  "Signetics",
	"NE": "Signetics",
	"SE": "Signetics",

	// RCA
	"CD": "RCA",
	"CA": "RCA",

	// Hitachi
	"HD": "Hitachi",
	"HA": "Hitachi",

	// NEC
	"UPD": "NEC",
	"UPC": "NEC",

	// Fujitsu
	"MB": "Fujitsu",

	// Mitsubishi
	"M5": "Mitsubishi",

	// Toshiba
	"TC": "Toshiba",
	"TMP": "Toshiba",

	// Samsung
	"KM": "Samsung",
	"KS": "Samsung",

	// Cypress
	"CY": "Cypress",

	// IDT
	"IDT": "IDT",

	// Xilinx
	"XC": "Xilinx",

	// Altera
	"EP": "Altera",
}

// Package suffixes for IC part numbers
var packageSuffixes = map[string]string{
	"N":   "DIP",
	"P":   "DIP",
	"AN":  "DIP",
	"J":   "CERDIP",
	"JG":  "CERDIP",
	"W":   "CFP",
	"D":   "SOIC",
	"DW":  "SOIC-Wide",
	"NS":  "SOP",
	"FK":  "PLCC",
	"FN":  "PLCC",
	"PC":  "PLCC",
	"PQ":  "QFP",
	"T":   "TO-220",
	"H":   "TO-39",
	"L":   "TO-39",
	"Z":   "TO-92",
	"LP":  "TO-92",
}

// Pattern for 74-series logic chips
var logic74Pattern = regexp.MustCompile(`(?i)([A-Z]{1,3})?(\d{2})?([A-Z]{0,4})?(74[A-Z]{0,4}\d{2,4})([A-Z]{1,3})?`)

// Pattern for date codes (YYWW format - 2 digit year, 2 digit week)
var dateCodePattern = regexp.MustCompile(`\b([789]\d)([0-5]\d)\b`)

// Pattern for 4-digit date codes (might also be YYWW)
var dateCode4Pattern = regexp.MustCompile(`\b(\d{4})\b`)

// parseComponentInfo extracts component information from raw OCR text.
func parseComponentInfo(text string) componentInfo {
	info := componentInfo{}

	// Normalize text - uppercase and clean up
	text = strings.ToUpper(text)
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")

	// Try to find a 74-series part number
	if matches := logic74Pattern.FindStringSubmatch(text); len(matches) > 0 {
		prefix := matches[1]
		corePN := matches[4] // The 74xxx part
		suffix := matches[5]

		// Look up manufacturer from prefix
		if prefix != "" {
			if mfg, ok := manufacturerPrefixes[prefix]; ok {
				info.Manufacturer = mfg
			}
		}

		// Core part number
		info.PartNumber = corePN

		// Package from suffix
		if suffix != "" {
			if pkg, ok := packageSuffixes[suffix]; ok {
				// Don't set - let the user's package field take precedence
				_ = pkg
			}
		}
	}

	// Try to find date code (YYWW format)
	if matches := dateCodePattern.FindStringSubmatch(text); len(matches) > 0 {
		info.DateCode = matches[0]
	} else if matches := dateCode4Pattern.FindStringSubmatch(text); len(matches) > 0 {
		// Check if it looks like a valid date code (80s-90s era ICs)
		code := matches[1]
		year := code[0:2]
		week := code[2:4]
		// Valid year range: 70-99 or 00-29 (1970-2029)
		// Valid week range: 01-53
		if (year >= "70" && year <= "99") || (year >= "00" && year <= "29") {
			if week >= "01" && week <= "53" {
				info.DateCode = code
			}
		}
	}

	// Look for manufacturing locations
	locations := []struct {
		pattern string
		place   string
	}{
		{"MALAYSIA", "Malaysia"},
		{"PHILIPPINES", "Philippines"},
		{"MEXICO", "Mexico"},
		{"TAIWAN", "Taiwan"},
		{"KOREA", "Korea"},
		{"JAPAN", "Japan"},
		{"CHINA", "China"},
		{"SINGAPORE", "Singapore"},
		{"THAILAND", "Thailand"},
		{"USA", "USA"},
		{"IRELAND", "Ireland"},
	}
	for _, loc := range locations {
		if strings.Contains(text, loc.pattern) {
			info.Place = loc.place
			break
		}
	}

	return info
}

// rotateForOCR rotates an image based on the text orientation selector.
// orientation indicates where the text bottom is facing:
//   - N: Normal (text bottom at image bottom) - no rotation
//   - S: Upside down (text bottom at image top) - rotate 180°
//   - E: Text runs bottom-to-top (text bottom at image right) - rotate 90° CCW
//   - W: Text runs top-to-bottom (text bottom at image left) - rotate 90° CW
func rotateForOCR(img *image.RGBA, orientation string) *image.RGBA {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	switch orientation {
	case "S": // Rotate 180°
		rotated := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				rotated.Set(w-1-x, h-1-y, img.At(x, y))
			}
		}
		return rotated

	case "E": // Rotate 90° counter-clockwise (270° clockwise)
		// Text bottom is at right side, so rotate CCW to bring it to bottom
		rotated := image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				rotated.Set(y, w-1-x, img.At(x, y))
			}
		}
		return rotated

	case "W": // Rotate 90° clockwise
		// Text bottom is at left side, so rotate CW to bring it to bottom
		rotated := image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				rotated.Set(h-1-y, x, img.At(x, y))
			}
		}
		return rotated

	default: // "N" or unknown - no rotation
		return img
	}
}
