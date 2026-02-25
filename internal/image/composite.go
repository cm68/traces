package image

import (
	"image"
	"image/color"
	"image/draw"
	"math"
)

// BlendMode specifies how layers are composited.
type BlendMode int

const (
	BlendNormal BlendMode = iota
	BlendMultiply
	BlendScreen
	BlendOverlay
	BlendDifference
)

func (m BlendMode) String() string {
	switch m {
	case BlendNormal:
		return "Normal"
	case BlendMultiply:
		return "Multiply"
	case BlendScreen:
		return "Screen"
	case BlendOverlay:
		return "Overlay"
	case BlendDifference:
		return "Difference"
	default:
		return "Unknown"
	}
}

// Composite combines multiple layers into a single image.
type Composite struct {
	Width     int
	Height    int
	Layers    []*CompositeLayer
	BackColor color.Color
}

// CompositeLayer wraps a Layer with compositing settings.
type CompositeLayer struct {
	Layer     *Layer
	BlendMode BlendMode
	OffsetX   int
	OffsetY   int
}

// NewComposite creates a new Composite with the specified dimensions.
func NewComposite(width, height int) *Composite {
	return &Composite{
		Width:     width,
		Height:    height,
		BackColor: color.RGBA{40, 40, 40, 255}, // Dark gray background
	}
}

// AddLayer adds a layer to the composite.
func (c *Composite) AddLayer(layer *Layer, mode BlendMode, offsetX, offsetY int) {
	c.Layers = append(c.Layers, &CompositeLayer{
		Layer:     layer,
		BlendMode: mode,
		OffsetX:   offsetX,
		OffsetY:   offsetY,
	})
}

// Render produces the final composited image.
func (c *Composite) Render() *image.RGBA {
	result := image.NewRGBA(image.Rect(0, 0, c.Width, c.Height))

	// Fill background
	draw.Draw(result, result.Bounds(), &image.Uniform{c.BackColor}, image.Point{}, draw.Src)

	// Composite each layer
	for _, cl := range c.Layers {
		if cl.Layer == nil || cl.Layer.Image == nil || !cl.Layer.Visible {
			continue
		}
		c.compositeLayer(result, cl)
	}

	return result
}

// compositeLayer blends a single layer onto the result.
func (c *Composite) compositeLayer(dst *image.RGBA, cl *CompositeLayer) {
	src := cl.Layer.Image
	srcBounds := src.Bounds()
	opacity := cl.Layer.Opacity

	for y := srcBounds.Min.Y; y < srcBounds.Max.Y; y++ {
		dstY := y - srcBounds.Min.Y + cl.OffsetY
		if dstY < 0 || dstY >= c.Height {
			continue
		}

		for x := srcBounds.Min.X; x < srcBounds.Max.X; x++ {
			dstX := x - srcBounds.Min.X + cl.OffsetX
			if dstX < 0 || dstX >= c.Width {
				continue
			}

			srcColor := src.At(x, y)
			dstColor := dst.At(dstX, dstY)

			blended := c.blend(dstColor, srcColor, cl.BlendMode, opacity)
			dst.Set(dstX, dstY, blended)
		}
	}
}

// blend performs the blend operation between two colors.
func (c *Composite) blend(dst, src color.Color, mode BlendMode, opacity float64) color.Color {
	sr, sg, sb, sa := src.RGBA()
	dr, dg, db, da := dst.RGBA()

	// Convert to 0-1 range
	sf := [4]float64{float64(sr) / 65535.0, float64(sg) / 65535.0, float64(sb) / 65535.0, float64(sa) / 65535.0}
	df := [4]float64{float64(dr) / 65535.0, float64(dg) / 65535.0, float64(db) / 65535.0, float64(da) / 65535.0}

	var rf [3]float64

	switch mode {
	case BlendNormal:
		rf[0] = sf[0]
		rf[1] = sf[1]
		rf[2] = sf[2]

	case BlendMultiply:
		rf[0] = sf[0] * df[0]
		rf[1] = sf[1] * df[1]
		rf[2] = sf[2] * df[2]

	case BlendScreen:
		rf[0] = 1 - (1-sf[0])*(1-df[0])
		rf[1] = 1 - (1-sf[1])*(1-df[1])
		rf[2] = 1 - (1-sf[2])*(1-df[2])

	case BlendOverlay:
		for i := 0; i < 3; i++ {
			if df[i] < 0.5 {
				rf[i] = 2 * sf[i] * df[i]
			} else {
				rf[i] = 1 - 2*(1-sf[i])*(1-df[i])
			}
		}

	case BlendDifference:
		rf[0] = math.Abs(sf[0] - df[0])
		rf[1] = math.Abs(sf[1] - df[1])
		rf[2] = math.Abs(sf[2] - df[2])
	}

	// Apply opacity and alpha blending
	alpha := sf[3] * opacity
	finalR := rf[0]*alpha + df[0]*(1-alpha)
	finalG := rf[1]*alpha + df[1]*(1-alpha)
	finalB := rf[2]*alpha + df[2]*(1-alpha)
	finalA := alpha + df[3]*(1-alpha)

	return color.RGBA{
		R: uint8(clamp(finalR, 0, 1) * 255),
		G: uint8(clamp(finalG, 0, 1) * 255),
		B: uint8(clamp(finalB, 0, 1) * 255),
		A: uint8(clamp(finalA, 0, 1) * 255),
	}
}

func clamp(x, min, max float64) float64 {
	if x < min {
		return min
	}
	if x > max {
		return max
	}
	return x
}
