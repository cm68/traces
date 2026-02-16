// Package via provides through-hole via detection for PCB images.
package via

import (
	"pcb-tracer/internal/image"
	"pcb-tracer/pkg/geometry"
)

// DetectionMethod indicates how a via was detected.
type DetectionMethod int

const (
	// MethodHoughCircle indicates detection via Hough circle transform.
	MethodHoughCircle DetectionMethod = iota
	// MethodContourFit indicates detection via contour circularity analysis.
	MethodContourFit
	// MethodManual indicates a manually placed via (user input).
	MethodManual
)

func (m DetectionMethod) String() string {
	switch m {
	case MethodHoughCircle:
		return "Hough"
	case MethodContourFit:
		return "Contour"
	case MethodManual:
		return "Manual"
	default:
		return "Unknown"
	}
}

// Via represents a detected or manually placed through-hole via.
type Via struct {
	ID          string           `json:"id"`          // Unique identifier, e.g., "via-001"
	Center      geometry.Point2D `json:"center"`      // Center in image coordinates (pixels)
	Radius      float64          `json:"radius"`      // Radius in pixels (approximate, for circular vias)
	Side        image.Side       `json:"side"`        // Which side it was detected on (or placed for manual)
	Circularity float64          `json:"circularity"` // Shape quality (0-1), 1.0 for manual
	Confidence  float64          `json:"confidence"`  // Detection confidence (0-1), 1.0 for manual
	Method      DetectionMethod  `json:"method"`      // How the via was detected/created

	// Pad boundary - the detected contour of the metallic pad area.
	// If empty, the via is assumed circular with the given Radius.
	// Points are in image coordinates (pixels).
	PadBoundary []geometry.Point2D `json:"pad_boundary,omitempty"`

	// Cross-side matching - vias should appear on both sides of a PCB.
	// This is the strongest indicator that a detection is a true via.
	MatchedViaID       string `json:"matched_via_id,omitempty"`   // ID of matching via on opposite side (empty if unmatched)
	BothSidesConfirmed bool   `json:"both_sides_confirmed,omitempty"` // True if via detected on both sides at same location
}

// Bounds returns the bounding rectangle for the via.
// If PadBoundary is set, computes bounds from the contour; otherwise uses Center/Radius.
func (v Via) Bounds() geometry.RectInt {
	if len(v.PadBoundary) > 0 {
		minX, minY := v.PadBoundary[0].X, v.PadBoundary[0].Y
		maxX, maxY := minX, minY
		for _, p := range v.PadBoundary[1:] {
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
		return geometry.RectInt{
			X:      int(minX),
			Y:      int(minY),
			Width:  int(maxX - minX + 1),
			Height: int(maxY - minY + 1),
		}
	}

	r := int(v.Radius + 0.5)
	return geometry.RectInt{
		X:      int(v.Center.X) - r,
		Y:      int(v.Center.Y) - r,
		Width:  r * 2,
		Height: r * 2,
	}
}

// HitTest returns true if the point (x, y) is within the via.
// Uses point-in-polygon for PadBoundary if set; otherwise checks circular area.
func (v Via) HitTest(x, y float64) bool {
	if len(v.PadBoundary) > 2 {
		return pointInPolygon(x, y, v.PadBoundary)
	}
	dx := x - v.Center.X
	dy := y - v.Center.Y
	return dx*dx+dy*dy <= v.Radius*v.Radius
}

// pointInPolygon uses ray casting algorithm to test if point is inside polygon.
func pointInPolygon(x, y float64, polygon []geometry.Point2D) bool {
	n := len(polygon)
	inside := false
	j := n - 1
	for i := 0; i < n; i++ {
		xi, yi := polygon[i].X, polygon[i].Y
		xj, yj := polygon[j].X, polygon[j].Y
		if ((yi > y) != (yj > y)) && (x < (xj-xi)*(y-yi)/(yj-yi)+xi) {
			inside = !inside
		}
		j = i
	}
	return inside
}

// ViaDetectionResult holds the results of via detection on an image.
type ViaDetectionResult struct {
	Vias   []Via          // All detected vias
	Side   image.Side     // Which side was scanned
	DPI    float64        // Image DPI used for detection
	Params DetectionParams // Parameters used for detection
}

// DetectionParams holds parameters for via detection.
// See params.go for defaults and DPI-based calculation.
type DetectionParams struct {
	// HSV color filtering for metallic/bright circles
	HueMin, HueMax float64 // Hue range (0-180, OpenCV scale)
	SatMin, SatMax float64 // Saturation range (0-255)
	ValMin, ValMax float64 // Value/brightness range (0-255)

	// Size constraints (in pixels, calculated from DPI)
	MinRadiusPixels int
	MaxRadiusPixels int

	// Physical size hints (in inches)
	MinDiamInches float64
	MaxDiamInches float64

	// Shape constraint
	CircularityMin float64 // Minimum circularity (0-1), e.g., 0.65

	// Hough circle detection parameters
	HoughDP       float64 // Inverse ratio of accumulator resolution (1.0-2.0)
	HoughMinDist  int     // Minimum distance between circle centers (pixels)
	HoughParam1   float64 // Canny edge detector high threshold
	HoughParam2   float64 // Accumulator threshold for circle detection

	// DPI for size calculations
	DPI float64
}
