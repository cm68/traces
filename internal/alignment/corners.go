package alignment

import (
	"image"
	"math"
	"sort"

	"pcb-tracer/internal/board"
	"pcb-tracer/pkg/geometry"

	"gocv.io/x/gocv"
)

// CornerDetectionResult holds detected board corners.
type CornerDetectionResult struct {
	Corners     []geometry.Point2D // Ordered: TL, TR, BR, BL
	BoardBounds geometry.Rect
	Confidence  float64
}

// DetectBoardCorners detects the four corners of a PCB board.
// Uses Canny edge detection and contour analysis.
func DetectBoardCorners(img gocv.Mat) (*CornerDetectionResult, error) {
	// Convert to grayscale
	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(img, &gray, gocv.ColorBGRToGray)

	// Blur to reduce noise
	blurred := gocv.NewMat()
	defer blurred.Close()
	gocv.GaussianBlur(gray, &blurred, image.Point{5, 5}, 0, 0, gocv.BorderDefault)

	// Canny edge detection
	edges := gocv.NewMat()
	defer edges.Close()
	gocv.Canny(blurred, &edges, 50, 150)

	// Dilate to connect edge segments
	kernel := gocv.GetStructuringElement(gocv.MorphRect, image.Point{3, 3})
	defer kernel.Close()
	gocv.Dilate(edges, &edges, kernel)

	// Find contours
	contours := gocv.FindContours(edges, gocv.RetrievalExternal, gocv.ChainApproxSimple)
	defer contours.Close()

	if contours.Size() == 0 {
		return nil, nil
	}

	// Find the largest quadrilateral-like contour
	var bestContour gocv.PointVector
	var bestArea float64
	imgArea := float64(img.Cols() * img.Rows())

	for i := 0; i < contours.Size(); i++ {
		contour := contours.At(i)
		area := gocv.ContourArea(contour)

		// Skip if too small or too large
		if area < imgArea*0.1 || area > imgArea*0.95 {
			continue
		}

		// Approximate to polygon
		epsilon := 0.02 * gocv.ArcLength(contour, true)
		approx := gocv.ApproxPolyDP(contour, epsilon, true)

		// Look for quadrilateral (4 vertices)
		if approx.Size() >= 4 && approx.Size() <= 6 {
			if area > bestArea {
				bestArea = area
				// Copy points from approx to bestContour
				if bestContour.Size() > 0 {
					bestContour.Close()
				}
				bestContour = gocv.NewPointVector()
				for j := 0; j < approx.Size(); j++ {
					bestContour.Append(approx.At(j))
				}
			}
		}
		approx.Close()
	}

	if bestContour.Size() == 0 {
		return nil, nil
	}
	defer bestContour.Close()

	// Extract corner points
	corners := extractCorners(bestContour, img.Cols(), img.Rows())
	if len(corners) != 4 {
		return nil, nil
	}

	// Calculate bounds
	minX, minY := corners[0].X, corners[0].Y
	maxX, maxY := corners[0].X, corners[0].Y
	for _, c := range corners[1:] {
		minX = math.Min(minX, c.X)
		minY = math.Min(minY, c.Y)
		maxX = math.Max(maxX, c.X)
		maxY = math.Max(maxY, c.Y)
	}

	return &CornerDetectionResult{
		Corners: corners,
		BoardBounds: geometry.Rect{
			X:      minX,
			Y:      minY,
			Width:  maxX - minX,
			Height: maxY - minY,
		},
		Confidence: bestArea / imgArea,
	}, nil
}

// extractCorners extracts and orders 4 corners from a contour.
// Returns corners in order: top-left, top-right, bottom-right, bottom-left.
func extractCorners(contour gocv.PointVector, imgWidth, imgHeight int) []geometry.Point2D {
	// Get all points
	points := make([]geometry.Point2D, contour.Size())
	for i := 0; i < contour.Size(); i++ {
		pt := contour.At(i)
		points[i] = geometry.Point2D{X: float64(pt.X), Y: float64(pt.Y)}
	}

	// Find image center
	centerX := float64(imgWidth) / 2
	centerY := float64(imgHeight) / 2

	// Classify points by quadrant
	var topLeft, topRight, bottomLeft, bottomRight []geometry.Point2D

	for _, p := range points {
		if p.X < centerX {
			if p.Y < centerY {
				topLeft = append(topLeft, p)
			} else {
				bottomLeft = append(bottomLeft, p)
			}
		} else {
			if p.Y < centerY {
				topRight = append(topRight, p)
			} else {
				bottomRight = append(bottomRight, p)
			}
		}
	}

	// Find the extreme point in each quadrant
	findExtreme := func(pts []geometry.Point2D, compareFunc func(a, b geometry.Point2D) bool) geometry.Point2D {
		if len(pts) == 0 {
			return geometry.Point2D{}
		}
		best := pts[0]
		for _, p := range pts[1:] {
			if compareFunc(p, best) {
				best = p
			}
		}
		return best
	}

	corners := []geometry.Point2D{
		findExtreme(topLeft, func(a, b geometry.Point2D) bool { return a.X+a.Y < b.X+b.Y }),     // TL: minimize x+y
		findExtreme(topRight, func(a, b geometry.Point2D) bool { return a.X-a.Y > b.X-b.Y }),   // TR: maximize x-y
		findExtreme(bottomRight, func(a, b geometry.Point2D) bool { return a.X+a.Y > b.X+b.Y }), // BR: maximize x+y
		findExtreme(bottomLeft, func(a, b geometry.Point2D) bool { return a.Y-a.X > b.Y-b.X }), // BL: maximize y-x
	}

	// Verify all corners were found
	for _, c := range corners {
		if c.X == 0 && c.Y == 0 {
			return nil
		}
	}

	return corners
}

// DetectEjectorHoles finds ejector/mounting holes using Hough circle detection.
func DetectEjectorHoles(img gocv.Mat, contacts []Contact, dpi float64, spec *board.BaseSpec) []geometry.Point2D {
	if len(contacts) == 0 || dpi <= 0 || spec == nil {
		return nil
	}

	// Get expected hole positions from board spec
	holes := spec.Holes()
	if len(holes) == 0 {
		return nil
	}

	// Calculate board position from first contact
	first := contacts[0]
	firstX := first.Center.X
	firstY := first.Center.Y

	// Get contact margin from spec
	margin := 2.125 // S-100 default
	if spec.ContactSpec() != nil {
		margin = spec.ContactSpec().MarginInches
	}

	boardLeftX := firstX - (margin * dpi)
	w, h := spec.Dimensions()
	boardRightX := boardLeftX + (w * dpi)

	// Finger height (edge connector area)
	fingerHeight := 0.3125 // S-100 default
	boardBottomY := firstY + ((fingerHeight + h) * dpi)

	// Search for each expected hole
	var foundHoles []geometry.Point2D

	for _, hole := range holes {
		// Calculate expected position
		var expectedX, expectedY float64
		if hole.XInches < w/2 {
			// Left side hole
			expectedX = boardLeftX + (hole.XInches * dpi)
		} else {
			// Right side hole
			expectedX = boardRightX - ((w - hole.XInches) * dpi)
		}
		expectedY = boardBottomY - ((h - hole.YInches) * dpi)

		// Search in region around expected position
		searchRadius := int(0.5 * dpi)
		holeFound := findHoleInRegion(img, expectedX, expectedY, searchRadius, hole.DiamInches*dpi)

		if holeFound != nil {
			foundHoles = append(foundHoles, *holeFound)
		}
	}

	return foundHoles
}

// findHoleInRegion searches for a circular hole in a region.
func findHoleInRegion(img gocv.Mat, expectedX, expectedY float64, searchRadius int, expectedDiam float64) *geometry.Point2D {
	imgH := img.Rows()
	imgW := img.Cols()

	// Define search region
	rx := max(0, int(expectedX)-searchRadius)
	ry := max(0, int(expectedY)-searchRadius)
	rw := min(searchRadius*2, imgW-rx)
	rh := min(searchRadius*2, imgH-ry)

	if rw <= 0 || rh <= 0 {
		return nil
	}

	// Extract region
	region := img.Region(image.Rect(rx, ry, rx+rw, ry+rh))
	defer region.Close()

	// Convert to grayscale
	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(region, &gray, gocv.ColorBGRToGray)

	// Blur
	blurred := gocv.NewMat()
	defer blurred.Close()
	gocv.GaussianBlur(gray, &blurred, image.Point{9, 9}, 2, 2, gocv.BorderDefault)

	// Hough circles
	minR := max(10, int(expectedDiam*0.3))
	maxR := max(20, int(expectedDiam*0.7))

	circles := gocv.NewMat()
	defer circles.Close()

	gocv.HoughCirclesWithParams(blurred, &circles, gocv.HoughGradient, 1.2, 50,
		80, 25, minR, maxR)

	if circles.Empty() || circles.Cols() == 0 {
		return nil
	}

	// Find darkest circle (holes are dark)
	var bestHole *geometry.Point2D
	bestDark := uint8(255)

	for i := 0; i < circles.Cols(); i++ {
		cx := circles.GetFloatAt(0, i*3)
		cy := circles.GetFloatAt(0, i*3+1)

		ix, iy := int(cx), int(cy)
		if ix >= 0 && ix < gray.Cols() && iy >= 0 && iy < gray.Rows() {
			dark := gray.GetUCharAt(iy, ix)
			if dark < bestDark {
				bestDark = dark
				bestHole = &geometry.Point2D{
					X: float64(rx) + float64(cx),
					Y: float64(ry) + float64(cy),
				}
			}
		}
	}

	return bestHole
}

// OrderCorners orders corner points in a consistent manner: TL, TR, BR, BL.
func OrderCorners(corners []geometry.Point2D) []geometry.Point2D {
	if len(corners) != 4 {
		return corners
	}

	// Sort by Y first to separate top and bottom pairs
	sorted := make([]geometry.Point2D, 4)
	copy(sorted, corners)

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Y < sorted[j].Y
	})

	// Top two points
	topPair := sorted[:2]
	bottomPair := sorted[2:]

	// Sort top pair by X
	sort.Slice(topPair, func(i, j int) bool {
		return topPair[i].X < topPair[j].X
	})

	// Sort bottom pair by X
	sort.Slice(bottomPair, func(i, j int) bool {
		return bottomPair[i].X < bottomPair[j].X
	})

	return []geometry.Point2D{
		topPair[0],    // TL
		topPair[1],    // TR
		bottomPair[1], // BR
		bottomPair[0], // BL
	}
}

// EjectorMark represents a detected ejector registration mark.
type EjectorMark struct {
	Center geometry.Point2D // Center of the hole
	Side   string           // "left" or "right"
}

// DetectEjectorMarksFromImage detects ejector registration marks from a Go image.
func DetectEjectorMarksFromImage(img image.Image, contacts []Contact, dpi float64) []EjectorMark {
	mat, err := imageToMat(img)
	if err != nil {
		return nil
	}
	defer mat.Close()
	return DetectEjectorMarks(mat, contacts, dpi)
}

// DetectEjectorMarks finds the registration holes in the card ejectors at the bottom corners.
// Card ejectors are bright white triangular regions with a small (~2.5mm) hole in the center.
// Returns the hole centers which serve as precise registration marks.
func DetectEjectorMarks(img gocv.Mat, contacts []Contact, dpi float64) []EjectorMark {
	if len(contacts) < 2 || dpi <= 0 {
		return nil
	}

	imgH := img.Rows()
	imgW := img.Cols()

	// Estimate board bounds from contacts
	// Contacts are at top edge, sorted by X
	firstContact := contacts[0]
	lastContact := contacts[len(contacts)-1]

	// Contact margin from left edge is about 2.125" for S-100
	// Board width is about 10"
	boardMarginPx := 2.125 * dpi
	boardLeftX := firstContact.Center.X - boardMarginPx
	boardRightX := lastContact.Center.X + boardMarginPx

	// Board height is about 5.4375" for S-100
	// Contacts are at top, so bottom is about 5.4" down
	boardHeightPx := 5.4375 * dpi
	boardBottomY := firstContact.Center.Y + boardHeightPx

	// Ejector hole is about 2.5mm diameter = 0.0984"
	ejectorHoleDiam := 0.1 * dpi // ~0.1 inches

	// Search region size - ejectors are in corners
	searchSize := int(1.0 * dpi) // 1 inch search region

	var marks []EjectorMark

	// Search bottom-left corner
	leftMark := findEjectorMark(img, int(boardLeftX), int(boardBottomY)-searchSize,
		searchSize, searchSize, ejectorHoleDiam, imgW, imgH)
	if leftMark != nil {
		marks = append(marks, EjectorMark{Center: *leftMark, Side: "left"})
	}

	// Search bottom-right corner
	rightMark := findEjectorMark(img, int(boardRightX)-searchSize, int(boardBottomY)-searchSize,
		searchSize, searchSize, ejectorHoleDiam, imgW, imgH)
	if rightMark != nil {
		marks = append(marks, EjectorMark{Center: *rightMark, Side: "right"})
	}

	return marks
}

// StepEdge represents a detected step edge where the board widens below the connector extension.
type StepEdge struct {
	Corner geometry.Point2D // The corner point where step meets board edge
	Side   string           // "left" or "right"
	EdgeY  float64          // Y coordinate of the horizontal step edge
}

// DetectStepEdgesFromImage detects step edges from a Go image.Image.
func DetectStepEdgesFromImage(img image.Image, contacts []Contact, dpi float64) []StepEdge {
	mat, err := imageToMat(img)
	if err != nil {
		return nil
	}
	defer mat.Close()
	return DetectStepEdges(mat, contacts, dpi)
}

// DetectStepEdges finds the step edges where the board widens below the connector extension.
// The connector "finger" area is narrower than the main board. At about 0.3" below the contacts,
// the board widens to full width, creating a high-contrast edge against the scanner background.
// Returns precise Y-axis registration points on left and right sides.
func DetectStepEdges(img gocv.Mat, contacts []Contact, dpi float64) []StepEdge {
	if len(contacts) < 2 || dpi <= 0 {
		return nil
	}

	imgH := img.Rows()
	imgW := img.Cols()

	// Estimate positions from contacts (sorted by X)
	firstContact := contacts[0]
	lastContact := contacts[len(contacts)-1]

	// Contact margin from edge is about 2.125" for S-100
	boardMarginPx := 2.125 * dpi
	boardLeftX := firstContact.Center.X - boardMarginPx
	boardRightX := lastContact.Center.X + boardMarginPx

	// Finger extension height is about 0.3" (S100ContactHeight)
	// Step edge is this distance below the contacts
	fingerHeight := 0.3 * dpi
	expectedStepY := firstContact.Center.Y + fingerHeight

	// Search region: narrow horizontal band around expected step Y
	searchHeightPx := int(0.4 * dpi) // 0.4" vertical search range
	searchWidthPx := int(1.0 * dpi)  // 1" horizontal search from board edge

	var edges []StepEdge

	// Detect left step edge
	leftEdge := findStepEdge(img, int(boardLeftX)-searchWidthPx/2, int(expectedStepY)-searchHeightPx/2,
		searchWidthPx, searchHeightPx, "left", imgW, imgH)
	if leftEdge != nil {
		edges = append(edges, *leftEdge)
	}

	// Detect right step edge
	rightEdge := findStepEdge(img, int(boardRightX)-searchWidthPx/2, int(expectedStepY)-searchHeightPx/2,
		searchWidthPx, searchHeightPx, "right", imgW, imgH)
	if rightEdge != nil {
		edges = append(edges, *rightEdge)
	}

	return edges
}

// findStepEdge searches for the horizontal step edge in a region.
// Uses Canny edge detection to find strong horizontal edges, then locates
// the corner where the horizontal step meets the vertical board edge.
func findStepEdge(img gocv.Mat, rx, ry, rw, rh int, side string, imgW, imgH int) *StepEdge {
	// Clamp to image bounds
	if rx < 0 {
		rw += rx
		rx = 0
	}
	if ry < 0 {
		rh += ry
		ry = 0
	}
	if rx+rw > imgW {
		rw = imgW - rx
	}
	if ry+rh > imgH {
		rh = imgH - ry
	}
	if rw <= 0 || rh <= 0 {
		return nil
	}

	// Extract region
	region := img.Region(image.Rect(rx, ry, rx+rw, ry+rh))
	defer region.Close()

	// Convert to grayscale
	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(region, &gray, gocv.ColorBGRToGray)

	// Apply Gaussian blur to reduce noise
	blurred := gocv.NewMat()
	defer blurred.Close()
	gocv.GaussianBlur(gray, &blurred, image.Point{5, 5}, 1.5, 1.5, gocv.BorderDefault)

	// Canny edge detection with lower thresholds for better edge pickup
	edges := gocv.NewMat()
	defer edges.Close()
	gocv.Canny(blurred, &edges, 30, 100)

	// Use Sobel to find horizontal edges specifically
	sobelY := gocv.NewMat()
	defer sobelY.Close()
	gocv.Sobel(blurred, &sobelY, gocv.MatTypeCV16S, 0, 1, 3, 1, 0, gocv.BorderDefault)

	// Convert to absolute values
	sobelYAbs := gocv.NewMat()
	defer sobelYAbs.Close()
	gocv.ConvertScaleAbs(sobelY, &sobelYAbs, 1, 0)

	// Find the row with strongest horizontal edge response
	// This should be the step edge
	bestRow := -1
	bestRowStrength := 0.0

	for y := 0; y < sobelYAbs.Rows(); y++ {
		var rowSum float64
		for x := 0; x < sobelYAbs.Cols(); x++ {
			rowSum += float64(sobelYAbs.GetUCharAt(y, x))
		}
		avgStrength := rowSum / float64(sobelYAbs.Cols())

		if avgStrength > bestRowStrength {
			bestRowStrength = avgStrength
			bestRow = y
		}
	}

	if bestRow < 0 || bestRowStrength < 20 {
		return nil
	}

	// Now find the X coordinate where the step corner is
	// For left side: look for rightmost edge point in the step row
	// For right side: look for leftmost edge point in the step row
	var cornerX int

	if side == "left" {
		// Scan from right to left to find where the edge starts
		cornerX = -1
		for x := edges.Cols() - 1; x >= 0; x-- {
			if edges.GetUCharAt(bestRow, x) > 0 {
				cornerX = x
				break
			}
		}
	} else {
		// Scan from left to right to find where the edge starts
		cornerX = -1
		for x := 0; x < edges.Cols(); x++ {
			if edges.GetUCharAt(bestRow, x) > 0 {
				cornerX = x
				break
			}
		}
	}

	if cornerX < 0 {
		// Fallback: use the center of the search region
		cornerX = rw / 2
	}

	// Sub-pixel refinement: fit a parabola to the gradient magnitudes around bestRow
	refineY := float64(bestRow)
	if bestRow > 0 && bestRow < sobelYAbs.Rows()-1 {
		// Sample 3 points for parabola fit
		y0 := getRowGradientSum(sobelYAbs, bestRow-1)
		y1 := getRowGradientSum(sobelYAbs, bestRow)
		y2 := getRowGradientSum(sobelYAbs, bestRow+1)

		// Parabola vertex: x = -b/(2a) where y = ax^2 + bx + c
		// Using finite differences: a = (y0 - 2*y1 + y2)/2, b = (y2 - y0)/2
		a := (y0 - 2*y1 + y2) / 2
		b := (y2 - y0) / 2
		if a != 0 {
			offset := -b / (2 * a)
			if offset > -1 && offset < 1 {
				refineY = float64(bestRow) + offset
			}
		}
	}

	return &StepEdge{
		Corner: geometry.Point2D{
			X: float64(rx + cornerX),
			Y: float64(ry) + refineY,
		},
		Side:  side,
		EdgeY: float64(ry) + refineY,
	}
}

// getRowGradientSum returns the sum of gradient magnitudes for a row.
func getRowGradientSum(sobelAbs gocv.Mat, row int) float64 {
	var sum float64
	for x := 0; x < sobelAbs.Cols(); x++ {
		sum += float64(sobelAbs.GetUCharAt(row, x))
	}
	return sum
}

// findEjectorMark searches for a bright white region with a dark hole in the center.
// Uses contour fitting to find the precise hole center, ignoring any pin reflections.
func findEjectorMark(img gocv.Mat, rx, ry, rw, rh int, holeDiam float64, imgW, imgH int) *geometry.Point2D {
	// Clamp to image bounds
	if rx < 0 {
		rw += rx
		rx = 0
	}
	if ry < 0 {
		rh += ry
		ry = 0
	}
	if rx+rw > imgW {
		rw = imgW - rx
	}
	if ry+rh > imgH {
		rh = imgH - ry
	}
	if rw <= 0 || rh <= 0 {
		return nil
	}

	// Extract region
	region := img.Region(image.Rect(rx, ry, rx+rw, ry+rh))
	defer region.Close()

	// Convert to grayscale
	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(region, &gray, gocv.ColorBGRToGray)

	// Find bright white regions (ejector body) - threshold at high value
	whiteMask := gocv.NewMat()
	defer whiteMask.Close()
	gocv.Threshold(gray, &whiteMask, 200, 255, gocv.ThresholdBinary)

	// Find the largest white blob - this is the ejector
	whiteContours := gocv.FindContours(whiteMask, gocv.RetrievalExternal, gocv.ChainApproxSimple)
	defer whiteContours.Close()

	if whiteContours.Size() == 0 {
		return nil
	}

	// Find largest white contour
	var largestIdx int
	var largestArea float64
	for i := 0; i < whiteContours.Size(); i++ {
		area := gocv.ContourArea(whiteContours.At(i))
		if area > largestArea {
			largestArea = area
			largestIdx = i
		}
	}

	// Need a reasonable size for ejector
	minArea := 0.25 * 0.25 * (600 * 600) / 4
	if largestArea < minArea {
		return nil
	}

	// Get bounding rect of ejector and extract that region
	ejectorRect := gocv.BoundingRect(whiteContours.At(largestIdx))
	ejectorGray := gray.Region(ejectorRect)
	defer ejectorGray.Close()

	// Create mask of non-white pixels (the hole) within the ejector region
	// Use inverse threshold: anything NOT white is part of the hole
	holeMask := gocv.NewMat()
	defer holeMask.Close()
	gocv.Threshold(ejectorGray, &holeMask, 180, 255, gocv.ThresholdBinaryInv)

	// Morphological close to fill small gaps from reflections
	kernel := gocv.GetStructuringElement(gocv.MorphEllipse, image.Point{3, 3})
	defer kernel.Close()
	gocv.MorphologyEx(holeMask, &holeMask, gocv.MorphClose, kernel)

	// Find contours of the hole
	holeContours := gocv.FindContours(holeMask, gocv.RetrievalExternal, gocv.ChainApproxSimple)
	defer holeContours.Close()

	if holeContours.Size() == 0 {
		return nil
	}

	// Find the contour that's most circular and closest to expected hole size
	expectedArea := math.Pi * (holeDiam / 2) * (holeDiam / 2)
	minExpectedArea := expectedArea * 0.3
	maxExpectedArea := expectedArea * 3.0

	var bestContourIdx = -1
	var bestCircularity float64

	for i := 0; i < holeContours.Size(); i++ {
		contour := holeContours.At(i)
		area := gocv.ContourArea(contour)

		// Filter by size
		if area < minExpectedArea || area > maxExpectedArea {
			continue
		}

		// Calculate circularity: 4*pi*area / perimeter^2
		// Perfect circle = 1.0
		perimeter := gocv.ArcLength(contour, true)
		if perimeter == 0 {
			continue
		}
		circularity := (4 * math.Pi * area) / (perimeter * perimeter)

		if circularity > bestCircularity {
			bestCircularity = circularity
			bestContourIdx = i
		}
	}

	if bestContourIdx < 0 || bestCircularity < 0.5 {
		return nil
	}

	// Fit minimum enclosing circle to the best contour
	// This gives precise center regardless of any internal features
	bestContour := holeContours.At(bestContourIdx)
	cx, cy, radius := gocv.MinEnclosingCircle(bestContour)

	// Sanity check on radius
	expectedRadius := holeDiam / 2
	if radius < float32(expectedRadius*0.3) || radius > float32(expectedRadius*2.0) {
		return nil
	}

	// Convert back to full image coordinates
	return &geometry.Point2D{
		X: float64(rx) + float64(ejectorRect.Min.X) + float64(cx),
		Y: float64(ry) + float64(ejectorRect.Min.Y) + float64(cy),
	}
}
