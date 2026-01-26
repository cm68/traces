// Package canvas provides overlay types for the image canvas.
package canvas

import (
	"image/color"

	"pcb-tracer/pkg/geometry"
)

// Overlay represents a drawable overlay on the canvas.
type Overlay struct {
	Rectangles []OverlayRect
	Polygons   []OverlayPolygon
	Color      color.RGBA
}

// FillPattern indicates how to fill a rectangle.
type FillPattern int

const (
	FillNone       FillPattern = iota // Just outline
	FillSolid                         // Solid fill
	FillStripe                        // Diagonal stripe
	FillCrosshatch                    // Diagonal crosshatch
	FillTarget                        // Crosshairs through center (target marker)
)

// OverlayRect represents a rectangle to draw on the overlay.
type OverlayRect struct {
	X, Y, Width, Height int
	Label               string      // Optional label to draw centered in the rectangle
	Fill                FillPattern // Fill pattern for the rectangle
	StripeInterval      int         // Interval for stripe/crosshatch patterns (0 = use width)
}

// OverlayPolygon represents a polygon to draw on the overlay.
type OverlayPolygon struct {
	Points []geometry.Point2D // Polygon vertices in image coordinates
	Label  string             // Optional label to draw at center
	Filled bool               // If true, fill the polygon; otherwise just outline
}
