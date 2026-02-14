// Package ocr provides OCR for silkscreen text detection.
package ocr

import (
	"fmt"
	"image"
	"regexp"
	"sort"
	"strings"

	"pcb-tracer/pkg/geometry"

	"github.com/otiai10/gosseract/v2"
	"gocv.io/x/gocv"
)

// ComponentDesignator represents a detected component label like "C32" or "R2".
type ComponentDesignator struct {
	Text     string           // Full text (e.g., "C32")
	Prefix   string           // Component type (e.g., "C", "R", "U")
	Number   int              // Component number (e.g., 32)
	Bounds   geometry.RectInt // Location in image
	Rotation int              // Rotation at which it was detected (0, 90, 180, 270)
}

// CoordinateMarker represents a grid coordinate (A, B, C... or 1, 2, 3...).
type CoordinateMarker struct {
	Text     string           // The marker text ("A", "1", etc.)
	Value    int              // Ordinal value (A=0, B=1, or 1=1, 2=2)
	IsLetter bool             // True for A-Z, false for 1-9
	Bounds   geometry.RectInt // Location in image
	Rotation int              // Rotation at which detected
}

// CoordinateAxis represents a detected row of coordinate markers.
type CoordinateAxis struct {
	Markers    []CoordinateMarker
	IsVertical bool    // True if markers are arranged vertically
	IsLetter   bool    // True for A-Z axis, false for 1-9
	AxisPos    float64 // X position for vertical, Y position for horizontal
}

// SilkscreenResult contains all detected silkscreen elements.
type SilkscreenResult struct {
	Designators []ComponentDesignator
	XAxis       *CoordinateAxis // Horizontal axis (usually numbers)
	YAxis       *CoordinateAxis // Vertical axis (usually letters)
	AllText     []Result        // All detected text
}

// Regex patterns for component designators
var (
	// Standard component prefixes: C=capacitor, R=resistor, U=IC, Q=transistor,
	// D=diode, L=inductor, J=connector, P=connector, T=transformer, Y=crystal
	designatorPattern = regexp.MustCompile(`^([CRUQDLJTPYKX])(\d+)$`)

	// Single letter (A-Z) for coordinate grid
	letterPattern = regexp.MustCompile(`^[A-Z]$`)

	// Single/double digit number for coordinate grid
	numberPattern = regexp.MustCompile(`^\d{1,2}$`)
)

// DetectSilkscreen performs OCR specifically tuned for white silkscreen text.
// Tries all 4 rotations to find the best orientation for text.
func (e *Engine) DetectSilkscreen(img gocv.Mat) (*SilkscreenResult, error) {
	if img.Empty() {
		return nil, fmt.Errorf("empty image")
	}

	result := &SilkscreenResult{}

	// Preprocess to isolate white silkscreen
	whiteText := extractWhiteSilkscreen(img)
	defer whiteText.Close()

	// Try all 4 rotations
	rotations := []int{0, 90, 180, 270}
	var allResults []Result

	for _, rotation := range rotations {
		rotated := rotateImage(whiteText, rotation)

		texts, err := e.detectTextInImage(rotated)
		rotated.Close()

		if err != nil {
			continue
		}

		// Adjust bounds back to original orientation
		for i := range texts {
			texts[i].Bounds = unrotateRect(texts[i].Bounds, rotation, whiteText.Cols(), whiteText.Rows())
		}

		// Tag results with rotation
		for _, t := range texts {
			// Check if this is a component designator
			if matches := designatorPattern.FindStringSubmatch(t.Text); matches != nil {
				var num int
				fmt.Sscanf(matches[2], "%d", &num)
				result.Designators = append(result.Designators, ComponentDesignator{
					Text:     t.Text,
					Prefix:   matches[1],
					Number:   num,
					Bounds:   t.Bounds,
					Rotation: rotation,
				})
			}
			allResults = append(allResults, t)
		}
	}

	// Explicitly detect single characters (A-Z, 0-9) for coordinate grid
	// This catches markers that PSM_SPARSE_TEXT misses
	singleChars := e.detectSingleCharacters(whiteText)
	fmt.Printf("  Single character detection found %d candidates\n", len(singleChars))

	// Merge single char results, avoiding duplicates
	for _, sc := range singleChars {
		if !isDuplicateResult(sc, allResults, 30) {
			allResults = append(allResults, sc)
		}
	}

	result.AllText = allResults

	// Find coordinate axes from single letters/numbers
	result.XAxis, result.YAxis = findCoordinateAxes(allResults, img.Cols(), img.Rows())

	fmt.Printf("Silkscreen OCR: found %d designators, %d total text items\n",
		len(result.Designators), len(result.AllText))

	// Print designators found
	for _, d := range result.Designators {
		fmt.Printf("  Designator: %s at (%d,%d) rot=%d\n",
			d.Text, d.Bounds.X, d.Bounds.Y, d.Rotation)
	}

	if result.XAxis != nil {
		fmt.Printf("  X-Axis: %d markers, isLetter=%v\n", len(result.XAxis.Markers), result.XAxis.IsLetter)
	}
	if result.YAxis != nil {
		fmt.Printf("  Y-Axis: %d markers, isLetter=%v\n", len(result.YAxis.Markers), result.YAxis.IsLetter)
	}

	return result, nil
}

// extractWhiteSilkscreen isolates bright white text from the image.
func extractWhiteSilkscreen(img gocv.Mat) gocv.Mat {
	// Convert to HSV for better color isolation
	hsv := gocv.NewMat()
	gocv.CvtColor(img, &hsv, gocv.ColorBGRToHSV)

	// White has low saturation and high value
	// In HSV: H is any, S is low (0-50), V is high (200-255)
	lowerWhite := gocv.NewScalar(0, 0, 200, 0)
	upperWhite := gocv.NewScalar(180, 50, 255, 0)

	mask := gocv.NewMat()
	gocv.InRangeWithScalar(hsv, lowerWhite, upperWhite, &mask)
	hsv.Close()

	// Morphological cleanup - remove noise, connect nearby text
	kernel := gocv.GetStructuringElement(gocv.MorphRect, image.Pt(2, 2))
	defer kernel.Close()

	cleaned := gocv.NewMat()
	gocv.MorphologyEx(mask, &cleaned, gocv.MorphClose, kernel)
	mask.Close()

	// Convert to BGR for OCR (white on black background)
	result := gocv.NewMat()
	gocv.CvtColor(cleaned, &result, gocv.ColorGrayToBGR)
	cleaned.Close()

	return result
}

// rotateImage rotates an image by the specified degrees (0, 90, 180, 270).
func rotateImage(img gocv.Mat, degrees int) gocv.Mat {
	result := gocv.NewMat()

	switch degrees {
	case 0:
		return img.Clone()
	case 90:
		gocv.Rotate(img, &result, gocv.Rotate90Clockwise)
	case 180:
		gocv.Rotate(img, &result, gocv.Rotate180Clockwise)
	case 270:
		gocv.Rotate(img, &result, gocv.Rotate90CounterClockwise)
	default:
		return img.Clone()
	}

	return result
}

// unrotateRect converts a bounding box from rotated coordinates back to original.
func unrotateRect(rect geometry.RectInt, rotation, origW, origH int) geometry.RectInt {
	x, y, w, h := rect.X, rect.Y, rect.Width, rect.Height

	switch rotation {
	case 0:
		return rect
	case 90:
		// 90 CW: (x,y) -> (origH-y-h, x)
		return geometry.RectInt{X: origH - y - h, Y: x, Width: h, Height: w}
	case 180:
		// 180: (x,y) -> (origW-x-w, origH-y-h)
		return geometry.RectInt{X: origW - x - w, Y: origH - y - h, Width: w, Height: h}
	case 270:
		// 90 CCW: (x,y) -> (y, origW-x-w)
		return geometry.RectInt{X: y, Y: origW - x - w, Width: h, Height: w}
	}
	return rect
}

// detectTextInImage runs OCR on an image and returns results.
func (e *Engine) detectTextInImage(img gocv.Mat) ([]Result, error) {
	// Invert for dark text on white background (Tesseract prefers this)
	inverted := gocv.NewMat()
	gocv.BitwiseNot(img, &inverted)
	defer inverted.Close()

	// Encode to PNG
	buf, err := gocv.IMEncode(gocv.PNGFileExt, inverted)
	if err != nil {
		return nil, err
	}
	defer buf.Close()

	// Configure Tesseract for sparse text (scattered labels)
	if err := e.client.SetPageSegMode(gosseract.PSM_SPARSE_TEXT); err != nil {
		return nil, err
	}

	// Use electronics character set
	if err := e.client.SetWhitelist(ElectronicsChars); err != nil {
		return nil, err
	}

	if err := e.client.SetImageFromBytes(buf.GetBytes()); err != nil {
		return nil, err
	}

	boxes, err := e.client.GetBoundingBoxes(gosseract.RIL_WORD)
	if err != nil {
		return nil, err
	}

	var results []Result
	for _, box := range boxes {
		text := strings.TrimSpace(strings.ToUpper(box.Word))
		if text == "" || len(text) > 10 {
			continue
		}

		// Filter out noise - require minimum confidence
		if box.Confidence < 30 {
			continue
		}

		results = append(results, Result{
			Text: text,
			Bounds: geometry.RectInt{
				X:      box.Box.Min.X,
				Y:      box.Box.Min.Y,
				Width:  box.Box.Dx(),
				Height: box.Box.Dy(),
			},
			Confidence: box.Confidence,
		})
	}

	return results, nil
}

// detectSingleCharacters finds isolated white blobs and runs single-char OCR on each.
// This catches coordinate markers (A, B, C, 1, 2, 3...) that word-level OCR misses.
func (e *Engine) detectSingleCharacters(img gocv.Mat) []Result {
	var results []Result

	// Convert to grayscale for contour detection
	gray := gocv.NewMat()
	gocv.CvtColor(img, &gray, gocv.ColorBGRToGray)
	defer gray.Close()

	// Threshold to get binary image
	binary := gocv.NewMat()
	gocv.Threshold(gray, &binary, 127, 255, gocv.ThresholdBinary)
	defer binary.Close()

	// Find contours (individual white blobs)
	contours := gocv.FindContours(binary, gocv.RetrievalExternal, gocv.ChainApproxSimple)
	defer contours.Close()

	// Size constraints for single characters (in pixels)
	// Silkscreen text is typically 10-60 pixels in size
	const minCharSize = 8
	const maxCharSize = 80
	const minAspect = 0.3
	const maxAspect = 3.0

	for i := 0; i < contours.Size(); i++ {
		contour := contours.At(i)
		rect := gocv.BoundingRect(contour)

		w, h := rect.Dx(), rect.Dy()

		// Skip if too small or too large
		if w < minCharSize || h < minCharSize {
			continue
		}
		if w > maxCharSize || h > maxCharSize {
			continue
		}

		// Check aspect ratio
		aspect := float64(w) / float64(h)
		if aspect < minAspect || aspect > maxAspect {
			continue
		}

		// Extract the character region with padding
		pad := 5
		x1 := max(0, rect.Min.X-pad)
		y1 := max(0, rect.Min.Y-pad)
		x2 := min(img.Cols(), rect.Max.X+pad)
		y2 := min(img.Rows(), rect.Max.Y+pad)

		region := img.Region(image.Rect(x1, y1, x2, y2))

		// Try OCR on this region with single-character mode
		text := e.ocrSingleCharRegion(region)
		region.Close()

		if text != "" {
			results = append(results, Result{
				Text: text,
				Bounds: geometry.RectInt{
					X:      rect.Min.X,
					Y:      rect.Min.Y,
					Width:  w,
					Height: h,
				},
				Confidence: 50, // Default confidence for single-char detection
			})
		}
	}

	return results
}

// ocrSingleCharRegion runs OCR on a small region expecting a single character.
func (e *Engine) ocrSingleCharRegion(region gocv.Mat) string {
	// Scale up small regions for better OCR
	var scaled gocv.Mat
	minDim := min(region.Rows(), region.Cols())
	if minDim < 30 {
		scale := 30.0 / float64(minDim)
		scaled = gocv.NewMat()
		gocv.Resize(region, &scaled, image.Point{}, scale, scale, gocv.InterpolationCubic)
	} else {
		scaled = region.Clone()
	}
	defer scaled.Close()

	// Invert for dark text on white background
	inverted := gocv.NewMat()
	gocv.BitwiseNot(scaled, &inverted)
	defer inverted.Close()

	// Encode to PNG
	buf, err := gocv.IMEncode(gocv.PNGFileExt, inverted)
	if err != nil {
		return ""
	}
	defer buf.Close()

	// Configure for single character
	if err := e.client.SetPageSegMode(gosseract.PSM_SINGLE_CHAR); err != nil {
		return ""
	}

	// Restrict to alphanumeric for coordinate markers
	if err := e.client.SetWhitelist("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"); err != nil {
		return ""
	}

	if err := e.client.SetImageFromBytes(buf.GetBytes()); err != nil {
		return ""
	}

	text, err := e.client.Text()
	if err != nil {
		return ""
	}

	text = strings.TrimSpace(strings.ToUpper(text))

	// Validate: must be single letter A-Z or number 1-20
	if letterPattern.MatchString(text) {
		return text
	}
	if numberPattern.MatchString(text) {
		var val int
		fmt.Sscanf(text, "%d", &val)
		if val >= 1 && val <= 20 {
			return text
		}
	}

	return ""
}

// isDuplicateResult checks if a result overlaps with existing results.
func isDuplicateResult(r Result, existing []Result, threshold int) bool {
	for _, e := range existing {
		// Check if same text and overlapping bounds
		if r.Text == e.Text {
			dx := abs(r.Bounds.X - e.Bounds.X)
			dy := abs(r.Bounds.Y - e.Bounds.Y)
			if dx < threshold && dy < threshold {
				return true
			}
		}
	}
	return false
}

// abs returns absolute value of int.
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// findCoordinateAxes looks for A,B,C,D... or 1,2,3,4... patterns along edges.
func findCoordinateAxes(results []Result, imgW, imgH int) (*CoordinateAxis, *CoordinateAxis) {
	var letters []CoordinateMarker
	var numbers []CoordinateMarker

	// Collect single-character matches
	for _, r := range results {
		if letterPattern.MatchString(r.Text) {
			letters = append(letters, CoordinateMarker{
				Text:     r.Text,
				Value:    int(r.Text[0] - 'A'),
				IsLetter: true,
				Bounds:   r.Bounds,
			})
		} else if numberPattern.MatchString(r.Text) {
			var val int
			fmt.Sscanf(r.Text, "%d", &val)
			numbers = append(numbers, CoordinateMarker{
				Text:     r.Text,
				Value:    val,
				IsLetter: false,
				Bounds:   r.Bounds,
			})
		}
	}

	// Try to find axes - letters typically on Y axis, numbers on X axis
	// But check both orientations

	var xAxis, yAxis *CoordinateAxis

	// Find horizontal axis (near top or bottom edge, consistent Y)
	xAxis = findAxisInMarkers(numbers, false, imgW, imgH)
	if xAxis == nil {
		xAxis = findAxisInMarkers(letters, false, imgW, imgH)
	}

	// Find vertical axis (near left or right edge, consistent X)
	yAxis = findAxisInMarkers(letters, true, imgW, imgH)
	if yAxis == nil {
		yAxis = findAxisInMarkers(numbers, true, imgW, imgH)
	}

	return xAxis, yAxis
}

// findAxisInMarkers looks for a sequence of markers forming an axis.
func findAxisInMarkers(markers []CoordinateMarker, vertical bool, imgW, imgH int) *CoordinateAxis {
	if len(markers) < 3 {
		return nil
	}

	// Group markers by their position along the non-axis dimension
	// For horizontal axis, group by Y; for vertical axis, group by X
	groups := make(map[int][]CoordinateMarker)

	for _, m := range markers {
		var key int
		if vertical {
			// Group by X position (quantized to 50px buckets)
			key = m.Bounds.X / 50
		} else {
			// Group by Y position
			key = m.Bounds.Y / 50
		}
		groups[key] = append(groups[key], m)
	}

	// Find the largest group that forms a sequence
	var bestAxis *CoordinateAxis
	var bestScore int

	for pos, group := range groups {
		if len(group) < 3 {
			continue
		}

		// Sort by value
		sorted := make([]CoordinateMarker, len(group))
		copy(sorted, group)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].Value < sorted[j].Value
		})

		// Check for consecutive or near-consecutive sequence
		sequenceLen := 1
		for i := 1; i < len(sorted); i++ {
			diff := sorted[i].Value - sorted[i-1].Value
			if diff >= 1 && diff <= 2 {
				sequenceLen++
			} else {
				break
			}
		}

		if sequenceLen >= 3 && sequenceLen > bestScore {
			bestScore = sequenceLen
			axisPos := float64(pos * 50)
			bestAxis = &CoordinateAxis{
				Markers:    sorted[:sequenceLen],
				IsVertical: vertical,
				IsLetter:   sorted[0].IsLetter,
				AxisPos:    axisPos,
			}
		}
	}

	return bestAxis
}

// GetDesignatorsByType returns all designators of a specific type (C, R, U, etc.).
func (r *SilkscreenResult) GetDesignatorsByType(prefix string) []ComponentDesignator {
	var result []ComponentDesignator
	for _, d := range r.Designators {
		if d.Prefix == prefix {
			result = append(result, d)
		}
	}
	return result
}

// GetDesignatorCounts returns a map of prefix to count.
func (r *SilkscreenResult) GetDesignatorCounts() map[string]int {
	counts := make(map[string]int)
	for _, d := range r.Designators {
		counts[d.Prefix]++
	}
	return counts
}
