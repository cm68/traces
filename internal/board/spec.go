// Package board provides board specification definitions and management.
package board

import (
	"encoding/json"
	"fmt"
	"os"
)

// AlignmentMethod defines how a board can be aligned.
type AlignmentMethod int

const (
	AlignByContacts AlignmentMethod = iota // Gold edge card contacts
	AlignByHoles                           // Ejector/mounting holes
	AlignByCorners                         // Board corner detection
	AlignByFeatures                        // Generic feature matching
)

func (m AlignmentMethod) String() string {
	switch m {
	case AlignByContacts:
		return "Edge Contacts"
	case AlignByHoles:
		return "Mounting Holes"
	case AlignByCorners:
		return "Board Corners"
	case AlignByFeatures:
		return "Feature Matching"
	default:
		return "Unknown"
	}
}

// Edge specifies which edge of the board.
type Edge string

const (
	EdgeTop    Edge = "top"
	EdgeBottom Edge = "bottom"
	EdgeLeft   Edge = "left"
	EdgeRight  Edge = "right"
)

// HSVRange defines a color range in HSV space for detection.
type HSVRange struct {
	HueMin    float64 `json:"hue_min"`    // 0-180 (OpenCV convention)
	HueMax    float64 `json:"hue_max"`    // 0-180
	SatMin    float64 `json:"sat_min"`    // 0-255
	SatMax    float64 `json:"sat_max"`    // 0-255
	ValMin    float64 `json:"val_min"`    // 0-255
	ValMax    float64 `json:"val_max"`    // 0-255
}

// ContactDetectionParams defines parameters for detecting contacts in images.
type ContactDetectionParams struct {
	Color           HSVRange `json:"color"`             // Expected color range (e.g., gold)
	AspectRatioMin  float64  `json:"aspect_ratio_min"`  // Minimum width/height ratio
	AspectRatioMax  float64  `json:"aspect_ratio_max"`  // Maximum width/height ratio
	MinAreaPixels   int      `json:"min_area_pixels"`   // Minimum blob area at 600 DPI
	MaxAreaPixels   int      `json:"max_area_pixels"`   // Maximum blob area at 600 DPI
}

// ContactSpec defines the edge contact configuration.
type ContactSpec struct {
	Edge         Edge    `json:"edge"`          // Which edge has contacts
	Count        int     `json:"count"`         // Number of contacts per side
	PitchInches  float64 `json:"pitch_inches"`  // Center-to-center spacing
	WidthInches  float64 `json:"width_inches"`  // Individual contact width
	HeightInches float64 `json:"height_inches"` // Contact height (length into board)
	MarginInches float64 `json:"margin_inches"` // Distance from board edge to first contact center

	// Detection parameters
	Detection *ContactDetectionParams `json:"detection,omitempty"`
}

// TotalWidthInches returns the total width of all contacts.
func (c *ContactSpec) TotalWidthInches() float64 {
	if c.Count == 0 {
		return 0
	}
	return float64(c.Count-1)*c.PitchInches + c.WidthInches
}

// HoleSpec defines a mounting or ejector hole.
type HoleSpec struct {
	XInches      float64 `json:"x_inches"`      // X position from left edge
	YInches      float64 `json:"y_inches"`      // Y position from top edge
	DiamInches   float64 `json:"diam_inches"`   // Hole diameter
	Name         string  `json:"name"`          // Optional name (e.g., "top_left_ejector")
}

// Spec defines a board specification.
type Spec interface {
	Name() string
	Dimensions() (widthInches, heightInches float64)
	ContactSpec() *ContactSpec
	Holes() []HoleSpec
	AlignmentMethods() []AlignmentMethod
	Validate() error
}

// BaseSpec provides a common implementation of Spec.
type BaseSpec struct {
	SpecName     string            `json:"name"`
	WidthInches  float64           `json:"width_inches"`
	HeightInches float64           `json:"height_inches"`
	Contacts     *ContactSpec      `json:"contacts,omitempty"`
	MountHoles   []HoleSpec        `json:"holes,omitempty"`
	AlignMethods []AlignmentMethod `json:"alignment_methods"`
}

func (s *BaseSpec) Name() string {
	return s.SpecName
}

func (s *BaseSpec) Dimensions() (widthInches, heightInches float64) {
	return s.WidthInches, s.HeightInches
}

func (s *BaseSpec) ContactSpec() *ContactSpec {
	return s.Contacts
}

func (s *BaseSpec) Holes() []HoleSpec {
	return s.MountHoles
}

func (s *BaseSpec) AlignmentMethods() []AlignmentMethod {
	return s.AlignMethods
}

func (s *BaseSpec) Validate() error {
	if s.SpecName == "" {
		return fmt.Errorf("board spec name is required")
	}
	if s.WidthInches <= 0 || s.HeightInches <= 0 {
		return fmt.Errorf("board dimensions must be positive")
	}
	if s.Contacts != nil {
		if s.Contacts.Count <= 0 {
			return fmt.Errorf("contact count must be positive")
		}
		if s.Contacts.PitchInches <= 0 {
			return fmt.Errorf("contact pitch must be positive")
		}
	}
	if len(s.AlignMethods) == 0 {
		return fmt.Errorf("at least one alignment method is required")
	}
	return nil
}

// SaveToFile saves the spec to a JSON file.
func (s *BaseSpec) SaveToFile(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// LoadFromFile loads a spec from a JSON file.
func LoadFromFile(path string) (*BaseSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var spec BaseSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, err
	}

	if err := spec.Validate(); err != nil {
		return nil, fmt.Errorf("invalid board spec: %w", err)
	}

	return &spec, nil
}

// Registry of known board specs
var registry = make(map[string]Spec)

// Register adds a board spec to the registry.
func Register(spec Spec) {
	registry[spec.Name()] = spec
}

// GetSpec returns a board spec by name.
func GetSpec(name string) Spec {
	if spec, ok := registry[name]; ok {
		return spec
	}
	return nil
}

// ListSpecs returns all registered board spec names.
func ListSpecs() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

func init() {
	// Register built-in board specs
	Register(S100Spec())
	Register(ISA8Spec())
	Register(ISA16Spec())
	Register(MultibusP1Spec())
	Register(MultibusP1P2Spec())
	Register(ECBSpec())
	Register(STDBusSpec())
}
