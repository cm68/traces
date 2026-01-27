package via

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"

	"pcb-tracer/pkg/colorutil"
	"pcb-tracer/pkg/geometry"
)

// BoundaryResult holds the detected boundary information.
type BoundaryResult struct {
	Center   geometry.Point2D   // Computed center of the boundary
	Radius   float64            // Approximate radius (for circular vias)
	Boundary []geometry.Point2D // Contour points (may be empty if circular)
	IsCircle bool               // True if the boundary is approximately circular
}

// DetectMetalBoundary finds the metallic pad boundary around a clicked point.
// Uses radial search to expand outward from the click point until hitting
// the green PCB substrate in each direction. Also handles open vias with
// dark center holes by detecting the annular ring pattern.
//
// Parameters:
//   - img: the source image
//   - clickX, clickY: the clicked point in image coordinates
//   - maxRadius: maximum search radius in pixels
//
// Returns the detected boundary, or a default circular result if detection fails.
func DetectMetalBoundary(img image.Image, clickX, clickY float64, maxRadius float64) BoundaryResult {
	fmt.Printf("    DetectMetalBoundary: click=(%.1f,%.1f) maxR=%.1f\n", clickX, clickY, maxRadius)
	bounds := img.Bounds()
	cx, cy := int(clickX), int(clickY)

	// Clamp to image bounds
	if cx < bounds.Min.X || cx >= bounds.Max.X || cy < bounds.Min.Y || cy >= bounds.Max.Y {
		fmt.Printf("    DetectMetalBoundary: OUT OF BOUNDS, returning default\n")
		return defaultResult(clickX, clickY, maxRadius*0.5)
	}

	// Check if we clicked on a dark area (inside an open via hole)
	clickedOnDark := isDarkHole(img, cx, cy)
	fmt.Printf("    DetectMetalBoundary: clickedOnDark=%v\n", clickedOnDark)

	// Cast rays in many directions from the click point
	numRays := 64
	var boundaryPoints []geometry.Point2D
	maxR := int(maxRadius)
	foundCount := 0

	for i := 0; i < numRays; i++ {
		angle := float64(i) * 2.0 * math.Pi / float64(numRays)
		dx := math.Cos(angle)
		dy := math.Sin(angle)

		var edgeX, edgeY float64
		var found bool

		if clickedOnDark {
			// Clicked in dark hole - find the metal ring first, then the green edge
			edgeX, edgeY, found = findAnnularRingEdge(img, bounds, cx, cy, dx, dy, maxR)
		} else {
			// Clicked on metal - find where metal meets green board
			edgeX, edgeY, found = findGreenEdge(img, bounds, cx, cy, dx, dy, maxR)
		}

		if found {
			// Validate that the boundary point is NOT on green (it should be on metal)
			px, py := int(edgeX), int(edgeY)
			if px >= bounds.Min.X && px < bounds.Max.X && py >= bounds.Min.Y && py < bounds.Max.Y {
				if !isGreenBoard(img, px, py) {
					boundaryPoints = append(boundaryPoints, geometry.Point2D{X: edgeX, Y: edgeY})
					foundCount++
				}
			}
		}
	}
	fmt.Printf("    DetectMetalBoundary: found %d/%d boundary points (green filtered)\n", foundCount, numRays)

	if len(boundaryPoints) < 4 {
		// Not enough boundary points found, use default
		fmt.Printf("    DetectMetalBoundary: NOT ENOUGH POINTS (<4), returning default\n")
		return defaultResult(clickX, clickY, maxRadius*0.5)
	}

	// Compute center and radius from boundary points
	center := geometry.Centroid(boundaryPoints)
	centerX := center.X
	centerY := center.Y

	// Compute radius statistics
	radii := make([]float64, len(boundaryPoints))
	var sumR, minR, maxRad float64
	minR = maxRadius * 2
	for i, p := range boundaryPoints {
		r := math.Sqrt((p.X-centerX)*(p.X-centerX) + (p.Y-centerY)*(p.Y-centerY))
		radii[i] = r
		sumR += r
		if r < minR {
			minR = r
		}
		if r > maxRad {
			maxRad = r
		}
	}
	avgRadius := sumR / float64(len(boundaryPoints))

	// Check if mostly circular using coefficient of variation (stddev/mean)
	// Lower CV means points are more consistently distant from center (more circular)
	var sumSqDiff float64
	for _, r := range radii {
		diff := r - avgRadius
		sumSqDiff += diff * diff
	}
	stdDev := math.Sqrt(sumSqDiff / float64(len(radii)))
	coeffVar := stdDev / avgRadius
	radiusSpread := maxRad - minR

	fmt.Printf("    DetectMetalBoundary: center=(%.1f,%.1f)\n", centerX, centerY)
	fmt.Printf("    DetectMetalBoundary: radii: avg=%.1f min=%.1f max=%.1f spread=%.1f\n",
		avgRadius, minR, maxRad, radiusSpread)
	fmt.Printf("    DetectMetalBoundary: circularity: stdDev=%.2f CV=%.3f (threshold=0.20)\n",
		stdDev, coeffVar)

	// If CV < 0.20, the shape is mostly circular - fit a tight circle
	// Use average radius (not max) to avoid including green background
	isMostlyCircular := coeffVar < 0.20

	if isMostlyCircular {
		// Use average radius (not max) to avoid including green background
		// Shrink slightly to ensure we stay on metal
		circleRadius := avgRadius * 0.95
		fmt.Printf("    DetectMetalBoundary: DECISION: CIRCULAR (CV %.3f < 0.20) → fitting circle r=%.1f (avg=%.1f)\n",
			coeffVar, circleRadius, avgRadius)
		fmt.Printf("    DetectMetalBoundary: PROMOTED from manual boundary points to full circle\n")
		circlePoints := geometry.GenerateCirclePoints(centerX, centerY, circleRadius, numRays)

		return BoundaryResult{
			Center:   geometry.Point2D{X: centerX, Y: centerY},
			Radius:   circleRadius,
			Boundary: circlePoints,
			IsCircle: true,
		}
	}

	fmt.Printf("    DetectMetalBoundary: DECISION: IRREGULAR (CV %.3f >= 0.20) → keeping %d raw boundary points\n",
		coeffVar, len(boundaryPoints))
	// Not circular enough - keep the irregular boundary points
	return BoundaryResult{
		Center:   geometry.Point2D{X: centerX, Y: centerY},
		Radius:   avgRadius,
		Boundary: boundaryPoints,
		IsCircle: false,
	}
}

// findAnnularRingEdge handles open vias where the click was in the dark center hole.
// It first searches outward to find the metal ring, then continues to find the green edge.
func findAnnularRingEdge(img image.Image, bounds image.Rectangle, cx, cy int, dx, dy float64, maxR int) (float64, float64, bool) {
	// First, walk outward from the dark hole to find metal
	metalStartStep := 0
	for step := 1; step <= maxR; step++ {
		x := int(float64(cx) + dx*float64(step))
		y := int(float64(cy) + dy*float64(step))

		if x < bounds.Min.X || x >= bounds.Max.X || y < bounds.Min.Y || y >= bounds.Max.Y {
			return 0, 0, false
		}

		// Found metal (not dark hole, not green board)
		if !isDarkHole(img, x, y) && !isGreenBoard(img, x, y) {
			metalStartStep = step
			break
		}
	}

	if metalStartStep == 0 {
		return 0, 0, false // Never found metal
	}

	// Track when we last saw metal (to handle thin rings or artifacts)
	lastMetalStep := metalStartStep

	// Now continue from where we found metal to find the green edge
	for step := metalStartStep; step <= maxR; step++ {
		x := int(float64(cx) + dx*float64(step))
		y := int(float64(cy) + dy*float64(step))

		if x < bounds.Min.X || x >= bounds.Max.X || y < bounds.Min.Y || y >= bounds.Max.Y {
			// Hit image edge - return last known metal position
			return float64(cx) + dx*float64(lastMetalStep), float64(cy) + dy*float64(lastMetalStep), true
		}

		// Update last metal position if we're still on metal
		if !isDarkHole(img, x, y) && !isGreenBoard(img, x, y) {
			lastMetalStep = step
		}

		if isGreenBoard(img, x, y) {
			// Found the edge - return last metal position
			return float64(cx) + dx*float64(lastMetalStep), float64(cy) + dy*float64(lastMetalStep), true
		}
	}

	// Reached max radius - return last metal position
	return float64(cx) + dx*float64(lastMetalStep), float64(cy) + dy*float64(lastMetalStep), true
}

// isDarkHole checks if a pixel appears to be a dark via hole.
// Dark holes have low brightness/value.
func isDarkHole(img image.Image, x, y int) bool {
	r, g, b, _ := img.At(x, y).RGBA()
	r8, g8, b8 := float64(r>>8), float64(g>>8), float64(b>>8)

	_, _, v := colorutil.RGBToHSV(r8, g8, b8)

	// Dark holes have very low brightness
	return v < 60
}

// findGreenEdge walks along a ray from (cx,cy) in direction (dx,dy) until
// it finds the transition away from bright metal. Returns the edge position.
func findGreenEdge(img image.Image, bounds image.Rectangle, cx, cy int, dx, dy float64, maxR int) (float64, float64, bool) {
	lastMetalStep := 0

	// Walk along the ray
	for step := 1; step <= maxR; step++ {
		x := int(float64(cx) + dx*float64(step))
		y := int(float64(cy) + dy*float64(step))

		// Check bounds
		if x < bounds.Min.X || x >= bounds.Max.X || y < bounds.Min.Y || y >= bounds.Max.Y {
			// Hit image edge - return last known metal position
			if lastMetalStep > 0 {
				return float64(cx) + dx*float64(lastMetalStep), float64(cy) + dy*float64(lastMetalStep), true
			}
			return float64(cx) + dx*float64(step-1), float64(cy) + dy*float64(step-1), step > 1
		}

		// Track if we're still on bright metal
		if isBrightMetal(img, x, y) {
			lastMetalStep = step
		}

		// Stop when we hit green PCB
		if isGreenBoard(img, x, y) {
			// Found green - return last metal position (or previous step)
			if lastMetalStep > 0 {
				return float64(cx) + dx*float64(lastMetalStep), float64(cy) + dy*float64(lastMetalStep), true
			}
			return float64(cx) + dx*float64(step-1), float64(cy) + dy*float64(step-1), true
		}

		// Stop when we've clearly left metal (not metal AND not green = edge zone)
		if lastMetalStep > 0 && step > lastMetalStep+3 {
			// We had metal, then 3+ steps of non-metal non-green - return last metal
			return float64(cx) + dx*float64(lastMetalStep), float64(cy) + dy*float64(lastMetalStep), true
		}
	}

	// Reached max radius - return last metal position if we found any
	if lastMetalStep > 0 {
		return float64(cx) + dx*float64(lastMetalStep), float64(cy) + dy*float64(lastMetalStep), true
	}
	return float64(cx) + dx*float64(maxR), float64(cy) + dy*float64(maxR), true
}

// isBrightMetal checks if a pixel appears to be via pad metal.
// Metal pads can be silver (low saturation) or have warm golden/copper tones
// (moderate saturation but very bright).
func isBrightMetal(img image.Image, x, y int) bool {
	r, g, b, _ := img.At(x, y).RGBA()
	r8, g8, b8 := float64(r>>8), float64(g>>8), float64(b>>8)

	h, s, v := colorutil.RGBToHSV(r8, g8, b8)

	// Very bright pixels with warm hues are metal (gold/copper tones)
	// These have H<40 (yellow/orange range) and V>180
	if v >= 180 && h < 40 {
		return true
	}

	// Standard metal: grayish (low saturation), not too dark, not green hue
	isLowSat := s < 40
	isNotDark := v >= 40
	isNotGreenHue := h < 40 || h > 90

	return isLowSat && isNotDark && isNotGreenHue
}

// isGreenBoard checks if a pixel appears to be green PCB substrate.
// Green PCB typically has:
//   - Hue in green range (roughly 100-170 degrees, or 50-85 in 0-180 scale)
//   - Moderate to high saturation (green is colorful, not gray)
//   - Moderate value (not too bright like shiny metal, not too dark)
func isGreenBoard(img image.Image, x, y int) bool {
	r, g, b, _ := img.At(x, y).RGBA()
	r8, g8, b8 := float64(r>>8), float64(g>>8), float64(b>>8)

	h, s, v := colorutil.RGBToHSV(r8, g8, b8)

	// Green hue range: 40-85 in 0-180 scale (80-170 degrees)
	// Lowered from 50 to catch olive-green PCB (H~40-50 with high saturation)
	// True green PCB has H around 65-85, olive-green has H~40-50
	isGreenHue := h >= 40 && h <= 85

	// Green boards have noticeable saturation (unlike gray metal)
	// Transition areas often have S >= 30-40
	hasSaturation := s >= 30

	// Moderate brightness (not super bright like shiny metal)
	moderateBrightness := v >= 40 && v <= 220

	return isGreenHue && hasSaturation && moderateBrightness
}

// FilterGreenPoints removes any boundary points that land on green PCB substrate.
// Use this to validate boundaries after merging or other operations.
func FilterGreenPoints(img image.Image, points []geometry.Point2D) []geometry.Point2D {
	if img == nil || len(points) == 0 {
		return points
	}
	bounds := img.Bounds()
	filtered := make([]geometry.Point2D, 0, len(points))
	for _, p := range points {
		px, py := int(p.X), int(p.Y)
		if px >= bounds.Min.X && px < bounds.Max.X && py >= bounds.Min.Y && py < bounds.Max.Y {
			if !isGreenBoard(img, px, py) {
				filtered = append(filtered, p)
			}
		}
	}
	return filtered
}

// DumpBoundaryPixels writes HSV color information for pixels inside a polygon boundary.
// This is for debugging green detection issues.
func DumpBoundaryPixels(img image.Image, boundary []geometry.Point2D, viaID string) {
	if img == nil || len(boundary) < 3 {
		return
	}

	f, err := os.OpenFile("via_pixel_dump.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("Error opening dump file: %v\n", err)
		return
	}
	defer f.Close()

	// Get bounding box
	minX, minY := boundary[0].X, boundary[0].Y
	maxX, maxY := boundary[0].X, boundary[0].Y
	for _, p := range boundary {
		if p.X < minX {
			minX = p.X
		}
		if p.X > maxX {
			maxX = p.X
		}
		if p.Y < minY {
			minY = p.Y
		}
		if p.Y > maxY {
			maxY = p.Y
		}
	}

	bounds := img.Bounds()
	fmt.Fprintf(f, "\n=== VIA %s ===\n", viaID)
	fmt.Fprintf(f, "Boundary bbox: (%.0f,%.0f) to (%.0f,%.0f)\n", minX, minY, maxX, maxY)
	fmt.Fprintf(f, "Green detection thresholds: H=[40-85], S>=30, V=[40-220]\n")
	fmt.Fprintf(f, "Metal detection: (V>=180 & H<40) OR (S<40 & V>=40 & H<40|H>90)\n")
	fmt.Fprintf(f, "Format: (x,y) R,G,B -> H,S,V isGreen?\n\n")

	greenCount := 0
	totalCount := 0

	// Sample pixels inside the polygon
	for y := int(minY); y <= int(maxY); y++ {
		for x := int(minX); x <= int(maxX); x++ {
			if x < bounds.Min.X || x >= bounds.Max.X || y < bounds.Min.Y || y >= bounds.Max.Y {
				continue
			}

			// Check if point is inside polygon
			p := geometry.Point2D{X: float64(x), Y: float64(y)}
			if !geometry.PointInPolygon(p, boundary) {
				continue
			}

			totalCount++

			// Get pixel color
			r, g, b, _ := img.At(x, y).RGBA()
			r8, g8, b8 := r>>8, g>>8, b>>8

			// Convert to HSV
			h, s, v := colorutil.RGBToHSV(float64(r8), float64(g8), float64(b8))

			// Check green detection
			isGreen := isGreenBoard(img, x, y)
			if isGreen {
				greenCount++
			}

			// Only dump pixels that SHOULD be green but aren't detected, or vice versa
			// (dump all for now to see the full picture)
			greenMarker := ""
			if isGreen {
				greenMarker = " GREEN"
			}

			fmt.Fprintf(f, "(%4d,%4d) %3d,%3d,%3d -> H=%5.1f S=%5.1f V=%5.1f%s\n",
				x, y, r8, g8, b8, h, s, v, greenMarker)
		}
	}

	fmt.Fprintf(f, "\nSummary: %d/%d pixels detected as green (%.1f%%)\n",
		greenCount, totalCount, float64(greenCount)*100/float64(totalCount))
	fmt.Fprintf(f, "=== END VIA %s ===\n\n", viaID)

	fmt.Printf("Dumped %d pixels for via %s to via_pixel_dump.txt\n", totalCount, viaID)
}

// DumpBoundaryPNG saves a PNG image of the via region with boundary overlay.
// Only for manually designated vias. Saves to via_dump_<viaID>.png.
func DumpBoundaryPNG(img image.Image, boundary []geometry.Point2D, viaID string) {
	fmt.Printf("DumpBoundaryPNG: viaID=%s, img=%v, boundary pts=%d\n", viaID, img != nil, len(boundary))
	if img == nil || len(boundary) < 3 {
		fmt.Printf("DumpBoundaryPNG: early return (img nil or boundary < 3)\n")
		return
	}

	// Get bounding box with padding
	minX, minY := boundary[0].X, boundary[0].Y
	maxX, maxY := boundary[0].X, boundary[0].Y
	for _, p := range boundary {
		if p.X < minX {
			minX = p.X
		}
		if p.X > maxX {
			maxX = p.X
		}
		if p.Y < minY {
			minY = p.Y
		}
		if p.Y > maxY {
			maxY = p.Y
		}
	}

	// Add padding
	padding := 10.0
	minX -= padding
	minY -= padding
	maxX += padding
	maxY += padding

	bounds := img.Bounds()
	if int(minX) < bounds.Min.X {
		minX = float64(bounds.Min.X)
	}
	if int(minY) < bounds.Min.Y {
		minY = float64(bounds.Min.Y)
	}
	if int(maxX) >= bounds.Max.X {
		maxX = float64(bounds.Max.X - 1)
	}
	if int(maxY) >= bounds.Max.Y {
		maxY = float64(bounds.Max.Y - 1)
	}

	width := int(maxX - minX + 1)
	height := int(maxY - minY + 1)
	if width <= 0 || height <= 0 {
		return
	}

	// Create output image
	out := image.NewRGBA(image.Rect(0, 0, width, height))

	// Copy pixels from source
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			srcX := int(minX) + x
			srcY := int(minY) + y
			out.Set(x, y, img.At(srcX, srcY))
		}
	}

	// Draw boundary outline in red (just the outline, preserve original pixels)
	red := color.RGBA{R: 255, G: 0, B: 0, A: 255}
	for i := 0; i < len(boundary); i++ {
		p1 := boundary[i]
		p2 := boundary[(i+1)%len(boundary)]

		// Convert to local coordinates
		x1, y1 := p1.X-minX, p1.Y-minY
		x2, y2 := p2.X-minX, p2.Y-minY

		// Draw line using Bresenham
		drawLine(out, int(x1), int(y1), int(x2), int(y2), red)
	}

	// Save to file
	filename := fmt.Sprintf("via_dump_%s.png", viaID)
	f, err := os.Create(filename)
	if err != nil {
		fmt.Printf("Error creating %s: %v\n", filename, err)
		return
	}
	defer f.Close()

	if err := png.Encode(f, out); err != nil {
		fmt.Printf("Error encoding PNG %s: %v\n", filename, err)
		return
	}

	fmt.Printf("Saved via region to %s (%dx%d)\n", filename, width, height)
}

// drawLine draws a line using Bresenham's algorithm.
func drawLine(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
	sx := 1
	if x0 >= x1 {
		sx = -1
	}
	sy := 1
	if y0 >= y1 {
		sy = -1
	}
	err := dx + dy

	bounds := img.Bounds()
	for {
		if x0 >= 0 && x0 < bounds.Max.X && y0 >= 0 && y0 < bounds.Max.Y {
			img.Set(x0, y0, c)
		}
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// defaultResult returns a default circular result when detection fails.
// It generates circle boundary points to ensure consistent polygon rendering.
func defaultResult(x, y, radius float64) BoundaryResult {
	// Generate circle boundary points so we always have a polygon
	// This ensures consistent rendering with black centered labels
	circlePoints := geometry.GenerateCirclePoints(x, y, radius, 64)
	return BoundaryResult{
		Center:   geometry.Point2D{X: x, Y: y},
		Radius:   radius,
		Boundary: circlePoints,
		IsCircle: true,
	}
}

// MergeBoundaries combines two boundary results into one.
// The resulting boundary encompasses all points from both boundaries.
// Uses convex hull to create a clean merged boundary.
func MergeBoundaries(a, b BoundaryResult) BoundaryResult {
	// Collect all boundary points from both results
	var allPoints []geometry.Point2D

	// Add points from first boundary (or generate circle points if circular)
	if len(a.Boundary) > 0 {
		allPoints = append(allPoints, a.Boundary...)
	} else if a.Radius > 0 {
		allPoints = append(allPoints, geometry.GenerateCirclePoints(a.Center.X, a.Center.Y, a.Radius, 32)...)
	}

	// Add points from second boundary (or generate circle points if circular)
	if len(b.Boundary) > 0 {
		allPoints = append(allPoints, b.Boundary...)
	} else if b.Radius > 0 {
		allPoints = append(allPoints, geometry.GenerateCirclePoints(b.Center.X, b.Center.Y, b.Radius, 32)...)
	}

	if len(allPoints) < 3 {
		// Not enough points, return the larger one
		if a.Radius >= b.Radius {
			return a
		}
		return b
	}

	// Compute convex hull of all points
	hull := convexHull(allPoints)

	// Compute center from all points (more stable than hull center)
	center := geometry.Centroid(allPoints)

	// Compute max radius from center to any point
	var maxR float64
	for _, p := range allPoints {
		r := center.Distance(p)
		if r > maxR {
			maxR = r
		}
	}

	// If hull is degenerate (< 3 points), generate a circle instead
	if len(hull) < 3 {
		circlePoints := geometry.GenerateCirclePoints(center.X, center.Y, maxR, 64)
		return BoundaryResult{
			Center:   center,
			Radius:   maxR,
			Boundary: circlePoints,
			IsCircle: true,
		}
	}

	return BoundaryResult{
		Center:   center,
		Radius:   maxR,
		Boundary: hull,
		IsCircle: false,
	}
}

// convexHull computes the convex hull of a set of points using Graham scan.
func convexHull(points []geometry.Point2D) []geometry.Point2D {
	if len(points) < 3 {
		return points
	}

	// Find the point with lowest y (and leftmost if tied)
	lowest := 0
	for i := 1; i < len(points); i++ {
		if points[i].Y < points[lowest].Y ||
			(points[i].Y == points[lowest].Y && points[i].X < points[lowest].X) {
			lowest = i
		}
	}

	// Swap to front
	points[0], points[lowest] = points[lowest], points[0]
	pivot := points[0]

	// Sort by polar angle with respect to pivot
	sorted := make([]geometry.Point2D, len(points)-1)
	copy(sorted, points[1:])

	// Sort by angle
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			cross := crossProduct(pivot, sorted[i], sorted[j])
			if cross < 0 || (cross == 0 && distSq(pivot, sorted[i]) > distSq(pivot, sorted[j])) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	// Build hull
	hull := []geometry.Point2D{pivot}
	for _, p := range sorted {
		for len(hull) > 1 && crossProduct(hull[len(hull)-2], hull[len(hull)-1], p) <= 0 {
			hull = hull[:len(hull)-1]
		}
		hull = append(hull, p)
	}

	return hull
}

// crossProduct computes the cross product of vectors OA and OB.
func crossProduct(o, a, b geometry.Point2D) float64 {
	return (a.X-o.X)*(b.Y-o.Y) - (a.Y-o.Y)*(b.X-o.X)
}

// distSq computes the squared distance between two points.
func distSq(a, b geometry.Point2D) float64 {
	dx := b.X - a.X
	dy := b.Y - a.Y
	return dx*dx + dy*dy
}
