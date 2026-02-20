package connector

import (
	"encoding/json"
	"os"
)

// LogicSense defines whether a signal is active high or low.
type LogicSense int

const (
	LogicActiveHigh LogicSense = iota
	LogicActiveLow
	LogicRisingEdge
	LogicFallingEdge
)

func (l LogicSense) String() string {
	switch l {
	case LogicActiveHigh:
		return "Active High"
	case LogicActiveLow:
		return "Active Low"
	case LogicRisingEdge:
		return "Rising Edge"
	case LogicFallingEdge:
		return "Falling Edge"
	default:
		return "Unknown"
	}
}

// MarshalJSON implements json.Marshaler.
func (l LogicSense) MarshalJSON() ([]byte, error) {
	return json.Marshal(l.String())
}

// UnmarshalJSON implements json.Unmarshaler.
func (l *LogicSense) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	switch s {
	case "Active High":
		*l = LogicActiveHigh
	case "Active Low":
		*l = LogicActiveLow
	case "Rising Edge":
		*l = LogicRisingEdge
	case "Falling Edge":
		*l = LogicFallingEdge
	default:
		*l = LogicActiveHigh
	}
	return nil
}

// SignalDirection defines the direction of a signal.
type SignalDirection int

const (
	DirectionInput SignalDirection = iota
	DirectionOutput
	DirectionBidirectional
	DirectionPower
	DirectionGround
)

func (d SignalDirection) String() string {
	switch d {
	case DirectionInput:
		return "Input"
	case DirectionOutput:
		return "Output"
	case DirectionBidirectional:
		return "Bidirectional"
	case DirectionPower:
		return "Power"
	case DirectionGround:
		return "Ground"
	default:
		return "Unknown"
	}
}

// MarshalJSON implements json.Marshaler.
func (d SignalDirection) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

// UnmarshalJSON implements json.Unmarshaler.
func (d *SignalDirection) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	switch s {
	case "Input":
		*d = DirectionInput
	case "Output":
		*d = DirectionOutput
	case "Bidirectional":
		*d = DirectionBidirectional
	case "Power":
		*d = DirectionPower
	case "Ground":
		*d = DirectionGround
	default:
		*d = DirectionBidirectional
	}
	return nil
}

// PinDefinition defines a single connector pin's signal characteristics.
type PinDefinition struct {
	PinNumber      int             `json:"pin_number"`      // Physical pin number (1-100)
	SignalName     string          `json:"signal_name"`     // e.g., "A0", "D7", "CLOCK"
	Description    string          `json:"description"`     // Longer description
	ConnectorIndex int             `json:"connector_index"` // 0-based index along edge (0-49)
	Side           string          `json:"side"`            // "front" or "back"
	LogicSense     LogicSense      `json:"logic_sense"`
	Direction      SignalDirection `json:"direction"`

	// Optional bus grouping
	BusGroup    string `json:"bus_group,omitempty"`    // e.g., "address", "data", "control"
	BusBitIndex int    `json:"bus_bit_index,omitempty"` // Bit position in bus (0-15 for address)
}

// BoardDefinition holds the complete pin mapping for a board type.
type BoardDefinition struct {
	Name        string           `json:"name"`          // e.g., "S-100 (IEEE 696)"
	Version     string           `json:"version"`       // Definition version
	TotalPins   int              `json:"total_pins"`    // Total pins (e.g., 100 for S-100)
	PinsPerSide int              `json:"pins_per_side"` // Pins per side (e.g., 50)
	Pins        []*PinDefinition `json:"pins"`

	// Quick lookup maps (populated on load, not serialized)
	byPinNumber map[int]*PinDefinition
	bySignal    map[string]*PinDefinition
}

// NewBoardDefinition creates an empty board definition.
func NewBoardDefinition(name string, totalPins int) *BoardDefinition {
	return &BoardDefinition{
		Name:        name,
		Version:     "1.0",
		TotalPins:   totalPins,
		PinsPerSide: totalPins / 2,
		Pins:        make([]*PinDefinition, 0, totalPins),
		byPinNumber: make(map[int]*PinDefinition),
		bySignal:    make(map[string]*PinDefinition),
	}
}

// AddPin adds a pin definition.
func (bd *BoardDefinition) AddPin(pin *PinDefinition) {
	bd.Pins = append(bd.Pins, pin)
	bd.byPinNumber[pin.PinNumber] = pin
	if pin.SignalName != "" {
		bd.bySignal[pin.SignalName] = pin
	}
}

// GetPinByNumber returns the pin definition for a given pin number.
func (bd *BoardDefinition) GetPinByNumber(pinNumber int) *PinDefinition {
	return bd.byPinNumber[pinNumber]
}

// GetPinBySignal returns the pin definition for a given signal name.
func (bd *BoardDefinition) GetPinBySignal(signalName string) *PinDefinition {
	return bd.bySignal[signalName]
}

// GetPinByPosition returns the pin definition for a connector index and side.
// Contacts are detected sorted left-to-right by X position.
// For S-100 front (component side): pin 1 is at the right edge, so index 0
// (leftmost) maps to pin 50 and index 49 (rightmost) maps to pin 1.
// For S-100 back (solder side): flipping the board left-to-right puts pin 51
// (behind pin 1) at the left edge — index 0 maps to pin 51, index 49 to pin 100.
func (bd *BoardDefinition) GetPinByPosition(index int, front bool) *PinDefinition {
	var pinNumber int
	if front {
		// Rightmost contact is pin 1
		pinNumber = bd.PinsPerSide - index
	} else {
		// Back image is flipped to match front orientation, so same reversal:
		// leftmost contact (same position as front pin 50) = pin 100
		pinNumber = bd.TotalPins - index
	}
	return bd.byPinNumber[pinNumber]
}

// RebuildMaps rebuilds the lookup maps from the Pins slice.
// Call this after loading from JSON.
func (bd *BoardDefinition) RebuildMaps() {
	bd.byPinNumber = make(map[int]*PinDefinition)
	bd.bySignal = make(map[string]*PinDefinition)
	for _, pin := range bd.Pins {
		bd.byPinNumber[pin.PinNumber] = pin
		if pin.SignalName != "" {
			bd.bySignal[pin.SignalName] = pin
		}
	}
}

// Save writes the board definition to a JSON file.
func (bd *BoardDefinition) Save(path string) error {
	data, err := json.MarshalIndent(bd, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// LoadBoardDefinition loads a board definition from JSON.
func LoadBoardDefinition(path string) (*BoardDefinition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var bd BoardDefinition
	if err := json.Unmarshal(data, &bd); err != nil {
		return nil, err
	}

	bd.RebuildMaps()
	return &bd, nil
}
