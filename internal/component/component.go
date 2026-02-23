// Package component provides component detection, storage, and management.
package component

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

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

// SizeTemplate represents an expected component size derived from training data.
type SizeTemplate struct {
	WidthMM     float64 // Mean width in mm
	HeightMM    float64 // Mean height in mm
	MinWidthMM  float64 // Min observed width in cluster
	MaxWidthMM  float64 // Max observed width in cluster
	MinHeightMM float64 // Min observed height in cluster
	MaxHeightMM float64 // Max observed height in cluster
	Count       int     // Number of training samples in this cluster
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

	// Cluster samples into distinct color profiles based on V value.
	// Uses max-diameter clustering: a sample joins a cluster only if the resulting
	// V range (max-min) stays within the threshold. This prevents chaining
	// (e.g., 8→24→40→56→72 all merging into one huge cluster).
	const clusterMaxDiameter = 20.0
	var profiles []ColorProfile
	used := make([]bool, len(samples))

	for i := 0; i < len(samples); i++ {
		if used[i] {
			continue
		}

		// Start new cluster with this sample
		cluster := []sampleData{samples[i]}
		used[i] = true
		clusterMinV := samples[i].bgVal
		clusterMaxV := samples[i].bgVal

		// Find other samples that fit within the diameter
		for j := i + 1; j < len(samples); j++ {
			if used[j] {
				continue
			}
			newMin := clusterMinV
			newMax := clusterMaxV
			if samples[j].bgVal < newMin {
				newMin = samples[j].bgVal
			}
			if samples[j].bgVal > newMax {
				newMax = samples[j].bgVal
			}
			if newMax-newMin <= clusterMaxDiameter {
				cluster = append(cluster, samples[j])
				used[j] = true
				clusterMinV = newMin
				clusterMaxV = newMax
			}
		}

		// Create profile from cluster with tight margins
		var maxSat float64
		for _, cs := range cluster {
			if cs.satMax > maxSat {
				maxSat = cs.satMax
			}
		}

		const margin = 8.0 // Half a histogram bucket
		profile := ColorProfile{
			ValueMin: clusterMinV - margin,
			ValueMax: clusterMaxV + margin,
			SatMax:   maxSat + 20,
		}
		if profile.ValueMin < 0 {
			profile.ValueMin = 0
		}
		if profile.ValueMax > 120 {
			profile.ValueMax = 120 // Components are dark; board surfaces are brighter
		}
		if profile.SatMax < 80 {
			profile.SatMax = 80
		}
		if profile.SatMax > 200 {
			profile.SatMax = 200
		}

		profiles = append(profiles, profile)
		fmt.Printf("  Profile %d: V=%.0f-%.0f, SatMax=%.0f (from %d samples, raw V=%.0f-%.0f)\n",
			len(profiles), profile.ValueMin, profile.ValueMax, profile.SatMax,
			len(cluster), clusterMinV, clusterMaxV)
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
		// Fallback thresholds
		ValueMax: avgBgVal + 30,
		SatMax:   avgSat * 1.5,

		// Multiple color profiles for distinct component types
		ColorProfiles: profiles,

		// Size constraints: moderate margins around observed sizes
		MinWidth:  minWidth * 0.7,
		MaxWidth:  maxWidth * 1.5,
		MinHeight: minHeight * 0.7,
		MaxHeight: maxHeight * 1.5,

		// Aspect ratio and quality
		MinAspectRatio: 0.5,
		MaxAspectRatio: 15.0,
		MinSolidity:    0.4,
		MinWhitePixels: 0.0,
	}

	// Ensure reasonable bounds for fallback
	if params.ValueMax < 80 {
		params.ValueMax = 80
	}
	if params.ValueMax > 120 {
		params.ValueMax = 120
	}
	if params.SatMax < 80 {
		params.SatMax = 80
	}
	if params.SatMax > 200 {
		params.SatMax = 200
	}

	// Compute cell size from training data: min dimension / 3
	minDim := minWidth
	if minHeight < minDim {
		minDim = minHeight
	}
	params.CellSizeMM = minDim / 3.0
	if params.CellSizeMM < 0.5 {
		params.CellSizeMM = 0.5
	}

	// Build size templates by clustering widths into narrow/wide DIP groups
	params.SizeTemplates = clusterSizeTemplates(ts.Samples)

	fmt.Printf("  Derived %d color profiles\n", len(profiles))
	fmt.Printf("  Fallback: ValueMax=%.0f SatMax=%.0f\n", params.ValueMax, params.SatMax)
	fmt.Printf("  Derived size: %.1f-%.1f x %.1f-%.1f mm\n",
		params.MinWidth, params.MaxWidth, params.MinHeight, params.MaxHeight)
	fmt.Printf("  Cell size: %.2f mm\n", params.CellSizeMM)
	fmt.Printf("  Size templates: %d\n", len(params.SizeTemplates))
	for i, t := range params.SizeTemplates {
		fmt.Printf("    Template %d: %.1fx%.1f mm (w=%.1f-%.1f, h=%.1f-%.1f, n=%d)\n",
			i+1, t.WidthMM, t.HeightMM, t.MinWidthMM, t.MaxWidthMM, t.MinHeightMM, t.MaxHeightMM, t.Count)
	}
	fmt.Printf("=============================================\n")

	return params
}

// clusterSizeTemplates groups training samples by DIP width class (narrow vs wide)
// and then by length (snapped to 0.1" = 2.54mm multiples).
// Returns one template per unique (width-class, length) combination.
func clusterSizeTemplates(samples []TrainingSample) []SizeTemplate {
	// Width threshold: 11mm separates narrow DIP (~6-8mm) from wide DIP (~15mm)
	const widthThreshold = 11.0
	const pinPitch = 2.54 // 0.1 inch in mm

	type sizeKey struct {
		wide       bool
		lengthUnit int // length in units of pinPitch
	}

	groups := make(map[sizeKey][]TrainingSample)

	for _, s := range samples {
		// Determine orientation: shorter dimension is width
		w, h := s.WidthMM, s.HeightMM
		if w > h {
			w, h = h, w
		}

		wide := w > widthThreshold
		lengthUnit := int(math.Round(h / pinPitch))

		key := sizeKey{wide: wide, lengthUnit: lengthUnit}
		groups[key] = append(groups[key], s)
	}

	var templates []SizeTemplate
	for key, group := range groups {
		var sumW, sumH, minW, maxW, minH, maxH float64
		minW, minH = 1e9, 1e9

		for _, s := range group {
			w, h := s.WidthMM, s.HeightMM
			if w > h {
				w, h = h, w
			}
			sumW += w
			sumH += h
			if w < minW {
				minW = w
			}
			if w > maxW {
				maxW = w
			}
			if h < minH {
				minH = h
			}
			if h > maxH {
				maxH = h
			}
		}

		n := float64(len(group))
		meanW := sumW / n
		meanH := sumH / n

		// Add tolerance: +/- 1.5mm on width, +/- half a pin pitch on height
		t := SizeTemplate{
			WidthMM:     meanW,
			HeightMM:    meanH,
			MinWidthMM:  minW - 1.5,
			MaxWidthMM:  maxW + 1.5,
			MinHeightMM: float64(key.lengthUnit)*pinPitch - pinPitch*0.75,
			MaxHeightMM: float64(key.lengthUnit)*pinPitch + pinPitch*0.75,
			Count:       len(group),
		}
		if t.MinWidthMM < 1 {
			t.MinWidthMM = 1
		}
		if t.MinHeightMM < 1 {
			t.MinHeightMM = 1
		}

		templates = append(templates, t)
	}

	// Sort by width then height for consistent output
	sort.Slice(templates, func(i, j int) bool {
		if templates[i].WidthMM != templates[j].WidthMM {
			return templates[i].WidthMM < templates[j].WidthMM
		}
		return templates[i].HeightMM < templates[j].HeightMM
	})

	return templates
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

// GridID represents a parsed grid-style component ID like "A10" or "10A".
type GridID struct {
	Letter string // Single letter A-Z
	Number int    // Number 0-99
	Format string // "LN" for letter-number (A10), "NL" for number-letter (10A)
}

// gridIDPattern matches grid-style IDs: letter+number or number+letter
var gridIDPatternLN = regexp.MustCompile(`^([A-Z])(\d{1,2})$`)
var gridIDPatternNL = regexp.MustCompile(`^(\d{1,2})([A-Z])$`)

// ParseGridID attempts to parse a grid-style ID like "A10", "B12", "10A", "12B".
// Returns nil if the ID doesn't match a grid pattern.
func ParseGridID(id string) *GridID {
	id = strings.ToUpper(strings.TrimSpace(id))

	// Try letter-number format (A10, B12)
	if m := gridIDPatternLN.FindStringSubmatch(id); m != nil {
		num, _ := strconv.Atoi(m[2])
		return &GridID{
			Letter: m[1],
			Number: num,
			Format: "LN",
		}
	}

	// Try number-letter format (10A, 12B)
	if m := gridIDPatternNL.FindStringSubmatch(id); m != nil {
		num, _ := strconv.Atoi(m[1])
		return &GridID{
			Letter: m[2],
			Number: num,
			Format: "NL",
		}
	}

	return nil
}

// String returns the grid ID in its original format.
func (g *GridID) String() string {
	if g.Format == "NL" {
		return fmt.Sprintf("%d%s", g.Number, g.Letter)
	}
	return fmt.Sprintf("%s%d", g.Letter, g.Number)
}

// WithLetter returns a new ID with the same number but different letter.
func (g *GridID) WithLetter(letter string) string {
	if g.Format == "NL" {
		return fmt.Sprintf("%d%s", g.Number, letter)
	}
	return fmt.Sprintf("%s%d", letter, g.Number)
}

// WithNumber returns a new ID with the same letter but different number.
func (g *GridID) WithNumber(num int) string {
	if g.Format == "NL" {
		return fmt.Sprintf("%d%s", num, g.Letter)
	}
	return fmt.Sprintf("%s%d", g.Letter, num)
}

// WithUnknownLetter returns an ID with the same number but "?" for letter.
func (g *GridID) WithUnknownLetter() string {
	if g.Format == "NL" {
		return fmt.Sprintf("%d?", g.Number)
	}
	return fmt.Sprintf("?%d", g.Number)
}

// WithUnknownNumber returns an ID with the same letter but "?" for number.
func (g *GridID) WithUnknownNumber() string {
	if g.Format == "NL" {
		return fmt.Sprintf("?%s", g.Letter)
	}
	return fmt.Sprintf("%s?", g.Letter)
}

// GridCoordinate holds a mapping from a coordinate value to a grid identifier.
type GridCoordinate struct {
	Value  float64 // X or Y coordinate (center of component)
	ID     string  // Letter or number string
	Count  int     // How many components at this coordinate
}

// GridMapping holds the discovered coordinate-to-ID mappings.
// The mapping is inferred from component positions - letters and numbers
// can map to either X or Y depending on the board layout.
type GridMapping struct {
	LetterCoords []GridCoordinate // Coordinates mapped to letters
	NumberCoords []GridCoordinate // Coordinates mapped to numbers
	LetterAxis   string           // "X" or "Y" - which axis letters map to
	NumberAxis   string           // "X" or "Y" - which axis numbers map to
	Format       string           // "LN" or "NL" based on majority
	Tolerance    float64          // Coordinate tolerance for matching (pixels)
}

// BuildGridMapping analyzes existing components to build a coordinate-to-ID mapping.
// It infers which axis (X or Y) maps to letters vs numbers based on component positions.
// tolerance is how close coordinates must be to be considered "same row/column" (in pixels).
func BuildGridMapping(components []*Component, tolerance float64) *GridMapping {
	if tolerance <= 0 {
		tolerance = 50 // Default 50 pixels
	}

	mapping := &GridMapping{
		Tolerance:  tolerance,
		Format:     "LN", // Default
		LetterAxis: "Y",  // Default: letters are rows
		NumberAxis: "X",  // Default: numbers are columns
	}

	// Collect coordinates for each letter and number
	letterX := make(map[string][]float64)
	letterY := make(map[string][]float64)
	numberX := make(map[int][]float64)
	numberY := make(map[int][]float64)
	formatCounts := map[string]int{"LN": 0, "NL": 0}

	for _, comp := range components {
		grid := ParseGridID(comp.ID)
		if grid == nil {
			continue
		}

		centerX := comp.Bounds.X + comp.Bounds.Width/2
		centerY := comp.Bounds.Y + comp.Bounds.Height/2

		letterX[grid.Letter] = append(letterX[grid.Letter], centerX)
		letterY[grid.Letter] = append(letterY[grid.Letter], centerY)
		numberX[grid.Number] = append(numberX[grid.Number], centerX)
		numberY[grid.Number] = append(numberY[grid.Number], centerY)
		formatCounts[grid.Format]++
	}

	// Determine dominant format
	if formatCounts["NL"] > formatCounts["LN"] {
		mapping.Format = "NL"
	}

	// Infer which axis letters map to by comparing variance
	// Same letter = same row/column, so should have low variance on that axis
	letterXVar := computeGroupVariance(letterX)
	letterYVar := computeGroupVariance(letterY)
	numberXVar := computeGroupVarianceInt(numberX)
	numberYVar := computeGroupVarianceInt(numberY)

	fmt.Printf("GridMapping variance: letterX=%.0f letterY=%.0f numberX=%.0f numberY=%.0f\n",
		letterXVar, letterYVar, numberXVar, numberYVar)

	// Check if we have valid variance data for each type
	letterVarValid := letterXVar != math.MaxFloat64 || letterYVar != math.MaxFloat64
	numberVarValid := numberXVar != math.MaxFloat64 || numberYVar != math.MaxFloat64

	// Lower variance means better grouping on that axis
	if letterVarValid {
		if letterXVar < letterYVar {
			mapping.LetterAxis = "X"
			fmt.Println("GridMapping: letters map to X axis (columns)")
		} else {
			mapping.LetterAxis = "Y"
			fmt.Println("GridMapping: letters map to Y axis (rows)")
		}
	}

	if numberVarValid {
		if numberXVar < numberYVar {
			mapping.NumberAxis = "X"
			fmt.Println("GridMapping: numbers map to X axis (columns)")
		} else {
			mapping.NumberAxis = "Y"
			fmt.Println("GridMapping: numbers map to Y axis (rows)")
		}
	}

	// If we couldn't determine one axis, infer it from the other
	// Letters and numbers must be on different axes
	if letterVarValid && !numberVarValid {
		if mapping.LetterAxis == "Y" {
			mapping.NumberAxis = "X"
		} else {
			mapping.NumberAxis = "Y"
		}
		fmt.Printf("GridMapping: inferred numbers on %s axis (opposite of letters)\n", mapping.NumberAxis)
	} else if numberVarValid && !letterVarValid {
		if mapping.NumberAxis == "Y" {
			mapping.LetterAxis = "X"
		} else {
			mapping.LetterAxis = "Y"
		}
		fmt.Printf("GridMapping: inferred letters on %s axis (opposite of numbers)\n", mapping.LetterAxis)
	} else if mapping.LetterAxis == mapping.NumberAxis {
		// Both valid but same axis - prefer letters on Y (rows), numbers on X (columns)
		mapping.LetterAxis = "Y"
		mapping.NumberAxis = "X"
		fmt.Println("GridMapping: conflict resolved - letters=Y, numbers=X")
	}

	// Build letter coordinate mapping
	letterCoords := letterX
	if mapping.LetterAxis == "Y" {
		letterCoords = letterY
	}
	for letter, coords := range letterCoords {
		avg := average(coords)
		mapping.LetterCoords = append(mapping.LetterCoords, GridCoordinate{
			Value: avg,
			ID:    letter,
			Count: len(coords),
		})
		fmt.Printf("GridMapping: letter %s -> %s=%.0f\n", letter, mapping.LetterAxis, avg)
	}

	// Build number coordinate mapping
	numberCoords := numberX
	if mapping.NumberAxis == "Y" {
		numberCoords = numberY
	}
	for num, coords := range numberCoords {
		avg := average(coords)
		mapping.NumberCoords = append(mapping.NumberCoords, GridCoordinate{
			Value: avg,
			ID:    strconv.Itoa(num),
			Count: len(coords),
		})
		fmt.Printf("GridMapping: number %d -> %s=%.0f\n", num, mapping.NumberAxis, avg)
	}

	return mapping
}

// computeGroupVariance calculates average within-group variance for coordinate groups.
// Lower variance means coordinates within each group are more consistent.
func computeGroupVariance(groups map[string][]float64) float64 {
	if len(groups) == 0 {
		return math.MaxFloat64
	}

	totalVar := 0.0
	count := 0

	for _, coords := range groups {
		if len(coords) < 2 {
			continue
		}
		avg := average(coords)
		sumSq := 0.0
		for _, c := range coords {
			diff := c - avg
			sumSq += diff * diff
		}
		totalVar += sumSq / float64(len(coords))
		count++
	}

	if count == 0 {
		return math.MaxFloat64
	}
	return totalVar / float64(count)
}

// computeGroupVarianceInt is like computeGroupVariance but for int keys.
func computeGroupVarianceInt(groups map[int][]float64) float64 {
	if len(groups) == 0 {
		return math.MaxFloat64
	}

	totalVar := 0.0
	count := 0

	for _, coords := range groups {
		if len(coords) < 2 {
			continue
		}
		avg := average(coords)
		sumSq := 0.0
		for _, c := range coords {
			diff := c - avg
			sumSq += diff * diff
		}
		totalVar += sumSq / float64(len(coords))
		count++
	}

	if count == 0 {
		return math.MaxFloat64
	}
	return totalVar / float64(count)
}

// SuggestGridID suggests a grid-style ID for a new component based on its position.
// Supports interpolation: if position is between known values, suggests the interpolated ID.
// Returns empty string if no suggestion can be made.
func (m *GridMapping) SuggestGridID(centerX, centerY float64) string {
	if m == nil || (len(m.LetterCoords) == 0 && len(m.NumberCoords) == 0) {
		return ""
	}

	fmt.Printf("SuggestGridID: checking position (%.0f, %.0f) tolerance=%.0f\n", centerX, centerY, m.Tolerance)
	fmt.Printf("  LetterAxis=%s NumberAxis=%s\n", m.LetterAxis, m.NumberAxis)

	// Get the coordinate value for the letter axis
	letterCoord := centerY
	if m.LetterAxis == "X" {
		letterCoord = centerX
	}

	// Get the coordinate value for the number axis
	numberCoord := centerX
	if m.NumberAxis == "Y" {
		numberCoord = centerY
	}

	// Find matching letter (exact match within tolerance)
	matchedLetter := ""
	for _, coord := range m.LetterCoords {
		diff := math.Abs(coord.Value - letterCoord)
		fmt.Printf("  letter %s: %s=%.0f diff=%.0f\n", coord.ID, m.LetterAxis, coord.Value, diff)
		if diff <= m.Tolerance {
			matchedLetter = coord.ID
			break
		}
	}

	// Find matching number (exact match within tolerance)
	matchedNumber := ""
	for _, coord := range m.NumberCoords {
		diff := math.Abs(coord.Value - numberCoord)
		fmt.Printf("  number %s: %s=%.0f diff=%.0f\n", coord.ID, m.NumberAxis, coord.Value, diff)
		if diff <= m.Tolerance {
			matchedNumber = coord.ID
			break
		}
	}

	// Try interpolation if no exact match
	if matchedLetter == "" && len(m.LetterCoords) >= 2 {
		matchedLetter = m.interpolateLetter(letterCoord)
	}
	if matchedNumber == "" && len(m.NumberCoords) >= 2 {
		matchedNumber = m.interpolateNumber(numberCoord)
	}

	// Build suggestion based on what we found
	if matchedLetter != "" && matchedNumber != "" {
		// Perfect match - we know both
		if m.Format == "NL" {
			return matchedNumber + matchedLetter
		}
		return matchedLetter + matchedNumber
	}

	if matchedLetter != "" {
		// We know the row, not the column
		if m.Format == "NL" {
			return "?" + matchedLetter
		}
		return matchedLetter + "?"
	}

	if matchedNumber != "" {
		// We know the column, not the row
		if m.Format == "NL" {
			return matchedNumber + "?"
		}
		return "?" + matchedNumber
	}

	return ""
}

// interpolateLetter finds an interpolated letter based on coordinate position.
func (m *GridMapping) interpolateLetter(coord float64) string {
	if len(m.LetterCoords) < 2 {
		return ""
	}

	// Find the two closest letters on either side
	var lower, upper *GridCoordinate
	var lowerLetter, upperLetter byte = 0, 'Z' + 1

	for i := range m.LetterCoords {
		gc := &m.LetterCoords[i]
		letter := gc.ID[0]
		if gc.Value <= coord && letter > lowerLetter {
			lower = gc
			lowerLetter = letter
		}
		if gc.Value >= coord && letter < upperLetter {
			upper = gc
			upperLetter = letter
		}
	}

	if lower == nil || upper == nil || lower == upper {
		return ""
	}

	// Calculate interpolated letter
	if upper.Value-lower.Value > 0 {
		ratio := (coord - lower.Value) / (upper.Value - lower.Value)
		interpolated := float64(lowerLetter) + ratio*float64(upperLetter-lowerLetter)
		result := byte(math.Round(interpolated))
		if result > lowerLetter && result < upperLetter && result >= 'A' && result <= 'Z' {
			fmt.Printf("  interpolateLetter: between %c and %c, ratio=%.2f -> %c\n",
				lowerLetter, upperLetter, ratio, result)
			return string(result)
		}
	}

	return ""
}

// interpolateNumber finds an interpolated number based on coordinate position.
func (m *GridMapping) interpolateNumber(coord float64) string {
	if len(m.NumberCoords) < 2 {
		return ""
	}

	// Find the two closest numbers on either side
	var lower, upper *GridCoordinate
	lowerNum, upperNum := -1, 1000

	for i := range m.NumberCoords {
		gc := &m.NumberCoords[i]
		num, err := strconv.Atoi(gc.ID)
		if err != nil {
			continue
		}
		if gc.Value <= coord && num > lowerNum {
			lower = gc
			lowerNum = num
		}
		if gc.Value >= coord && num < upperNum {
			upper = gc
			upperNum = num
		}
	}

	if lower == nil || upper == nil || lower == upper {
		return ""
	}

	// Calculate interpolated number
	if upper.Value-lower.Value > 0 {
		ratio := (coord - lower.Value) / (upper.Value - lower.Value)
		interpolated := float64(lowerNum) + ratio*float64(upperNum-lowerNum)
		result := int(math.Round(interpolated))
		if result > lowerNum && result < upperNum {
			fmt.Printf("  interpolateNumber: between %d and %d, ratio=%.2f -> %d\n",
				lowerNum, upperNum, ratio, result)
			return strconv.Itoa(result)
		}
	}

	return ""
}

// SuggestComponentID suggests an ID for a new component based on existing components.
// centerX, centerY are the center coordinates of the new component.
// tolerance is how close coordinates must be to match (in pixels).
// fallbackPrefix is used if no grid match found (e.g., "U" for "U1", "U2").
// Returns the suggested ID.
func SuggestComponentID(components []*Component, centerX, centerY, tolerance float64, fallbackPrefix string) string {
	// First try grid-style ID matching
	mapping := BuildGridMapping(components, tolerance)
	suggestion := mapping.SuggestGridID(centerX, centerY)

	if suggestion != "" {
		fmt.Printf("SuggestComponentID: grid match -> %s\n", suggestion)
		return suggestion
	}

	// If no grid match, try rectangle overlap matching
	// Pass the mapping so we know which axis maps to letter vs number
	suggestion = suggestFromRectOverlap(components, centerX, centerY, tolerance, mapping)
	if suggestion != "" {
		fmt.Printf("SuggestComponentID: rect overlap match -> %s\n", suggestion)
		return suggestion
	}

	// Fall back to sequential numbering
	fallback := fmt.Sprintf("%s%d", fallbackPrefix, len(components)+1)
	fmt.Printf("SuggestComponentID: no match, using fallback -> %s\n", fallback)
	return fallback
}

// overlapInfo holds information about a component that overlaps with a target position.
type overlapInfo struct {
	comp    *Component
	grid    *GridID
	sharedX bool    // true if X ranges overlap
	sharedY bool    // true if Y ranges overlap
	centerX float64 // component center X
	centerY float64 // component center Y
}

// suggestFromRectOverlap finds components with overlapping X or Y ranges and suggests based on their IDs.
// Uses the axis mapping from BuildGridMapping to correctly extract letters vs numbers.
// Also supports interpolation: if position is between A9 and A11, suggest A10.
func suggestFromRectOverlap(components []*Component, centerX, centerY, tolerance float64, mapping *GridMapping) string {
	var overlaps []overlapInfo

	for _, comp := range components {
		grid := ParseGridID(comp.ID)
		if grid == nil {
			continue
		}

		compMinX := comp.Bounds.X
		compMaxX := comp.Bounds.X + comp.Bounds.Width
		compMinY := comp.Bounds.Y
		compMaxY := comp.Bounds.Y + comp.Bounds.Height
		compCenterX := comp.Bounds.X + comp.Bounds.Width/2
		compCenterY := comp.Bounds.Y + comp.Bounds.Height/2

		sharedX := centerX >= compMinX-tolerance && centerX <= compMaxX+tolerance
		sharedY := centerY >= compMinY-tolerance && centerY <= compMaxY+tolerance

		if sharedX || sharedY {
			overlaps = append(overlaps, overlapInfo{
				comp:    comp,
				grid:    grid,
				sharedX: sharedX,
				sharedY: sharedY,
				centerX: compCenterX,
				centerY: compCenterY,
			})
			fmt.Printf("  overlap with %s: sharedX=%v sharedY=%v (letter=%s num=%d)\n",
				comp.ID, sharedX, sharedY, grid.Letter, grid.Number)
		}
	}

	if len(overlaps) == 0 {
		fmt.Println("suggestFromRectOverlap: no overlapping components found")
		return ""
	}

	// Use axis mapping to correctly assign overlaps
	// If LetterAxis == "Y", then Y-only overlaps share the letter (same row)
	// If LetterAxis == "X", then X-only overlaps share the letter (same column)
	letterAxis := "Y" // default
	numberAxis := "X" // default
	format := "LN"

	if mapping != nil {
		letterAxis = mapping.LetterAxis
		numberAxis = mapping.NumberAxis
		format = mapping.Format
	}

	fmt.Printf("  Using axis mapping: LetterAxis=%s NumberAxis=%s\n", letterAxis, numberAxis)

	// Collect letters and numbers based on axis mapping
	var sameRowLetters []string
	var sameColNumbers []int
	var sameRowComps, sameColComps []overlapInfo
	var bothLetters []string
	var bothNumbers []int

	for _, o := range overlaps {
		if o.grid.Format != "" {
			format = o.grid.Format
		}

		if o.sharedX && o.sharedY {
			// Direct overlap
			bothLetters = append(bothLetters, o.grid.Letter)
			bothNumbers = append(bothNumbers, o.grid.Number)
			fmt.Printf("  %s: BOTH overlap\n", o.comp.ID)
		} else if o.sharedY && !o.sharedX {
			// Same row (Y overlap only)
			if letterAxis == "Y" {
				sameRowLetters = append(sameRowLetters, o.grid.Letter)
				fmt.Printf("  %s: Y-only -> letter %s (same row)\n", o.comp.ID, o.grid.Letter)
			} else {
				// Letter is on X axis, so Y-only gives us the number
				sameColNumbers = append(sameColNumbers, o.grid.Number)
				fmt.Printf("  %s: Y-only -> number %d (letter on X)\n", o.comp.ID, o.grid.Number)
			}
			sameRowComps = append(sameRowComps, o)
		} else if o.sharedX && !o.sharedY {
			// Same column (X overlap only)
			if numberAxis == "X" {
				sameColNumbers = append(sameColNumbers, o.grid.Number)
				fmt.Printf("  %s: X-only -> number %d (same col)\n", o.comp.ID, o.grid.Number)
			} else {
				// Number is on Y axis, so X-only gives us the letter
				sameRowLetters = append(sameRowLetters, o.grid.Letter)
				fmt.Printf("  %s: X-only -> letter %s (number on Y)\n", o.comp.ID, o.grid.Letter)
			}
			sameColComps = append(sameColComps, o)
		}
	}

	fmt.Printf("  sameRowLetters=%v sameColNumbers=%v\n", sameRowLetters, sameColNumbers)

	var resultLetter, resultNumber string

	// Get letter from same-row components
	if len(sameRowLetters) > 0 && allSame(sameRowLetters) {
		resultLetter = sameRowLetters[0]
		fmt.Printf("  Letter: %s from same-row\n", resultLetter)
	}

	// Get number from same-column components, with interpolation support
	if len(sameColNumbers) > 0 {
		if allSameInt(sameColNumbers) {
			resultNumber = strconv.Itoa(sameColNumbers[0])
			fmt.Printf("  Number: %s from same-col\n", resultNumber)
		}
	}

	// Try interpolation if we have same-row components but no exact number match
	// Example: between A9 and A11, suggest A10
	if resultLetter != "" && resultNumber == "" && len(sameRowComps) >= 2 {
		interpolated := interpolateNumber(sameRowComps, centerX, centerY, numberAxis)
		if interpolated != "" {
			resultNumber = interpolated
			fmt.Printf("  Number: %s from interpolation\n", resultNumber)
		}
	}

	// Try interpolation for letters if we have same-col components but no exact letter
	if resultNumber != "" && resultLetter == "" && len(sameColComps) >= 2 {
		interpolated := interpolateLetter(sameColComps, centerX, centerY, letterAxis)
		if interpolated != "" {
			resultLetter = interpolated
			fmt.Printf("  Letter: %s from interpolation\n", resultLetter)
		}
	}

	// Fallback to "both" overlaps
	if resultLetter == "" && len(bothLetters) > 0 {
		resultLetter = bothLetters[0]
		fmt.Printf("  Fallback: letter %s from both-overlap\n", resultLetter)
	}
	if resultNumber == "" && len(bothNumbers) > 0 {
		resultNumber = strconv.Itoa(bothNumbers[0])
		fmt.Printf("  Fallback: number %s from both-overlap\n", resultNumber)
	}

	// Build suggestion
	if resultLetter != "" && resultNumber != "" {
		if format == "NL" {
			return resultNumber + resultLetter
		}
		return resultLetter + resultNumber
	}

	if resultLetter != "" {
		if format == "NL" {
			return "?" + resultLetter
		}
		return resultLetter + "?"
	}

	if resultNumber != "" {
		if format == "NL" {
			return resultNumber + "?"
		}
		return "?" + resultNumber
	}

	return ""
}

// interpolateNumber finds the number between two components based on position.
// Example: if between A9 (at x=100) and A11 (at x=300), and centerX=200, returns "10".
func interpolateNumber(comps []overlapInfo, centerX, centerY float64, numberAxis string) string {
	if len(comps) < 2 {
		return ""
	}

	// Find the two closest components on either side
	var lower, upper *overlapInfo
	var lowerNum, upperNum int = -1, 1000

	coord := centerX
	if numberAxis == "Y" {
		coord = centerY
	}

	for i := range comps {
		o := &comps[i]
		compCoord := o.centerX
		if numberAxis == "Y" {
			compCoord = o.centerY
		}

		if compCoord <= coord && o.grid.Number > lowerNum {
			lower = o
			lowerNum = o.grid.Number
		}
		if compCoord >= coord && o.grid.Number < upperNum {
			upper = o
			upperNum = o.grid.Number
		}
	}

	if lower == nil || upper == nil || lower == upper {
		return ""
	}

	// Check if we're between them and can interpolate
	lowerCoord := lower.centerX
	upperCoord := upper.centerX
	if numberAxis == "Y" {
		lowerCoord = lower.centerY
		upperCoord = upper.centerY
	}

	if lowerCoord > upperCoord {
		// Swap if lower is actually at higher coordinate
		lowerCoord, upperCoord = upperCoord, lowerCoord
		lowerNum, upperNum = upperNum, lowerNum
	}

	// Calculate interpolated number
	if upperCoord-lowerCoord > 0 {
		ratio := (coord - lowerCoord) / (upperCoord - lowerCoord)
		interpolated := float64(lowerNum) + ratio*float64(upperNum-lowerNum)
		result := int(math.Round(interpolated))
		if result > lowerNum && result < upperNum {
			fmt.Printf("  interpolateNumber: between %d and %d, ratio=%.2f -> %d\n",
				lowerNum, upperNum, ratio, result)
			return strconv.Itoa(result)
		}
	}

	return ""
}

// interpolateLetter finds the letter between two components based on position.
// Example: if between A (at y=100) and C (at y=300), and centerY=200, returns "B".
func interpolateLetter(comps []overlapInfo, centerX, centerY float64, letterAxis string) string {
	if len(comps) < 2 {
		return ""
	}

	coord := centerY
	if letterAxis == "X" {
		coord = centerX
	}

	// Find the two closest components on either side
	var lower, upper *overlapInfo
	var lowerLetter, upperLetter byte = 0, 'Z' + 1

	for i := range comps {
		o := &comps[i]
		compCoord := o.centerY
		if letterAxis == "X" {
			compCoord = o.centerX
		}

		letter := o.grid.Letter[0]
		if compCoord <= coord && letter > lowerLetter {
			lower = o
			lowerLetter = letter
		}
		if compCoord >= coord && letter < upperLetter {
			upper = o
			upperLetter = letter
		}
	}

	if lower == nil || upper == nil || lower == upper {
		return ""
	}

	lowerCoord := lower.centerY
	upperCoord := upper.centerY
	if letterAxis == "X" {
		lowerCoord = lower.centerX
		upperCoord = upper.centerX
	}

	if lowerCoord > upperCoord {
		lowerCoord, upperCoord = upperCoord, lowerCoord
		lowerLetter, upperLetter = upperLetter, lowerLetter
	}

	// Calculate interpolated letter
	if upperCoord-lowerCoord > 0 {
		ratio := (coord - lowerCoord) / (upperCoord - lowerCoord)
		interpolated := float64(lowerLetter) + ratio*float64(upperLetter-lowerLetter)
		result := byte(math.Round(interpolated))
		if result > lowerLetter && result < upperLetter && result >= 'A' && result <= 'Z' {
			fmt.Printf("  interpolateLetter: between %c and %c, ratio=%.2f -> %c\n",
				lowerLetter, upperLetter, ratio, result)
			return string(result)
		}
	}

	return ""
}

// allSame returns true if all strings in the slice are identical.
func allSame(s []string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] != s[0] {
			return false
		}
	}
	return true
}

// allSameInt returns true if all ints in the slice are identical.
func allSameInt(s []int) bool {
	if len(s) == 0 {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] != s[0] {
			return false
		}
	}
	return true
}

// average calculates the average of a slice of float64.
func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// getTrainingLibPath returns the path to component_training.json in the lib/ directory
// next to the executable, or empty string if it can't be determined.
func getTrainingLibPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "..", "lib", "component_training.json")
}

// GetTrainingPath returns the path to the global component training file.
// Prefers lib/component_training.json next to the executable; falls back to
// ~/.config/pcb-tracer/component_training.json.
func GetTrainingPath() (string, error) {
	if libPath := getTrainingLibPath(); libPath != "" {
		if _, err := os.Stat(libPath); err == nil {
			return libPath, nil
		}
		if dir := filepath.Dir(libPath); dir != "" {
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				return libPath, nil
			}
		}
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine config directory: %w", err)
		}
		configDir = filepath.Join(home, ".config")
	}

	appDir := filepath.Join(configDir, "pcb-tracer")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return "", fmt.Errorf("cannot create config directory: %w", err)
	}

	return filepath.Join(appDir, "component_training.json"), nil
}

// SaveGlobalTraining saves the global component training set to disk.
func SaveGlobalTraining(ts *TrainingSet) error {
	path, err := GetTrainingPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(ts, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot serialize component training: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("cannot write component training: %w", err)
	}

	fmt.Printf("Saved %d component training samples to %s\n", len(ts.Samples), path)
	return nil
}

// LoadGlobalTraining loads the global component training set from disk.
// Returns an empty training set if no file exists.
func LoadGlobalTraining() (*TrainingSet, error) {
	if libPath := getTrainingLibPath(); libPath != "" {
		if data, err := os.ReadFile(libPath); err == nil {
			var ts TrainingSet
			if err := json.Unmarshal(data, &ts); err == nil {
				fmt.Printf("Loaded %d component training samples from %s\n", len(ts.Samples), libPath)
				return &ts, nil
			}
		}
	}

	path, err := GetTrainingPath()
	if err != nil {
		return NewTrainingSet(), err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewTrainingSet(), nil
		}
		return NewTrainingSet(), fmt.Errorf("cannot read component training: %w", err)
	}

	var ts TrainingSet
	if err := json.Unmarshal(data, &ts); err != nil {
		return NewTrainingSet(), fmt.Errorf("cannot parse component training: %w", err)
	}

	fmt.Printf("Loaded %d component training samples from %s\n", len(ts.Samples), path)
	return &ts, nil
}
