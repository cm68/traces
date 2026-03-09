package schematic

import (
	"fmt"
	"sort"
	"strings"

	"pcb-tracer/internal/app"
	"pcb-tracer/internal/component"
	"pcb-tracer/internal/connector"
)

// powerNetNames identifies nets that should use PowerPort symbols instead of wires.
var powerNetNames = map[string]bool{
	"VCC": true, "GND": true, "+5V": true, "-5V": true,
	"+12V": true, "-12V": true, "+8V": true, "+16V": true,
	"PWR": true, "POWER": true,
}

// isPowerNet returns true if the net name indicates a power/ground net.
func isPowerNet(name string) bool {
	upper := strings.ToUpper(name)
	if powerNetNames[upper] {
		return true
	}
	if strings.HasPrefix(upper, "VCC") || strings.HasPrefix(upper, "GND") ||
		strings.HasPrefix(upper, "VDD") || strings.HasPrefix(upper, "VSS") {
		return true
	}
	return false
}

// isGroundNet returns true if the net name indicates a ground net.
func isGroundNet(name string) bool {
	upper := strings.ToUpper(name)
	return upper == "GND" || strings.HasPrefix(upper, "GND") ||
		upper == "VSS" || strings.HasPrefix(upper, "VSS")
}

// filterStubs removes connector symbols and wires for nets that have fewer than
// 2 connected pins in the schematic. These are dead-end connections with nothing
// to wire to.
func filterStubs(doc *SchematicDoc) {
	// Count how many schematic pins reference each netID
	netPinCount := make(map[string]int)
	for _, sym := range doc.Symbols {
		for _, pin := range sym.Pins {
			if pin.NetID != "" {
				netPinCount[pin.NetID]++
			}
		}
	}

	// Remove connector symbols whose only pin is on a stub net
	filtered := doc.Symbols[:0]
	for _, sym := range doc.Symbols {
		if sym.GateType == "CONNECTOR" && len(sym.Pins) == 1 {
			if netPinCount[sym.Pins[0].NetID] < 2 {
				// Removing this connector also reduces pin count
				if sym.Pins[0].NetID != "" {
					netPinCount[sym.Pins[0].NetID]--
				}
				continue
			}
		}
		filtered = append(filtered, sym)
	}
	doc.Symbols = filtered
}

// GenerateSchematic creates a schematic document from the current app state.
// If showStubs is true, connectors and nets with only one terminus are included.
func GenerateSchematic(state *app.State, showStubs ...bool) *SchematicDoc {
	doc := &SchematicDoc{
		ProjectName: "Schematic",
		Sheets:      []Sheet{{Number: 1, Name: "Main"}},
	}

	if state == nil || state.FeaturesLayer == nil {
		return doc
	}

	lib := state.ComponentLibrary
	nets := state.FeaturesLayer.GetNets()

	// Build pad-to-net index: "ComponentID.PinNumber" → net
	padToNet := make(map[string]string)   // padID → netID
	padToName := make(map[string]string)  // padID → net name
	for _, net := range nets {
		for _, padID := range net.PadIDs {
			padToNet[padID] = net.ID
			padToName[padID] = net.Name
		}
		// Resolve ViaIDs to component pins — this is how most pads connect to nets
		for _, viaID := range net.ViaIDs {
			cv := state.FeaturesLayer.GetConfirmedViaByID(viaID)
			if cv == nil || cv.ComponentID == "" || cv.PinNumber == "" {
				continue
			}
			padID := cv.ComponentID + "." + cv.PinNumber
			padToNet[padID] = net.ID
			if net.Name != "" {
				padToName[padID] = net.Name
			}
		}
	}

	// Step 1: Create symbols from components
	for _, comp := range state.Components {
		var partDef *component.PartDefinition
		if lib != nil {
			partDef = lib.GetByAlias(comp.PartNumber, comp.Package)
			if partDef == nil {
				partDef = lib.FindByPartNumber(comp.PartNumber)
			}
		}

		if partDef != nil && partDef.HasFunctions() {
			// Create one symbol per logic function
			for _, fn := range partDef.Functions {
				sym := &PlacedSymbol{
					ID:           fmt.Sprintf("%s-%s", comp.ID, fn.Name),
					ComponentID:  comp.ID,
					FunctionName: fn.Name,
					GateType:     string(fn.Type),
					PartNumber:   comp.PartNumber,
					Description:  partDef.Description,
				}

				// Build pins from the logic function
				sym.Pins = BuildPinsFromFunction(&fn, partDef)

				// Link pins to nets
				for _, pin := range sym.Pins {
					padID := fmt.Sprintf("%s.%d", comp.ID, pin.PinNumber)
					if netID, ok := padToNet[padID]; ok {
						pin.NetID = netID
						pin.NetName = padToName[padID]
					}
				}

				doc.Symbols = append(doc.Symbols, sym)
			}
		} else {
			// No functions defined — create a single BLOCK symbol with all pins
			sym := &PlacedSymbol{
				ID:          comp.ID,
				ComponentID: comp.ID,
				GateType:    "BLOCK",
				PartNumber:  comp.PartNumber,
			}
			if partDef != nil {
				sym.Description = partDef.Description
				for _, p := range partDef.Pins {
					dir := "input"
					if p.Direction == connector.DirectionOutput {
						dir = "output"
					} else if p.Direction == connector.DirectionBidirectional {
						dir = "input" // default to input side
					} else if p.Direction == connector.DirectionPower || p.Direction == connector.DirectionGround {
						dir = "power"
					}
					pin := &SchematicPin{
						PinNumber: p.Number,
						Name:      p.Name,
						Direction: dir,
					}
					padID := fmt.Sprintf("%s.%d", comp.ID, p.Number)
					if netID, ok := padToNet[padID]; ok {
						pin.NetID = netID
						pin.NetName = padToName[padID]
					}
					sym.Pins = append(sym.Pins, pin)
				}
			} else {
				// No library entry: create pins from padToNet so the component
				// still participates in wiring (e.g. resistor packs, unlisted parts).
				prefix := comp.ID + "."
				var pinNums []int
				seen := make(map[int]bool)
				for padID := range padToNet {
					if !strings.HasPrefix(padID, prefix) {
						continue
					}
					var n int
					if _, err := fmt.Sscanf(padID[len(prefix):], "%d", &n); err == nil && !seen[n] {
						seen[n] = true
						pinNums = append(pinNums, n)
					}
				}
				sort.Ints(pinNums)
				for _, n := range pinNums {
					padID := fmt.Sprintf("%s.%d", comp.ID, n)
					netName := padToName[padID]
					dir := "input"
					if isPowerNet(netName) {
						dir = "power"
					}
					sym.Pins = append(sym.Pins, &SchematicPin{
						PinNumber: n,
						Direction: dir,
						NetID:     padToNet[padID],
						NetName:   netName,
					})
				}
			}
			doc.Symbols = append(doc.Symbols, sym)
		}
	}

	// Step 2: Create connector port symbols for board-edge connectors
	boardDef := state.BoardDefinition
	connectors := state.FeaturesLayer.GetConnectors()
	for _, conn := range connectors {
		if conn.SignalName == "" {
			continue
		}
		// Skip power/ground connectors — handled as power ports
		if isPowerNet(conn.SignalName) {
			continue
		}

		// Determine direction from board definition
		dir := "input" // default: signal comes in from connector
		if boardDef != nil {
			if pinDef := boardDef.GetPinByNumber(conn.PinNumber); pinDef != nil {
				switch pinDef.Direction {
				case connector.DirectionOutput:
					dir = "output"
				case connector.DirectionBidirectional:
					dir = "input"
				}
			}
		}

		sym := &PlacedSymbol{
			ID:          fmt.Sprintf("CONN-%d", conn.PinNumber),
			ComponentID: conn.ID,
			GateType:    "CONNECTOR",
			PartNumber:  conn.SignalName,
		}

		// Connector has one pin: the signal
		net := state.FeaturesLayer.GetNetForElement(conn.ID)
		pin := &SchematicPin{
			PinNumber: conn.PinNumber,
			Name:      conn.SignalName,
			Direction: dir,
		}
		if net != nil {
			pin.NetID = net.ID
			pin.NetName = net.Name
		}
		sym.Pins = append(sym.Pins, pin)
		doc.Symbols = append(doc.Symbols, sym)
	}

	// Step 3: Identify power nets and create per-pin PowerPort symbols
	doc.PowerNetIDs = make(map[string]bool)
	for _, net := range nets {
		if !isPowerNet(net.Name) {
			continue
		}
		doc.PowerNetIDs[net.ID] = true
		isGnd := isGroundNet(net.Name)

		// Create one power port for each component pin on this net
		for _, sym := range doc.Symbols {
			for _, pin := range sym.Pins {
				if pin.NetID != net.ID {
					continue
				}
				doc.PowerPorts = append(doc.PowerPorts, &PowerPort{
					NetName:       net.Name,
					IsGround:      isGnd,
					OwnerSymbolID: sym.ID,
					OwnerPinNum:   pin.PinNumber,
					// Positions computed by positionPowerPorts after pin layout
				})
			}
		}
	}

	// Step 3.5: Filter single-terminus stubs unless requested
	includeStubs := len(showStubs) > 0 && showStubs[0]
	doc.ShowStubs = includeStubs
	if !includeStubs {
		filterStubs(doc)
	}

	// Step 4: Auto-layout
	AutoLayout(doc)

	// Step 5: Compute pin positions after layout
	for _, sym := range doc.Symbols {
		def := GetSymbolDef(sym.GateType,
			countPinsByDir(sym, "input"),
			countPinsByDir(sym, "output"),
			countPinsByDir(sym, "enable"),
			countPinsByDir(sym, "clock"))
		ComputePinPositions(sym, def)
	}

	// Step 5.5: Position power ports adjacent to their owner pins
	positionPowerPorts(doc)

	// Step 6: Generate off-sheet connectors (before routing, so they participate as endpoints)
	generateOffSheetConnectors(doc)

	// Step 7: Route wires (includes off-sheet connector positions as endpoints)
	RouteAllWires(doc)

	// Step 8: Populate wire NetName from the nets list (regenerateNetLabels uses it as fallback).
	netNameByID := make(map[string]string, len(nets))
	for _, net := range nets {
		if net.Name != "" {
			netNameByID[net.ID] = net.Name
		}
	}
	for _, wire := range doc.Wires {
		if wire.NetName == "" {
			wire.NetName = netNameByID[wire.NetID]
		}
	}

	// Step 9: Place net labels on wires (one per wire, sheet-aware).
	regenerateNetLabels(doc)

	return doc
}

// positionPowerPorts places each power port adjacent to its owner pin.
// VCC/power ports go above the pin; GND ports go below.
func positionPowerPorts(doc *SchematicDoc) {
	// Build index: symbolID → symbol
	symByID := make(map[string]*PlacedSymbol)
	for _, sym := range doc.Symbols {
		symByID[sym.ID] = sym
	}

	for _, pp := range doc.PowerPorts {
		sym := symByID[pp.OwnerSymbolID]
		if sym == nil {
			continue
		}
		// Find the owner pin
		for _, pin := range sym.Pins {
			if pin.PinNumber == pp.OwnerPinNum {
				pp.PinX = pin.X
				pp.PinY = pin.Y
				if pp.IsGround {
					pp.X = pin.X
					pp.Y = pin.Y + 40
				} else {
					pp.X = pin.X
					pp.Y = pin.Y - 40
				}
				pp.Sheet = effectiveSheet(sym.Sheet)
				break
			}
		}
	}
}
