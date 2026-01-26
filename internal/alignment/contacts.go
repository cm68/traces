// Package alignment provides image alignment algorithms for PCB scans.
package alignment

import (
	"fmt"
	"image"
	"sort"

	"pcb-tracer/internal/board"
	"pcb-tracer/pkg/geometry"

	"gocv.io/x/gocv"
)

// DetectContactsFromImage detects gold edge contacts from a Go image.Image.
// This is the main entry point for detection from the UI.
// If dpi > 0, it will be used to calculate expected contact sizes.
func DetectContactsFromImage(img image.Image, spec board.Spec, dpi float64) (*DetectionResult, error) {
	return DetectContactsFromImageWithColors(img, spec, dpi, nil)
}

// DetectContactsFromImageWithColors detects contacts using custom color parameters.
// If colorParams is nil, uses default parameters from spec.
// This function tries both top and bottom edges and selects the best one.
func DetectContactsFromImageWithColors(img image.Image, spec board.Spec, dpi float64, colorParams *DetectionParams) (*DetectionResult, error) {
	if img == nil {
		return nil, fmt.Errorf("nil image")
	}

	// Convert Go image to OpenCV Mat
	mat, err := imageToMat(img)
	if err != nil {
		return nil, fmt.Errorf("failed to convert image: %w", err)
	}
	defer mat.Close()

	return DetectGoldContactsWithColors(mat, spec, dpi, false, colorParams)
}

// DetectContactsOnTopEdge detects contacts only on the top edge of the image.
// This should be used after the image has been rotated to have contacts at top.
// It runs rescue pass to fill in missing contacts based on the grid pattern.
func DetectContactsOnTopEdge(img image.Image, spec board.Spec, dpi float64, colorParams *DetectionParams) (*DetectionResult, error) {
	if img == nil {
		return nil, fmt.Errorf("nil image")
	}

	// Convert Go image to OpenCV Mat
	mat, err := imageToMat(img)
	if err != nil {
		return nil, fmt.Errorf("failed to convert image: %w", err)
	}
	defer mat.Close()

	return detectContactsOnTopEdgeOnly(mat, spec, dpi, colorParams)
}

// DetectGoldContacts detects gold edge card contacts in an image.
// Auto-detects which edge has contacts and returns detection results.
// Always returns a result (even with 0 contacts) so partial results can be visualized.
// If dpi > 0, it will be used to calculate expected contact sizes.
func DetectGoldContacts(img gocv.Mat, spec board.Spec, dpi float64, debug bool) (*DetectionResult, error) {
	return DetectGoldContactsWithColors(img, spec, dpi, debug, nil)
}

// DetectGoldContactsWithColors detects contacts with optional custom color parameters.
func DetectGoldContactsWithColors(img gocv.Mat, spec board.Spec, dpi float64, debug bool, colorParams *DetectionParams) (*DetectionResult, error) {
	if img.Empty() {
		return nil, fmt.Errorf("empty image")
	}

	var params DetectionParams
	if colorParams != nil {
		// Use provided color params, but get size params from spec
		params = *colorParams
		if dpi > 0 {
			sizeParams := ParamsFromSpecWithDPI(spec, dpi)
			params.MinArea = sizeParams.MinArea
			params.MaxArea = sizeParams.MaxArea
			params.AspectMin = sizeParams.AspectMin
			params.AspectMax = sizeParams.AspectMax
		}
	} else if dpi > 0 {
		params = ParamsFromSpecWithDPI(spec, dpi)
	} else {
		params = ParamsFromSpec(spec)
	}

	// Find which edge has contacts (returns seed angle from raw detected contacts)
	edge, contacts, boardBounds, searchBounds, expectedPositions, seedAngle := findContactEdge(img, spec, params, debug)

	// Remove size/position outliers
	if len(contacts) > 5 {
		beforeOutlier := len(contacts)
		contacts = removeOutliers(contacts, 0.10)
		if len(contacts) < beforeOutlier {
			fmt.Printf("Removed %d outliers from detection\n", beforeOutlier-len(contacts))
		}
	}

	// Normalize contact widths - contacts bleeding into insulator will be too wide
	if len(contacts) > 5 {
		contacts = normalizeContactWidths(contacts)
	}

	// Calculate DPI from contact spacing if not provided
	resultDPI := dpi
	if resultDPI == 0 {
		resultDPI = calculateDPI(contacts, spec)
	}

	// Use the seed angle (from raw detected contacts before grid alignment)
	contactAngle := seedAngle

	result := &DetectionResult{
		Contacts:          contacts,
		ExpectedPositions: expectedPositions,
		Edge:              edge,
		Rotation:          0,
		BoardBounds:       boardBounds,
		SearchBounds:      searchBounds,
		DPI:               resultDPI,
		ContactAngle:      contactAngle,
	}

	// If contacts not at top, determine rotation needed
	switch edge {
	case "bottom":
		result.Rotation = 180
	case "left":
		result.Rotation = 90
	case "right":
		result.Rotation = 270
	}

	// Get expected count from spec
	expectedCount := 50
	if spec != nil && spec.ContactSpec() != nil {
		expectedCount = spec.ContactSpec().Count
	}

	// Log the contact angle for debugging
	if len(contacts) > 1 {
		firstY := contacts[0].Center.Y
		lastY := contacts[len(contacts)-1].Center.Y
		fmt.Printf("Contact line: Y ranges from %.1f to %.1f (delta=%.1f), angle=%.2f°\n",
			firstY, lastY, lastY-firstY, contactAngle)
	}

	// Return result with error if not enough contacts (but still return the result)
	if len(contacts) < expectedCount {
		return result, fmt.Errorf("found %d contacts (need %d)", len(contacts), expectedCount)
	}

	return result, nil
}

// findContactEdge finds which edge has the gold contacts.
// Returns the edge name, contacts (seeds + rescued), board bounds, search bounds, expected positions, and seed contact angle.
func findContactEdge(img gocv.Mat, spec board.Spec, params DetectionParams, debug bool) (string, []Contact, geometry.RectInt, geometry.RectInt, []geometry.RectInt, float64) {
	// Detect board bounds
	boardBounds := detectBoardBounds(img)

	// Create gold mask using detection parameters
	goldMask := createGoldMaskWithParams(img, params)
	defer goldMask.Close()

	// Get expected count from spec (needed for edge scoring)
	expectedCount := 50
	if spec != nil && spec.ContactSpec() != nil {
		expectedCount = spec.ContactSpec().Count
	}

	// Try each edge - return seeds directly
	bestEdge := "top"
	var bestContacts []Contact
	var bestSearchBounds geometry.RectInt
	var bestLineParams *ContactLineParams
	var bestSeedAngle float64
	bestCount := 0

	edges := []string{"top", "bottom"}
	for _, edge := range edges {
		// Get seed contacts and line params
		seedContacts, searchBounds, lineParams := detectContactsOnEdge(img, goldMask, boardBounds, edge, spec, params)

		if len(seedContacts) < 2 {
			fmt.Printf("  %s edge: %d seed contacts (skipping)\n", edge, len(seedContacts))
			continue
		}

		// Debug: print seed contact positions and sample their colors
		fmt.Printf("  %s seed contacts (%d): ", edge, len(seedContacts))
		for i, c := range seedContacts {
			if i < 5 || i >= len(seedContacts)-2 {
				// Sample color at seed center
				cx, cy := int(c.Center.X), int(c.Center.Y)
				var r, g, b uint8
				if cx >= 0 && cx < img.Cols() && cy >= 0 && cy < img.Rows() {
					pixel := img.GetVecbAt(cy, cx)
					b, g, r = pixel[0], pixel[1], pixel[2]
				}
				fmt.Printf("(%.0f,%.0f RGB=%d/%d/%d) ", c.Center.X, c.Center.Y, r, g, b)
			} else if i == 5 {
				fmt.Printf("... ")
			}
		}
		fmt.Println()

		// Calculate angle from seed contacts
		seedAngle := CalculateContactLineAngle(seedContacts, edge)
		fmt.Printf("  %s edge: %d seeds, angle=%.2f°, lineParams=%v\n", edge, len(seedContacts), seedAngle, lineParams != nil)

		// Score this edge - prefer edges with:
		// 1. Valid lineParams (required for rescue)
		// 2. Count close to expected (not too few, not too many)
		edgeScore := len(seedContacts)
		if lineParams == nil {
			edgeScore = 0 // No rescue possible without lineParams
		} else if len(seedContacts) > expectedCount*3/2 {
			// Too many contacts suggests false positives (e.g., double row)
			edgeScore = expectedCount / 2
		}

		if edgeScore > bestCount {
			bestCount = edgeScore
			bestEdge = edge
			bestContacts = seedContacts
			bestSearchBounds = searchBounds
			bestLineParams = lineParams
			bestSeedAngle = seedAngle
		}
	}

	fmt.Printf("  Selected edge: %s (%d seeds, angle: %.2f°)\n", bestEdge, bestCount, bestSeedAngle)

	// Debug: log image size and contact Y range
	if len(bestContacts) > 0 {
		minY, maxY := bestContacts[0].Center.Y, bestContacts[0].Center.Y
		for _, c := range bestContacts {
			if c.Center.Y < minY {
				minY = c.Center.Y
			}
			if c.Center.Y > maxY {
				maxY = c.Center.Y
			}
		}
		fmt.Printf("  Image: %dx%d, contacts Y range: %.0f-%.0f\n",
			img.Cols(), img.Rows(), minY, maxY)
	}

	// Sort contacts by X position
	sort.Slice(bestContacts, func(i, j int) bool {
		return bestContacts[i].Center.X < bestContacts[j].Center.X
	})

	// Run grid-based rescue to find missing contacts and get expected positions
	var expectedPositions []geometry.RectInt
	if len(bestContacts) >= 2 && bestLineParams != nil {
		isHorizontal := (bestEdge == "top" || bestEdge == "bottom")
		expectedPositions, bestContacts = GridBasedRescue(img, bestContacts, bestLineParams, expectedCount, isHorizontal)
	}

	return bestEdge, bestContacts, boardBounds, bestSearchBounds, expectedPositions, bestSeedAngle
}

// detectContactsOnTopEdgeOnly detects contacts only on the top edge.
// This is used after the image has been rotated so contacts are at top.
func detectContactsOnTopEdgeOnly(img gocv.Mat, spec board.Spec, dpi float64, colorParams *DetectionParams) (*DetectionResult, error) {
	if img.Empty() {
		return nil, fmt.Errorf("empty image")
	}

	var params DetectionParams
	if colorParams != nil {
		// Use provided color params, but get size params from spec
		params = *colorParams
		if dpi > 0 {
			sizeParams := ParamsFromSpecWithDPI(spec, dpi)
			params.MinArea = sizeParams.MinArea
			params.MaxArea = sizeParams.MaxArea
			params.AspectMin = sizeParams.AspectMin
			params.AspectMax = sizeParams.AspectMax
		}
	} else if dpi > 0 {
		params = ParamsFromSpecWithDPI(spec, dpi)
	} else {
		params = ParamsFromSpec(spec)
	}

	// Detect board bounds
	boardBounds := detectBoardBounds(img)

	// Create gold mask using detection parameters
	goldMask := createGoldMaskWithParams(img, params)
	defer goldMask.Close()

	// Detect on top edge only
	fmt.Printf("Detecting contacts on TOP edge only\n")
	seedContacts, searchBounds, lineParams := detectContactsOnEdge(img, goldMask, boardBounds, "top", spec, params)

	if len(seedContacts) < 2 {
		return &DetectionResult{
			Contacts:     seedContacts,
			Edge:         "top",
			BoardBounds:  boardBounds,
			SearchBounds: searchBounds,
		}, fmt.Errorf("found only %d contacts on top edge (need at least 2)", len(seedContacts))
	}

	// Calculate seed angle
	seedAngle := CalculateContactLineAngle(seedContacts, "top")
	fmt.Printf("  Top edge: %d seeds, angle=%.2f°\n", len(seedContacts), seedAngle)

	// Sort contacts by X position
	sort.Slice(seedContacts, func(i, j int) bool {
		return seedContacts[i].Center.X < seedContacts[j].Center.X
	})

	// Get expected count from spec
	expectedCount := 50
	if spec != nil && spec.ContactSpec() != nil {
		expectedCount = spec.ContactSpec().Count
	}

	// Run grid-based rescue to find missing contacts and get expected positions
	var expectedPositions []geometry.RectInt
	var contacts []Contact
	if lineParams != nil {
		expectedPositions, contacts = GridBasedRescue(img, seedContacts, lineParams, expectedCount, true)
	} else {
		contacts = seedContacts
	}

	// Remove size/position outliers
	if len(contacts) > 5 {
		beforeOutlier := len(contacts)
		contacts = removeOutliers(contacts, 0.10)
		if len(contacts) < beforeOutlier {
			fmt.Printf("Removed %d outliers from detection\n", beforeOutlier-len(contacts))
		}
	}

	// Normalize contact widths
	if len(contacts) > 5 {
		contacts = normalizeContactWidths(contacts)
	}

	// Calculate DPI from contact spacing if not provided
	resultDPI := dpi
	if resultDPI == 0 {
		resultDPI = calculateDPI(contacts, spec)
	}

	result := &DetectionResult{
		Contacts:          contacts,
		ExpectedPositions: expectedPositions,
		Edge:              "top",
		Rotation:          0, // Already rotated, no further rotation needed
		BoardBounds:       boardBounds,
		SearchBounds:      searchBounds,
		DPI:               resultDPI,
		ContactAngle:      seedAngle,
	}

	// Log contact info
	if len(contacts) > 1 {
		firstY := contacts[0].Center.Y
		lastY := contacts[len(contacts)-1].Center.Y
		fmt.Printf("Contact line: Y ranges from %.1f to %.1f (delta=%.1f), angle=%.2f°\n",
			firstY, lastY, lastY-firstY, seedAngle)
	}

	// Return result with error if not enough contacts
	if len(contacts) < expectedCount {
		return result, fmt.Errorf("found %d contacts (need %d)", len(contacts), expectedCount)
	}

	return result, nil
}
