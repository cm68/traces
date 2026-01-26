package alignment

import (
	"fmt"
	"image"
	"math"
	"sort"
	"sync"

	"pcb-tracer/pkg/geometry"

	"gocv.io/x/gocv"
)

// GridBasedRescue creates a regular grid of candidate positions aligned with seed contacts.
// Every seed has an exactly coincident candidate rectangle.
// The pitch is derived such that all pairwise seed offsets are integer multiples of it.
// Grid extends from image margin to margin.
// Scores each candidate by gold pixel coverage and selects the top expectedCount.
func GridBasedRescue(img gocv.Mat, seeds []Contact, lineParams *ContactLineParams, expectedCount int, isHorizontal bool) ([]geometry.RectInt, []Contact) {
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

	// Use the median width and height from seeds (these define candidate dimensions)
	widths := make([]int, len(sorted))
	heights := make([]int, len(sorted))
	for i, c := range sorted {
		widths[i] = c.Bounds.Width
		heights[i] = c.Bounds.Height
	}
	sort.Ints(widths)
	sort.Ints(heights)
	medianWidth := widths[len(widths)/2]
	medianHeight := heights[len(heights)/2]

	// Collect left edges (Bounds.X for horizontal)
	leftEdges := make([]float64, len(sorted))
	for i, c := range sorted {
		if isHorizontal {
			leftEdges[i] = float64(c.Bounds.X)
		} else {
			leftEdges[i] = float64(c.Bounds.Y)
		}
	}

	// Find the pitch: the value N where every pairwise difference is k*N for integer k
	// Minimum pitch must be > width (contacts can't overlap)
	minPitch := float64(medianWidth) * 1.5
	pitch := findBestFitPitch(leftEdges, minPitch)

	// Use median Y (top edge) for the horizontal line
	topEdges := make([]int, len(sorted))
	for i, c := range sorted {
		if isHorizontal {
			topEdges[i] = c.Bounds.Y
		} else {
			topEdges[i] = c.Bounds.X
		}
	}
	sort.Ints(topEdges)
	medianY := topEdges[len(topEdges)/2]

	// Find best anchor seed - the one whose position best fits the grid
	anchorIdx := findBestAnchorIndex(leftEdges, pitch)
	anchorX := leftEdges[anchorIdx]

	fmt.Printf("Grid rescue: pitch=%.2f, width=%d, height=%d, medianY=%d\n",
		pitch, medianWidth, medianHeight, medianY)
	fmt.Printf("  Anchor: seed %d at X=%.1f\n", anchorIdx, anchorX)

	// Project grid from anchor to both image margins
	imgExtent := float64(img.Cols())
	if !isHorizontal {
		imgExtent = float64(img.Rows())
	}

	// Calculate grid positions: anchorX + k*pitch for all integer k that stay in image
	var expectedPositions []geometry.RectInt

	// Go backwards from anchor to left margin
	for k := 0; ; k++ {
		x := anchorX - float64(k)*pitch
		if x < -float64(medianWidth) {
			break
		}
		var rect geometry.RectInt
		if isHorizontal {
			rect = geometry.RectInt{
				X:      int(math.Round(x)),
				Y:      medianY,
				Width:  medianWidth,
				Height: medianHeight,
			}
		} else {
			rect = geometry.RectInt{
				X:      medianY,
				Y:      int(math.Round(x)),
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
		x := anchorX + float64(k)*pitch
		if x > imgExtent {
			break
		}
		var rect geometry.RectInt
		if isHorizontal {
			rect = geometry.RectInt{
				X:      int(math.Round(x)),
				Y:      medianY,
				Width:  medianWidth,
				Height: medianHeight,
			}
		} else {
			rect = geometry.RectInt{
				X:      medianY,
				Y:      int(math.Round(x)),
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

