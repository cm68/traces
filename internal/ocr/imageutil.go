package ocr

import (
	"image"
	"image/color"

	"pcb-tracer/pkg/geometry"
)

// CalculateBackgroundColor samples the border pixels of an RGBA image and
// returns their average color. Useful for filling masked-out regions.
func CalculateBackgroundColor(img *image.RGBA) color.RGBA {
	bounds := img.Bounds()
	var r, g, b, count uint64

	for x := bounds.Min.X; x < bounds.Max.X; x++ {
		c := img.RGBAAt(x, bounds.Min.Y)
		r += uint64(c.R)
		g += uint64(c.G)
		b += uint64(c.B)
		count++
		c = img.RGBAAt(x, bounds.Max.Y-1)
		r += uint64(c.R)
		g += uint64(c.G)
		b += uint64(c.B)
		count++
	}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		c := img.RGBAAt(bounds.Min.X, y)
		r += uint64(c.R)
		g += uint64(c.G)
		b += uint64(c.B)
		count++
		c = img.RGBAAt(bounds.Max.X-1, y)
		r += uint64(c.R)
		g += uint64(c.G)
		b += uint64(c.B)
		count++
	}

	if count == 0 {
		return color.RGBA{A: 255}
	}
	return color.RGBA{
		R: uint8(r / count),
		G: uint8(g / count),
		B: uint8(b / count),
		A: 255,
	}
}

// MaskRegion fills a rectangular region of an RGBA image with a solid color.
func MaskRegion(img *image.RGBA, bounds geometry.RectInt, c color.RGBA) {
	imgBounds := img.Bounds()
	x0 := max(bounds.X, imgBounds.Min.X)
	y0 := max(bounds.Y, imgBounds.Min.Y)
	x1 := min(bounds.X+bounds.Width, imgBounds.Max.X)
	y1 := min(bounds.Y+bounds.Height, imgBounds.Max.Y)
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}
