// Package component provides component detection, storage, and management.
package component

import (
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
	OCRText     string        `json:"ocr_text"`    // Raw OCR result
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
