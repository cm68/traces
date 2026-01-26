// Package alignment provides image alignment algorithms for PCB scans.
package alignment

import (
	"fmt"

	"pcb-tracer/internal/board"
	"pcb-tracer/pkg/geometry"
)

// DetectionPass indicates which detection pass found the contact.
type DetectionPass int

const (
	PassFirst      DetectionPass = iota // Found in first/normal pass
	PassBruteForce                      // Found in brute force pass
	PassRescue                          // Found in rescue pass
)

// Contact represents a detected gold edge contact.
type Contact struct {
	Bounds geometry.RectInt // Bounding rectangle
	Center geometry.Point2D // Center point
	Pass   DetectionPass    // Which detection pass found this contact
}

// DetectionResult holds contact detection results.
type DetectionResult struct {
	Contacts          []Contact
	ExpectedPositions []geometry.RectInt // All 50 expected contact positions (for overlay)
	Edge              string             // "top", "bottom", "left", "right"
	Rotation          int                // Degrees rotated (0, 90, 180, 270)
	BoardBounds       geometry.RectInt
	SearchBounds      geometry.RectInt // The area that was searched for contacts
	DPI               float64
	ContactAngle      float64 // Fine rotation angle to align contacts (degrees)
}

// DetectionParams holds parameters for contact detection.
type DetectionParams struct {
	HueMin, HueMax float64
	SatMin, SatMax float64
	ValMin, ValMax float64
	AspectMin      float64
	AspectMax      float64
	MinArea        int
	MaxArea        int
	DPI            float64 // For logging dimensions in inches
}

// DefaultDetectionParams returns default gold contact detection parameters.
func DefaultDetectionParams() DetectionParams {
	return DetectionParams{
		HueMin:    15,
		HueMax:    35,
		SatMin:    80,
		SatMax:    255,
		ValMin:    120,
		ValMax:    255,
		AspectMin: 4.0,
		AspectMax: 8.0,
		MinArea:   2000,
		MaxArea:   20000,
	}
}

// ParamsFromSpec extracts detection parameters from a board spec.
func ParamsFromSpec(spec board.Spec) DetectionParams {
	params := DefaultDetectionParams()
	if spec == nil {
		return params
	}
	contacts := spec.ContactSpec()
	if contacts == nil || contacts.Detection == nil {
		return params
	}
	det := contacts.Detection
	params.HueMin = det.Color.HueMin
	params.HueMax = det.Color.HueMax
	params.SatMin = det.Color.SatMin
	params.SatMax = det.Color.SatMax
	params.ValMin = det.Color.ValMin
	params.ValMax = det.Color.ValMax
	params.AspectMin = det.AspectRatioMin
	params.AspectMax = det.AspectRatioMax
	params.MinArea = det.MinAreaPixels
	params.MaxArea = det.MaxAreaPixels
	return params
}

// ParamsFromSpecWithDPI calculates detection parameters using known DPI.
func ParamsFromSpecWithDPI(spec board.Spec, dpi float64) DetectionParams {
	params := ParamsFromSpec(spec)
	params.DPI = dpi

	if dpi <= 0 || spec == nil {
		return params
	}

	contacts := spec.ContactSpec()
	if contacts == nil {
		return params
	}

	// Calculate expected contact size in pixels
	widthPixels := contacts.WidthInches * dpi
	heightPixels := contacts.HeightInches * dpi

	// Expected area with tolerance
	nominalArea := widthPixels * heightPixels
	params.MinArea = int(nominalArea * 0.4)  // Allow 60% smaller
	params.MaxArea = int(nominalArea * 1.8)  // Allow 80% larger

	// Aspect ratio (height/width for vertical contacts)
	nominalAspect := heightPixels / widthPixels
	params.AspectMin = nominalAspect * 0.5
	params.AspectMax = nominalAspect * 1.5

	fmt.Printf("DPI-based params: contact=%.1fx%.1f px, area=%d-%d, aspect=%.1f-%.1f\n",
		widthPixels, heightPixels, params.MinArea, params.MaxArea, params.AspectMin, params.AspectMax)

	return params
}

// ExpectedContactDimensions returns expected contact dimensions in pixels for a given DPI.
type ExpectedContactDimensions struct {
	Width      float64 // Individual contact width in pixels
	Height     float64 // Individual contact height in pixels
	Pitch      float64 // Center-to-center spacing in pixels
	TotalWidth float64 // Total connector width in pixels
	Count      int     // Expected number of contacts
	MarginLeft float64 // Left margin to first contact center in pixels
}

// GetExpectedDimensions calculates expected contact dimensions from spec and DPI.
func GetExpectedDimensions(spec board.Spec, dpi float64) *ExpectedContactDimensions {
	if spec == nil || spec.ContactSpec() == nil || dpi <= 0 {
		return nil
	}
	contacts := spec.ContactSpec()
	return &ExpectedContactDimensions{
		Width:      contacts.WidthInches * dpi,
		Height:     contacts.HeightInches * dpi,
		Pitch:      contacts.PitchInches * dpi,
		TotalWidth: contacts.TotalWidthInches() * dpi,
		Count:      contacts.Count,
		MarginLeft: contacts.MarginInches * dpi,
	}
}

// ContactLineParams holds parameters derived from detected contacts for finding missing ones.
type ContactLineParams struct {
	LineY     float64 // Y position of contact centers (for horizontal edges)
	LineX     float64 // X position of contact centers (for vertical edges)
	Pitch     float64 // Spacing between contact centers in pixels
	StartPos  float64 // Position of first contact along edge
	AvgWidth  float64 // Average contact width in pixels
	AvgHeight float64 // Average contact height in pixels
	Count     int     // Expected total count
}

// ContactTemplate holds size/aspect info from a successful detection for use on another image.
type ContactTemplate struct {
	AvgWidth  float64
	AvgHeight float64
	MinWidth  int
	MaxWidth  int
	MinHeight int
	MaxHeight int
	MinAspect float64
	MaxAspect float64
}

// ContactCandidate represents a potential contact at an expected grid position.
type ContactCandidate struct {
	Position  geometry.RectInt // Expected position on the grid
	Contact   *Contact         // Detected contact (nil if not found)
	Score     float64          // Quality score based on color match
	ColorAvgR float64          // Average red in the region
	ColorAvgG float64          // Average green
	ColorAvgB float64          // Average blue
	ColorStdR float64          // Std deviation of red
	ColorStdG float64          // Std deviation of green
	ColorStdB float64          // Std deviation of blue
}
