package via

import (
	"fmt"

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

// ComputeIntersection computes the polygon intersection of two vias' boundaries.
// If either via lacks boundary points, circle points are generated.
// Returns the intersection polygon, or a circle at the averaged center if intersection fails.
func ComputeIntersection(front, back *Via) []geometry.Point2D {
	frontBoundary := front.PadBoundary
	if len(frontBoundary) < 3 {
		frontBoundary = geometry.GenerateCirclePoints(
			front.Center.X, front.Center.Y, front.Radius, 32)
	}

	backBoundary := back.PadBoundary
	if len(backBoundary) < 3 {
		backBoundary = geometry.GenerateCirclePoints(
			back.Center.X, back.Center.Y, back.Radius, 32)
	}

	// Ensure convex polygons for Sutherland-Hodgman
	if !geometry.IsConvex(frontBoundary) {
		frontBoundary = geometry.ConvexHull(frontBoundary)
	}
	if !geometry.IsConvex(backBoundary) {
		backBoundary = geometry.ConvexHull(backBoundary)
	}

	intersection := geometry.IntersectPolygons(frontBoundary, backBoundary)

	// If no intersection (shouldn't happen for matched vias), use averaged circle
	if len(intersection) < 3 {
		avgCenter := geometry.Point2D{
			X: (front.Center.X + back.Center.X) / 2,
			Y: (front.Center.Y + back.Center.Y) / 2,
		}
		avgRadius := (front.Radius + back.Radius) / 2
		return geometry.GenerateCirclePoints(avgCenter.X, avgCenter.Y, avgRadius, 32)
	}

	return intersection
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
