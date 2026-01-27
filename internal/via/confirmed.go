package via

import (
	"fmt"
	"math"

	"pcb-tracer/pkg/geometry"
)

// ConfirmedVia represents a via detected on both front and back sides of the PCB.
// This provides stronger confidence that the via is real, as it was independently
// detected from both sides at the same location.
type ConfirmedVia struct {
	ID                   string             `json:"id"`                    // Unified ID, e.g., "cvia-001"
	FrontViaID           string             `json:"front_via_id"`          // Reference to front side via
	BackViaID            string             `json:"back_via_id"`           // Reference to back side via
	Center               geometry.Point2D   `json:"center"`                // Averaged center from both sides
	Radius               float64            `json:"radius"`                // Average radius
	IntersectionBoundary []geometry.Point2D `json:"intersection_boundary"` // Computed polygon intersection
	Confidence           float64            `json:"confidence"`            // Combined confidence score (boosted)
}

// NewConfirmedVia creates a new confirmed via from matched front and back vias.
// It computes the intersection of their boundaries and averages their properties.
func NewConfirmedVia(id string, front, back *Via) *ConfirmedVia {
	cv := &ConfirmedVia{
		ID:         id,
		FrontViaID: front.ID,
		BackViaID:  back.ID,
		Center: geometry.Point2D{
			X: (front.Center.X + back.Center.X) / 2,
			Y: (front.Center.Y + back.Center.Y) / 2,
		},
		Radius: (front.Radius + back.Radius) / 2,
		// Boost confidence for cross-side confirmation (cap at 1.0)
		Confidence: min(1.0, (front.Confidence+back.Confidence)/2*1.2),
	}

	// Compute intersection boundary
	cv.IntersectionBoundary = ComputeIntersection(front, back)

	return cv
}

// ComputeIntersection computes a combined boundary for the confirmed via.
// Uses union (convex hull) instead of intersection to get a fuller, rounder boundary.
// Then fits the result to a circle, clamped to the max detected radius to prevent overlap.
func ComputeIntersection(front, back *Via) []geometry.Point2D {
	avgCenter := geometry.Point2D{
		X: (front.Center.X + back.Center.X) / 2,
		Y: (front.Center.Y + back.Center.Y) / 2,
	}

	// Clamp radius to the larger of the two detected radii (prevents overlap)
	maxAllowedRadius := front.Radius
	if back.Radius > maxAllowedRadius {
		maxAllowedRadius = back.Radius
	}

	frontBoundary := front.PadBoundary
	backBoundary := back.PadBoundary

	// If both have boundaries, combine them and fit a circle
	if len(frontBoundary) >= 3 && len(backBoundary) >= 3 {
		// Combine all points from both boundaries
		combined := make([]geometry.Point2D, 0, len(frontBoundary)+len(backBoundary))
		combined = append(combined, frontBoundary...)
		combined = append(combined, backBoundary...)

		// Compute convex hull (union)
		hull := geometry.ConvexHull(combined)

		// Find the maximum distance from averaged center to hull points
		maxRadiusSq := 0.0
		for _, p := range hull {
			dx := p.X - avgCenter.X
			dy := p.Y - avgCenter.Y
			distSq := dx*dx + dy*dy
			if distSq > maxRadiusSq {
				maxRadiusSq = distSq
			}
		}

		// Use this radius to generate a clean circle
		circleRadius := math.Sqrt(maxRadiusSq) * 0.95
		if circleRadius < 1 {
			circleRadius = (front.Radius + back.Radius) / 2
		}
		// Clamp to prevent overlap with neighboring vias
		if circleRadius > maxAllowedRadius {
			circleRadius = maxAllowedRadius
		}

		return geometry.GenerateCirclePoints(avgCenter.X, avgCenter.Y, circleRadius, 32)
	}

	// Fall back to using whichever boundary exists, or generate circle
	if len(frontBoundary) >= 3 {
		// Fit circle to front boundary
		maxRadiusSq := 0.0
		for _, p := range frontBoundary {
			dx := p.X - avgCenter.X
			dy := p.Y - avgCenter.Y
			distSq := dx*dx + dy*dy
			if distSq > maxRadiusSq {
				maxRadiusSq = distSq
			}
		}
		circleRadius := math.Sqrt(maxRadiusSq) * 0.95
		if circleRadius > maxAllowedRadius {
			circleRadius = maxAllowedRadius
		}
		return geometry.GenerateCirclePoints(avgCenter.X, avgCenter.Y, circleRadius, 32)
	}

	if len(backBoundary) >= 3 {
		// Fit circle to back boundary
		maxRadiusSq := 0.0
		for _, p := range backBoundary {
			dx := p.X - avgCenter.X
			dy := p.Y - avgCenter.Y
			distSq := dx*dx + dy*dy
			if distSq > maxRadiusSq {
				maxRadiusSq = distSq
			}
		}
		circleRadius := math.Sqrt(maxRadiusSq) * 0.95
		if circleRadius > maxAllowedRadius {
			circleRadius = maxAllowedRadius
		}
		return geometry.GenerateCirclePoints(avgCenter.X, avgCenter.Y, circleRadius, 32)
	}

	// Neither has boundary - use averaged radius
	avgRadius := (front.Radius + back.Radius) / 2
	return geometry.GenerateCirclePoints(avgCenter.X, avgCenter.Y, avgRadius, 32)
}

// HitTest returns true if the point (x, y) is inside this confirmed via.
func (cv *ConfirmedVia) HitTest(x, y float64) bool {
	p := geometry.Point2D{X: x, Y: y}

	if len(cv.IntersectionBoundary) >= 3 {
		return geometry.PointInPolygon(p, cv.IntersectionBoundary)
	}

	// Fall back to circle test
	dx := x - cv.Center.X
	dy := y - cv.Center.Y
	return dx*dx+dy*dy <= cv.Radius*cv.Radius
}

// Bounds returns the bounding rectangle of this confirmed via.
func (cv *ConfirmedVia) Bounds() geometry.RectInt {
	if len(cv.IntersectionBoundary) >= 3 {
		bbox := geometry.BoundingBox(cv.IntersectionBoundary)
		return geometry.RectInt{
			X:      int(bbox.X),
			Y:      int(bbox.Y),
			Width:  int(bbox.Width),
			Height: int(bbox.Height),
		}
	}

	// Fall back to circle bounds
	r := int(cv.Radius)
	return geometry.RectInt{
		X:      int(cv.Center.X) - r,
		Y:      int(cv.Center.Y) - r,
		Width:  2 * r,
		Height: 2 * r,
	}
}

// UpdateIntersection recomputes the intersection boundary from the given vias.
// Call this after expanding either side's boundary.
func (cv *ConfirmedVia) UpdateIntersection(front, back *Via) {
	cv.IntersectionBoundary = ComputeIntersection(front, back)
	// Update center to centroid of new intersection
	if len(cv.IntersectionBoundary) >= 3 {
		cv.Center = geometry.Centroid(cv.IntersectionBoundary)
	}
}

// String returns a human-readable representation of the confirmed via.
func (cv *ConfirmedVia) String() string {
	return fmt.Sprintf("ConfirmedVia{ID:%s, Front:%s, Back:%s, Center:(%.1f,%.1f), Radius:%.1f, Confidence:%.2f}",
		cv.ID, cv.FrontViaID, cv.BackViaID, cv.Center.X, cv.Center.Y, cv.Radius, cv.Confidence)
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
