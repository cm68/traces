package alignment

import (
	"fmt"
	"image"
	"math"
	"sort"
	"sync"

	"pcb-tracer/internal/board"
	"pcb-tracer/pkg/geometry"

	"gocv.io/x/gocv"
)

// GridBasedRescue creates a regular grid of candidate positions aligned with seed contacts.
// Every seed has an exactly coincident candidate rectangle.
// The pitch is derived such that all pairwise seed offsets are integer multiples of it.
// Grid extends from image margin to margin.
// Scores each candidate by gold pixel coverage and selects the top expectedCount.
func GridBasedRescue(img gocv.Mat, seeds []Contact, lineParams *ContactLineParams, expectedCount int, isHorizontal bool, dpi float64, spec board.Spec) ([]geometry.RectInt, []Contact) {
	if len(seeds) < 2 {
		return nil, seeds
	}

	// Sort seeds by position along the line
	sorted := make([]Contact, len(seeds))
	copy(sorted, seeds)
	sort.Slice(sorted, func(i, j int) bool {
		if isHorizontal {
			return sorted[i].Bounds.X < sorted[j].Bounds.X
		}
		return sorted[i].Bounds.Y < sorted[j].Bounds.Y
	})

	// Determine candidate dimensions: use spec when available, fall back to seed median
	var medianWidth, medianHeight int
	if dpi > 0 && spec != nil && spec.ContactSpec() != nil && spec.ContactSpec().WidthInches > 0 {
		medianWidth = int(math.Round(spec.ContactSpec().WidthInches * dpi))
		medianHeight = int(math.Round(spec.ContactSpec().HeightInches * dpi))
	} else {
		widths := make([]int, len(sorted))
		heights := make([]int, len(sorted))
		for i, c := range sorted {
			widths[i] = c.Bounds.Width
			heights[i] = c.Bounds.Height
		}
		sort.Ints(widths)
		sort.Ints(heights)
		medianWidth = widths[len(widths)/2]
		medianHeight = heights[len(heights)/2]
	}

	// Collect seed centers along the contact line direction
	centers := make([]float64, len(sorted))
	for i, c := range sorted {
		if isHorizontal {
			centers[i] = c.Center.X
		} else {
			centers[i] = c.Center.Y
		}
	}

	// Determine pitch: use spec pitch when available, fall back to histogram
	var pitch float64
	if dpi > 0 && spec != nil && spec.ContactSpec() != nil && spec.ContactSpec().PitchInches > 0 {
		pitch = spec.ContactSpec().PitchInches * dpi
		fmt.Printf("  Pitch from spec: %.2f px (%.4f\" * %.0f DPI)\n", pitch, spec.ContactSpec().PitchInches, dpi)
	} else {
		minPitch := float64(medianWidth) * 1.5
		pitch = findBestFitPitch(centers, minPitch)
		fmt.Printf("  Pitch from histogram: %.2f px (no spec/DPI available)\n", pitch)
	}

	// Fit the contact line from seed centers using linear regression.
	// For horizontal edges: Y = slope * X + intercept
	// This captures the actual rotation of the contact row.
	var sumX, sumY, sumXY, sumX2 float64
	n := float64(len(sorted))
	for _, c := range sorted {
		var px, py float64
		if isHorizontal {
			px, py = c.Center.X, c.Center.Y
		} else {
			px, py = c.Center.Y, c.Center.X
		}
		sumX += px
		sumY += py
		sumXY += px * py
		sumX2 += px * px
	}
	denom := n*sumX2 - sumX*sumX
	var lineSlope, lineIntercept float64
	if math.Abs(denom) > 0.001 {
		lineSlope = (n*sumXY - sumX*sumY) / denom
		lineIntercept = (sumY - lineSlope*sumX) / n
	} else {
		// Fallback: use median cross position, zero slope
		crossPositions := make([]float64, len(sorted))
		for i, c := range sorted {
			if isHorizontal {
				crossPositions[i] = c.Center.Y
			} else {
				crossPositions[i] = c.Center.X
			}
		}
		sort.Float64s(crossPositions)
		lineIntercept = crossPositions[len(crossPositions)/2]
	}

	// crossCenterAt returns the Y (or X for vertical) center for a given position along the edge
	crossCenterAt := func(pos float64) float64 {
		return lineSlope*pos + lineIntercept
	}

	// Find best anchor seed - the one whose center best fits the grid
	anchorIdx := findBestAnchorIndex(centers, pitch)
	anchorCenter := centers[anchorIdx]

	halfW := float64(medianWidth) / 2

	// medianY for logging (at anchor position)
	medianCrossCenter := crossCenterAt(anchorCenter)
	medianY := int(math.Round(medianCrossCenter - float64(medianHeight)/2))
	lineAngleDeg := math.Atan(lineSlope) * 180 / math.Pi

	fmt.Printf("Grid rescue: pitch=%.2f, width=%d, height=%d, medianY=%d, lineAngle=%.2f°\n",
		pitch, medianWidth, medianHeight, medianY, lineAngleDeg)
	fmt.Printf("  Anchor: seed %d at center=%.1f\n", anchorIdx, anchorCenter)

	// Project grid from anchor center to both image margins
	imgExtent := float64(img.Cols())
	if !isHorizontal {
		imgExtent = float64(img.Rows())
	}

	// Calculate grid positions from centers: anchorCenter + k*pitch, then offset to top-left
	var expectedPositions []geometry.RectInt

	// Go backwards from anchor to left margin
	for k := 0; ; k++ {
		center := anchorCenter - float64(k)*pitch
		left := center - halfW
		if left < -float64(medianWidth) {
			break
		}
		// Y position follows the fitted line
		crossCenter := crossCenterAt(center)
		crossY := int(math.Round(crossCenter - float64(medianHeight)/2))
		var rect geometry.RectInt
		if isHorizontal {
			rect = geometry.RectInt{
				X:      int(math.Round(left)),
				Y:      crossY,
				Width:  medianWidth,
				Height: medianHeight,
			}
		} else {
			rect = geometry.RectInt{
				X:      crossY,
				Y:      int(math.Round(left)),
				Width:  medianWidth,
				Height: medianHeight,
			}
		}
		expectedPositions = append(expectedPositions, rect)
	}

	// Reverse so they're in left-to-right order
	for i, j := 0, len(expectedPositions)-1; i < j; i, j = i+1, j-1 {
		expectedPositions[i], expectedPositions[j] = expectedPositions[j], expectedPositions[i]
	}

	// Go forwards from anchor to right margin (skip k=0, already added)
	for k := 1; ; k++ {
		center := anchorCenter + float64(k)*pitch
		left := center - halfW
		if left > imgExtent {
			break
		}
		crossCenter := crossCenterAt(center)
		crossY := int(math.Round(crossCenter - float64(medianHeight)/2))
		var rect geometry.RectInt
		if isHorizontal {
			rect = geometry.RectInt{
				X:      int(math.Round(left)),
				Y:      crossY,
				Width:  medianWidth,
				Height: medianHeight,
			}
		} else {
			rect = geometry.RectInt{
				X:      crossY,
				Y:      int(math.Round(left)),
				Width:  medianWidth,
				Height: medianHeight,
			}
		}
		expectedPositions = append(expectedPositions, rect)
	}

	fmt.Printf("  Generated %d candidate positions from margin to margin\n", len(expectedPositions))

	// Score each candidate by gold pixel coverage (parallel)
	type scoredCandidate struct {
		rect  geometry.RectInt
		score float64
	}
	scored := make([]scoredCandidate, len(expectedPositions))

	var wg sync.WaitGroup
	for i, rect := range expectedPositions {
		wg.Add(1)
		go func(idx int, r geometry.RectInt) {
			defer wg.Done()
			scored[idx] = scoredCandidate{rect: r, score: scoreRectForGold(img, r)}
		}(i, rect)
	}
	wg.Wait()

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Select top expectedCount candidates
	topCount := expectedCount
	if topCount > len(scored) {
		topCount = len(scored)
	}

	// Convert to contacts
	contacts := make([]Contact, topCount)
	for i := 0; i < topCount; i++ {
		rect := scored[i].rect
		contacts[i] = Contact{
			Bounds: rect,
			Center: geometry.Point2D{
				X: float64(rect.X) + float64(rect.Width)/2,
				Y: float64(rect.Y) + float64(rect.Height)/2,
			},
			Pass: PassRescue,
		}
	}

	// Sort contacts by X position
	sort.Slice(contacts, func(i, j int) bool {
		return contacts[i].Center.X < contacts[j].Center.X
	})

	fmt.Printf("  Selected top %d candidates by gold score (top score=%.2f, cutoff=%.2f)\n",
		topCount, scored[0].score, scored[topCount-1].score)

	return expectedPositions, contacts
}

// scoreRectForGold scores a rectangle by the percentage of gold-colored pixels.
// Uses HSV color space to detect gold/yellow tones.
func scoreRectForGold(img gocv.Mat, rect geometry.RectInt) float64 {
	// Clamp rectangle to image bounds
	x := rect.X
	y := rect.Y
	w := rect.Width
	h := rect.Height

	if x < 0 {
		w += x
		x = 0
	}
	if y < 0 {
		h += y
		y = 0
	}
	if x+w > img.Cols() {
		w = img.Cols() - x
	}
	if y+h > img.Rows() {
		h = img.Rows() - y
	}
	if w <= 0 || h <= 0 {
		return 0
	}

	// Extract ROI
	roi := img.Region(image.Rect(x, y, x+w, y+h))
	defer roi.Close()

	// Convert to HSV
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(roi, &hsv, gocv.ColorBGRToHSV)

	// Create gold mask - use typical gold/yellow HSV range
	mask := gocv.NewMat()
	defer mask.Close()
	lower := gocv.NewScalar(15, 80, 120, 0)  // Gold hue range
	upper := gocv.NewScalar(35, 255, 255, 0)
	gocv.InRangeWithScalar(hsv, lower, upper, &mask)

	// Count gold pixels
	goldPixels := gocv.CountNonZero(mask)
	totalPixels := w * h

	return float64(goldPixels) / float64(totalPixels)
}

// findBestAnchorIndex finds the seed index that best aligns with the grid.
func findBestAnchorIndex(positions []float64, pitch float64) int {
	if len(positions) == 0 || pitch == 0 {
		return 0
	}

	bestIdx := 0
	bestError := math.MaxFloat64

	for i, pos := range positions {
		totalError := 0.0
		for j, other := range positions {
			if i == j {
				continue
			}
			interval := math.Abs(other - pos)
			ratio := interval / pitch
			nearest := math.Round(ratio)
			if nearest < 1 {
				nearest = 1
			}
			err := math.Abs(interval - nearest*pitch)
			totalError += err
		}
		if totalError < bestError {
			bestError = totalError
			bestIdx = i
		}
	}

	return bestIdx
}

// findBestFitPitch finds the pitch that best explains all pairwise intervals.
// Uses a histogram approach: for each interval, compute candidate pitches (interval/1, interval/2, ...)
// and find the pitch value with the most votes.
// minPitch is the minimum allowed pitch (should be > contact width).
func findBestFitPitch(positions []float64, minPitch float64) float64 {
	if len(positions) < 2 {
		return 0
	}

	// Collect all pairwise intervals
	var intervals []float64
	for i := 0; i < len(positions); i++ {
		for j := i + 1; j < len(positions); j++ {
			interval := positions[j] - positions[i]
			if interval > 0 {
				intervals = append(intervals, interval)
			}
		}
	}

	if len(intervals) == 0 {
		return minPitch
	}

	// For each interval, generate candidate pitch values: interval/k for k=1,2,3,...
	// Each candidate is a "vote" for that pitch value
	// Use a histogram with bins to accumulate votes

	// First find the range of possible pitches
	sort.Float64s(intervals)
	maxPitch := intervals[len(intervals)-1] // largest interval could be 1x pitch

	// Bin width - use 1 pixel resolution
	binWidth := 1.0
	numBins := int(maxPitch/binWidth) + 1
	histogramCount := make([]int, numBins)
	histogramSum := make([]float64, numBins) // Track sum for precise average

	// For each interval, vote for all possible pitch values
	for _, interval := range intervals {
		// interval = k * pitch, so pitch = interval / k
		// Try k = 1, 2, 3, ... up to where pitch would be < minPitch
		for k := 1; ; k++ {
			candidatePitch := interval / float64(k)
			if candidatePitch < minPitch {
				break
			}
			bin := int(candidatePitch / binWidth)
			if bin >= 0 && bin < numBins {
				histogramCount[bin]++
				histogramSum[bin] += candidatePitch
			}
		}
	}

	// Find the bin with the most votes
	bestBin := 0
	bestVotes := 0
	for bin, votes := range histogramCount {
		if votes > bestVotes {
			bestVotes = votes
			bestBin = bin
		}
	}

	// The pitch is the precise average of votes in the winning bin
	pitch := histogramSum[bestBin] / float64(histogramCount[bestBin])

	fmt.Printf("  Pitch histogram: %d intervals, best bin=%d with %d votes, pitch=%.2f\n",
		len(intervals), bestBin, bestVotes, pitch)

	return pitch
}

