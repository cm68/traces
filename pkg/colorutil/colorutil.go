// Package colorutil provides shared color utilities for the PCB tracer application.
package colorutil

import (
	"image/color"
	"math"
)

// Common overlay colors used throughout the application.
var (
	Black   = color.RGBA{R: 0, G: 0, B: 0, A: 255}
	White   = color.RGBA{R: 255, G: 255, B: 255, A: 255}
	Cyan    = color.RGBA{R: 0, G: 255, B: 255, A: 255}
	Magenta = color.RGBA{R: 255, G: 0, B: 255, A: 255}
	Blue    = color.RGBA{R: 0, G: 0, B: 255, A: 255}
	Green   = color.RGBA{R: 0, G: 255, B: 0, A: 255}
	Yellow  = color.RGBA{R: 255, G: 255, B: 0, A: 255}
)

// RGBToHSV converts RGB (0-255) to HSV (OpenCV convention: H 0-180, S 0-255, V 0-255).
func RGBToHSV(r, g, b float64) (h, s, v float64) {
	r /= 255.0
	g /= 255.0
	b /= 255.0

	maxC := math.Max(r, math.Max(g, b))
	minC := math.Min(r, math.Min(g, b))
	diff := maxC - minC

	v = maxC * 255.0 // V in 0-255

	if maxC == 0 {
		s = 0
	} else {
		s = (diff / maxC) * 255.0 // S in 0-255
	}

	if diff == 0 {
		h = 0
	} else if maxC == r {
		h = 60 * math.Mod((g-b)/diff, 6)
	} else if maxC == g {
		h = 60 * ((b-r)/diff + 2)
	} else {
		h = 60 * ((r-g)/diff + 4)
	}

	if h < 0 {
		h += 360
	}

	h = h / 2 // Convert to OpenCV's 0-180 range

	return h, s, v
}
