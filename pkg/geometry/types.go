// Package geometry provides basic geometric types used throughout the application.
package geometry

import (
	"math"
)

// Point2D represents a 2D point with floating-point coordinates.
type Point2D struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// NewPoint2D creates a new Point2D.
func NewPoint2D(x, y float64) Point2D {
	return Point2D{X: x, Y: y}
}

// Distance returns the Euclidean distance to another point.
func (p Point2D) Distance(other Point2D) float64 {
	dx := p.X - other.X
	dy := p.Y - other.Y
	return math.Sqrt(dx*dx + dy*dy)
}

// Add returns the sum of two points.
func (p Point2D) Add(other Point2D) Point2D {
	return Point2D{X: p.X + other.X, Y: p.Y + other.Y}
}

// Sub returns the difference of two points.
func (p Point2D) Sub(other Point2D) Point2D {
	return Point2D{X: p.X - other.X, Y: p.Y - other.Y}
}

// Scale returns the point scaled by a factor.
func (p Point2D) Scale(factor float64) Point2D {
	return Point2D{X: p.X * factor, Y: p.Y * factor}
}

// PointInt represents a 2D point with integer coordinates.
type PointInt struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// ToFloat converts to Point2D.
func (p PointInt) ToFloat() Point2D {
	return Point2D{X: float64(p.X), Y: float64(p.Y)}
}

// Rect represents a rectangle with floating-point coordinates.
type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// NewRect creates a new Rect.
func NewRect(x, y, width, height float64) Rect {
	return Rect{X: x, Y: y, Width: width, Height: height}
}

// Contains returns true if the point is inside the rectangle.
func (r Rect) Contains(p Point2D) bool {
	return p.X >= r.X && p.X <= r.X+r.Width &&
		p.Y >= r.Y && p.Y <= r.Y+r.Height
}

// Center returns the center point of the rectangle.
func (r Rect) Center() Point2D {
	return Point2D{X: r.X + r.Width/2, Y: r.Y + r.Height/2}
}

// TopLeft returns the top-left corner.
func (r Rect) TopLeft() Point2D {
	return Point2D{X: r.X, Y: r.Y}
}

// BottomRight returns the bottom-right corner.
func (r Rect) BottomRight() Point2D {
	return Point2D{X: r.X + r.Width, Y: r.Y + r.Height}
}

// Intersects returns true if this rectangle intersects with another.
func (r Rect) Intersects(other Rect) bool {
	return r.X < other.X+other.Width && r.X+r.Width > other.X &&
		r.Y < other.Y+other.Height && r.Y+r.Height > other.Y
}

// Union returns the smallest rectangle containing both rectangles.
func (r Rect) Union(other Rect) Rect {
	x := math.Min(r.X, other.X)
	y := math.Min(r.Y, other.Y)
	x2 := math.Max(r.X+r.Width, other.X+other.Width)
	y2 := math.Max(r.Y+r.Height, other.Y+other.Height)
	return Rect{X: x, Y: y, Width: x2 - x, Height: y2 - y}
}

// RectInt represents a rectangle with integer coordinates.
type RectInt struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// ToFloat converts to Rect.
func (r RectInt) ToFloat() Rect {
	return Rect{X: float64(r.X), Y: float64(r.Y), Width: float64(r.Width), Height: float64(r.Height)}
}

// AffineTransform represents a 2x3 affine transformation matrix.
// [a b tx]
// [c d ty]
type AffineTransform struct {
	A, B, TX float64
	C, D, TY float64
}

// Identity returns the identity transform.
func Identity() AffineTransform {
	return AffineTransform{A: 1, D: 1}
}

// Translation returns a translation transform.
func Translation(tx, ty float64) AffineTransform {
	return AffineTransform{A: 1, D: 1, TX: tx, TY: ty}
}

// Rotation returns a rotation transform around the origin.
func Rotation(radians float64) AffineTransform {
	cos := math.Cos(radians)
	sin := math.Sin(radians)
	return AffineTransform{A: cos, B: -sin, C: sin, D: cos}
}

// Scale returns a scaling transform.
func Scale(sx, sy float64) AffineTransform {
	return AffineTransform{A: sx, D: sy}
}

// Apply applies the transform to a point.
func (t AffineTransform) Apply(p Point2D) Point2D {
	return Point2D{
		X: t.A*p.X + t.B*p.Y + t.TX,
		Y: t.C*p.X + t.D*p.Y + t.TY,
	}
}

// Compose returns this transform composed with another (this * other).
func (t AffineTransform) Compose(other AffineTransform) AffineTransform {
	return AffineTransform{
		A:  t.A*other.A + t.B*other.C,
		B:  t.A*other.B + t.B*other.D,
		TX: t.A*other.TX + t.B*other.TY + t.TX,
		C:  t.C*other.A + t.D*other.C,
		D:  t.C*other.B + t.D*other.D,
		TY: t.C*other.TX + t.D*other.TY + t.TY,
	}
}

// Inverse returns the inverse transform, if it exists.
func (t AffineTransform) Inverse() (AffineTransform, bool) {
	det := t.A*t.D - t.B*t.C
	if math.Abs(det) < 1e-10 {
		return AffineTransform{}, false
	}

	invDet := 1.0 / det
	return AffineTransform{
		A:  t.D * invDet,
		B:  -t.B * invDet,
		TX: (t.B*t.TY - t.D*t.TX) * invDet,
		C:  -t.C * invDet,
		D:  t.A * invDet,
		TY: (t.C*t.TX - t.A*t.TY) * invDet,
	}, true
}

// ToMatrix returns the transform as a [2][3]float64 array.
func (t AffineTransform) ToMatrix() [2][3]float64 {
	return [2][3]float64{
		{t.A, t.B, t.TX},
		{t.C, t.D, t.TY},
	}
}

// FromMatrix creates an AffineTransform from a [2][3]float64 array.
func FromMatrix(m [2][3]float64) AffineTransform {
	return AffineTransform{
		A: m[0][0], B: m[0][1], TX: m[0][2],
		C: m[1][0], D: m[1][1], TY: m[1][2],
	}
}

// Size represents a 2D size.
type Size struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// NewSize creates a new Size.
func NewSize(width, height float64) Size {
	return Size{Width: width, Height: height}
}

// GenerateCirclePoints generates n evenly-spaced points around a circle.
func GenerateCirclePoints(centerX, centerY, radius float64, n int) []Point2D {
	points := make([]Point2D, n)
	for i := 0; i < n; i++ {
		angle := float64(i) * 2.0 * math.Pi / float64(n)
		points[i] = Point2D{
			X: centerX + radius*math.Cos(angle),
			Y: centerY + radius*math.Sin(angle),
		}
	}
	return points
}

// Centroid computes the centroid (average position) of a set of points.
func Centroid(points []Point2D) Point2D {
	if len(points) == 0 {
		return Point2D{}
	}
	var sumX, sumY float64
	for _, p := range points {
		sumX += p.X
		sumY += p.Y
	}
	n := float64(len(points))
	return Point2D{X: sumX / n, Y: sumY / n}
}

// BoundingBox computes the axis-aligned bounding box of a set of points.
func BoundingBox(points []Point2D) Rect {
	if len(points) == 0 {
		return Rect{}
	}
	minX, minY := points[0].X, points[0].Y
	maxX, maxY := minX, minY
	for _, p := range points[1:] {
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
	return Rect{X: minX, Y: minY, Width: maxX - minX, Height: maxY - minY}
}
