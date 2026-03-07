// Package schematic provides an interactive schematic viewer for PCB netlists.
package schematic

import (
	"pcb-tracer/pkg/geometry"
)

// Sheet represents one page of a multi-sheet schematic.
type Sheet struct {
	Number int    `json:"number"` // 1-based sheet number
	Name   string `json:"name"`   // User-friendly name, e.g. "Address Decode"
}

// SchematicDoc is the top-level container for a schematic drawing.
// Coordinates use schematic units (1 unit = 1/100 inch).
type SchematicDoc struct {
	ProjectName        string               `json:"project_name"`
	Sheets             []Sheet              `json:"sheets"`
	Symbols            []*PlacedSymbol      `json:"symbols"`
	Wires              []*Wire              `json:"wires"`
	NetLabels          []*NetLabel          `json:"net_labels"`
	PowerPorts         []*PowerPort         `json:"power_ports"`
	OffSheetConnectors []*OffSheetConnector `json:"off_sheet_connectors,omitempty"`
	ShowStubs          bool                 `json:"-"`
	PowerNetIDs        map[string]bool      `json:"-"` // Net IDs that are power/ground
}

// PlacedSymbol is one logic function placed on the schematic.
// A 74LS00 (quad NAND) becomes 4 PlacedSymbols (U3A, U3B, U3C, U3D).
type PlacedSymbol struct {
	ID           string `json:"id"`            // e.g., "U3-1" (component-function)
	ComponentID  string `json:"component_id"`  // PCB component ID, e.g., "U3"
	FunctionName string `json:"function_name"` // Gate designator within component
	GateType     string `json:"gate_type"`     // component.GateType as string
	PartNumber   string `json:"part_number"`   // e.g., "74LS00"
	Description  string `json:"description"`   // e.g., "Quad 2-input NAND gate"

	// Position in schematic units
	X float64 `json:"x"`
	Y float64 `json:"y"`

	// Transform state (persisted in layout file)
	FlipH    bool `json:"flip_h,omitempty"`    // Mirror horizontally (swap left/right pins)
	FlipV    bool `json:"flip_v,omitempty"`    // Mirror vertically (swap top/bottom)
	Rotation int  `json:"rotation,omitempty"`  // Degrees: 0, 90, 180, 270
	Sheet    int  `json:"sheet,omitempty"`     // Sheet number (1-based; 0 = sheet 1)

	// Pins with their absolute positions
	Pins []*SchematicPin `json:"pins"`

	// Selection/UI state (not persisted)
	Selected bool `json:"-"`
	Column   int  `json:"-"` // auto-layout column
	Row      int  `json:"-"` // auto-layout row
}

// SchematicPin is a pin on a placed symbol.
type SchematicPin struct {
	PinNumber int    `json:"pin_number"`
	Name      string `json:"name"`
	Direction string `json:"direction"` // "input", "output", "enable", "clock", "power"

	// Absolute schematic coordinates
	X float64 `json:"x"` // Pin tip (where wires connect)
	Y float64 `json:"y"`

	// Where the stub meets the symbol body
	StubX float64 `json:"stub_x"`
	StubY float64 `json:"stub_y"`

	Negated bool   `json:"negated,omitempty"` // Draw negation bubble
	Clock   bool   `json:"clock,omitempty"`   // Draw clock wedge
	NetID   string `json:"net_id,omitempty"`  // Linked electrical net
	NetName string `json:"net_name,omitempty"`
}

// Wire is a connection path between pins using Manhattan routing.
type Wire struct {
	ID       string             `json:"id"`
	NetID    string             `json:"net_id"`
	NetName  string             `json:"net_name,omitempty"`
	Points   []geometry.Point2D `json:"points"` // Ordered waypoints
	IsBus    bool               `json:"is_bus,omitempty"`
	Sheet    int                `json:"sheet,omitempty"`
	Selected bool               `json:"-"`
}

// NetLabel is a text label placed on a wire showing the net/signal name.
type NetLabel struct {
	NetID   string  `json:"net_id"`
	NetName string  `json:"net_name"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
}

// PowerPort represents a VCC or GND symbol on the schematic.
// Each component pin on a power net gets its own local PowerPort.
type PowerPort struct {
	NetName       string  `json:"net_name"`                  // "VCC", "GND", "+5V", etc.
	X             float64 `json:"x"`
	Y             float64 `json:"y"`
	IsGround      bool    `json:"is_ground"`
	PinX          float64 `json:"pin_x"`                     // Connection point (at component pin tip)
	PinY          float64 `json:"pin_y"`
	OwnerSymbolID string  `json:"owner_symbol_id,omitempty"` // The symbol this port is attached to
	OwnerPinNum   int     `json:"owner_pin_num,omitempty"`   // The specific pin number
	Sheet         int     `json:"sheet,omitempty"`
}

// OffSheetConnector indicates a net continues on another sheet.
type OffSheetConnector struct {
	NetID       string  `json:"net_id"`
	NetName     string  `json:"net_name"`
	Sheet       int     `json:"sheet"`        // Sheet this indicator appears on
	TargetSheet int     `json:"target_sheet"` // Sheet the net continues to
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	Direction   string  `json:"direction"` // "input" or "output"
}

// effectiveSheet returns the sheet number, treating 0 as 1.
func effectiveSheet(s int) int {
	if s <= 0 {
		return 1
	}
	return s
}

// Bounds returns the bounding rectangle of all symbols in the schematic.
func (doc *SchematicDoc) Bounds() (minX, minY, maxX, maxY float64) {
	if len(doc.Symbols) == 0 {
		return 0, 0, 1000, 1000
	}
	minX, minY = 1e9, 1e9
	maxX, maxY = -1e9, -1e9
	for _, sym := range doc.Symbols {
		for _, pin := range sym.Pins {
			if pin.X < minX {
				minX = pin.X
			}
			if pin.Y < minY {
				minY = pin.Y
			}
			if pin.X > maxX {
				maxX = pin.X
			}
			if pin.Y > maxY {
				maxY = pin.Y
			}
		}
		// Also include symbol center with some padding
		if sym.X-100 < minX {
			minX = sym.X - 100
		}
		if sym.Y-100 < minY {
			minY = sym.Y - 100
		}
		if sym.X+200 > maxX {
			maxX = sym.X + 200
		}
		if sym.Y+100 > maxY {
			maxY = sym.Y + 100
		}
	}
	for _, pp := range doc.PowerPorts {
		if pp.X-50 < minX {
			minX = pp.X - 50
		}
		if pp.Y-50 < minY {
			minY = pp.Y - 50
		}
		if pp.X+50 > maxX {
			maxX = pp.X + 50
		}
		if pp.Y+50 > maxY {
			maxY = pp.Y + 50
		}
	}
	// Add margin
	minX -= 100
	minY -= 100
	maxX += 100
	maxY += 100
	return
}

// SymbolByID returns the placed symbol with the given ID, or nil.
func (doc *SchematicDoc) SymbolByID(id string) *PlacedSymbol {
	for _, sym := range doc.Symbols {
		if sym.ID == id {
			return sym
		}
	}
	return nil
}

// WireByID returns the wire with the given ID, or nil.
func (doc *SchematicDoc) WireByID(id string) *Wire {
	for _, w := range doc.Wires {
		if w.ID == id {
			return w
		}
	}
	return nil
}

// WiresForNet returns all wires belonging to the given net.
func (doc *SchematicDoc) WiresForNet(netID string) []*Wire {
	var result []*Wire
	for _, w := range doc.Wires {
		if w.NetID == netID {
			result = append(result, w)
		}
	}
	return result
}

// WiresConnectedToSymbol returns all wires that have an endpoint at any pin of the symbol.
func (doc *SchematicDoc) WiresConnectedToSymbol(sym *PlacedSymbol) []*Wire {
	pinSet := make(map[string]bool)
	for _, pin := range sym.Pins {
		if pin.NetID != "" {
			pinSet[pin.NetID] = true
		}
	}
	var result []*Wire
	for _, w := range doc.Wires {
		if pinSet[w.NetID] {
			result = append(result, w)
		}
	}
	return result
}
