// Package canvas provides drawing primitives for the image canvas.
package canvas

import (
	"fmt"
	"image"
	"image/color"

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

// drawOverlay draws an overlay on the output image.
func (ic *ImageCanvas) drawOverlay(output *image.RGBA, overlay *Overlay) {
	col := overlay.Color
	for _, rect := range overlay.Rectangles {
		// Scale rectangle coordinates by zoom
		x1 := int(float64(rect.X) * ic.zoom)
		y1 := int(float64(rect.Y) * ic.zoom)
		x2 := int(float64(rect.X+rect.Width) * ic.zoom)
		y2 := int(float64(rect.Y+rect.Height) * ic.zoom)

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
		ic.drawPolygon(output, poly, col)
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
func (ic *ImageCanvas) drawPolygon(output *image.RGBA, poly OverlayPolygon, col color.RGBA) {
	fmt.Printf("      drawPolygon: label=%s pts=%d filled=%v\n", poly.Label, len(poly.Points), poly.Filled)
	if len(poly.Points) < 3 {
		fmt.Printf("      drawPolygon: SKIPPED (< 3 points)\n")
		return
	}

	bounds := output.Bounds()

	// Scale points by zoom
	scaledPoints := make([]geometry.Point2D, len(poly.Points))
	var minX, minY, maxX, maxY float64
	minX, minY = poly.Points[0].X*ic.zoom, poly.Points[0].Y*ic.zoom
	maxX, maxY = minX, minY

	for i, p := range poly.Points {
		scaledPoints[i] = geometry.Point2D{X: p.X * ic.zoom, Y: p.Y * ic.zoom}
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
	fmt.Printf("      drawPolygon: bounds minX=%.1f maxX=%.1f minY=%.1f maxY=%.1f zoom=%.2f\n",
		minX, maxX, minY, maxY, ic.zoom)

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
		// Calculate centroid for label placement
		center := geometry.Centroid(scaledPoints)
		// Debug: compare centroid with bounding box center
		bboxCenterX := (minX + maxX) / 2
		bboxCenterY := (minY + maxY) / 2
		fmt.Printf("      drawPolygon: label=%s centroid=(%.1f,%.1f) bboxCenter=(%.1f,%.1f) diff=(%.1f,%.1f)\n",
			poly.Label, center.X, center.Y, bboxCenterX, bboxCenterY,
			center.X-bboxCenterX, center.Y-bboxCenterY)
		ic.drawOverlayLabel(output, poly.Label, int(center.X), int(center.Y))
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
