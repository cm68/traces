package schematic

import (
	"fmt"
	"math"

	"github.com/gotk3/gotk3/cairo"
)

// render draws the entire schematic to the Cairo context.
func (sc *SchematicCanvas) render(cr *cairo.Context, w, h int) {
	// 1. White background
	cr.SetSourceRGB(1, 1, 1)
	cr.Rectangle(0, 0, float64(w), float64(h))
	cr.Fill()

	if sc.doc == nil {
		return
	}

	// 2. Apply view transform: translate origin, then scale
	cr.Save()
	cr.Scale(sc.zoom, sc.zoom)
	cr.Translate(-sc.originX, -sc.originY)

	// 3. Draw grid
	sc.drawGrid(cr, w, h)

	// 4. Draw wires (behind symbols)
	for _, wire := range sc.doc.Wires {
		sc.drawWire(cr, wire)
	}

	// 5. Draw symbols
	for _, sym := range sc.doc.Symbols {
		sc.drawSymbol(cr, sym)
	}

	// 6. Draw net labels
	for _, label := range sc.doc.NetLabels {
		sc.drawNetLabel(cr, label)
	}

	// 7. Draw power ports
	for _, pp := range sc.doc.PowerPorts {
		sc.drawPowerPort(cr, pp)
	}

	cr.Restore()
}

// drawGrid draws a light dot grid.
func (sc *SchematicCanvas) drawGrid(cr *cairo.Context, w, h int) {
	// Only draw grid if zoom is large enough to see it
	if sc.zoom < 0.3 {
		return
	}

	cr.SetSourceRGB(0.8, 0.8, 0.8)
	gridSize := 50.0

	minX, minY, maxX, maxY := sc.doc.Bounds()
	for x := math.Floor(minX/gridSize) * gridSize; x <= maxX; x += gridSize {
		for y := math.Floor(minY/gridSize) * gridSize; y <= maxY; y += gridSize {
			cr.Rectangle(x-0.5, y-0.5, 1, 1)
			cr.Fill()
		}
	}
}

// drawWire draws a wire with Manhattan routing.
func (sc *SchematicCanvas) drawWire(cr *cairo.Context, wire *Wire) {
	if len(wire.Points) < 2 {
		return
	}

	lineW := 2.0
	if wire.IsBus {
		lineW = 4.0
	}

	if wire.Selected {
		cr.SetSourceRGB(1, 0, 0) // red for selected
	} else {
		cr.SetSourceRGB(0, 0, 0.5) // dark blue
	}
	cr.SetLineWidth(lineW)

	cr.MoveTo(wire.Points[0].X, wire.Points[0].Y)
	for _, p := range wire.Points[1:] {
		cr.LineTo(p.X, p.Y)
	}
	cr.Stroke()

	// Draw junction dots where this wire bends (internal waypoints)
	if len(wire.Points) > 2 {
		if wire.Selected {
			cr.SetSourceRGB(1, 0, 0)
		} else {
			cr.SetSourceRGB(0, 0, 0.5)
		}
		for _, p := range wire.Points[1 : len(wire.Points)-1] {
			cr.Arc(p.X, p.Y, 3, 0, 2*math.Pi)
			cr.Fill()
		}
	}
}

// drawSymbol draws a placed symbol with body, pins, labels.
func (sc *SchematicCanvas) drawSymbol(cr *cairo.Context, sym *PlacedSymbol) {
	def := GetSymbolDef(sym.GateType,
		countPinsByDir(sym, "input"),
		countPinsByDir(sym, "output"),
		countPinsByDir(sym, "enable"),
		countPinsByDir(sym, "clock"))
	if def == nil {
		return
	}

	// Selection highlight
	if sym.Selected {
		cr.Save()
		hw := def.BodyWidth/2 + 10
		hh := def.BodyHeight/2 + 10
		// Use the larger dimension for both when rotated 90/270
		if sym.Rotation == 90 || sym.Rotation == 270 {
			hw, hh = hh, hw
		}
		cr.Rectangle(sym.X-hw, sym.Y-hh, hw*2, hh*2)
		cr.SetSourceRGBA(1.0, 0.85, 0.85, 0.3)
		cr.Fill()
		cr.Restore()
	}

	// Draw body (translated to symbol center, with optional flip and rotation)
	cr.Save()
	cr.Translate(sym.X, sym.Y)
	if sym.Rotation != 0 {
		cr.Rotate(float64(sym.Rotation) * math.Pi / 180.0)
	}
	if sym.FlipH {
		cr.Scale(-1, 1)
	}
	if sym.FlipV {
		cr.Scale(1, -1)
	}
	def.DrawBody(cr, def.BodyWidth, def.BodyHeight)
	cr.Restore()

	// Draw pin stubs and decorations
	cr.SetSourceRGB(0, 0, 0)
	cr.SetLineWidth(2)
	for _, pin := range sym.Pins {
		// Stub line from body to tip
		cr.MoveTo(pin.StubX, pin.StubY)
		cr.LineTo(pin.X, pin.Y)
		cr.Stroke()

		// Negation bubble
		if pin.Negated {
			// Bubble is between body and stub
			bx := pin.StubX
			by := pin.StubY
			if pin.Direction == "output" {
				bx = pin.StubX - bubbleR
			} else {
				bx = pin.StubX + bubbleR
			}
			DrawNegationBubble(cr, bx, by)
		}

		// Clock wedge
		if pin.Clock {
			DrawClockWedge(cr, pin.StubX, pin.StubY)
		}

		// Pin name label
		if pin.Name != "" {
			cr.Save()
			cr.SetSourceRGB(0.4, 0.4, 0.4) // gray
			cr.SelectFontFace("sans-serif", cairo.FONT_SLANT_NORMAL, cairo.FONT_WEIGHT_NORMAL)
			cr.SetFontSize(14)
			extents := cr.TextExtents(pin.Name)

			switch pin.Direction {
			case "input", "clock", "enable":
				// Label to the right of the pin tip (inside body area)
				cr.MoveTo(pin.StubX+4, pin.StubY+extents.Height/2)
			case "output":
				// Label to the left of the pin tip (inside body area)
				cr.MoveTo(pin.StubX-extents.Width-4, pin.StubY+extents.Height/2)
			default:
				cr.MoveTo(pin.X+4, pin.Y+extents.Height/2)
			}
			cr.ShowText(pin.Name)
			cr.Restore()
		}

		// Pin number label (small, near tip)
		if pin.PinNumber > 0 {
			cr.Save()
			cr.SetSourceRGB(0.6, 0.6, 0.6)
			cr.SelectFontFace("sans-serif", cairo.FONT_SLANT_NORMAL, cairo.FONT_WEIGHT_NORMAL)
			cr.SetFontSize(10)
			numStr := fmt.Sprintf("%d", pin.PinNumber)
			switch pin.Direction {
			case "input", "clock", "enable":
				cr.MoveTo(pin.X-4, pin.Y-6)
			default:
				cr.MoveTo(pin.X+4, pin.Y-6)
			}
			cr.ShowText(numStr)
			cr.Restore()
		}
	}

	// Component reference label above symbol (e.g., "U3-1")
	cr.Save()
	cr.SetSourceRGB(0.8, 0, 0) // red
	cr.SelectFontFace("sans-serif", cairo.FONT_SLANT_NORMAL, cairo.FONT_WEIGHT_BOLD)
	cr.SetFontSize(18)
	refLabel := sym.ID
	extents := cr.TextExtents(refLabel)
	cr.MoveTo(sym.X-extents.Width/2, sym.Y-def.BodyHeight/2-8)
	cr.ShowText(refLabel)
	cr.Restore()

	// Part number label below symbol
	if sym.PartNumber != "" {
		cr.Save()
		cr.SetSourceRGB(0.4, 0.4, 0.4)
		cr.SelectFontFace("sans-serif", cairo.FONT_SLANT_NORMAL, cairo.FONT_WEIGHT_NORMAL)
		cr.SetFontSize(14)
		extents := cr.TextExtents(sym.PartNumber)
		cr.MoveTo(sym.X-extents.Width/2, sym.Y+def.BodyHeight/2+18)
		cr.ShowText(sym.PartNumber)
		cr.Restore()
	}
}

// drawNetLabel draws a signal name label at its position.
func (sc *SchematicCanvas) drawNetLabel(cr *cairo.Context, label *NetLabel) {
	cr.Save()
	cr.SetSourceRGB(0, 0.4, 0) // dark green
	cr.SelectFontFace("sans-serif", cairo.FONT_SLANT_NORMAL, cairo.FONT_WEIGHT_BOLD)
	cr.SetFontSize(16)
	cr.MoveTo(label.X, label.Y)
	cr.ShowText(label.NetName)
	cr.Restore()
}

// drawPowerPort draws a VCC or GND symbol.
func (sc *SchematicCanvas) drawPowerPort(cr *cairo.Context, pp *PowerPort) {
	cr.Save()
	cr.SetLineWidth(2)

	if pp.IsGround {
		// GND: horizontal line + three decreasing lines below
		cr.SetSourceRGB(0, 0, 0)
		// Vertical stub from pin
		cr.MoveTo(pp.PinX, pp.PinY)
		cr.LineTo(pp.X, pp.Y)
		cr.Stroke()
		// Three horizontal lines
		for i := 0; i < 3; i++ {
			w := 20.0 - float64(i)*6
			y := pp.Y + float64(i)*6
			cr.MoveTo(pp.X-w, y)
			cr.LineTo(pp.X+w, y)
			cr.Stroke()
		}
		// Label
		cr.SelectFontFace("sans-serif", cairo.FONT_SLANT_NORMAL, cairo.FONT_WEIGHT_BOLD)
		cr.SetFontSize(14)
		extents := cr.TextExtents(pp.NetName)
		cr.MoveTo(pp.X-extents.Width/2, pp.Y+24)
		cr.ShowText(pp.NetName)
	} else {
		// VCC: vertical stub + upward-pointing bar or circle
		cr.SetSourceRGB(0.8, 0, 0)
		// Vertical stub from pin
		cr.MoveTo(pp.PinX, pp.PinY)
		cr.LineTo(pp.X, pp.Y)
		cr.Stroke()
		// Horizontal bar at top
		cr.MoveTo(pp.X-15, pp.Y)
		cr.LineTo(pp.X+15, pp.Y)
		cr.Stroke()
		// Label
		cr.SelectFontFace("sans-serif", cairo.FONT_SLANT_NORMAL, cairo.FONT_WEIGHT_BOLD)
		cr.SetFontSize(14)
		extents := cr.TextExtents(pp.NetName)
		cr.MoveTo(pp.X-extents.Width/2, pp.Y-8)
		cr.ShowText(pp.NetName)
	}

	cr.Restore()
}
