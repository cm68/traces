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
// Uses color-based flood fill to find the connected metallic region, then
// extracts the boundary contour.
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

	// Sample the clicked pixel's color
	seedR, seedG, seedB, _ := img.At(cx, cy).RGBA()
	seedR8, seedG8, seedB8 := float64(seedR>>8), float64(seedG>>8), float64(seedB>>8)

	// Compute HSV of seed pixel
	seedH, seedS, seedV := rgbToHSVBoundary(seedR8, seedG8, seedB8)

	// Metallic surfaces (tin/solder) typically have:
	// - Low saturation (< 60)
	// - High value/brightness (> 150)
	// - Any hue (metallic is gray-ish)
	isMetallic := seedS < 80 && seedV > 120

	if !isMetallic {
		// Clicked point doesn't look metallic, use default
		return defaultResult(clickX, clickY, maxRadius*0.5)
	}

	// Flood fill to find connected metallic region
	maxR := int(maxRadius)
	visited := make([][]bool, maxR*2+1)
	for i := range visited {
		visited[i] = make([]bool, maxR*2+1)
	}

	// Track boundary pixels and extents
	var boundaryPixels []geometry.Point2D
	minX, maxX := cx, cx
	minY, maxY := cy, cy
	pixelCount := 0

	// Use a stack for flood fill
	type point struct{ x, y int }
	stack := []point{{cx, cy}}

	// Color tolerance for matching metallic surface
	hTol := 30.0  // Hue tolerance (wide for metallic)
	sTol := 40.0  // Saturation tolerance
	vTol := 60.0  // Value tolerance

	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		// Convert to local coordinates
		lx, ly := p.x-cx+maxR, p.y-cy+maxR
		if lx < 0 || lx >= len(visited) || ly < 0 || ly >= len(visited[0]) {
			continue
		}
		if visited[lx][ly] {
			continue
		}
		visited[lx][ly] = true

		// Check if within image bounds
		if p.x < bounds.Min.X || p.x >= bounds.Max.X || p.y < bounds.Min.Y || p.y >= bounds.Max.Y {
			continue
		}

		// Check if this pixel matches the metallic color
		r, g, b, _ := img.At(p.x, p.y).RGBA()
		h, s, v := rgbToHSVBoundary(float64(r>>8), float64(g>>8), float64(b>>8))

		// Check color similarity (for metallic, mainly saturation and value)
		hDiff := math.Abs(h - seedH)
		if hDiff > 90 {
			hDiff = 180 - hDiff // Handle hue wrap-around
		}
		sDiff := math.Abs(s - seedS)
		vDiff := math.Abs(v - seedV)

		if hDiff > hTol || sDiff > sTol || vDiff > vTol {
			// This is a boundary pixel (edge of metallic region)
			boundaryPixels = append(boundaryPixels, geometry.Point2D{X: float64(p.x), Y: float64(p.y)})
			continue
		}

		// This pixel is part of the metallic region
		pixelCount++
		if p.x < minX {
			minX = p.x
		}
		if p.x > maxX {
			maxX = p.x
		}
		if p.y < minY {
			minY = p.y
		}
		if p.y > maxY {
			maxY = p.y
		}

		// Add neighbors to stack (4-connected)
		stack = append(stack, point{p.x + 1, p.y})
		stack = append(stack, point{p.x - 1, p.y})
		stack = append(stack, point{p.x, p.y + 1})
		stack = append(stack, point{p.x, p.y - 1})
	}

	if pixelCount < 10 {
		// Too few pixels found, use default
		return defaultResult(clickX, clickY, maxRadius*0.5)
	}

	// Compute center and radius from extents
	centerX := float64(minX+maxX) / 2
	centerY := float64(minY+maxY) / 2
	width := float64(maxX - minX)
	height := float64(maxY - minY)
	radius := math.Max(width, height) / 2

	// Check circularity
	aspectRatio := width / height
	if aspectRatio > 1 {
		aspectRatio = height / width
	}
	isCircle := aspectRatio > 0.7 // Reasonably circular

	// Simplify boundary if we have one
	var simplifiedBoundary []geometry.Point2D
	if !isCircle && len(boundaryPixels) > 8 {
		// For non-circular shapes, keep a simplified boundary
		simplifiedBoundary = simplifyBoundary(boundaryPixels, centerX, centerY)
	}

	return BoundaryResult{
		Center:   geometry.Point2D{X: centerX, Y: centerY},
		Radius:   radius,
		Boundary: simplifiedBoundary,
		IsCircle: isCircle,
	}
}

// defaultResult returns a default circular result when detection fails.
func defaultResult(x, y, radius float64) BoundaryResult {
	return BoundaryResult{
		Center:   geometry.Point2D{X: x, Y: y},
		Radius:   radius,
		IsCircle: true,
	}
}

// simplifyBoundary reduces boundary points by sampling at regular angular intervals.
func simplifyBoundary(points []geometry.Point2D, cx, cy float64) []geometry.Point2D {
	if len(points) < 8 {
		return points
	}

	// Group points by angle from center
	numBuckets := 16
	buckets := make([][]geometry.Point2D, numBuckets)
	for i := range buckets {
		buckets[i] = []geometry.Point2D{}
	}

	for _, p := range points {
		angle := math.Atan2(p.Y-cy, p.X-cx)
		if angle < 0 {
			angle += 2 * math.Pi
		}
		bucket := int(angle / (2 * math.Pi) * float64(numBuckets))
		if bucket >= numBuckets {
			bucket = numBuckets - 1
		}
		buckets[bucket] = append(buckets[bucket], p)
	}

	// Take the farthest point from each bucket
	var simplified []geometry.Point2D
	for _, bucket := range buckets {
		if len(bucket) == 0 {
			continue
		}
		var farthest geometry.Point2D
		maxDist := 0.0
		for _, p := range bucket {
			dist := math.Sqrt((p.X-cx)*(p.X-cx) + (p.Y-cy)*(p.Y-cy))
			if dist > maxDist {
				maxDist = dist
				farthest = p
			}
		}
		simplified = append(simplified, farthest)
	}

	return simplified
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
