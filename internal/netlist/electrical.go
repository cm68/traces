package netlist

import (
	"fmt"
	"math"
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
// At equal priority, prefers the shorter name so "GND" wins over "GND#2".
func BetterNetName(a, b string) string {
	pa := netNamePriority(a)
	pb := netNamePriority(b)
	if pa > pb {
		return a
	}
	if pb > pa {
		return b
	}
	// Same priority: prefer shorter (drops instance suffix)
	if len(a) <= len(b) {
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
	ID          string `json:"id"`                       // Unique net ID, e.g., "net-001"
	Name        string `json:"name"`                     // Display name (signal, user, or auto)
	ManualName  bool   `json:"manual_name,omitempty"`    // True if name was explicitly set by user
	Description string `json:"description"`              // Optional description

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
// The caller must supply a unique id (e.g. from DetectedFeaturesLayer.NextNetID()).
func NewElectricalNet(id string, conn *connector.Connector) *ElectricalNet {
	return &ElectricalNet{
		ID:              id,
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

// RebuildIDLists rebuilds ConnectorIDs, ViaIDs, TraceIDs, and PadIDs
// from the Elements slice. Call after modifying Elements directly.
func (n *ElectricalNet) RebuildIDLists() {
	n.ConnectorIDs = nil
	n.ViaIDs = nil
	n.TraceIDs = nil
	n.PadIDs = nil
	for _, e := range n.Elements {
		switch e.Type {
		case ElementConnector:
			n.ConnectorIDs = append(n.ConnectorIDs, e.ID)
		case ElementVia:
			n.ViaIDs = append(n.ViaIDs, e.ID)
		case ElementTrace:
			n.TraceIDs = append(n.TraceIDs, e.ID)
		case ElementPad:
			n.PadIDs = append(n.PadIDs, e.ID)
		}
	}
}

// TraceEndpoint describes the two endpoints of a trace for connectivity analysis.
type TraceEndpoint struct {
	Start geometry.Point2D
	End   geometry.Point2D
}

// ConnectedComponents partitions the non-trace elements of this net into
// groups that are interconnected by the net's traces. nodePositions maps
// element IDs to their current positions. traceEndpoints maps trace IDs
// to their start/end points. tolerance is the snap distance.
// Returns a slice of element-ID groups; len==1 means the net is fully connected.
func (n *ElectricalNet) ConnectedComponents(
	nodePositions map[string]geometry.Point2D,
	traceEndpoints map[string]TraceEndpoint,
	tolerance float64,
) [][]string {
	// Collect node (non-trace) element IDs
	var nodeIDs []string
	for _, e := range n.Elements {
		if e.Type != ElementTrace {
			nodeIDs = append(nodeIDs, e.ID)
		}
	}
	if len(nodeIDs) <= 1 {
		return [][]string{nodeIDs}
	}

	// Build adjacency list: nodeID -> set of connected nodeIDs
	adj := make(map[string]map[string]bool)
	for _, id := range nodeIDs {
		adj[id] = make(map[string]bool)
	}

	for _, tid := range n.TraceIDs {
		ep, ok := traceEndpoints[tid]
		if !ok {
			continue
		}
		var startNode, endNode string
		for _, nid := range nodeIDs {
			pos, ok := nodePositions[nid]
			if !ok {
				continue
			}
			if math.Hypot(pos.X-ep.Start.X, pos.Y-ep.Start.Y) <= tolerance {
				startNode = nid
			}
			if math.Hypot(pos.X-ep.End.X, pos.Y-ep.End.Y) <= tolerance {
				endNode = nid
			}
		}
		if startNode != "" && endNode != "" && startNode != endNode {
			adj[startNode][endNode] = true
			adj[endNode][startNode] = true
		}
	}

	// BFS to find connected components
	visited := make(map[string]bool)
	var components [][]string
	for _, nid := range nodeIDs {
		if visited[nid] {
			continue
		}
		var comp []string
		queue := []string{nid}
		visited[nid] = true
		for len(queue) > 0 {
			curr := queue[0]
			queue = queue[1:]
			comp = append(comp, curr)
			for neighbor := range adj[curr] {
				if !visited[neighbor] {
					visited[neighbor] = true
					queue = append(queue, neighbor)
				}
			}
		}
		components = append(components, comp)
	}
	return components
}

// BaseNetName strips an instance suffix (e.g. "GND#2" -> "GND").
func BaseNetName(name string) string {
	if idx := strings.LastIndex(name, "#"); idx > 0 {
		return name[:idx]
	}
	return name
}
