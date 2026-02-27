package alignment

import (
	"fmt"
	"math"
	"sort"

	"pcb-tracer/internal/board"
)

// calculateDPI calculates DPI from contact spacing.
func calculateDPI(contacts []Contact, spec board.Spec) float64 {
	if len(contacts) < 10 {
		return 0
	}

	// Get X centers
	centers := make([]float64, len(contacts))
	for i, c := range contacts {
		centers[i] = c.Center.X
	}

	// Calculate spacings
	spacings := make([]float64, len(centers)-1)
	for i := range spacings {
		spacings[i] = centers[i+1] - centers[i]
	}

	// Filter outliers
	sort.Float64s(spacings)
	medianSpacing := spacings[len(spacings)/2]

	var validSpacings []float64
	for _, s := range spacings {
		if s > medianSpacing*0.7 && s < medianSpacing*1.3 {
			validSpacings = append(validSpacings, s)
		}
	}

	if len(validSpacings) == 0 {
		return 0
	}

	// Average spacing
	sum := 0.0
	for _, s := range validSpacings {
		sum += s
	}
	avgSpacing := sum / float64(len(validSpacings))

	// Calculate DPI
	pitch := 0.125 // S-100 default
	if spec != nil && spec.ContactSpec() != nil {
		pitch = spec.ContactSpec().PitchInches
	}

	return avgSpacing / pitch
}

// findPitchFromIntervals finds the actual pitch from a list of intervals.
// Intervals should be multiples of the pitch (1x, 2x, 3x, etc. for gaps).
// Returns the most likely base pitch.
func findPitchFromIntervals(intervals []float64) float64 {
	if len(intervals) == 0 {
		return 0
	}

	// Find the minimum interval that appears frequently
	// This is likely to be the actual pitch (1x)
	sorted := make([]float64, len(intervals))
	copy(sorted, intervals)
	sort.Float64s(sorted)

	// The pitch should be close to the smallest intervals
	// Use the median of the smallest 50% of intervals as initial estimate
	n := len(sorted)
	smallHalf := sorted[:n/2+1]
	if len(smallHalf) == 0 {
		smallHalf = sorted
	}

	// Find median of small half
	medianIdx := len(smallHalf) / 2
	estimatedPitch := smallHalf[medianIdx]

	// Refine: count how many intervals are close to multiples of this pitch
	// and adjust if needed
	tolerance := estimatedPitch * 0.15

	// Count intervals that match 1x pitch
	count1x := 0
	sum1x := 0.0
	for _, interval := range intervals {
		if math.Abs(interval-estimatedPitch) < tolerance {
			count1x++
			sum1x += interval
		}
	}

	// If we have enough 1x matches, refine the estimate
	if count1x >= len(intervals)/3 {
		estimatedPitch = sum1x / float64(count1x)
	}

	return estimatedPitch
}

// normalizeContactWidths ensures all contacts have consistent widths.
// Contacts that bleed into the insulator will be too wide - this trims them.
func normalizeContactWidths(contacts []Contact) []Contact {
	if len(contacts) < 3 {
		return contacts
	}

	// Sort by X position
	sorted := make([]Contact, len(contacts))
	copy(sorted, contacts)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Bounds.X < sorted[j].Bounds.X
	})

	// Collect all widths and find median
	widths := make([]int, len(sorted))
	for i, c := range sorted {
		widths[i] = c.Bounds.Width
	}
	sortedWidths := make([]int, len(widths))
	copy(sortedWidths, widths)
	sort.Ints(sortedWidths)
	medianWidth := sortedWidths[len(sortedWidths)/2]

	// If a contact is wider than medianWidth + tolerance, it's bleeding into insulator
	maxWidth := int(float64(medianWidth) * 1.15)    // Allow 15% tolerance for trimming
	rejectWidth := int(float64(medianWidth) * 1.30) // Reject if >30% wider
	minWidth := int(float64(medianWidth) * 0.75)    // Reject if <75% of median

	// Track how many were adjusted or rejected
	adjustedCount := 0
	rejectedCount := 0

	// For each contact that's too wide, trim it or reject
	var result []Contact
	for _, c := range sorted {
		// Reject contacts that are way too wide or too narrow
		if c.Bounds.Width > rejectWidth {
			rejectedCount++
			continue
		}
		if c.Bounds.Width < minWidth {
			rejectedCount++
			continue
		}

		contact := c
		if c.Bounds.Width > maxWidth {
			// This contact is too wide - determine which side to trim
			excessWidth := c.Bounds.Width - medianWidth

			// Check if left edge is out of line with neighbors
			// Expected left edge based on center and median width
			expectedLeft := int(c.Center.X) - medianWidth/2
			leftError := c.Bounds.X - expectedLeft

			// Expected right edge
			expectedRight := int(c.Center.X) + medianWidth/2
			actualRight := c.Bounds.X + c.Bounds.Width
			rightError := actualRight - expectedRight

			// Trim the side that's more out of line
			newX := c.Bounds.X
			newWidth := c.Bounds.Width

			if leftError < 0 && math.Abs(float64(leftError)) > math.Abs(float64(rightError)) {
				// Left edge is too far left - trim from left
				trimLeft := -leftError
				if trimLeft > excessWidth {
					trimLeft = excessWidth
				}
				newX = c.Bounds.X + trimLeft
				newWidth = c.Bounds.Width - trimLeft
			} else if rightError > 0 {
				// Right edge is too far right - trim from right
				trimRight := rightError
				if trimRight > excessWidth {
					trimRight = excessWidth
				}
				newWidth = c.Bounds.Width - trimRight
			}

			// Update the contact
			contact.Bounds.X = newX
			contact.Bounds.Width = newWidth
			contact.Center.X = float64(newX) + float64(newWidth)/2
			adjustedCount++
		}

		result = append(result, contact)
	}

	if adjustedCount > 0 || rejectedCount > 0 {
		fmt.Printf("Width normalization: %d trimmed, %d rejected (median=%d, max=%d, reject>%d)\n",
			adjustedCount, rejectedCount, medianWidth, maxWidth, rejectWidth)
	}

	return result
}

// deduplicateCandidates removes duplicate contacts from overlapping regions.
// Two contacts are considered duplicates if their centers are very close.
func deduplicateCandidates(candidates []Contact, template *ContactTemplate) []Contact {
	if len(candidates) <= 1 {
		return candidates
	}

	// Threshold for duplicate detection - use the smaller dimension (width)
	// since contacts are spaced by pitch which is close to height
	// Use 80% of average width to be conservative
	threshold := template.AvgWidth * 0.8

	// Sort by X then Y for consistent ordering
	sort.Slice(candidates, func(i, j int) bool {
		if math.Abs(candidates[i].Center.X-candidates[j].Center.X) < threshold/2 {
			return candidates[i].Center.Y < candidates[j].Center.Y
		}
		return candidates[i].Center.X < candidates[j].Center.X
	})

	// Keep track of which candidates to keep
	var result []Contact
	for i := range candidates {
		isDuplicate := false
		for j := range result {
			dx := candidates[i].Center.X - result[j].Center.X
			dy := candidates[i].Center.Y - result[j].Center.Y
			dist := math.Sqrt(dx*dx + dy*dy)
			if dist < threshold {
				isDuplicate = true
				break
			}
		}
		if !isDuplicate {
			result = append(result, candidates[i])
		}
	}

	return result
}

// removeOutliers removes contacts that are outliers by position OR size.
// This helps eliminate false positives that don't belong to the contact row.
func removeOutliers(contacts []Contact, maxFraction float64) []Contact {
	if len(contacts) <= 2 {
		return contacts
	}

	// Fit a line through contact centers (Y = slope*X + intercept)
	// so position outliers are measured from the line, not a flat median
	var sumX, sumY, sumXY, sumX2 float64
	nn := float64(len(contacts))
	for _, c := range contacts {
		sumX += c.Center.X
		sumY += c.Center.Y
		sumXY += c.Center.X * c.Center.Y
		sumX2 += c.Center.X * c.Center.X
	}
	denom := nn*sumX2 - sumX*sumX
	var lineSlope, lineIntercept float64
	if math.Abs(denom) > 0.001 {
		lineSlope = (nn*sumXY - sumX*sumY) / denom
		lineIntercept = (sumY - lineSlope*sumX) / nn
	} else {
		yPositions := make([]float64, len(contacts))
		for i, c := range contacts {
			yPositions[i] = c.Center.Y
		}
		sort.Float64s(yPositions)
		lineIntercept = yPositions[len(yPositions)/2]
	}

	// Calculate residuals from the fitted line
	residuals := make([]float64, len(contacts))
	for i, c := range contacts {
		expectedY := lineSlope*c.Center.X + lineIntercept
		residuals[i] = math.Abs(c.Center.Y - expectedY)
	}
	sort.Float64s(residuals)
	medianResidual := residuals[len(residuals)/2]
	_ = medianResidual // used below in posScore

	// Calculate median width (more important than area for detecting bleeding)
	widths := make([]float64, len(contacts))
	for i, c := range contacts {
		widths[i] = float64(c.Bounds.Width)
	}
	sort.Float64s(widths)
	medianWidth := widths[len(widths)/2]

	// Calculate median height
	heights := make([]float64, len(contacts))
	for i, c := range contacts {
		heights[i] = float64(c.Bounds.Height)
	}
	sort.Float64s(heights)
	medianHeight := heights[len(heights)/2]

	// Threshold for position outliers: use IQR of residuals from the line
	q1Idx := len(residuals) / 4
	q3Idx := len(residuals) * 3 / 4
	iqrResidual := residuals[q3Idx] - residuals[q1Idx]
	thresholdY := iqrResidual * 3.0
	if thresholdY < 5.0 {
		thresholdY = 5.0 // minimum 5px threshold to avoid removing contacts on a clean line
	}

	// Keep at least (1 - maxFraction) of contacts
	minKeep := int(float64(len(contacts)) * (1.0 - maxFraction))
	if minKeep < 2 {
		minKeep = 2
	}

	// Score each contact by how "normal" it is
	type scoredContact struct {
		contact Contact
		score   float64
		reason  string
	}
	scored := make([]scoredContact, len(contacts))
	for i, c := range contacts {
		expectedY := lineSlope*c.Center.X + lineIntercept
		yDist := math.Abs(c.Center.Y - expectedY)

		// Width score: how close to median width (0 = same, higher = worse)
		widthRatio := float64(c.Bounds.Width) / medianWidth
		if widthRatio < 1 {
			widthRatio = 1 / widthRatio
		}
		widthScore := (widthRatio - 1.0) * 3.0 // Weight width heavily

		// Height score
		heightRatio := float64(c.Bounds.Height) / medianHeight
		if heightRatio < 1 {
			heightRatio = 1 / heightRatio
		}
		heightScore := (heightRatio - 1.0) * 1.5

		// Position score
		posScore := yDist / math.Max(thresholdY, 1)

		// Total score
		totalScore := widthScore + heightScore + posScore

		reason := ""
		if widthScore > 0.5 {
			reason = fmt.Sprintf("width %.0f vs median %.0f", float64(c.Bounds.Width), medianWidth)
		}

		scored[i] = scoredContact{
			contact: c,
			score:   totalScore,
			reason:  reason,
		}
	}

	// Sort by score (best first)
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score < scored[j].score
	})

	// Keep contacts with good scores
	var result []Contact
	removedCount := 0
	for i, sc := range scored {
		// Always keep up to minKeep
		if i < minKeep {
			result = append(result, sc.contact)
		} else if sc.score < 1.0 {
			// Keep if score is reasonable
			result = append(result, sc.contact)
		} else {
			removedCount++
		}
	}

	if removedCount > 0 {
		fmt.Printf("Removed %d outliers from detection\n", removedCount)
	}

	// Re-sort by X position
	sort.Slice(result, func(i, j int) bool {
		return result[i].Center.X < result[j].Center.X
	})

	return result
}
