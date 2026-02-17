// Package canvas provides drawing primitives for the image canvas.
package canvas

import (
	"image"
	"image/color"

	pcbimage "pcb-tracer/internal/image"
	"pcb-tracer/pkg/colorutil"
	"pcb-tracer/pkg/geometry"
)

// digitPatterns contains 3x5 pixel patterns for digits 0-9.
// Each digit is represented as 5 rows of 3 bits.
var digitPatterns = [10][5]uint8{
	{0b111, 0b101, 0b101, 0b101, 0b111}, // 0
	{0b010, 0b110, 0b010, 0b010, 0b111}, // 1
	{0b111, 0b001, 0b111, 0b100, 0b111}, // 2
	{0b111, 0b001, 0b111, 0b001, 0b111}, // 3
	{0b101, 0b101, 0b111, 0b001, 0b001}, // 4
	{0b111, 0b100, 0b111, 0b001, 0b111}, // 5
	{0b111, 0b100, 0b111, 0b101, 0b111}, // 6
	{0b111, 0b001, 0b001, 0b001, 0b001}, // 7
	{0b111, 0b101, 0b111, 0b101, 0b111}, // 8
	{0b111, 0b101, 0b111, 0b001, 0b111}, // 9
}

// letterPatterns contains 3x5 pixel patterns for letters A-Z and common symbols.
// Each letter is represented as 5 rows of 3 bits.
var letterPatterns = map[rune][5]uint8{
	'A': {0b010, 0b101, 0b111, 0b101, 0b101},
	'B': {0b110, 0b101, 0b110, 0b101, 0b110},
	'C': {0b011, 0b100, 0b100, 0b100, 0b011},
	'D': {0b110, 0b101, 0b101, 0b101, 0b110},
	'E': {0b111, 0b100, 0b110, 0b100, 0b111},
	'F': {0b111, 0b100, 0b110, 0b100, 0b100},
	'G': {0b011, 0b100, 0b101, 0b101, 0b011},
	'H': {0b101, 0b101, 0b111, 0b101, 0b101},
	'I': {0b111, 0b010, 0b010, 0b010, 0b111},
	'J': {0b001, 0b001, 0b001, 0b101, 0b010},
	'K': {0b101, 0b101, 0b110, 0b101, 0b101},
	'L': {0b100, 0b100, 0b100, 0b100, 0b111},
	'M': {0b101, 0b111, 0b101, 0b101, 0b101},
	'N': {0b101, 0b111, 0b111, 0b101, 0b101},
	'O': {0b010, 0b101, 0b101, 0b101, 0b010},
	'P': {0b110, 0b101, 0b110, 0b100, 0b100},
	'Q': {0b010, 0b101, 0b101, 0b111, 0b011},
	'R': {0b110, 0b101, 0b110, 0b101, 0b101},
	'S': {0b011, 0b100, 0b010, 0b001, 0b110},
	'T': {0b111, 0b010, 0b010, 0b010, 0b010},
	'U': {0b101, 0b101, 0b101, 0b101, 0b111},
	'V': {0b101, 0b101, 0b101, 0b101, 0b010},
	'W': {0b101, 0b101, 0b101, 0b111, 0b101},
	'X': {0b101, 0b101, 0b010, 0b101, 0b101},
	'Y': {0b101, 0b101, 0b010, 0b010, 0b010},
	'Z': {0b111, 0b001, 0b010, 0b100, 0b111},
	'+': {0b000, 0b010, 0b111, 0b010, 0b000},
	'-': {0b000, 0b000, 0b111, 0b000, 0b000},
	'*': {0b000, 0b101, 0b010, 0b101, 0b000},
	' ': {0b000, 0b000, 0b000, 0b000, 0b000},
}

// getCharPattern returns the 3x5 pixel pattern for a character.
// Returns a zero pattern for unsupported characters.
func getCharPattern(ch rune) [5]uint8 {
	if ch >= '0' && ch <= '9' {
		return digitPatterns[ch-'0']
	}
	// Convert lowercase to uppercase
	if ch >= 'a' && ch <= 'z' {
		ch = ch - 'a' + 'A'
	}
	if pattern, ok := letterPatterns[ch]; ok {
		return pattern
	}
	return [5]uint8{} // Empty pattern for unsupported characters
}

// drawOverlay draws an overlay on the output image.
func (ic *ImageCanvas) drawOverlay(output *image.RGBA, overlay *Overlay) {
	col := overlay.Color

	// Get layer offset if overlay is associated with a non-normalized layer.
	// Normalized layers have all transforms baked in, so overlay coordinates
	// are already in the correct image space — no offset adjustment needed.
	var offsetX, offsetY float64
	if overlay.Layer != LayerNone {
		for _, layer := range ic.layers {
			if overlay.Layer == LayerFront && layer.Side == pcbimage.SideFront {
				if !layer.IsNormalized {
					offsetX = float64(layer.ManualOffsetX)
					offsetY = float64(layer.ManualOffsetY)
				}
				break
			} else if overlay.Layer == LayerBack && layer.Side == pcbimage.SideBack {
				if !layer.IsNormalized {
					offsetX = float64(layer.ManualOffsetX)
					offsetY = float64(layer.ManualOffsetY)
				}
				break
			}
		}
	}

	for _, rect := range overlay.Rectangles {
		// Scale rectangle coordinates by zoom, applying layer offset
		x1 := int((float64(rect.X) + offsetX) * ic.zoom)
		y1 := int((float64(rect.Y) + offsetY) * ic.zoom)
		x2 := int((float64(rect.X+rect.Width) + offsetX) * ic.zoom)
		y2 := int((float64(rect.Y+rect.Height) + offsetY) * ic.zoom)

		bounds := output.Bounds()

		// Draw fill pattern first (before outline)
		if rect.Fill != FillNone {
			interval := rect.StripeInterval
			if interval <= 0 {
				interval = rect.Width // Default to contact width
			}
			// Scale interval by zoom
			interval = int(float64(interval) * ic.zoom)
			if interval < 2 {
				interval = 2
			}

			ic.drawFillPattern(output, x1, y1, x2, y2, col, rect.Fill, interval)
		}

		// Draw rectangle outline (2 pixel thick)
		for t := 0; t < 2; t++ {
			// Top edge
			for x := x1; x <= x2; x++ {
				if x >= bounds.Min.X && x < bounds.Max.X && y1+t >= bounds.Min.Y && y1+t < bounds.Max.Y {
					output.Set(x, y1+t, col)
				}
			}
			// Bottom edge
			for x := x1; x <= x2; x++ {
				if x >= bounds.Min.X && x < bounds.Max.X && y2-t >= bounds.Min.Y && y2-t < bounds.Max.Y {
					output.Set(x, y2-t, col)
				}
			}
			// Left edge
			for y := y1; y <= y2; y++ {
				if x1+t >= bounds.Min.X && x1+t < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
					output.Set(x1+t, y, col)
				}
			}
			// Right edge
			for y := y1; y <= y2; y++ {
				if x2-t >= bounds.Min.X && x2-t < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
					output.Set(x2-t, y, col)
				}
			}
		}

		// Draw label if present
		if rect.Label != "" {
			centerX := (x1 + x2) / 2
			centerY := (y1 + y2) / 2
			ic.drawOverlayLabel(output, rect.Label, centerX, centerY)
		}
	}

	// Draw polygons
	for _, poly := range overlay.Polygons {
		if len(poly.Points) < 3 {
			continue
		}
		ic.drawPolygon(output, poly, col, offsetX, offsetY)
	}

	// Draw circles
	for _, circle := range overlay.Circles {
		ic.drawCircle(output, circle, col, offsetX, offsetY)
	}

	// Draw lines
	for _, line := range overlay.Lines {
		x1 := int((line.X1 + offsetX) * ic.zoom)
		y1 := int((line.Y1 + offsetY) * ic.zoom)
		x2 := int((line.X2 + offsetX) * ic.zoom)
		y2 := int((line.Y2 + offsetY) * ic.zoom)
		thickness := line.Thickness
		if thickness <= 0 {
			thickness = 2
		}
		ic.drawLine(output, x1, y1, x2, y2, col, thickness)
	}
}

// drawSelectionRect draws a selection rectangle with a distinctive pattern.
func (ic *ImageCanvas) drawSelectionRect(output *image.RGBA, rect *OverlayRect) {
	// Use yellow for selection
	col := color.RGBA{R: 255, G: 255, B: 0, A: 255}

	// rect is already in canvas coordinates
	x1 := rect.X
	y1 := rect.Y
	x2 := rect.X + rect.Width
	y2 := rect.Y + rect.Height

	bounds := output.Bounds()

	// Draw dashed rectangle outline (alternate pixels)
	// Top edge
	for x := x1; x <= x2; x++ {
		if (x+y1)%4 < 2 && x >= bounds.Min.X && x < bounds.Max.X && y1 >= bounds.Min.Y && y1 < bounds.Max.Y {
			output.Set(x, y1, col)
		}
	}
	// Bottom edge
	for x := x1; x <= x2; x++ {
		if (x+y2)%4 < 2 && x >= bounds.Min.X && x < bounds.Max.X && y2 >= bounds.Min.Y && y2 < bounds.Max.Y {
			output.Set(x, y2, col)
		}
	}
	// Left edge
	for y := y1; y <= y2; y++ {
		if (x1+y)%4 < 2 && x1 >= bounds.Min.X && x1 < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
			output.Set(x1, y, col)
		}
	}
	// Right edge
	for y := y1; y <= y2; y++ {
		if (x2+y)%4 < 2 && x2 >= bounds.Min.X && x2 < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
			output.Set(x2, y, col)
		}
	}
}

// drawPolygon draws a filled or outlined polygon on the output image.
func (ic *ImageCanvas) drawPolygon(output *image.RGBA, poly OverlayPolygon, col color.RGBA, offsetX, offsetY float64) {
	if len(poly.Points) < 3 {
		return
	}

	bounds := output.Bounds()

	// Scale points by zoom, applying offset
	scaledPoints := make([]geometry.Point2D, len(poly.Points))
	var minX, minY, maxX, maxY float64
	minX, minY = (poly.Points[0].X+offsetX)*ic.zoom, (poly.Points[0].Y+offsetY)*ic.zoom
	maxX, maxY = minX, minY

	for i, p := range poly.Points {
		scaledPoints[i] = geometry.Point2D{X: (p.X + offsetX) * ic.zoom, Y: (p.Y + offsetY) * ic.zoom}
		if scaledPoints[i].X < minX {
			minX = scaledPoints[i].X
		}
		if scaledPoints[i].X > maxX {
			maxX = scaledPoints[i].X
		}
		if scaledPoints[i].Y < minY {
			minY = scaledPoints[i].Y
		}
		if scaledPoints[i].Y > maxY {
			maxY = scaledPoints[i].Y
		}
	}

	if poly.Filled {
		// Fill polygon using scanline algorithm
		for y := int(minY); y <= int(maxY); y++ {
			if y < bounds.Min.Y || y >= bounds.Max.Y {
				continue
			}

			// Find all x intersections with polygon edges at this y
			var xIntersections []float64
			n := len(scaledPoints)
			for i := 0; i < n; i++ {
				p1 := scaledPoints[i]
				p2 := scaledPoints[(i+1)%n]

				// Check if edge crosses this scanline
				if (p1.Y <= float64(y) && p2.Y > float64(y)) ||
					(p2.Y <= float64(y) && p1.Y > float64(y)) {
					// Calculate x intersection
					t := (float64(y) - p1.Y) / (p2.Y - p1.Y)
					xInt := p1.X + t*(p2.X-p1.X)
					xIntersections = append(xIntersections, xInt)
				}
			}

			// Sort intersections
			for i := 0; i < len(xIntersections)-1; i++ {
				for j := i + 1; j < len(xIntersections); j++ {
					if xIntersections[j] < xIntersections[i] {
						xIntersections[i], xIntersections[j] = xIntersections[j], xIntersections[i]
					}
				}
			}

			// Fill between pairs of intersections
			for i := 0; i+1 < len(xIntersections); i += 2 {
				x1 := int(xIntersections[i])
				x2 := int(xIntersections[i+1])
				for x := x1; x <= x2; x++ {
					if x >= bounds.Min.X && x < bounds.Max.X {
						output.Set(x, y, col)
					}
				}
			}
		}
	}

	// Draw polygon outline (always, thicker for filled)
	thickness := 2
	if poly.Filled {
		thickness = 3
	}
	n := len(scaledPoints)
	for i := 0; i < n; i++ {
		p1 := scaledPoints[i]
		p2 := scaledPoints[(i+1)%n]
		ic.drawLine(output, int(p1.X), int(p1.Y), int(p2.X), int(p2.Y), col, thickness)
	}

	// Draw label if present
	if poly.Label != "" {
		center := geometry.Centroid(scaledPoints)
		ic.drawOverlayLabel(output, poly.Label, int(center.X), int(center.Y))
	}
}

// drawCircle draws a filled or outlined circle on the output image.
func (ic *ImageCanvas) drawCircle(output *image.RGBA, circle OverlayCircle, col color.RGBA, offsetX, offsetY float64) {
	bounds := output.Bounds()

	// Scale by zoom, applying offset
	cx := (circle.X + offsetX) * ic.zoom
	cy := (circle.Y + offsetY) * ic.zoom
	r := circle.Radius * ic.zoom

	// Integer bounds for iteration
	minX := int(cx - r - 1)
	maxX := int(cx + r + 1)
	minY := int(cy - r - 1)
	maxY := int(cy + r + 1)

	r2 := r * r
	innerR2 := (r - 2) * (r - 2) // 2 pixel outline thickness

	for y := minY; y <= maxY; y++ {
		if y < bounds.Min.Y || y >= bounds.Max.Y {
			continue
		}
		for x := minX; x <= maxX; x++ {
			if x < bounds.Min.X || x >= bounds.Max.X {
				continue
			}
			// Distance from center squared
			dx := float64(x) - cx
			dy := float64(y) - cy
			dist2 := dx*dx + dy*dy

			if circle.Filled {
				// Fill entire circle
				if dist2 <= r2 {
					output.Set(x, y, col)
				}
			} else {
				// Draw outline only (ring between innerR and r)
				if dist2 <= r2 && dist2 >= innerR2 {
					output.Set(x, y, col)
				}
			}
		}
	}
}

// drawOverlayLabel draws a black label centered at the given coordinates.
// This is the single labeling function used by all overlay shapes.
func (ic *ImageCanvas) drawOverlayLabel(output *image.RGBA, label string, centerX, centerY int) {
	// Pass center point directly - drawLabel calculates its own centering
	ic.drawLabel(output, label, centerX, centerY, centerX, centerY, colorutil.Black)
}

// drawLine draws a line between two points using Bresenham's algorithm.
func (ic *ImageCanvas) drawLine(output *image.RGBA, x1, y1, x2, y2 int, col color.RGBA, thickness int) {
	bounds := output.Bounds()

	dx := x2 - x1
	dy := y2 - y1
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}

	sx := 1
	if x1 > x2 {
		sx = -1
	}
	sy := 1
	if y1 > y2 {
		sy = -1
	}

	err := dx - dy

	for {
		// Draw thick point
		for t := -thickness / 2; t <= thickness/2; t++ {
			for s := -thickness / 2; s <= thickness/2; s++ {
				px, py := x1+s, y1+t
				if px >= bounds.Min.X && px < bounds.Max.X && py >= bounds.Min.Y && py < bounds.Max.Y {
					output.Set(px, py, col)
				}
			}
		}

		if x1 == x2 && y1 == y2 {
			break
		}

		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x1 += sx
		}
		if e2 < dx {
			err += dx
			y1 += sy
		}
	}
}

// drawFillPattern fills a rectangle with the specified pattern.
func (ic *ImageCanvas) drawFillPattern(output *image.RGBA, x1, y1, x2, y2 int, col color.RGBA, pattern FillPattern, interval int) {
	bounds := output.Bounds()

	switch pattern {
	case FillSolid:
		// Fill entire rectangle
		for y := y1; y <= y2; y++ {
			for x := x1; x <= x2; x++ {
				if x >= bounds.Min.X && x < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
					output.Set(x, y, col)
				}
			}
		}

	case FillStripe:
		// Diagonal stripes (top-left to bottom-right)
		// A pixel is on the stripe if (x + y) mod interval < lineWidth
		lineWidth := interval / 4
		if lineWidth < 1 {
			lineWidth = 1
		}
		for y := y1; y <= y2; y++ {
			for x := x1; x <= x2; x++ {
				if ((x + y) % interval) < lineWidth {
					if x >= bounds.Min.X && x < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
						output.Set(x, y, col)
					}
				}
			}
		}

	case FillCrosshatch:
		// Diagonal crosshatch (both directions)
		lineWidth := interval / 4
		if lineWidth < 1 {
			lineWidth = 1
		}
		for y := y1; y <= y2; y++ {
			for x := x1; x <= x2; x++ {
				// Stripe in one direction OR stripe in other direction
				stripe1 := ((x + y) % interval) < lineWidth
				stripe2 := ((x - y + 10000*interval) % interval) < lineWidth // +10000*interval to keep positive
				if stripe1 || stripe2 {
					if x >= bounds.Min.X && x < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
						output.Set(x, y, col)
					}
				}
			}
		}

	case FillTarget:
		// Crosshairs through center (target marker)
		centerX := (x1 + x2) / 2
		centerY := (y1 + y2) / 2
		lineWidth := 2
		if ic.zoom > 1 {
			lineWidth = int(2 * ic.zoom)
		}

		// Horizontal line through center
		for x := x1; x <= x2; x++ {
			for t := -lineWidth / 2; t <= lineWidth/2; t++ {
				py := centerY + t
				if x >= bounds.Min.X && x < bounds.Max.X && py >= bounds.Min.Y && py < bounds.Max.Y {
					output.Set(x, py, col)
				}
			}
		}

		// Vertical line through center
		for y := y1; y <= y2; y++ {
			for t := -lineWidth / 2; t <= lineWidth/2; t++ {
				px := centerX + t
				if px >= bounds.Min.X && px < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
					output.Set(px, y, col)
				}
			}
		}
	}
}

// drawLabel draws a centered label inside a rectangle.
func (ic *ImageCanvas) drawLabel(output *image.RGBA, label string, x1, y1, x2, y2 int, col color.RGBA) {
	// Calculate scale based on zoom (base scale is 2 pixels per font pixel at zoom 1.0)
	scale := int(ic.zoom * 2)
	if scale < 1 {
		scale = 1
	}
	if scale > 6 {
		scale = 6
	}

	// Calculate total width of label (3 pixels per digit + 1 pixel spacing)
	charWidth := 3 * scale
	charHeight := 5 * scale
	spacing := scale
	labelWidth := len(label)*charWidth + (len(label)-1)*spacing

	// Calculate center position
	centerX := (x1 + x2) / 2
	centerY := (y1 + y2) / 2

	// Start position for first character
	startX := centerX - labelWidth/2
	startY := centerY - charHeight/2

	bounds := output.Bounds()

	// Draw each character
	for i, ch := range label {
		if ch < '0' || ch > '9' {
			continue
		}
		digit := int(ch - '0')
		pattern := digitPatterns[digit]

		charX := startX + i*(charWidth+spacing)

		// Draw the digit pattern
		for row := 0; row < 5; row++ {
			for c := 0; c < 3; c++ {
				if (pattern[row] & (1 << (2 - c))) != 0 {
					// Draw a scaled pixel block
					for dy := 0; dy < scale; dy++ {
						for dx := 0; dx < scale; dx++ {
							px := charX + c*scale + dx
							py := startY + row*scale + dy
							if px >= bounds.Min.X && px < bounds.Max.X &&
								py >= bounds.Min.Y && py < bounds.Max.Y {
								output.Set(px, py, col)
							}
						}
					}
				}
			}
		}
	}
}

// DrawRotatedLabel draws a label rotated -90 degrees (text reads top-to-bottom).
// The label is drawn at the specified image coordinates (not zoomed).
// This function is intended for drawing onto layer images before compositing.
func DrawRotatedLabel(output *image.RGBA, label string, centerX, centerY int, col color.RGBA, scale int) {
	if scale < 1 {
		scale = 1
	}
	if scale > 6 {
		scale = 6
	}

	// For -90 degree rotation (counterclockwise):
	// - Original character: 3 wide x 5 tall
	// - Rotated character: 5 wide x 3 tall
	charWidth := 5 * scale  // Rotated: original height becomes width
	charHeight := 3 * scale // Rotated: original width becomes height
	spacing := scale

	// Total height of rotated label (characters stacked vertically)
	labelHeight := len(label)*charHeight + (len(label)-1)*spacing

	// Start position for first character at top, drawing downward
	startX := centerX - charWidth/2
	startY := centerY - labelHeight/2

	bounds := output.Bounds()

	// Draw each character (reverse order so text reads correctly top-to-bottom after -90 rotation)
	runes := []rune(label)
	for i := 0; i < len(runes); i++ {
		ch := runes[len(runes)-1-i] // Reverse: last char at top
		pattern := getCharPattern(ch)

		// Character Y position (moving downward for each character)
		charY := startY + i*(charHeight+spacing)

		// Draw the character pattern rotated -90 degrees (counterclockwise)
		// Original pattern: row 0-4 (top to bottom), col 0-2 (left to right)
		// After -90 rotation: top->left, bottom->right, left->bottom, right->top
		// Mapping: (col, row) -> (row, 2-col)
		for row := 0; row < 5; row++ {
			for c := 0; c < 3; c++ {
				if (pattern[row] & (1 << (2 - c))) != 0 {
					// px = row (0-4), py = 2-c (0-2)
					for dy := 0; dy < scale; dy++ {
						for dx := 0; dx < scale; dx++ {
							px := startX + row*scale + dx
							py := charY + (2-c)*scale + dy
							if px >= bounds.Min.X && px < bounds.Max.X &&
								py >= bounds.Min.Y && py < bounds.Max.Y {
								output.Set(px, py, col)
							}
						}
					}
				}
			}
		}
	}
}

// DrawRotatedLabelWithOpacity draws a label with alpha blending at the specified opacity.
func DrawRotatedLabelWithOpacity(output *image.RGBA, label string, centerX, centerY int, col color.RGBA, scale int, opacity float64) {
	if opacity <= 0 {
		return
	}
	if opacity >= 1 {
		DrawRotatedLabel(output, label, centerX, centerY, col, scale)
		return
	}

	if scale < 1 {
		scale = 1
	}
	if scale > 6 {
		scale = 6
	}

	charWidth := 5 * scale  // Rotated width
	charHeight := 3 * scale // Rotated height
	spacing := scale
	labelHeight := len(label)*charHeight + (len(label)-1)*spacing
	startX := centerX - charWidth/2
	startY := centerY - labelHeight/2

	bounds := output.Bounds()

	// Draw each character (reverse order so text reads correctly top-to-bottom after -90 rotation)
	runes := []rune(label)
	for i := 0; i < len(runes); i++ {
		ch := runes[len(runes)-1-i] // Reverse: last char at top
		pattern := getCharPattern(ch)
		charY := startY + i*(charHeight+spacing)

		for row := 0; row < 5; row++ {
			for c := 0; c < 3; c++ {
				if (pattern[row] & (1 << (2 - c))) != 0 {
					for dy := 0; dy < scale; dy++ {
						for dx := 0; dx < scale; dx++ {
							px := startX + row*scale + dx
							py := charY + (2-c)*scale + dy
							if px >= bounds.Min.X && px < bounds.Max.X &&
								py >= bounds.Min.Y && py < bounds.Max.Y {
								// Alpha blend with existing pixel
								existing := output.RGBAAt(px, py)
								invAlpha := 1 - opacity
								r := uint8(float64(col.R)*opacity + float64(existing.R)*invAlpha)
								g := uint8(float64(col.G)*opacity + float64(existing.G)*invAlpha)
								b := uint8(float64(col.B)*opacity + float64(existing.B)*invAlpha)
								output.Set(px, py, color.RGBA{r, g, b, 255})
							}
						}
					}
				}
			}
		}
	}
}
