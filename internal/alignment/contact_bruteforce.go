package alignment

import (
	"fmt"
	"image"
	"runtime"
	"sort"
	"sync"

	"pcb-tracer/internal/board"
	"pcb-tracer/pkg/geometry"

	"gocv.io/x/gocv"
)

// NewContactTemplateFromResult creates a template from a detection result.
func NewContactTemplateFromResult(result *DetectionResult) *ContactTemplate {
	if result == nil || len(result.Contacts) < 5 {
		return nil
	}

	minW, maxW := result.Contacts[0].Bounds.Width, result.Contacts[0].Bounds.Width
	minH, maxH := result.Contacts[0].Bounds.Height, result.Contacts[0].Bounds.Height
	var sumW, sumH float64

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
		sumW += float64(w)
		sumH += float64(h)
	}

	avgW := sumW / float64(len(result.Contacts))
	avgH := sumH / float64(len(result.Contacts))

	// Calculate aspect based on averages - use tight bounds since contacts are regular
	avgAspect := avgH / avgW
	minAspect := avgAspect * 0.75 // 25% smaller aspect
	maxAspect := avgAspect * 1.25 // 25% larger aspect

	return &ContactTemplate{
		AvgWidth:  avgW,
		AvgHeight: avgH,
		MinWidth:  int(avgW * 0.7), // 30% smaller than average
		MaxWidth:  int(avgW * 1.3), // 30% larger than average
		MinHeight: int(avgH * 0.7),
		MaxHeight: int(avgH * 1.3),
		MinAspect: minAspect,
		MaxAspect: maxAspect,
	}
}

// BruteForceSearchWithTemplate searches the entire image for contacts matching the template.
func BruteForceSearchWithTemplate(img image.Image, template *ContactTemplate, params DetectionParams, spec board.Spec) (*DetectionResult, error) {
	if img == nil {
		return nil, fmt.Errorf("nil image")
	}
	if template == nil {
		return nil, fmt.Errorf("nil template")
	}

	mat, err := imageToMat(img)
	if err != nil {
		return nil, fmt.Errorf("failed to convert image: %w", err)
	}
	defer mat.Close()

	return bruteForceSearchMat(mat, template, params, spec)
}

// bruteForceSearchMat does a full-image search using template dimensions.
// Uses parallel processing with overlapping grid regions.
func bruteForceSearchMat(img gocv.Mat, template *ContactTemplate, params DetectionParams, spec board.Spec) (*DetectionResult, error) {
	if img.Empty() {
		return nil, fmt.Errorf("empty image")
	}

	fmt.Printf("Brute force search: looking for contacts %d-%d x %d-%d px, aspect %.1f-%.1f\n",
		template.MinWidth, template.MaxWidth, template.MinHeight, template.MaxHeight,
		template.MinAspect, template.MaxAspect)

	// Create gold mask
	goldMask := createGoldMaskWithParams(img, params)
	defer goldMask.Close()

	imgH := img.Rows()
	imgW := img.Cols()

	// Calculate grid parameters
	numCPU := runtime.NumCPU()
	overlap := int(template.AvgHeight * 2) // 2x contact height overlap
	stripHeight := (imgH + numCPU - 1) / numCPU
	if stripHeight < overlap*3 {
		stripHeight = overlap * 3 // Ensure strips are at least 3x overlap
	}

	fmt.Printf("Brute force: using %d parallel workers, strip height=%d, overlap=%d\n",
		numCPU, stripHeight, overlap)

	// Mutex-protected candidates collection
	var mu sync.Mutex
	var allCandidates []Contact

	// Create worker pool
	var wg sync.WaitGroup
	sem := make(chan struct{}, numCPU)

	// Process overlapping strips
	for y := 0; y < imgH; y += stripHeight - overlap {
		y1 := y
		y2 := y + stripHeight
		if y2 > imgH {
			y2 = imgH
		}
		if y1 >= y2 {
			break
		}

		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore

		go func(stripY1, stripY2 int) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore

			// Extract region from gold mask
			region := goldMask.Region(image.Rect(0, stripY1, imgW, stripY2))
			defer region.Close()

			// Find contours in this strip
			contours := gocv.FindContours(region, gocv.RetrievalExternal, gocv.ChainApproxSimple)
			defer contours.Close()

			// Filter contours by template dimensions
			var stripCandidates []Contact
			for i := 0; i < contours.Size(); i++ {
				rect := gocv.BoundingRect(contours.At(i))
				w, h := rect.Dx(), rect.Dy()

				// Check size bounds
				if w < template.MinWidth || w > template.MaxWidth {
					continue
				}
				if h < template.MinHeight || h > template.MaxHeight {
					continue
				}

				// Check aspect ratio (height/width for vertical contacts)
				aspect := float64(h) / float64(w)
				if aspect < template.MinAspect || aspect > template.MaxAspect {
					continue
				}

				// Convert to image coordinates (add strip offset)
				stripCandidates = append(stripCandidates, Contact{
					Bounds: geometry.RectInt{
						X:      rect.Min.X,
						Y:      stripY1 + rect.Min.Y,
						Width:  w,
						Height: h,
					},
					Center: geometry.Point2D{
						X: float64(rect.Min.X) + float64(w)/2,
						Y: float64(stripY1+rect.Min.Y) + float64(h)/2,
					},
					Pass: PassBruteForce,
				})
			}

			// Add to global candidates with mutex
			if len(stripCandidates) > 0 {
				mu.Lock()
				allCandidates = append(allCandidates, stripCandidates...)
				mu.Unlock()
			}
		}(y1, y2)
	}

	wg.Wait()

	// Deduplicate candidates from overlapping regions
	candidates := deduplicateCandidates(allCandidates, template)

	fmt.Printf("Brute force: found %d candidates matching template (after dedup)\n", len(candidates))

	if len(candidates) < 2 {
		return &DetectionResult{
			Contacts: candidates,
			Edge:     "unknown",
		}, fmt.Errorf("only found %d candidates", len(candidates))
	}

	// Sort by X position (assuming horizontal contact row)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Center.X < candidates[j].Center.X
	})

	// Filter to grid pattern
	expectedCount := 50
	if spec != nil && spec.ContactSpec() != nil {
		expectedCount = spec.ContactSpec().Count
	}

	filtered, lineParams := filterToGridStrictWithParams(candidates, spec, params, true)

	// Grid-based rescue: calculate expected positions and score each by color
	if len(filtered) >= 2 && len(filtered) < expectedCount && lineParams != nil {
		_, filtered = GridBasedRescue(img, filtered, lineParams, expectedCount, true, params.DPI, spec)
	}

	// Remove outliers (up to 10% of candidates that are far from the others)
	if len(filtered) > 5 {
		beforeOutlier := len(filtered)
		filtered = removeOutliers(filtered, 0.10)
		if len(filtered) < beforeOutlier {
			fmt.Printf("Brute force: removed %d outliers\n", beforeOutlier-len(filtered))
		}
	}

	// Determine which edge based on Y position
	edge := "top"
	if len(filtered) > 0 {
		avgY := 0.0
		for _, c := range filtered {
			avgY += c.Center.Y
		}
		avgY /= float64(len(filtered))
		imgH := float64(img.Rows())
		if avgY > imgH/2 {
			edge = "bottom"
		}
	}

	// Calculate contact angle
	contactAngle := CalculateContactLineAngle(filtered, edge)

	result := &DetectionResult{
		Contacts:     filtered,
		Edge:         edge,
		ContactAngle: contactAngle,
		BoardBounds:  geometry.RectInt{X: 0, Y: 0, Width: img.Cols(), Height: img.Rows()},
	}

	if len(filtered) > 1 {
		firstY := filtered[0].Center.Y
		lastY := filtered[len(filtered)-1].Center.Y
		fmt.Printf("Brute force result: %d contacts on %s edge, Y delta=%.1f, angle=%.2f°\n",
			len(filtered), edge, lastY-firstY, contactAngle)
	}

	if len(filtered) < expectedCount {
		return result, fmt.Errorf("found %d contacts (need %d)", len(filtered), expectedCount)
	}

	return result, nil
}
