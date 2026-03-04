package schematic

import (
	"math"

	"pcb-tracer/internal/component"
)

// ComputePinPositions calculates absolute schematic coordinates for all pins
// on a placed symbol, based on its position and the symbol definition.
func ComputePinPositions(sym *PlacedSymbol, def *SymbolDef) {
	if def == nil || sym == nil {
		return
	}

	// Assign stubs to pins by direction
	inputIdx := 0
	outputIdx := 0
	enableIdx := 0
	clockIdx := 0

	for _, pin := range sym.Pins {
		var stub *PinStub
		switch pin.Direction {
		case "input":
			if inputIdx < len(def.InputStubs) {
				stub = &def.InputStubs[inputIdx]
				inputIdx++
			}
		case "output":
			if outputIdx < len(def.OutputStubs) {
				stub = &def.OutputStubs[outputIdx]
				outputIdx++
			}
		case "enable":
			if enableIdx < len(def.EnableStubs) {
				stub = &def.EnableStubs[enableIdx]
				enableIdx++
			}
		case "clock":
			if clockIdx < len(def.ClockStubs) {
				stub = &def.ClockStubs[clockIdx]
				clockIdx++
			}
		}

		if stub != nil {
			tipX, tipY := stub.TipX, stub.TipY
			bodyX, bodyY := stub.BodyX, stub.BodyY
			if sym.FlipH {
				tipX = -tipX
				bodyX = -bodyX
			}
			if sym.FlipV {
				tipY = -tipY
				bodyY = -bodyY
			}
			// Apply rotation
			if sym.Rotation != 0 {
				tipX, tipY = rotatePoint(tipX, tipY, sym.Rotation)
				bodyX, bodyY = rotatePoint(bodyX, bodyY, sym.Rotation)
			}
			pin.X = sym.X + tipX
			pin.Y = sym.Y + tipY
			pin.StubX = sym.X + bodyX
			pin.StubY = sym.Y + bodyY
		}
	}
}

// rotatePoint rotates (x, y) by the given degrees (0, 90, 180, 270) around the origin.
func rotatePoint(x, y float64, degrees int) (float64, float64) {
	rad := float64(degrees) * math.Pi / 180.0
	cos := math.Cos(rad)
	sin := math.Sin(rad)
	rx := x*cos - y*sin
	ry := x*sin + y*cos
	// Snap to avoid floating-point drift on exact 90° multiples
	rx = math.Round(rx*1000) / 1000
	ry = math.Round(ry*1000) / 1000
	return rx, ry
}

// BuildPinsFromFunction creates SchematicPin entries for a logic function,
// using the part definition to look up pin names and signal directions.
func BuildPinsFromFunction(fn *component.LogicFunction, partDef *component.PartDefinition) []*SchematicPin {
	var pins []*SchematicPin

	// Helper to find pin name from part definition
	pinName := func(pinNum int) string {
		for _, p := range partDef.Pins {
			if p.Number == pinNum {
				return p.Name
			}
		}
		return ""
	}

	// Input pins
	for _, pn := range fn.Inputs {
		name := pinName(pn)
		negated := len(name) > 0 && name[0] == '/'
		pins = append(pins, &SchematicPin{
			PinNumber: pn,
			Name:      name,
			Direction: "input",
			Negated:   negated,
		})
	}

	// Clock pins
	for _, pn := range fn.Clocks {
		pins = append(pins, &SchematicPin{
			PinNumber: pn,
			Name:      pinName(pn),
			Direction: "clock",
			Clock:     true,
		})
	}

	// Enable pins
	for _, pn := range fn.Enables {
		name := pinName(pn)
		negated := len(name) > 0 && name[0] == '/'
		pins = append(pins, &SchematicPin{
			PinNumber: pn,
			Name:      name,
			Direction: "enable",
			Negated:   negated,
		})
	}

	// Output pins
	for _, pn := range fn.Outputs {
		name := pinName(pn)
		negated := len(name) > 0 && (name[0] == '/' || name[0] == '~')
		pins = append(pins, &SchematicPin{
			PinNumber: pn,
			Name:      name,
			Direction: "output",
			Negated:   negated,
		})
	}

	return pins
}
