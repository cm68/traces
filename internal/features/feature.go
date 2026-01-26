// Package features provides a unified layer for detected vias and traces
// with support for bus assignment and color coding.
package features

import (
	"image/color"

	"pcb-tracer/internal/image"
	"pcb-tracer/internal/trace"
	"pcb-tracer/internal/via"
	"pcb-tracer/pkg/geometry"
)

// Feature is the common interface for vias and traces.
type Feature interface {
	// FeatureID returns the unique identifier for this feature.
	FeatureID() string

	// FeatureType returns "via" or "trace".
	FeatureType() string

	// FeatureSide returns which board side this feature is on.
	FeatureSide() image.Side

	// HitTest returns true if the point (x, y) is within this feature.
	HitTest(x, y float64) bool

	// GetBounds returns the bounding rectangle for this feature.
	GetBounds() geometry.RectInt
}

// ViaFeature wraps a Via to implement the Feature interface.
type ViaFeature struct {
	via.Via
}

func (v ViaFeature) FeatureID() string {
	return v.ID
}

func (v ViaFeature) FeatureType() string {
	return "via"
}

func (v ViaFeature) FeatureSide() image.Side {
	return v.Side
}

func (v ViaFeature) HitTest(x, y float64) bool {
	return v.Via.HitTest(x, y)
}

func (v ViaFeature) GetBounds() geometry.RectInt {
	return v.Via.Bounds()
}

// TraceFeature wraps a Trace to implement the Feature interface.
type TraceFeature struct {
	trace.ExtendedTrace
}

func (t TraceFeature) FeatureID() string {
	return t.ID
}

func (t TraceFeature) FeatureType() string {
	return "trace"
}

func (t TraceFeature) FeatureSide() image.Side {
	switch t.Layer {
	case trace.LayerFront:
		return image.SideFront
	case trace.LayerBack:
		return image.SideBack
	default:
		return image.SideUnknown
	}
}

func (t TraceFeature) HitTest(x, y float64) bool {
	// Use a tolerance based on trace width
	tolerance := t.Width/2 + 3 // At least 3 pixels
	return trace.HitTestTrace(t.ExtendedTrace, x, y, tolerance)
}

func (t TraceFeature) GetBounds() geometry.RectInt {
	return t.Bounds
}

// Bus represents a named group of features (e.g., address bus, data bus).
type Bus struct {
	ID       string     // Unique identifier
	Name     string     // User-friendly display name
	Color    color.RGBA // Display color (highly saturated)
	Features []string   // Feature IDs assigned to this bus
}

// FeatureRef wraps a feature with bus assignment and display color.
type FeatureRef struct {
	Feature  Feature
	BusID    string     // Empty string = unassigned
	Color    color.RGBA // Effective display color (from bus or default)
	Selected bool       // Whether this feature is currently selected
}

// DefaultColors provides a palette of highly saturated colors for buses.
var DefaultColors = []color.RGBA{
	{255, 0, 0, 255},     // Red
	{0, 255, 0, 255},     // Green
	{0, 0, 255, 255},     // Blue
	{255, 255, 0, 255},   // Yellow
	{255, 0, 255, 255},   // Magenta
	{0, 255, 255, 255},   // Cyan
	{255, 128, 0, 255},   // Orange
	{128, 0, 255, 255},   // Purple
	{0, 255, 128, 255},   // Spring Green
	{255, 0, 128, 255},   // Rose
	{128, 255, 0, 255},   // Lime
	{0, 128, 255, 255},   // Sky Blue
}

// UnassignedColor is used for features not assigned to a bus.
var UnassignedColor = color.RGBA{255, 255, 255, 255} // White

// SelectionColor is used to highlight selected features.
var SelectionColor = color.RGBA{255, 255, 0, 255} // Yellow with high visibility

// NextColor returns the next color from the palette based on bus count.
func NextColor(busCount int) color.RGBA {
	return DefaultColors[busCount%len(DefaultColors)]
}

// NewBus creates a new bus with the given name and auto-assigned color.
func NewBus(name string, busCount int) *Bus {
	return &Bus{
		ID:       generateBusID(),
		Name:     name,
		Color:    NextColor(busCount),
		Features: make([]string, 0),
	}
}

var busCounter int

func generateBusID() string {
	busCounter++
	return "bus-" + itoa(busCounter)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
