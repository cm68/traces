// Package canvas provides overlay types for the image canvas.
package canvas

import (
	"image/color"

	"pcb-tracer/pkg/geometry"
)

// LayerRef indicates which layer an overlay is associated with.
type LayerRef int

const (
	LayerNone  LayerRef = iota // No layer association (use canvas coordinates)
	LayerFront                 // Associated with front layer
	LayerBack                  // Associated with back layer
)

// Overlay represents a drawable overlay on the canvas.
type Overlay struct {
	Rectangles []OverlayRect
	Polygons   []OverlayPolygon
	Circles    []OverlayCircle
	Lines      []OverlayLine
	Color      color.RGBA
	Layer      LayerRef // Which layer this overlay is associated with (for offset)
}

// OverlayCircle represents a circle to draw on the overlay.
type OverlayCircle struct {
	X, Y   float64 // Center position in image coordinates
	Radius float64 // Radius in pixels (image coordinates)
	Filled bool    // If true, fill the circle; otherwise just outline
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

// OverlayLine represents a line segment to draw on the overlay.
type OverlayLine struct {
	X1, Y1, X2, Y2 float64 // Endpoints in image coordinates
	Thickness       int     // Line thickness in pixels (0 = default 2)
}
