// Package component provides component detection, storage, and management.
package component

import (
	"fmt"
	"math"
	"regexp"
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

	// Lower variance means better grouping on that axis
	if letterXVar < letterYVar {
		mapping.LetterAxis = "X"
		fmt.Println("GridMapping: letters map to X axis (columns)")
	} else {
		mapping.LetterAxis = "Y"
		fmt.Println("GridMapping: letters map to Y axis (rows)")
	}

	if numberXVar < numberYVar {
		mapping.NumberAxis = "X"
		fmt.Println("GridMapping: numbers map to X axis (columns)")
	} else {
		mapping.NumberAxis = "Y"
		fmt.Println("GridMapping: numbers map to Y axis (rows)")
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

	// Find matching letter
	matchedLetter := ""
	for _, coord := range m.LetterCoords {
		diff := math.Abs(coord.Value - letterCoord)
		fmt.Printf("  letter %s: %s=%.0f diff=%.0f\n", coord.ID, m.LetterAxis, coord.Value, diff)
		if diff <= m.Tolerance {
			matchedLetter = coord.ID
			break
		}
	}

	// Find matching number
	matchedNumber := ""
	for _, coord := range m.NumberCoords {
		diff := math.Abs(coord.Value - numberCoord)
		fmt.Printf("  number %s: %s=%.0f diff=%.0f\n", coord.ID, m.NumberAxis, coord.Value, diff)
		if diff <= m.Tolerance {
			matchedNumber = coord.ID
			break
		}
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
		// We know the column, not the row
		if m.Format == "NL" {
			return "?" + matchedLetter
		}
		return matchedLetter + "?"
	}

	if matchedNumber != "" {
		// We know the row, not the column
		if m.Format == "NL" {
			return matchedNumber + "?"
		}
		return "?" + matchedNumber
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
	// Look for components whose bounding boxes overlap on X or Y axis
	suggestion = suggestFromRectOverlap(components, centerX, centerY, tolerance)
	if suggestion != "" {
		fmt.Printf("SuggestComponentID: rect overlap match -> %s\n", suggestion)
		return suggestion
	}

	// Fall back to sequential numbering
	fallback := fmt.Sprintf("%s%d", fallbackPrefix, len(components)+1)
	fmt.Printf("SuggestComponentID: no match, using fallback -> %s\n", fallback)
	return fallback
}

// suggestFromRectOverlap finds components with overlapping X or Y ranges and suggests based on their IDs.
func suggestFromRectOverlap(components []*Component, centerX, centerY, tolerance float64) string {
	var sameRowComps []*Component  // Same Y range (horizontal neighbors)
	var sameColComps []*Component  // Same X range (vertical neighbors)

	for _, comp := range components {
		// Check if Y ranges overlap (same row)
		compMinY := comp.Bounds.Y
		compMaxY := comp.Bounds.Y + comp.Bounds.Height
		if centerY >= compMinY-tolerance && centerY <= compMaxY+tolerance {
			sameRowComps = append(sameRowComps, comp)
		}

		// Check if X ranges overlap (same column)
		compMinX := comp.Bounds.X
		compMaxX := comp.Bounds.X + comp.Bounds.Width
		if centerX >= compMinX-tolerance && centerX <= compMaxX+tolerance {
			sameColComps = append(sameColComps, comp)
		}
	}

	fmt.Printf("suggestFromRectOverlap: found %d same-row, %d same-col components\n",
		len(sameRowComps), len(sameColComps))

	// Try to extract grid info from overlapping components
	var matchedLetter, matchedNumber string
	var format string = "LN"

	// From same-row components, extract the letter (row identifier)
	for _, comp := range sameRowComps {
		grid := ParseGridID(comp.ID)
		if grid != nil {
			matchedLetter = grid.Letter
			format = grid.Format
			fmt.Printf("  same-row %s has letter %s\n", comp.ID, matchedLetter)
			break
		}
	}

	// From same-column components, extract the number (column identifier)
	for _, comp := range sameColComps {
		grid := ParseGridID(comp.ID)
		if grid != nil {
			matchedNumber = strconv.Itoa(grid.Number)
			if matchedLetter == "" {
				format = grid.Format
			}
			fmt.Printf("  same-col %s has number %s\n", comp.ID, matchedNumber)
			break
		}
	}

	// Build suggestion
	if matchedLetter != "" && matchedNumber != "" {
		if format == "NL" {
			return matchedNumber + matchedLetter
		}
		return matchedLetter + matchedNumber
	}

	if matchedLetter != "" {
		if format == "NL" {
			return "?" + matchedLetter
		}
		return matchedLetter + "?"
	}

	if matchedNumber != "" {
		if format == "NL" {
			return matchedNumber + "?"
		}
		return "?" + matchedNumber
	}

	return ""
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
