// Package connector provides board edge connector representation and management.
package connector

import (
	"fmt"

	"pcb-tracer/internal/alignment"
	"pcb-tracer/internal/image"
	"pcb-tracer/pkg/geometry"
)

// Connector represents a single board edge contact (gold finger).
// These are the contacts detected during alignment that connect to the bus backplane.
type Connector struct {
	ID            string           `json:"id"`             // Unique identifier, e.g., "conn-F-001"
	Index         int              `json:"index"`          // 0-based index along the edge (0-49 for S-100)
	Side          image.Side       `json:"side"`           // Front or back side
	Center        geometry.Point2D `json:"center"`         // Center in aligned image coordinates
	Bounds        geometry.RectInt `json:"bounds"`         // Bounding rectangle

	// Detection metadata
	DetectionPass alignment.DetectionPass `json:"detection_pass"` // How it was detected
	Confidence    float64                 `json:"confidence"`     // Detection confidence (0-1)

	// Pin mapping (from BoardDefinition)
	PinNumber  int    `json:"pin_number"`  // Physical pin number (1-100 for S-100)
	SignalName string `json:"signal_name"` // Signal name (e.g., "A0", "D7", "CLOCK")

	// Netlist membership
	NetID string `json:"net_id,omitempty"` // ID of the net this connector belongs to
}

// NewConnectorFromContact creates a Connector from an alignment Contact.
func NewConnectorFromContact(index int, side image.Side, contact *alignment.Contact, pinNumber int) *Connector {
	confidence := 1.0
	if contact.Pass == alignment.PassBruteForce {
		confidence = 0.8
	} else if contact.Pass == alignment.PassRescue {
		confidence = 0.6
	}

	return &Connector{
		ID:            fmt.Sprintf("conn-%s-%03d", sidePrefix(side), index),
		Index:         index,
		Side:          side,
		Center:        contact.Center,
		Bounds:        contact.Bounds,
		DetectionPass: contact.Pass,
		Confidence:    confidence,
		PinNumber:     pinNumber,
	}
}

// HitTest returns true if the point is within the connector bounds.
func (c *Connector) HitTest(x, y float64) bool {
	return x >= float64(c.Bounds.X) && x <= float64(c.Bounds.X+c.Bounds.Width) &&
		y >= float64(c.Bounds.Y) && y <= float64(c.Bounds.Y+c.Bounds.Height)
}

// sidePrefix returns a short prefix for the side.
func sidePrefix(side image.Side) string {
	if side == image.SideFront {
		return "F"
	}
	return "B"
}

// GetBounds returns the connector's bounding rectangle.
func (c *Connector) GetBounds() geometry.RectInt {
	return c.Bounds
}
