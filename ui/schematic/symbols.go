package schematic

import (
	"math"

	"github.com/gotk3/gotk3/cairo"
)

// PinStub defines where a pin extends from the symbol body.
// Coordinates are relative to the symbol center (0,0).
type PinStub struct {
	BodyX     float64 // Where stub meets the body
	BodyY     float64
	TipX      float64 // Where wire connects (end of stub)
	TipY      float64
	Side      string // "left", "right", "top", "bottom"
	HasBubble bool   // Draw negation bubble at the body end (e.g. NAND/NOR outputs)
}

// SymbolDef defines the visual geometry of one gate type.
type SymbolDef struct {
	GateType    string
	BodyWidth   float64 // Width in schematic units
	BodyHeight  float64 // Height in schematic units
	InputStubs  []PinStub
	OutputStubs []PinStub
	EnableStubs []PinStub
	ClockStubs  []PinStub
	DrawBody    func(cr *cairo.Context, w, h float64)
}

const (
	stubLength  = 50.0 // Length of pin stub lines
	bubbleR     = 8.0  // Negation bubble radius
	clockWedgeH = 12.0 // Clock wedge height
	clockWedgeW = 10.0 // Clock wedge width
)

// GetSymbolDef returns the symbol definition for a gate type with the given pin counts.
func GetSymbolDef(gateType string, numInputs, numOutputs, numEnables, numClocks int) *SymbolDef {
	switch gateType {
	case "NOT":
		return notSymbol()
	case "BUFFER":
		return bufferSymbol()
	case "AND":
		return andSymbol(numInputs)
	case "NAND":
		return nandSymbol(numInputs)
	case "OR":
		return orSymbol(numInputs)
	case "NOR":
		return norSymbol(numInputs)
	case "XOR":
		return xorSymbol(numInputs)
	case "TRISTATE":
		return tristateSymbol()
	case "FLIPFLOP":
		return flipflopSymbol(numInputs, numOutputs, numEnables, numClocks)
	default:
		// LATCH, DECODER, MUX, COUNTER, SHIFTREG, RAM, BLOCK
		return blockSymbol(gateType, numInputs, numOutputs, numEnables, numClocks)
	}
}

// --- NOT gate: triangle + bubble ---

func notSymbol() *SymbolDef {
	w, h := 100.0, 80.0
	return &SymbolDef{
		GateType:  "NOT",
		BodyWidth: w, BodyHeight: h,
		InputStubs:  inputStubs(1, w, h),
		OutputStubs: outputStubsWithBubble(1, w, h),
		DrawBody:    drawTriangleBody,
	}
}

// --- BUFFER gate: triangle ---

func bufferSymbol() *SymbolDef {
	w, h := 100.0, 80.0
	return &SymbolDef{
		GateType:  "BUFFER",
		BodyWidth: w, BodyHeight: h,
		InputStubs:  inputStubs(1, w, h),
		OutputStubs: outputStubs(1, w, h),
		DrawBody:    drawTriangleBody,
	}
}

// --- AND gate: D-shape ---

func andSymbol(numInputs int) *SymbolDef {
	if numInputs < 2 {
		numInputs = 2
	}
	w, h := 150.0, pinSpacedHeight(numInputs)
	return &SymbolDef{
		GateType:  "AND",
		BodyWidth: w, BodyHeight: h,
		InputStubs:  inputStubs(numInputs, w, h),
		OutputStubs: outputStubs(1, w, h),
		DrawBody:    drawANDBody,
	}
}

func nandSymbol(numInputs int) *SymbolDef {
	if numInputs < 2 {
		numInputs = 2
	}
	w, h := 150.0, pinSpacedHeight(numInputs)
	return &SymbolDef{
		GateType:  "NAND",
		BodyWidth: w, BodyHeight: h,
		InputStubs:  inputStubs(numInputs, w, h),
		OutputStubs: outputStubsWithBubble(1, w, h),
		DrawBody:    drawANDBody,
	}
}

// --- OR gate: curved body ---

func orSymbol(numInputs int) *SymbolDef {
	if numInputs < 2 {
		numInputs = 2
	}
	w, h := 150.0, pinSpacedHeight(numInputs)
	return &SymbolDef{
		GateType:  "OR",
		BodyWidth: w, BodyHeight: h,
		InputStubs:  inputStubs(numInputs, w, h),
		OutputStubs: outputStubs(1, w, h),
		DrawBody:    drawORBody,
	}
}

func norSymbol(numInputs int) *SymbolDef {
	if numInputs < 2 {
		numInputs = 2
	}
	w, h := 150.0, pinSpacedHeight(numInputs)
	return &SymbolDef{
		GateType:  "NOR",
		BodyWidth: w, BodyHeight: h,
		InputStubs:  inputStubs(numInputs, w, h),
		OutputStubs: outputStubsWithBubble(1, w, h),
		DrawBody:    drawORBody,
	}
}

func xorSymbol(numInputs int) *SymbolDef {
	if numInputs < 2 {
		numInputs = 2
	}
	w, h := 150.0, pinSpacedHeight(numInputs)
	return &SymbolDef{
		GateType:  "XOR",
		BodyWidth: w, BodyHeight: h,
		InputStubs:  inputStubs(numInputs, w, h),
		OutputStubs: outputStubs(1, w, h),
		DrawBody:    drawXORBody,
	}
}

// --- TRISTATE: triangle + enable ---

func tristateSymbol() *SymbolDef {
	w, h := 100.0, 80.0
	return &SymbolDef{
		GateType:  "TRISTATE",
		BodyWidth: w, BodyHeight: h,
		InputStubs:  inputStubs(1, w, h),
		OutputStubs: outputStubs(1, w, h),
		EnableStubs: []PinStub{
			{BodyX: 0, BodyY: -h / 2, TipX: 0, TipY: -h/2 - stubLength, Side: "top"},
		},
		DrawBody: drawTriangleBody,
	}
}

// --- FLIPFLOP: rectangle with clock wedge ---

func flipflopSymbol(numInputs, numOutputs, numEnables, numClocks int) *SymbolDef {
	leftPins := numInputs + numClocks
	rightPins := numOutputs
	maxPins := leftPins
	if rightPins > maxPins {
		maxPins = rightPins
	}
	if numEnables > 0 {
		leftPins += numEnables // enables on left side too for flip-flops
	}
	if leftPins > maxPins {
		maxPins = leftPins
	}
	if maxPins < 2 {
		maxPins = 2
	}
	w, h := 200.0, pinSpacedHeight(maxPins)

	ins := inputStubs(numInputs, w, h)
	outs := outputStubs(numOutputs, w, h)

	// Clock stubs at bottom of left side
	var clocks []PinStub
	for i := 0; i < numClocks; i++ {
		y := h/2 - float64(i)*50 - 25
		clocks = append(clocks, PinStub{
			BodyX: -w / 2, BodyY: y,
			TipX: -w/2 - stubLength, TipY: y,
			Side: "left",
		})
	}

	// Enable stubs on top
	var enables []PinStub
	for i := 0; i < numEnables; i++ {
		x := -w/4 + float64(i)*50
		enables = append(enables, PinStub{
			BodyX: x, BodyY: -h / 2,
			TipX: x, TipY: -h/2 - stubLength,
			Side: "top",
		})
	}

	return &SymbolDef{
		GateType:    "FLIPFLOP",
		BodyWidth:   w,
		BodyHeight:  h,
		InputStubs:  ins,
		OutputStubs: outs,
		EnableStubs: enables,
		ClockStubs:  clocks,
		DrawBody:    drawRectBody,
	}
}

// --- BLOCK: generic rectangle with pin labels ---

func blockSymbol(gateType string, numInputs, numOutputs, numEnables, numClocks int) *SymbolDef {
	// Left side order matches BuildPinsFromFunction: inputs, clocks, enables.
	totalLeft := numInputs + numClocks + numEnables
	rightPins := numOutputs
	maxPins := totalLeft
	if rightPins > maxPins {
		maxPins = rightPins
	}
	if maxPins < 2 {
		maxPins = 2
	}
	w, h := 250.0, pinSpacedHeight(maxPins)
	if w < 200 {
		w = 200
	}

	// Build all left-side stubs in one pass, then split by direction bucket.
	allLeft := make([]PinStub, totalLeft)
	for i := 0; i < totalLeft; i++ {
		y := -h/2 + float64(i)*50 + 25
		if totalLeft == 1 {
			y = 0
		}
		allLeft[i] = PinStub{
			BodyX: -w / 2, BodyY: y,
			TipX: -w/2 - stubLength, TipY: y,
			Side: "left",
		}
	}

	// Slice the stubs to match the order ComputePinPositions expects:
	// InputStubs for "input", then ClockStubs for "clock", then EnableStubs for "enable".
	ins := allLeft[:numInputs]
	clks := allLeft[numInputs : numInputs+numClocks]
	ens := allLeft[numInputs+numClocks:]

	return &SymbolDef{
		GateType:    gateType,
		BodyWidth:   w,
		BodyHeight:  h,
		InputStubs:  ins,
		ClockStubs:  clks,
		EnableStubs: ens,
		OutputStubs: outputStubs(numOutputs, w, h),
		DrawBody:    drawRectBody,
	}
}

// --- Helper functions for pin stub layout ---

func pinSpacedHeight(n int) float64 {
	if n <= 1 {
		return 80
	}
	return float64(n)*50 + 10
}

// inputStubs creates evenly-spaced input stubs on the left side.
func inputStubs(n int, w, h float64) []PinStub {
	stubs := make([]PinStub, n)
	for i := 0; i < n; i++ {
		y := pinY(i, n, h)
		stubs[i] = PinStub{
			BodyX: -w / 2, BodyY: y,
			TipX: -w/2 - stubLength, TipY: y,
			Side: "left",
		}
	}
	return stubs
}

// outputStubs creates evenly-spaced output stubs on the right side.
func outputStubs(n int, w, h float64) []PinStub {
	stubs := make([]PinStub, n)
	for i := 0; i < n; i++ {
		y := pinY(i, n, h)
		stubs[i] = PinStub{
			BodyX: w / 2, BodyY: y,
			TipX: w/2 + stubLength, TipY: y,
			Side: "right",
		}
	}
	return stubs
}

// outputStubsWithBubble creates output stubs with negation bubble offset.
func outputStubsWithBubble(n int, w, h float64) []PinStub {
	stubs := make([]PinStub, n)
	for i := 0; i < n; i++ {
		y := pinY(i, n, h)
		stubs[i] = PinStub{
			BodyX: w/2 + bubbleR*2, BodyY: y,
			TipX: w/2 + bubbleR*2 + stubLength, TipY: y,
			Side:      "right",
			HasBubble: true,
		}
	}
	return stubs
}

// pinY returns the Y offset for pin index i of n pins, centered on the body.
func pinY(i, n int, h float64) float64 {
	if n == 1 {
		return 0
	}
	spacing := h / float64(n+1)
	return -h/2 + spacing*float64(i+1)
}

// --- Cairo drawing functions ---

// drawTriangleBody draws a triangle pointing right (for NOT, BUFFER, TRISTATE).
func drawTriangleBody(cr *cairo.Context, w, h float64) {
	cr.NewPath()
	cr.MoveTo(-w/2, -h/2) // top-left
	cr.LineTo(w/2, 0)     // right point
	cr.LineTo(-w/2, h/2)  // bottom-left
	cr.ClosePath()
	cr.SetSourceRGB(1, 1, 1) // white fill
	cr.FillPreserve()
	cr.SetSourceRGB(0, 0, 0) // black outline
	cr.SetLineWidth(2)
	cr.Stroke()
}

// drawANDBody draws a D-shape (flat left, semicircle right) for AND/NAND.
func drawANDBody(cr *cairo.Context, w, h float64) {
	// The flat left side and top/bottom connect to a semicircular right side
	midX := w/2 - h/2 // where the arc center is (may be left of w/2 for tall gates)
	if midX < 0 {
		midX = 0
	}
	r := h / 2

	cr.NewPath()
	cr.MoveTo(-w/2, -h/2) // top-left corner
	cr.LineTo(midX, -h/2) // top to arc start
	cr.Arc(midX, 0, r, -math.Pi/2, math.Pi/2)
	cr.LineTo(-w/2, h/2) // bottom back to left
	cr.ClosePath()
	cr.SetSourceRGB(1, 1, 1)
	cr.FillPreserve()
	cr.SetSourceRGB(0, 0, 0)
	cr.SetLineWidth(2)
	cr.Stroke()
}

// drawORBody draws a curved OR gate body using cubic Bezier curves.
func drawORBody(cr *cairo.Context, w, h float64) {
	cr.NewPath()
	// Start at top-left
	cr.MoveTo(-w/2, -h/2)
	// Top curve to output point (convex outward)
	cr.CurveTo(0, -h/2, w/4, -h/4, w/2, 0)
	// Bottom curve back (convex outward)
	cr.CurveTo(w/4, h/4, 0, h/2, -w/2, h/2)
	// Input side concave curve back to top
	cr.CurveTo(-w/4, h/4, -w/4, -h/4, -w/2, -h/2)
	cr.ClosePath()
	cr.SetSourceRGB(1, 1, 1)
	cr.FillPreserve()
	cr.SetSourceRGB(0, 0, 0)
	cr.SetLineWidth(2)
	cr.Stroke()
}

// drawXORBody draws an OR body with an extra input-side curve.
func drawXORBody(cr *cairo.Context, w, h float64) {
	// Draw the OR body first
	drawORBody(cr, w, h)
	// Draw extra curve offset 15 units to the left of the input side
	offset := 15.0
	cr.NewPath()
	cr.MoveTo(-w/2-offset, -h/2)
	cr.CurveTo(-w/4-offset, -h/4, -w/4-offset, h/4, -w/2-offset, h/2)
	cr.SetSourceRGB(0, 0, 0)
	cr.SetLineWidth(2)
	cr.Stroke()
}

// drawRectBody draws a rectangle (for FLIPFLOP, BLOCK, and complex ICs).
func drawRectBody(cr *cairo.Context, w, h float64) {
	cr.Rectangle(-w/2, -h/2, w, h)
	cr.SetSourceRGB(1, 1, 1)
	cr.FillPreserve()
	cr.SetSourceRGB(0, 0, 0)
	cr.SetLineWidth(2)
	cr.Stroke()
}

// DrawNegationBubble draws a small circle at the given position.
func DrawNegationBubble(cr *cairo.Context, x, y float64) {
	cr.NewPath()
	cr.Arc(x, y, bubbleR, 0, 2*math.Pi)
	cr.SetSourceRGB(1, 1, 1)
	cr.FillPreserve()
	cr.SetSourceRGB(0, 0, 0)
	cr.SetLineWidth(1.5)
	cr.Stroke()
}

// DrawClockWedge draws a small triangle at the given position on the left edge.
func DrawClockWedge(cr *cairo.Context, x, y float64) {
	cr.NewPath()
	cr.MoveTo(x, y-clockWedgeH/2)
	cr.LineTo(x+clockWedgeW, y)
	cr.LineTo(x, y+clockWedgeH/2)
	cr.SetSourceRGB(0, 0, 0)
	cr.SetLineWidth(1.5)
	cr.Stroke()
}
