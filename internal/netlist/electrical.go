package netlist

import (
	"fmt"
	"regexp"
	"strings"

	"pcb-tracer/internal/connector"
	"pcb-tracer/internal/trace"
	"pcb-tracer/internal/via"
	"pcb-tracer/pkg/geometry"
)

// autoNetRe matches auto-generated net names like "net-001", "net-042".
var autoNetRe = regexp.MustCompile(`^net-\d+$`)

// netNamePriority returns a priority score for a net name.
// Higher is better: 0=auto-generated, 1=component pin, 2=signal/user name.
func netNamePriority(name string) int {
	if autoNetRe.MatchString(name) {
		return 0
	}
	if strings.Contains(name, ".") {
		return 1 // component pin name like "B13.1"
	}
	return 2 // signal name or user-assigned name
}

// IsLowPriorityName returns true if the name is auto-generated ("net-NNN") or
// a component.pin format ("B13.1") — i.e. safe to overwrite with a signal name.
// User-assigned or signal names (priority 2) are NOT low priority.
func IsLowPriorityName(name string) bool {
	return netNamePriority(name) < 2
}

// BetterNetName returns the higher-priority name between a and b.
// Priority: signal/user names > component pin names > auto-generated "net-NNN".
func BetterNetName(a, b string) string {
	pa := netNamePriority(a)
	pb := netNamePriority(b)
	if pa >= pb {
		return a
	}
	return b
}

// NetElementType identifies what kind of element is in the net.
type NetElementType int

const (
	ElementConnector NetElementType = iota // Board edge connector
	ElementVia                             // Confirmed via
	ElementPad                             // Component pad
	ElementTrace                           // Copper trace
)

func (t NetElementType) String() string {
	switch t {
	case ElementConnector:
		return "Connector"
	case ElementVia:
		return "Via"
	case ElementPad:
		return "Pad"
	case ElementTrace:
		return "Trace"
	default:
		return "Unknown"
	}
}

// NetElement identifies an element in a net with its position.
type NetElement struct {
	Type     NetElementType   `json:"type"`
	ID       string           `json:"id"`
	Position geometry.Point2D `json:"position"` // For spatial queries
}

// ElectricalNet represents a single electrical net with all its connections.
// Named by the connector's signal name when rooted at a connector.
type ElectricalNet struct {
	ID          string `json:"id"`          // Unique net ID, e.g., "net-A0"
	Name        string `json:"name"`        // Signal name (from connector or user)
	Description string `json:"description"` // Optional description

	// The connector that roots this net (empty for internal nets)
	RootConnectorID string `json:"root_connector_id,omitempty"`

	// All elements in this net
	Elements []NetElement `json:"elements"`

	// Element IDs for quick lookup
	ConnectorIDs []string `json:"connector_ids"` // Connector IDs in this net
	ViaIDs       []string `json:"via_ids"`       // ConfirmedVia IDs
	TraceIDs     []string `json:"trace_ids"`     // Trace IDs
	PadIDs       []string `json:"pad_ids"`       // Component pad refs (ComponentID.PinNumber)

	// Computed properties
	IsComplete bool `json:"is_complete"` // True if fully traced
	HasErrors  bool `json:"has_errors"`  // True if connectivity issues detected
}

// NewElectricalNet creates a new net rooted at a connector.
func NewElectricalNet(conn *connector.Connector) *ElectricalNet {
	return &ElectricalNet{
		ID:              "net-" + conn.SignalName,
		Name:            conn.SignalName,
		RootConnectorID: conn.ID,
		ConnectorIDs:    []string{conn.ID},
		Elements: []NetElement{{
			Type:     ElementConnector,
			ID:       conn.ID,
			Position: conn.Center,
		}},
		ViaIDs:   make([]string, 0),
		TraceIDs: make([]string, 0),
		PadIDs:   make([]string, 0),
	}
}

// NewElectricalNetWithName creates a new net with a custom name (no connector root).
func NewElectricalNetWithName(id, name string) *ElectricalNet {
	return &ElectricalNet{
		ID:           id,
		Name:         name,
		Elements:     make([]NetElement, 0),
		ConnectorIDs: make([]string, 0),
		ViaIDs:       make([]string, 0),
		TraceIDs:     make([]string, 0),
		PadIDs:       make([]string, 0),
	}
}

// AddConnector adds a connector to the net.
func (n *ElectricalNet) AddConnector(conn *connector.Connector) {
	n.ConnectorIDs = append(n.ConnectorIDs, conn.ID)
	n.Elements = append(n.Elements, NetElement{
		Type:     ElementConnector,
		ID:       conn.ID,
		Position: conn.Center,
	})
}

// AddVia adds a confirmed via to the net.
func (n *ElectricalNet) AddVia(v *via.ConfirmedVia) {
	n.ViaIDs = append(n.ViaIDs, v.ID)
	n.Elements = append(n.Elements, NetElement{
		Type:     ElementVia,
		ID:       v.ID,
		Position: v.Center,
	})
}

// AddTrace adds a trace to the net.
func (n *ElectricalNet) AddTrace(t *trace.ExtendedTrace) {
	n.TraceIDs = append(n.TraceIDs, t.ID)
	// Use first point as position
	pos := geometry.Point2D{}
	if len(t.Points) > 0 {
		pos = t.Points[0]
	}
	n.Elements = append(n.Elements, NetElement{
		Type:     ElementTrace,
		ID:       t.ID,
		Position: pos,
	})
}

// AddComponentPin adds a component pin to the net.
func (n *ElectricalNet) AddComponentPin(componentID string, pinNumber int, position geometry.Point2D) {
	padID := fmt.Sprintf("%s.%d", componentID, pinNumber)
	n.PadIDs = append(n.PadIDs, padID)
	n.Elements = append(n.Elements, NetElement{
		Type:     ElementPad,
		ID:       padID,
		Position: position,
	})
}

// ContainsElement checks if an element is in this net.
func (n *ElectricalNet) ContainsElement(elementID string) bool {
	for _, e := range n.Elements {
		if e.ID == elementID {
			return true
		}
	}
	return false
}

// ContainsConnector checks if a connector is in this net.
func (n *ElectricalNet) ContainsConnector(connectorID string) bool {
	for _, id := range n.ConnectorIDs {
		if id == connectorID {
			return true
		}
	}
	return false
}

// ContainsVia checks if a via is in this net.
func (n *ElectricalNet) ContainsVia(viaID string) bool {
	for _, id := range n.ViaIDs {
		if id == viaID {
			return true
		}
	}
	return false
}

// ElementCount returns the total number of elements in the net.
func (n *ElectricalNet) ElementCount() int {
	return len(n.Elements)
}

// RemoveElement removes an element from the net by ID.
func (n *ElectricalNet) RemoveElement(elementID string) bool {
	// Remove from Elements slice
	found := false
	for i, e := range n.Elements {
		if e.ID == elementID {
			n.Elements = append(n.Elements[:i], n.Elements[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		return false
	}

	// Remove from appropriate ID slice
	for i, id := range n.ConnectorIDs {
		if id == elementID {
			n.ConnectorIDs = append(n.ConnectorIDs[:i], n.ConnectorIDs[i+1:]...)
			return true
		}
	}
	for i, id := range n.ViaIDs {
		if id == elementID {
			n.ViaIDs = append(n.ViaIDs[:i], n.ViaIDs[i+1:]...)
			return true
		}
	}
	for i, id := range n.TraceIDs {
		if id == elementID {
			n.TraceIDs = append(n.TraceIDs[:i], n.TraceIDs[i+1:]...)
			return true
		}
	}
	for i, id := range n.PadIDs {
		if id == elementID {
			n.PadIDs = append(n.PadIDs[:i], n.PadIDs[i+1:]...)
			return true
		}
	}

	return true
}

// GetElementsByType returns all elements of a specific type.
func (n *ElectricalNet) GetElementsByType(t NetElementType) []NetElement {
	result := make([]NetElement, 0)
	for _, e := range n.Elements {
		if e.Type == t {
			result = append(result, e)
		}
	}
	return result
}
