package alignment

import (
	"fmt"
	"math"
	"sort"

	"pcb-tracer/internal/board"
	"pcb-tracer/pkg/geometry"

	"gocv.io/x/gocv"
)

// gridBasedDetection generates candidates across full image width on a common Y line and scores them.
// Returns expected positions, selected contacts, and total score.
func gridBasedDetection(img gocv.Mat, seedContacts []Contact, lineParams *ContactLineParams, expectedCount int, pitchPixels float64, isHorizontal bool) ([]geometry.RectInt, []Contact, float64) {
	// Sort seed contacts by position
	sorted := make([]Contact, len(seedContacts))
	copy(sorted, seedContacts)
	sort.Slice(sorted, func(i, j int) bool {
		if isHorizontal {
			return sorted[i].Center.X < sorted[j].Center.X
		}
		return sorted[i].Center.Y < sorted[j].Center.Y
	})

	// Helper functions
	getPos := func(c Contact) float64 {
		if isHorizontal {
			return c.Center.X
		}
		return c.Center.Y
	}
	getLinePos := func(c Contact) float64 {
		if isHorizontal {
			return c.Center.Y
		}
		return c.Center.X
	}

	// Calculate line position using MEDIAN of seed contacts (enforcing horizontal line)
	// We DO NOT calculate slope - all contacts should be on a common horizontal line
	// to enforce parallel board alignment
	firstContact := sorted[0]
	var lineSlope float64 = 0 // Horizontal line - no slope

	// Calculate MEDIAN line position from seed contacts (robust to outliers)
	linePositions := make([]float64, len(sorted))
	for i, c := range sorted {
		linePositions[i] = getLinePos(c)
	}
	sort.Float64s(linePositions)
	var medianLinePos float64
	n := len(linePositions)
	if n%2 == 0 {
		medianLinePos = (linePositions[n/2-1] + linePositions[n/2]) / 2
	} else {
		medianLinePos = linePositions[n/2]
	}

	fmt.Printf("Grid Y position: median=%.1f (from %d seeds, range %.1f-%.1f)\n",
		medianLinePos, n, linePositions[0], linePositions[n-1])

	// Reference point: use first seed contact for position, median for line position
	refPos := getPos(firstContact)
	refLinePos := medianLinePos // Use median Y for horizontal line

	// Image dimensions
	imgW := img.Cols()
	imgH := img.Rows()
	avgWidth := lineParams.AvgWidth
	avgHeight := lineParams.AvgHeight

	// Generate candidates across full image width, anchored to the first seed contact
	var maxExtent float64
	if isHorizontal {
		maxExtent = float64(imgW)
	} else {
		maxExtent = float64(imgH)
	}

	// Anchor grid to first seed contact position
	// Calculate how many positions before the first contact
	positionsBefore := int(refPos / pitchPixels)
	startPos := refPos - float64(positionsBefore)*pitchPixels

	numPositions := int((maxExtent - startPos) / pitchPixels)

	expectedPositions := make([]geometry.RectInt, numPositions)
	candidates := make([]ContactCandidate, numPositions)

	// Build map of seed contact positions (using grid-aligned indices)
	seedPositions := make(map[int]Contact)
	for _, c := range sorted {
		// Find the nearest grid position for this contact
		contactPos := getPos(c)
		idx := int(math.Round((contactPos - startPos) / pitchPixels))
		if idx >= 0 && idx < numPositions {
			seedPositions[idx] = c
		}
	}

	// Generate positions and score each
	for i := 0; i < numPositions; i++ {
		pos := startPos + float64(i)*pitchPixels
		linePos := refLinePos + lineSlope*(pos-refPos)

		if isHorizontal {
			expectedPositions[i] = geometry.RectInt{
				X:      int(pos - avgWidth/2),
				Y:      int(linePos - avgHeight/2),
				Width:  int(avgWidth),
				Height: int(avgHeight),
			}
		} else {
			expectedPositions[i] = geometry.RectInt{
				X:      int(linePos - avgWidth/2),
				Y:      int(pos - avgHeight/2),
				Width:  int(avgWidth),
				Height: int(avgHeight),
			}
		}

		candidates[i].Position = expectedPositions[i]
		if c, ok := seedPositions[i]; ok {
			candidates[i].Contact = &c
		}

		// Sample color from this region
		expPos := expectedPositions[i]
		x1, y1 := expPos.X, expPos.Y
		x2, y2 := expPos.X+expPos.Width, expPos.Y+expPos.Height

		// Clamp to image bounds
		if x1 < 0 {
			x1 = 0
		}
		if y1 < 0 {
			y1 = 0
		}
		if x2 > imgW {
			x2 = imgW
		}
		if y2 > imgH {
			y2 = imgH
		}

		if x2 > x1 && y2 > y1 {
			var sumR, sumG, sumB float64
			count := 0
			for y := y1; y < y2; y++ {
				for x := x1; x < x2; x++ {
					pixel := img.GetVecbAt(y, x)
					sumR += float64(pixel[2])
					sumG += float64(pixel[1])
					sumB += float64(pixel[0])
					count++
				}
			}
			if count > 0 {
				candidates[i].ColorAvgR = sumR / float64(count)
				candidates[i].ColorAvgG = sumG / float64(count)
				candidates[i].ColorAvgB = sumB / float64(count)
			}
		}
	}

	// Calculate reference color from seed contacts
	var refR, refG, refB float64
	refCount := 0
	for _, c := range candidates {
		if c.Contact != nil {
			refR += c.ColorAvgR
			refG += c.ColorAvgG
			refB += c.ColorAvgB
			refCount++
		}
	}
	if refCount > 0 {
		refR /= float64(refCount)
		refG /= float64(refCount)
		refB /= float64(refCount)
	}

	// Score each candidate
	for i := range candidates {
		c := &candidates[i]
		colorDist := math.Sqrt(
			math.Pow(c.ColorAvgR-refR, 2) +
				math.Pow(c.ColorAvgG-refG, 2) +
				math.Pow(c.ColorAvgB-refB, 2))
		brightness := (c.ColorAvgR + c.ColorAvgG + c.ColorAvgB) / 3
		c.Score = brightness / (1 + colorDist/50)
		if c.Contact != nil {
			c.Score *= 1.5
		}
	}

	// Sort by score and select top expectedCount
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	// Calculate total score (sum of top expectedCount scores)
	var totalScore float64
	var result []Contact
	for i := 0; i < expectedCount && i < len(candidates); i++ {
		totalScore += candidates[i].Score
		c := candidates[i]
		// All contacts use grid position to ensure common Y alignment
		// Use seed contact's Pass if available, otherwise PassRescue
		pass := PassRescue
		if c.Contact != nil {
			pass = c.Contact.Pass
		}
		result = append(result, Contact{
			Bounds: c.Position,
			Center: geometry.Point2D{
				X: float64(c.Position.X) + float64(c.Position.Width)/2,
				Y: float64(c.Position.Y) + float64(c.Position.Height)/2,
			},
			Pass: pass,
		})
	}

	// Sort result by position
	sort.Slice(result, func(i, j int) bool {
		if isHorizontal {
			return result[i].Center.X < result[j].Center.X
		}
		return result[i].Center.Y < result[j].Center.Y
	})

	return expectedPositions, result, totalScore
}

// filterToGridStrict filters contacts using stricter grid fitting and size validation.
// This is a wrapper that discards the line parameters for backward compatibility.
func filterToGridStrict(candidates []Contact, spec board.Spec, params DetectionParams, isHorizontal bool) []Contact {
	contacts, _ := filterToGridStrictWithParams(candidates, spec, params, isHorizontal)
	return contacts
}

// filterToGridStrictWithParams filters contacts and returns line parameters for rescue pass.
func filterToGridStrictWithParams(candidates []Contact, spec board.Spec, params DetectionParams, isHorizontal bool) ([]Contact, *ContactLineParams) {
	// Need at least 2 candidates for any meaningful analysis
	if len(candidates) < 2 {
		return candidates, nil
	}

	// For 2-4 candidates, do minimal filtering but still compute line params
	if len(candidates) < 5 {
		// Calculate average position for the line
		var sumLinePos, sumWidth, sumHeight float64
		for _, c := range candidates {
			if isHorizontal {
				sumLinePos += c.Center.Y
			} else {
				sumLinePos += c.Center.X
			}
			sumWidth += float64(c.Bounds.Width)
			sumHeight += float64(c.Bounds.Height)
		}
		avgLinePos := sumLinePos / float64(len(candidates))

		// Estimate pitch from spec and DPI if possible
		expectedCount := 50
		var pitchPixels float64
		if spec != nil && spec.ContactSpec() != nil {
			cs := spec.ContactSpec()
			expectedCount = cs.Count
			// Try to estimate DPI from contact size
			avgWidth := sumWidth / float64(len(candidates))
			estimatedDPI := avgWidth / cs.WidthInches
			if estimatedDPI > 200 && estimatedDPI < 1500 {
				pitchPixels = cs.PitchInches * estimatedDPI
			}
		}

		if pitchPixels == 0 {
			return candidates, nil // Can't estimate pitch
		}

		// Get first position
		var startPos float64
		if isHorizontal {
			startPos = candidates[0].Center.X
		} else {
			startPos = candidates[0].Center.Y
		}

		lineParams := &ContactLineParams{
			Pitch:     pitchPixels,
			StartPos:  startPos,
			AvgWidth:  sumWidth / float64(len(candidates)),
			AvgHeight: sumHeight / float64(len(candidates)),
			Count:     expectedCount,
		}
		if isHorizontal {
			lineParams.LineY = avgLinePos
		} else {
			lineParams.LineX = avgLinePos
		}

		fmt.Printf("Sparse detection: %d candidates, estimated pitch=%.1f px\n", len(candidates), pitchPixels)
		return candidates, lineParams
	}

	// Get expected dimensions
	expectedCount := 50
	var expectedPitch float64 = 0
	var expectedContactWidth float64 = 0
	var expectedContactHeight float64 = 0

	if spec != nil && spec.ContactSpec() != nil {
		cs := spec.ContactSpec()
		expectedCount = cs.Count
		// We need to estimate pitch from the data since we might not know DPI
	}

	// Get positions along the edge
	var positions []float64
	for _, c := range candidates {
		if isHorizontal {
			positions = append(positions, c.Center.X)
		} else {
			positions = append(positions, c.Center.Y)
		}
	}

	// Calculate spacings between adjacent candidates
	spacings := make([]float64, len(positions)-1)
	for i := range spacings {
		spacings[i] = positions[i+1] - positions[i]
	}

	if len(spacings) == 0 {
		return candidates, nil
	}

	// Find the modal spacing (most common spacing = likely the true pitch)
	sort.Float64s(spacings)
	medianSpacing := spacings[len(spacings)/2]

	// Filter to "regular" spacings (within 30% of median)
	var regularSpacings []float64
	for _, s := range spacings {
		if s > medianSpacing*0.7 && s < medianSpacing*1.3 {
			regularSpacings = append(regularSpacings, s)
		}
	}

	if len(regularSpacings) < 5 {
		// Not enough regular spacings, fall back to loose filtering
		return candidates, nil
	}

	// Calculate average pitch from regular spacings
	pitchSum := 0.0
	for _, s := range regularSpacings {
		pitchSum += s
	}
	pitchPixels := pitchSum / float64(len(regularSpacings))

	// Estimate DPI from pitch if we have spec
	estimatedDPI := 0.0
	if spec != nil && spec.ContactSpec() != nil {
		expectedPitch = spec.ContactSpec().PitchInches
		estimatedDPI = pitchPixels / expectedPitch
		expectedContactWidth = spec.ContactSpec().WidthInches * estimatedDPI
		expectedContactHeight = spec.ContactSpec().HeightInches * estimatedDPI
	}

	fmt.Printf("Grid analysis: pitch=%.1f px, estimated DPI=%.1f, expected contact=%.1fx%.1f px\n",
		pitchPixels, estimatedDPI, expectedContactWidth, expectedContactHeight)

	// Now find the best run of contacts at regular spacing
	tolerance := pitchPixels * 0.25

	var bestRun []Contact
	var bestStartPos float64
	for startIdx := 0; startIdx < len(candidates); startIdx++ {
		var run []Contact
		run = append(run, candidates[startIdx])
		startPos := positions[startIdx]

		// Try to find contacts at expected positions
		for n := 1; n < expectedCount; n++ {
			expectedPos := startPos + float64(n)*pitchPixels

			// Find the closest candidate to this expected position
			var closest *Contact
			closestDist := tolerance
			for i := range candidates {
				var pos float64
				if isHorizontal {
					pos = candidates[i].Center.X
				} else {
					pos = candidates[i].Center.Y
				}
				dist := math.Abs(pos - expectedPos)
				if dist < closestDist {
					closestDist = dist
					closest = &candidates[i]
				}
			}

			if closest != nil {
				// Validate contact size if we have expected dimensions
				valid := true
				if expectedContactWidth > 0 && expectedContactHeight > 0 {
					w := float64(closest.Bounds.Width)
					h := float64(closest.Bounds.Height)
					// Contact should be within 50% of expected size
					if w < expectedContactWidth*0.5 || w > expectedContactWidth*2.0 {
						valid = false
					}
					if h < expectedContactHeight*0.5 || h > expectedContactHeight*2.0 {
						valid = false
					}
				}
				if valid {
					run = append(run, *closest)
				}
			}
		}

		if len(run) > len(bestRun) {
			bestRun = run
			bestStartPos = startPos
		}
	}

	fmt.Printf("Grid filtering: %d candidates -> %d matched (expected %d)\n",
		len(candidates), len(bestRun), expectedCount)

	// Require at least 80% of expected contacts
	minRequired := expectedCount * 4 / 5
	if len(bestRun) < minRequired {
		fmt.Printf("Warning: only found %d contacts (need %d)\n", len(bestRun), minRequired)
	}

	// Calculate line parameters from found contacts for rescue pass
	var lineParams *ContactLineParams
	if len(bestRun) >= 2 {
		// Calculate average Y (for horizontal) or X (for vertical) position
		var sumLinePos, sumWidth, sumHeight float64
		for _, c := range bestRun {
			if isHorizontal {
				sumLinePos += c.Center.Y
			} else {
				sumLinePos += c.Center.X
			}
			sumWidth += float64(c.Bounds.Width)
			sumHeight += float64(c.Bounds.Height)
		}
		avgLinePos := sumLinePos / float64(len(bestRun))

		lineParams = &ContactLineParams{
			Pitch:     pitchPixels,
			StartPos:  bestStartPos,
			AvgWidth:  sumWidth / float64(len(bestRun)),
			AvgHeight: sumHeight / float64(len(bestRun)),
			Count:     expectedCount,
		}
		if isHorizontal {
			lineParams.LineY = avgLinePos
		} else {
			lineParams.LineX = avgLinePos
		}
	}

	return bestRun, lineParams
}

// filterToGrid filters contacts to those matching expected grid pattern (legacy).
func filterToGrid(candidates []Contact, spec board.Spec, isHorizontal bool) []Contact {
	if len(candidates) < 10 {
		return candidates
	}

	// Calculate spacings
	var positions []float64
	for _, c := range candidates {
		if isHorizontal {
			positions = append(positions, c.Center.X)
		} else {
			positions = append(positions, c.Center.Y)
		}
	}

	spacings := make([]float64, len(positions)-1)
	for i := range spacings {
		spacings[i] = positions[i+1] - positions[i]
	}

	// Get expected pitch in pixels
	expectedPitch := 0.125 // S-100 default
	if spec != nil && spec.ContactSpec() != nil {
		expectedPitch = spec.ContactSpec().PitchInches
	}

	// Estimate DPI from median spacing
	sort.Float64s(spacings)
	medianSpacing := spacings[len(spacings)/2]

	// Filter to spacings that give reasonable DPI (200-1500)
	estimatedDPI := medianSpacing / expectedPitch
	if estimatedDPI < 200 || estimatedDPI > 1500 {
		return candidates
	}

	pitchPixels := medianSpacing
	tolerance := pitchPixels * 0.20

	// Find best starting position
	var bestContacts []Contact
	for startIdx := range positions {
		startPos := positions[startIdx]
		matched := []Contact{candidates[startIdx]}

		expectedCount := 50
		if spec != nil && spec.ContactSpec() != nil {
			expectedCount = spec.ContactSpec().Count
		}

		for n := 1; n < expectedCount; n++ {
			expectedPos := startPos + float64(n)*pitchPixels

			var closest *Contact
			closestDist := tolerance
			for i, pos := range positions {
				dist := math.Abs(pos - expectedPos)
				if dist < closestDist {
					closestDist = dist
					closest = &candidates[i]
				}
			}
			if closest != nil {
				matched = append(matched, *closest)
			}
		}

		if len(matched) > len(bestContacts) {
			bestContacts = matched
		}
	}

	// Require at least 45 contacts for S-100
	minContacts := 45
	if spec != nil && spec.ContactSpec() != nil {
		minContacts = spec.ContactSpec().Count - 5
	}

	if len(bestContacts) < minContacts {
		return nil
	}

	return bestContacts
}

// CalculateContactLineAngle calculates the rotation angle of the contact line.
// Returns the angle in degrees needed to make the contacts horizontal (for top/bottom edge)
// or vertical (for left/right edge).
// In image coordinates: Y increases downward, positive angle = counter-clockwise rotation.
func CalculateContactLineAngle(contacts []Contact, edge string) float64 {
	if len(contacts) < 2 {
		return 0
	}

	// Use linear regression to find the best-fit line through contact centers
	var sumX, sumY, sumXY, sumX2 float64
	n := float64(len(contacts))

	for _, c := range contacts {
		sumX += c.Center.X
		sumY += c.Center.Y
		sumXY += c.Center.X * c.Center.Y
		sumX2 += c.Center.X * c.Center.X
	}

	// Calculate slope (dy/dx in image coordinates)
	denominator := n*sumX2 - sumX*sumX
	if math.Abs(denominator) < 0.001 {
		return 0 // Vertical line or insufficient data
	}

	slope := (n*sumXY - sumX*sumY) / denominator

	// Convert slope to angle in degrees
	// slope = tan(angle), so angle = atan(slope)
	angle := math.Atan(slope) * 180 / math.Pi

	// To make the line horizontal, we rotate by the line's angle (which cancels it out)
	// In OpenCV/gocv: positive angle = counter-clockwise, negative = clockwise
	//
	// Example: Line slopes up-left to down-right (Y increases with X in image coords)
	//   - slope is positive, angle is positive
	//   - Apply positive (counter-clockwise) rotation to level it
	// Example: Line slopes down-left to up-right (Y decreases with X)
	//   - slope is negative, angle is negative
	//   - Apply negative (clockwise) rotation to level it
	switch edge {
	case "top", "bottom":
		// Return the line angle directly - applying it will level the line
		return angle
	case "left", "right":
		// Return angle to rotate to make vertical
		return angle - 90
	}

	return angle
}
