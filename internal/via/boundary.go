package via

import (
	"fmt"
	"image"
	"math"

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
			boundaryPoints = append(boundaryPoints, geometry.Point2D{X: edgeX, Y: edgeY})
			foundCount++
		}
	}
	fmt.Printf("    DetectMetalBoundary: found %d/%d boundary points\n", foundCount, numRays)

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

	// If CV < 0.20, the shape is mostly circular - expand to containing circle
	// This fills in gaps from visual artifacts (reflections, shadows, etc.)
	isMostlyCircular := coeffVar < 0.20

	if isMostlyCircular {
		fmt.Printf("    DetectMetalBoundary: DECISION: CIRCULAR (CV %.3f < 0.20) → expanding to circle r=%.1f\n",
			coeffVar, maxRad)
		fmt.Printf("    DetectMetalBoundary: PROMOTED from manual boundary points to full circle\n")
		// Use the max radius to create a circle that contains all points
		circlePoints := geometry.GenerateCirclePoints(centerX, centerY, maxRad, numRays)

		return BoundaryResult{
			Center:   geometry.Point2D{X: centerX, Y: centerY},
			Radius:   maxRad,
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
// it finds the transition to green PCB board. Returns the edge position.
func findGreenEdge(img image.Image, bounds image.Rectangle, cx, cy int, dx, dy float64, maxR int) (float64, float64, bool) {
	// Walk along the ray
	for step := 1; step <= maxR; step++ {
		x := int(float64(cx) + dx*float64(step))
		y := int(float64(cy) + dy*float64(step))

		// Check bounds
		if x < bounds.Min.X || x >= bounds.Max.X || y < bounds.Min.Y || y >= bounds.Max.Y {
			// Hit image edge - use this as boundary
			return float64(cx) + dx*float64(step-1), float64(cy) + dy*float64(step-1), step > 1
		}

		// Check if this pixel is green PCB board
		if isGreenBoard(img, x, y) {
			// Found the edge - return the previous (non-green) position
			edgeX := float64(cx) + dx*float64(step-1)
			edgeY := float64(cy) + dy*float64(step-1)
			return edgeX, edgeY, true
		}
	}

	// Reached max radius without finding green - use max radius point
	return float64(cx) + dx*float64(maxR), float64(cy) + dy*float64(maxR), true
}

// isGreenBoard checks if a pixel appears to be green PCB substrate.
// Green PCB typically has:
//   - Hue in green range (roughly 60-150 degrees, or 30-75 in 0-180 scale)
//   - Moderate to high saturation (green is colorful, not gray)
//   - Moderate value (not too bright like metal, not too dark)
func isGreenBoard(img image.Image, x, y int) bool {
	r, g, b, _ := img.At(x, y).RGBA()
	r8, g8, b8 := float64(r>>8), float64(g>>8), float64(b>>8)

	h, s, v := colorutil.RGBToHSV(r8, g8, b8)

	// Green hue range: 30-75 in 0-180 scale (60-150 degrees)
	// Allow some tolerance for different PCB colors
	isGreenHue := h >= 25 && h <= 85

	// Green boards have noticeable saturation (unlike gray metal)
	hasSaturation := s >= 30

	// Moderate brightness (not super bright like shiny metal)
	moderateBrightness := v >= 40 && v <= 220

	return isGreenHue && hasSaturation && moderateBrightness
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
