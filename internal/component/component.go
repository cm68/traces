// Package component provides component detection, storage, and management.
package component

import (
	"fmt"

	"pcb-tracer/internal/image"
	"pcb-tracer/pkg/geometry"
)

// Component represents a detected or user-defined component on the board.
type Component struct {
	ID          string        `json:"id"`          // Unique identifier, e.g., "U1", "C5"
	PartNumber  string        `json:"part_number"` // Part number, e.g., "74LS244"
	Description string        `json:"description"` // Description from OCR or user
	Package     string        `json:"package"`     // Package type, e.g., "DIP-20"
	Bounds      geometry.Rect `json:"bounds"`      // Bounding box in image coordinates
	Layer       image.Side    `json:"layer"`       // Which side of the board
	Confirmed   bool          `json:"confirmed"`   // User verified
	Pins        []Pin         `json:"pins"`        // Pin positions and nets
	Rotation    float64       `json:"rotation"`    // Rotation in degrees
	OCRText     string        `json:"ocr_text"`    // Raw OCR result from detection

	// OCR orientation and corrected text for training
	OCROrientation string `json:"ocr_orientation,omitempty"` // N/S/E/W - remembered orientation
	CorrectedText  string `json:"corrected_text,omitempty"`  // User-verified text for training

	// Additional component metadata
	Manufacturer string `json:"manufacturer,omitempty"` // Manufacturer name, e.g., "Texas Instruments"
	Place        string `json:"place,omitempty"`        // Manufacturing location
	DateCode     string `json:"date_code,omitempty"`    // Date code, e.g., "8523" (year/week)
	Revision     string `json:"revision,omitempty"`     // Revision/version
	SpeedGrade   string `json:"speed_grade,omitempty"`  // Speed grade, e.g., "-25", "-45"
}

// Pin represents a single pin on a component.
type Pin struct {
	Number   int            `json:"number"`   // Pin number
	Name     string         `json:"name"`     // Pin name (e.g., "VCC", "GND")
	Position geometry.Point2D `json:"position"` // Position relative to component
	Net      string         `json:"net"`      // Connected net name
}

// NewComponent creates a new Component with default values.
func NewComponent(id string) *Component {
	return &Component{
		ID:        id,
		Confirmed: false,
	}
}

// Center returns the center point of the component.
func (c *Component) Center() geometry.Point2D {
	return c.Bounds.Center()
}

// AddPin adds a pin to the component.
func (c *Component) AddPin(number int, name string, pos geometry.Point2D) {
	c.Pins = append(c.Pins, Pin{
		Number:   number,
		Name:     name,
		Position: pos,
	})
}

// GetPin returns the pin with the specified number.
func (c *Component) GetPin(number int) *Pin {
	for i := range c.Pins {
		if c.Pins[i].Number == number {
			return &c.Pins[i]
		}
	}
	return nil
}

// PackageType represents a standard component package type.
type PackageType struct {
	Name       string  // e.g., "DIP-20"
	Width      float64 // Package width in mm
	Height     float64 // Package height in mm
	PinCount   int     // Number of pins
	PinPitch   float64 // Pin pitch in mm
	RowSpacing float64 // Distance between pin rows in mm
}

// StandardPackages contains definitions for common IC packages.
var StandardPackages = map[string]PackageType{
	"DIP-8":  {Name: "DIP-8", Width: 6.35, Height: 9.65, PinCount: 8, PinPitch: 2.54, RowSpacing: 7.62},
	"DIP-14": {Name: "DIP-14", Width: 6.35, Height: 19.05, PinCount: 14, PinPitch: 2.54, RowSpacing: 7.62},
	"DIP-16": {Name: "DIP-16", Width: 6.35, Height: 20.32, PinCount: 16, PinPitch: 2.54, RowSpacing: 7.62},
	"DIP-18": {Name: "DIP-18", Width: 6.35, Height: 22.86, PinCount: 18, PinPitch: 2.54, RowSpacing: 7.62},
	"DIP-20": {Name: "DIP-20", Width: 6.35, Height: 25.40, PinCount: 20, PinPitch: 2.54, RowSpacing: 7.62},
	"DIP-24": {Name: "DIP-24", Width: 6.35, Height: 31.75, PinCount: 24, PinPitch: 2.54, RowSpacing: 7.62},
	"DIP-28": {Name: "DIP-28", Width: 6.35, Height: 35.56, PinCount: 28, PinPitch: 2.54, RowSpacing: 7.62},
	"DIP-40": {Name: "DIP-40", Width: 15.24, Height: 52.58, PinCount: 40, PinPitch: 2.54, RowSpacing: 15.24},
}

// List manages a collection of components.
type List struct {
	Components []*Component
	nextID     map[string]int // Track next ID for each prefix (U, R, C, etc.)
}

// NewList creates a new component list.
func NewList() *List {
	return &List{
		nextID: make(map[string]int),
	}
}

// Add adds a component to the list.
func (l *List) Add(c *Component) {
	l.Components = append(l.Components, c)
}

// Remove removes a component by ID.
func (l *List) Remove(id string) bool {
	for i, c := range l.Components {
		if c.ID == id {
			l.Components = append(l.Components[:i], l.Components[i+1:]...)
			return true
		}
	}
	return false
}

// Get returns a component by ID.
func (l *List) Get(id string) *Component {
	for _, c := range l.Components {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// GenerateID generates a unique ID with the given prefix (e.g., "U" for ICs).
func (l *List) GenerateID(prefix string) string {
	l.nextID[prefix]++
	return prefix + string(rune('0'+l.nextID[prefix]))
}

// Count returns the number of components.
func (l *List) Count() int {
	return len(l.Components)
}

// Filter returns components matching the given layer.
func (l *List) Filter(layer image.Side) []*Component {
	var result []*Component
	for _, c := range l.Components {
		if c.Layer == layer {
			result = append(result, c)
		}
	}
	return result
}

// TrainingSample represents a user-selected region for training the detector.
type TrainingSample struct {
	Bounds geometry.Rect `json:"bounds"` // Bounding box in image coordinates

	// Extracted features (populated when sample is added)
	MeanHue    float64 `json:"mean_hue"`    // Average hue (0-180)
	MeanSat    float64 `json:"mean_sat"`    // Average saturation (0-255)
	MeanVal    float64 `json:"mean_val"`    // Average value (0-255)
	WidthMM    float64 `json:"width_mm"`    // Width in mm
	HeightMM   float64 `json:"height_mm"`   // Height in mm
	WhiteRatio float64 `json:"white_ratio"` // Percentage of white pixels

	// Histogram-derived peaks (bimodal: dark background + light markings)
	BackgroundVal float64 `json:"bg_val"`      // V peak for dark background (black plastic)
	MarkingVal    float64 `json:"marking_val"` // V peak for light markings (white text)
	BackgroundPct float64 `json:"bg_pct"`      // Percentage of pixels at background peak
	MarkingPct    float64 `json:"marking_pct"` // Percentage of pixels at marking peak
}

// TrainingSet holds training samples for conditioning the detector.
type TrainingSet struct {
	Samples []TrainingSample `json:"samples"`
}

// NewTrainingSet creates an empty training set.
func NewTrainingSet() *TrainingSet {
	return &TrainingSet{}
}

// Add adds a training sample to the set.
func (ts *TrainingSet) Add(sample TrainingSample) {
	ts.Samples = append(ts.Samples, sample)
}

// Clear removes all training samples.
func (ts *TrainingSet) Clear() {
	ts.Samples = nil
}

// Count returns the number of samples.
func (ts *TrainingSet) Count() int {
	return len(ts.Samples)
}

// DeriveParams derives detection parameters from the training samples.
// Creates distinct color profiles for different component types (e.g. black ICs, grey ICs).
// Returns default params if no samples are available.
func (ts *TrainingSet) DeriveParams() DetectionParams {
	if len(ts.Samples) == 0 {
		return DefaultParams()
	}

	fmt.Printf("=== Deriving params from %d training samples ===\n", len(ts.Samples))

	// Collect sample V values and saturation for clustering
	type sampleData struct {
		bgVal  float64
		satMax float64
	}
	var samples []sampleData
	var minWidth, maxWidth, minHeight, maxHeight float64 = 1e9, 0, 1e9, 0

	for i, s := range ts.Samples {
		// Use histogram-derived background value if available, else fall back to mean
		bgVal := s.BackgroundVal
		if bgVal == 0 {
			bgVal = s.MeanVal
		}
		fmt.Printf("  Sample %d: BgVal=%.0f MeanSat=%.0f Size=%.1fx%.1f mm\n",
			i+1, bgVal, s.MeanSat, s.WidthMM, s.HeightMM)

		samples = append(samples, sampleData{bgVal: bgVal, satMax: s.MeanSat})

		if i == 0 || s.WidthMM < minWidth {
			minWidth = s.WidthMM
		}
		if s.WidthMM > maxWidth {
			maxWidth = s.WidthMM
		}
		if i == 0 || s.HeightMM < minHeight {
			minHeight = s.HeightMM
		}
		if s.HeightMM > maxHeight {
			maxHeight = s.HeightMM
		}
	}

	fmt.Printf("  Size range: %.1f-%.1f x %.1f-%.1f mm\n", minWidth, maxWidth, minHeight, maxHeight)

	// Cluster samples into distinct color profiles based on V value
	// Two samples are in the same cluster if their V values are within 30 of each other
	const clusterThreshold = 30.0
	var profiles []ColorProfile
	used := make([]bool, len(samples))

	for i := 0; i < len(samples); i++ {
		if used[i] {
			continue
		}

		// Start new cluster with this sample
		cluster := []sampleData{samples[i]}
		used[i] = true

		// Find other samples within threshold
		for j := i + 1; j < len(samples); j++ {
			if used[j] {
				continue
			}
			// Check if within threshold of any sample in cluster
			for _, cs := range cluster {
				if abs64(samples[j].bgVal-cs.bgVal) <= clusterThreshold {
					cluster = append(cluster, samples[j])
					used[j] = true
					break
				}
			}
		}

		// Create profile from cluster
		var minV, maxV, maxSat float64 = 255, 0, 0
		for _, cs := range cluster {
			if cs.bgVal < minV {
				minV = cs.bgVal
			}
			if cs.bgVal > maxV {
				maxV = cs.bgVal
			}
			if cs.satMax > maxSat {
				maxSat = cs.satMax
			}
		}

		// Add generous margins to the V range to catch similar components
		profile := ColorProfile{
			ValueMin: minV * 0.3,          // 70% below observed min (very permissive)
			ValueMax: maxV * 2.0,          // 100% above observed max (very permissive)
			SatMax:   maxSat * 3.0,        // 3x observed max saturation
		}
		// Clamp ranges
		if profile.ValueMin < 0 {
			profile.ValueMin = 0
		}
		if profile.ValueMax > 255 {
			profile.ValueMax = 255 // Allow full range - don't cap at 200
		}
		if profile.SatMax < 100 {
			profile.SatMax = 100
		}
		if profile.SatMax > 255 {
			profile.SatMax = 255
		}

		profiles = append(profiles, profile)
		fmt.Printf("  Profile %d: V=%.0f-%.0f, SatMax=%.0f (from %d samples)\n",
			len(profiles), profile.ValueMin, profile.ValueMax, profile.SatMax, len(cluster))
	}

	// Calculate fallback single threshold (for compatibility)
	var sumBgVal, sumSat float64
	for _, s := range samples {
		sumBgVal += s.bgVal
		sumSat += s.satMax
	}
	n := float64(len(samples))
	avgBgVal := sumBgVal / n
	avgSat := sumSat / n

	params := DetectionParams{
		// Fallback thresholds (generous)
		ValueMax: avgBgVal * 2.0,
		SatMax:   avgSat * 3.0,

		// Multiple color profiles for distinct component types
		ColorProfiles: profiles,

		// Size constraints: very permissive - allow smaller and larger than samples
		MinWidth:  minWidth * 0.5,  // Allow 50% smaller
		MaxWidth:  maxWidth * 2.0,  // Allow 100% larger
		MinHeight: minHeight * 0.5, // Allow 50% smaller
		MaxHeight: maxHeight * 2.0, // Allow 100% larger

		// Very permissive aspect ratio and quality
		MinAspectRatio: 0.3,
		MaxAspectRatio: 20.0,
		MinSolidity:    0.3,
		MinWhitePixels: 0.0,
	}

	// Ensure reasonable bounds for fallback (permissive)
	if params.ValueMax < 80 {
		params.ValueMax = 80
	}
	if params.ValueMax > 255 {
		params.ValueMax = 255
	}
	if params.SatMax < 100 {
		params.SatMax = 100
	}
	if params.SatMax > 255 {
		params.SatMax = 255
	}

	fmt.Printf("  Derived %d color profiles\n", len(profiles))
	fmt.Printf("  Fallback: ValueMax=%.0f SatMax=%.0f\n", params.ValueMax, params.SatMax)
	fmt.Printf("  Derived size: %.1f-%.1f x %.1f-%.1f mm\n",
		params.MinWidth, params.MaxWidth, params.MinHeight, params.MaxHeight)
	fmt.Printf("=============================================\n")

	return params
}

// abs64 returns the absolute value of a float64.
func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// LogoSample represents a user-selected logo region for training.
type LogoSample struct {
	Bounds geometry.Rect `json:"bounds"` // Bounding box in image coordinates
	Name   string        `json:"name"`   // Optional name (e.g., "TI", "Motorola", "NatSemi")

	// Size in mm
	WidthMM  float64 `json:"width_mm"`
	HeightMM float64 `json:"height_mm"`

	// Color profile from histogram analysis
	BackgroundVal float64 `json:"bg_val"`      // V peak for background
	ForegroundVal float64 `json:"fg_val"`      // V peak for foreground (logo)
	BackgroundPct float64 `json:"bg_pct"`      // Percentage at background peak
	ForegroundPct float64 `json:"fg_pct"`      // Percentage at foreground peak

	// Contrast ratio (foreground/background brightness)
	ContrastRatio float64 `json:"contrast_ratio"`
}

// LogoSet holds logo samples for detection.
type LogoSet struct {
	Samples []LogoSample `json:"samples"`
}

// NewLogoSet creates an empty logo set.
func NewLogoSet() *LogoSet {
	return &LogoSet{}
}

// Add adds a logo sample to the set.
func (ls *LogoSet) Add(sample LogoSample) {
	ls.Samples = append(ls.Samples, sample)
}

// Clear removes all logo samples.
func (ls *LogoSet) Clear() {
	ls.Samples = nil
}

// Count returns the number of samples.
func (ls *LogoSet) Count() int {
	return len(ls.Samples)
}
