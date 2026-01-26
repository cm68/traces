package via

import (
	"image"
	"math"

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
	bounds := img.Bounds()
	cx, cy := int(clickX), int(clickY)

	// Clamp to image bounds
	if cx < bounds.Min.X || cx >= bounds.Max.X || cy < bounds.Min.Y || cy >= bounds.Max.Y {
		return defaultResult(clickX, clickY, maxRadius*0.5)
	}

	// Check if we clicked on a dark area (inside an open via hole)
	clickedOnDark := isDarkHole(img, cx, cy)

	// Cast rays in many directions from the click point
	numRays := 64
	var boundaryPoints []geometry.Point2D
	maxR := int(maxRadius)

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
		}
	}

	if len(boundaryPoints) < 4 {
		// Not enough boundary points found, use default
		return defaultResult(clickX, clickY, maxRadius*0.5)
	}

	// Compute center and radius from boundary points
	var sumX, sumY float64
	for _, p := range boundaryPoints {
		sumX += p.X
		sumY += p.Y
	}
	centerX := sumX / float64(len(boundaryPoints))
	centerY := sumY / float64(len(boundaryPoints))

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

	// If CV < 0.20, the shape is mostly circular - expand to containing circle
	// This fills in gaps from visual artifacts (reflections, shadows, etc.)
	isMostlyCircular := coeffVar < 0.20

	if isMostlyCircular {
		// Use the max radius (plus small padding) to create a circle that contains all points
		containingRadius := maxRad

		// Generate smooth circle boundary points
		circlePoints := make([]geometry.Point2D, numRays)
		for i := 0; i < numRays; i++ {
			angle := float64(i) * 2.0 * math.Pi / float64(numRays)
			circlePoints[i] = geometry.Point2D{
				X: centerX + containingRadius*math.Cos(angle),
				Y: centerY + containingRadius*math.Sin(angle),
			}
		}

		return BoundaryResult{
			Center:   geometry.Point2D{X: centerX, Y: centerY},
			Radius:   containingRadius,
			Boundary: circlePoints,
			IsCircle: true,
		}
	}

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

	_, _, v := rgbToHSVBoundary(r8, g8, b8)

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

	h, s, v := rgbToHSVBoundary(r8, g8, b8)

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
func defaultResult(x, y, radius float64) BoundaryResult {
	return BoundaryResult{
		Center:   geometry.Point2D{X: x, Y: y},
		Radius:   radius,
		IsCircle: true,
	}
}

// rgbToHSVBoundary converts RGB (0-255) to HSV (H: 0-180, S: 0-255, V: 0-255).
func rgbToHSVBoundary(r, g, b float64) (h, s, v float64) {
	r /= 255.0
	g /= 255.0
	b /= 255.0

	maxC := math.Max(r, math.Max(g, b))
	minC := math.Min(r, math.Min(g, b))
	diff := maxC - minC

	v = maxC * 255.0

	if maxC == 0 {
		s = 0
	} else {
		s = (diff / maxC) * 255.0
	}

	if diff == 0 {
		h = 0
	} else if maxC == r {
		h = 60 * math.Mod((g-b)/diff, 6)
	} else if maxC == g {
		h = 60 * ((b-r)/diff + 2)
	} else {
		h = 60 * ((r-g)/diff + 4)
	}

	if h < 0 {
		h += 360
	}
	h = h / 2 // Convert to 0-180 range

	return h, s, v
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
		// Generate circle points
		numPoints := 32
		for i := 0; i < numPoints; i++ {
			angle := float64(i) * 2.0 * math.Pi / float64(numPoints)
			allPoints = append(allPoints, geometry.Point2D{
				X: a.Center.X + a.Radius*math.Cos(angle),
				Y: a.Center.Y + a.Radius*math.Sin(angle),
			})
		}
	}

	// Add points from second boundary (or generate circle points if circular)
	if len(b.Boundary) > 0 {
		allPoints = append(allPoints, b.Boundary...)
	} else if b.Radius > 0 {
		numPoints := 32
		for i := 0; i < numPoints; i++ {
			angle := float64(i) * 2.0 * math.Pi / float64(numPoints)
			allPoints = append(allPoints, geometry.Point2D{
				X: b.Center.X + b.Radius*math.Cos(angle),
				Y: b.Center.Y + b.Radius*math.Sin(angle),
			})
		}
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
	var sumX, sumY float64
	for _, p := range allPoints {
		sumX += p.X
		sumY += p.Y
	}
	centerX := sumX / float64(len(allPoints))
	centerY := sumY / float64(len(allPoints))

	// Compute max radius from center to any point
	var maxR float64
	for _, p := range allPoints {
		r := math.Sqrt((p.X-centerX)*(p.X-centerX) + (p.Y-centerY)*(p.Y-centerY))
		if r > maxR {
			maxR = r
		}
	}

	// If hull is degenerate (< 3 points), generate a circle instead
	if len(hull) < 3 {
		containingRadius := maxR
		numPoints := 64
		circlePoints := make([]geometry.Point2D, numPoints)
		for i := 0; i < numPoints; i++ {
			angle := float64(i) * 2.0 * math.Pi / float64(numPoints)
			circlePoints[i] = geometry.Point2D{
				X: centerX + containingRadius*math.Cos(angle),
				Y: centerY + containingRadius*math.Sin(angle),
			}
		}
		return BoundaryResult{
			Center:   geometry.Point2D{X: centerX, Y: centerY},
			Radius:   containingRadius,
			Boundary: circlePoints,
			IsCircle: true,
		}
	}

	return BoundaryResult{
		Center:   geometry.Point2D{X: centerX, Y: centerY},
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
