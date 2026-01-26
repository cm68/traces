package geometry

import "math"

// ConvexHull computes the convex hull of a set of points using Graham scan.
// Returns the points forming the convex hull in counter-clockwise order.
func ConvexHull(points []Point2D) []Point2D {
	if len(points) < 3 {
		return points
	}

	// Make a copy to avoid modifying the input
	pts := make([]Point2D, len(points))
	copy(pts, points)

	// Find the point with lowest y (and leftmost if tied)
	lowest := 0
	for i := 1; i < len(pts); i++ {
		if pts[i].Y < pts[lowest].Y ||
			(pts[i].Y == pts[lowest].Y && pts[i].X < pts[lowest].X) {
			lowest = i
		}
	}

	// Swap to front
	pts[0], pts[lowest] = pts[lowest], pts[0]
	pivot := pts[0]

	// Sort by polar angle with respect to pivot
	sorted := make([]Point2D, len(pts)-1)
	copy(sorted, pts[1:])

	// Sort by angle (bubble sort for simplicity)
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			cross := crossProduct(pivot, sorted[i], sorted[j])
			if cross < 0 || (cross == 0 && distSq(pivot, sorted[i]) > distSq(pivot, sorted[j])) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	// Build hull
	hull := []Point2D{pivot}
	for _, p := range sorted {
		for len(hull) > 1 && crossProduct(hull[len(hull)-2], hull[len(hull)-1], p) <= 0 {
			hull = hull[:len(hull)-1]
		}
		hull = append(hull, p)
	}

	return hull
}

// IsConvex returns true if the polygon vertices form a convex polygon.
// The polygon is assumed to be simple (non-self-intersecting).
func IsConvex(polygon []Point2D) bool {
	if len(polygon) < 3 {
		return false
	}

	n := len(polygon)
	var sign int

	for i := 0; i < n; i++ {
		cross := crossProduct(
			polygon[i],
			polygon[(i+1)%n],
			polygon[(i+2)%n],
		)

		if cross != 0 {
			currentSign := 1
			if cross < 0 {
				currentSign = -1
			}

			if sign == 0 {
				sign = currentSign
			} else if currentSign != sign {
				return false
			}
		}
	}

	return true
}

// IntersectPolygons computes the intersection of two convex polygons using
// the Sutherland-Hodgman algorithm. Both input polygons must be convex.
// Returns nil if there is no intersection or if inputs are invalid.
func IntersectPolygons(subject, clip []Point2D) []Point2D {
	if len(subject) < 3 || len(clip) < 3 {
		return nil
	}

	output := make([]Point2D, len(subject))
	copy(output, subject)

	// Clip against each edge of the clip polygon
	for i := 0; i < len(clip); i++ {
		if len(output) == 0 {
			return nil
		}

		edgeStart := clip[i]
		edgeEnd := clip[(i+1)%len(clip)]
		output = clipPolygonByEdge(output, edgeStart, edgeEnd)
	}

	if len(output) < 3 {
		return nil
	}

	return output
}

// clipPolygonByEdge clips a polygon against a single edge using
// the Sutherland-Hodgman algorithm.
func clipPolygonByEdge(polygon []Point2D, edgeStart, edgeEnd Point2D) []Point2D {
	var clipped []Point2D

	for i := 0; i < len(polygon); i++ {
		current := polygon[i]
		next := polygon[(i+1)%len(polygon)]

		currentInside := isInsideEdge(current, edgeStart, edgeEnd)
		nextInside := isInsideEdge(next, edgeStart, edgeEnd)

		if currentInside {
			clipped = append(clipped, current)
			if !nextInside {
				// Exiting: add intersection point
				if intersection, ok := lineIntersection(current, next, edgeStart, edgeEnd); ok {
					clipped = append(clipped, intersection)
				}
			}
		} else if nextInside {
			// Entering: add intersection point
			if intersection, ok := lineIntersection(current, next, edgeStart, edgeEnd); ok {
				clipped = append(clipped, intersection)
			}
		}
	}

	return clipped
}

// isInsideEdge checks if a point is on the inside (left side) of the directed edge.
// The clip polygon is assumed to be in counter-clockwise order.
func isInsideEdge(p, edgeStart, edgeEnd Point2D) bool {
	return (edgeEnd.X-edgeStart.X)*(p.Y-edgeStart.Y)-
		(edgeEnd.Y-edgeStart.Y)*(p.X-edgeStart.X) >= 0
}

// lineIntersection computes the intersection point of line segment p1-p2
// with line segment e1-e2. Returns the point and true if they intersect.
func lineIntersection(p1, p2, e1, e2 Point2D) (Point2D, bool) {
	x1, y1 := p1.X, p1.Y
	x2, y2 := p2.X, p2.Y
	x3, y3 := e1.X, e1.Y
	x4, y4 := e2.X, e2.Y

	denom := (x1-x2)*(y3-y4) - (y1-y2)*(x3-x4)
	if math.Abs(denom) < 1e-10 {
		// Lines are parallel
		return Point2D{}, false
	}

	t := ((x1-x3)*(y3-y4) - (y1-y3)*(x3-x4)) / denom

	return Point2D{
		X: x1 + t*(x2-x1),
		Y: y1 + t*(y2-y1),
	}, true
}

// PointInPolygon tests if a point is inside a polygon using ray casting.
func PointInPolygon(p Point2D, polygon []Point2D) bool {
	if len(polygon) < 3 {
		return false
	}

	inside := false
	n := len(polygon)

	for i := 0; i < n; i++ {
		j := (i + 1) % n
		pi, pj := polygon[i], polygon[j]

		// Check if ray from p going right intersects edge pi-pj
		if ((pi.Y > p.Y) != (pj.Y > p.Y)) &&
			(p.X < (pj.X-pi.X)*(p.Y-pi.Y)/(pj.Y-pi.Y)+pi.X) {
			inside = !inside
		}
	}

	return inside
}

// crossProduct computes the cross product of vectors OA and OB.
func crossProduct(o, a, b Point2D) float64 {
	return (a.X-o.X)*(b.Y-o.Y) - (a.Y-o.Y)*(b.X-o.X)
}

// distSq computes the squared distance between two points.
func distSq(a, b Point2D) float64 {
	dx := b.X - a.X
	dy := b.Y - a.Y
	return dx*dx + dy*dy
}
