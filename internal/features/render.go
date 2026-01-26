package features

import (
	"image"
	"image/color"
	"math"
)

// RenderOptions configures how features are rendered.
type RenderOptions struct {
	// Via rendering
	ViaOutlineWidth int  // Outline width in pixels
	ViaFillVias     bool // Whether to fill vias or just draw outline

	// Trace rendering
	TraceOutlineWidth int // Additional outline width around traces

	// Selection rendering
	SelectionOutlineWidth int // Width of selection highlight

	// Inferred trace rendering
	InferredDashLength int // Dash length for inferred traces (0 = solid)
	InferredGapLength  int // Gap length between dashes
}

// DefaultRenderOptions returns default rendering options.
func DefaultRenderOptions() RenderOptions {
	return RenderOptions{
		ViaOutlineWidth:       2,
		ViaFillVias:           true,
		TraceOutlineWidth:     1,
		SelectionOutlineWidth: 3,
		InferredDashLength:    10,
		InferredGapLength:     5,
	}
}

// Render produces an RGBA image of all features in the layer.
func (l *DetectedFeaturesLayer) Render(width, height int, opts RenderOptions) *image.RGBA {
	l.mu.RLock()
	defer l.mu.RUnlock()

	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// Render traces first (behind vias)
	for _, id := range l.traces {
		if ref := l.features[id]; ref != nil {
			renderTrace(img, ref, opts)
		}
	}

	// Render vias on top
	for _, id := range l.vias {
		if ref := l.features[id]; ref != nil {
			renderVia(img, ref, opts)
		}
	}

	// Render selection highlights on top of everything
	for id := range l.selected {
		if ref := l.features[id]; ref != nil {
			renderSelectionHighlight(img, ref, opts)
		}
	}

	return img
}

// renderVia draws a single via.
func renderVia(img *image.RGBA, ref *FeatureRef, opts RenderOptions) {
	vf, ok := ref.Feature.(ViaFeature)
	if !ok {
		return
	}

	cx, cy := int(vf.Center.X), int(vf.Center.Y)
	r := int(vf.Radius)

	if opts.ViaFillVias {
		// Fill the via circle
		fillCircle(img, cx, cy, r, ref.Color)
	}

	// Draw outline
	if opts.ViaOutlineWidth > 0 {
		outlineColor := darken(ref.Color, 0.3)
		for w := 0; w < opts.ViaOutlineWidth; w++ {
			drawCircle(img, cx, cy, r-w, outlineColor)
		}
	}
}

// renderTrace draws a single trace.
func renderTrace(img *image.RGBA, ref *FeatureRef, opts RenderOptions) {
	tf, ok := ref.Feature.(TraceFeature)
	if !ok {
		return
	}

	points := tf.Points
	width := int(tf.Width)
	if width < 1 {
		width = 2
	}

	// Draw outline first (darker)
	if opts.TraceOutlineWidth > 0 {
		outlineColor := darken(ref.Color, 0.4)
		for i := 0; i < len(points)-1; i++ {
			drawThickLine(img, points[i].X, points[i].Y,
				points[i+1].X, points[i+1].Y,
				width+opts.TraceOutlineWidth*2, outlineColor)
		}
	}

	// Draw main trace
	for i := 0; i < len(points)-1; i++ {
		drawThickLine(img, points[i].X, points[i].Y,
			points[i+1].X, points[i+1].Y,
			width, ref.Color)
	}
}

// renderSelectionHighlight draws a highlight around selected features.
func renderSelectionHighlight(img *image.RGBA, ref *FeatureRef, opts RenderOptions) {
	bounds := ref.Feature.GetBounds()

	// Draw a yellow rectangle around the feature
	x1, y1 := bounds.X-opts.SelectionOutlineWidth, bounds.Y-opts.SelectionOutlineWidth
	x2, y2 := bounds.X+bounds.Width+opts.SelectionOutlineWidth, bounds.Y+bounds.Height+opts.SelectionOutlineWidth

	for w := 0; w < opts.SelectionOutlineWidth; w++ {
		drawRect(img, x1+w, y1+w, x2-w, y2-w, SelectionColor)
	}
}

// fillCircle fills a circle with the given color.
func fillCircle(img *image.RGBA, cx, cy, r int, c color.RGBA) {
	bounds := img.Bounds()

	for y := cy - r; y <= cy+r; y++ {
		if y < bounds.Min.Y || y >= bounds.Max.Y {
			continue
		}
		for x := cx - r; x <= cx+r; x++ {
			if x < bounds.Min.X || x >= bounds.Max.X {
				continue
			}
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy <= r*r {
				img.Set(x, y, c)
			}
		}
	}
}

// drawCircle draws a circle outline using Bresenham's algorithm.
func drawCircle(img *image.RGBA, cx, cy, r int, c color.RGBA) {
	bounds := img.Bounds()

	setPixel := func(x, y int) {
		if x >= bounds.Min.X && x < bounds.Max.X && y >= bounds.Min.Y && y < bounds.Max.Y {
			img.Set(x, y, c)
		}
	}

	x := r
	y := 0
	err := 0

	for x >= y {
		setPixel(cx+x, cy+y)
		setPixel(cx+y, cy+x)
		setPixel(cx-y, cy+x)
		setPixel(cx-x, cy+y)
		setPixel(cx-x, cy-y)
		setPixel(cx-y, cy-x)
		setPixel(cx+y, cy-x)
		setPixel(cx+x, cy-y)

		y++
		if err <= 0 {
			err += 2*y + 1
		}
		if err > 0 {
			x--
			err -= 2*x + 1
		}
	}
}

// drawThickLine draws a line with given thickness.
func drawThickLine(img *image.RGBA, x1, y1, x2, y2 float64, thickness int, c color.RGBA) {
	bounds := img.Bounds()

	// Calculate perpendicular direction
	dx := x2 - x1
	dy := y2 - y1
	length := math.Sqrt(dx*dx + dy*dy)
	if length == 0 {
		return
	}

	// Perpendicular unit vector
	px := -dy / length
	py := dx / length

	halfThick := float64(thickness) / 2

	// Draw filled polygon (simple approach: draw multiple parallel lines)
	for t := -halfThick; t <= halfThick; t += 1.0 {
		lx1 := x1 + px*t
		ly1 := y1 + py*t
		lx2 := x2 + px*t
		ly2 := y2 + py*t

		drawLine(img, int(lx1), int(ly1), int(lx2), int(ly2), c, bounds)
	}
}

// drawLine draws a line using Bresenham's algorithm.
func drawLine(img *image.RGBA, x1, y1, x2, y2 int, c color.RGBA, bounds image.Rectangle) {
	dx := abs(x2 - x1)
	dy := abs(y2 - y1)

	var sx, sy int
	if x1 < x2 {
		sx = 1
	} else {
		sx = -1
	}
	if y1 < y2 {
		sy = 1
	} else {
		sy = -1
	}

	err := dx - dy

	for {
		if x1 >= bounds.Min.X && x1 < bounds.Max.X && y1 >= bounds.Min.Y && y1 < bounds.Max.Y {
			img.Set(x1, y1, c)
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

// drawRect draws a rectangle outline.
func drawRect(img *image.RGBA, x1, y1, x2, y2 int, c color.RGBA) {
	bounds := img.Bounds()

	// Top and bottom edges
	for x := x1; x <= x2; x++ {
		if x >= bounds.Min.X && x < bounds.Max.X {
			if y1 >= bounds.Min.Y && y1 < bounds.Max.Y {
				img.Set(x, y1, c)
			}
			if y2 >= bounds.Min.Y && y2 < bounds.Max.Y {
				img.Set(x, y2, c)
			}
		}
	}

	// Left and right edges
	for y := y1; y <= y2; y++ {
		if y >= bounds.Min.Y && y < bounds.Max.Y {
			if x1 >= bounds.Min.X && x1 < bounds.Max.X {
				img.Set(x1, y, c)
			}
			if x2 >= bounds.Min.X && x2 < bounds.Max.X {
				img.Set(x2, y, c)
			}
		}
	}
}

// darken reduces the brightness of a color.
func darken(c color.RGBA, factor float64) color.RGBA {
	return color.RGBA{
		R: uint8(float64(c.R) * (1 - factor)),
		G: uint8(float64(c.G) * (1 - factor)),
		B: uint8(float64(c.B) * (1 - factor)),
		A: c.A,
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
