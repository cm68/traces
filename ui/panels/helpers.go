package panels

import (
	"fmt"
	"image"
	"math"
	"strings"

	"pcb-tracer/internal/alignment"
	"pcb-tracer/pkg/colorutil"
	"pcb-tracer/pkg/geometry"
)

// hsvStats holds HSV statistics for a region (mean ± 1 sigma).
type hsvStats struct {
	hueMean, hueStd float64
	satMean, satStd float64
	valMean, valStd float64
}

func applyShearAlignment(img image.Image, backLeft, backRight, frontLeft, frontRight geometry.Point2D, contactY float64) (image.Image, string) {
	backYDistLeft := backLeft.Y - contactY
	backYDistRight := backRight.Y - contactY
	frontYDistLeft := frontLeft.Y - contactY
	frontYDistRight := frontRight.Y - contactY

	backYDist := (backYDistLeft + backYDistRight) / 2
	frontYDist := (frontYDistLeft + frontYDistRight) / 2

	if math.Abs(backYDist) < 1 {
		return img, "ejectors too close to contacts"
	}

	yScale := frontYDist / backYDist

	scaledBackLeftY := contactY + backYDistLeft*yScale
	scaledBackRightY := contactY + backYDistRight*yScale

	deltaLeftX := frontLeft.X - backLeft.X
	deltaRightX := frontRight.X - backRight.X

	shearLeft := deltaLeftX / frontYDist
	shearRight := deltaRightX / frontYDist

	ejectorLeftX := backLeft.X
	ejectorRightX := backRight.X
	ejectorSpanX := ejectorRightX - ejectorLeftX

	if math.Abs(ejectorSpanX) < 1 {
		return img, "ejectors too close together"
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	result := image.NewRGBA(image.Rect(0, 0, w, h))

	for y := 0; y < h; y++ {
		outYDist := float64(y) - contactY
		srcYDist := outYDist / yScale
		srcY := contactY + srcYDist

		for x := 0; x < w; x++ {
			t := (float64(x) - ejectorLeftX) / ejectorSpanX
			if t < 0 {
				t = 0
			}
			if t > 1 {
				t = 1
			}
			shear := shearLeft*(1-t) + shearRight*t

			xShift := shear * outYDist
			srcX := float64(x) - xShift

			sx := int(srcX + 0.5)
			sy := int(srcY + 0.5)

			if sx >= 0 && sx < w && sy >= 0 && sy < h {
				result.Set(x, y, img.At(sx+bounds.Min.X, sy+bounds.Min.Y))
			}
		}
	}

	_ = scaledBackLeftY
	_ = scaledBackRightY

	return result, fmt.Sprintf("yScale=%.4f, shear L=%.4f R=%.4f", yScale, shearLeft, shearRight)
}

func flipHorizontal(img image.Image) image.Image {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	flipped := image.NewRGBA(image.Rect(0, 0, w, h))

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			flipped.Set(w-1-x, y, img.At(x+bounds.Min.X, y+bounds.Min.Y))
		}
	}
	return flipped
}

func translateImage(img image.Image, dx, dy int) image.Image {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	newW := w + absInt(dx)
	newH := h + absInt(dy)
	translated := image.NewRGBA(image.Rect(0, 0, newW, newH))

	offsetX := 0
	offsetY := 0
	if dx > 0 {
		offsetX = dx
	}
	if dy > 0 {
		offsetY = dy
	}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			translated.Set(x+offsetX, y+offsetY, img.At(x+bounds.Min.X, y+bounds.Min.Y))
		}
	}

	return translated
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func extractHSVStats(img image.Image, x1, y1, x2, y2 int) hsvStats {
	var hues, sats, vals []float64

	bounds := img.Bounds()
	for y := y1; y <= y2; y++ {
		if y < bounds.Min.Y || y >= bounds.Max.Y {
			continue
		}
		for x := x1; x <= x2; x++ {
			if x < bounds.Min.X || x >= bounds.Max.X {
				continue
			}
			r, g, b, _ := img.At(x, y).RGBA()
			r8 := float64(r >> 8)
			g8 := float64(g >> 8)
			b8 := float64(b >> 8)

			h, s, v := colorutil.RGBToHSV(r8, g8, b8)
			hues = append(hues, h)
			sats = append(sats, s)
			vals = append(vals, v)
		}
	}

	return hsvStats{
		hueMean: mean(hues),
		hueStd:  stdDev(hues),
		satMean: mean(sats),
		satStd:  stdDev(sats),
		valMean: mean(vals),
		valStd:  stdDev(vals),
	}
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func stdDev(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	m := mean(values)
	sumSq := 0.0
	for _, v := range values {
		diff := v - m
		sumSq += diff * diff
	}
	return math.Sqrt(sumSq / float64(len(values)))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// naturalLess compares two strings using natural numeric ordering.
// "A2" < "A10", "U1" < "U2" < "U10", etc.
func naturalLess(a, b string) bool {
	chunksA := splitNatural(a)
	chunksB := splitNatural(b)
	for i := 0; i < len(chunksA) && i < len(chunksB); i++ {
		ca, cb := chunksA[i], chunksB[i]
		if isNumeric(ca) && isNumeric(cb) {
			na := parseNum(ca)
			nb := parseNum(cb)
			if na != nb {
				return na < nb
			}
		} else {
			cmp := strings.Compare(strings.ToUpper(ca), strings.ToUpper(cb))
			if cmp != 0 {
				return cmp < 0
			}
		}
	}
	return len(chunksA) < len(chunksB)
}

func splitNatural(s string) []string {
	var chunks []string
	var current strings.Builder
	wasDigit := false
	for i, r := range s {
		isDigit := r >= '0' && r <= '9'
		if i > 0 && isDigit != wasDigit {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		current.WriteRune(r)
		wasDigit = isDigit
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}

func isNumeric(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

func parseNum(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n = n*10 + int(r-'0')
		}
	}
	return n
}

func printContactStats(img image.Image, contacts []alignment.Contact, dpi float64, layerName string) {
	if len(contacts) == 0 {
		return
	}

	fmt.Printf("\n=== Contact Statistics for %s ===\n", layerName)
	fmt.Printf("%-4s %12s %12s %12s %12s %20s %20s %12s\n",
		"#", "W (px)", "H (px)", "W (in)", "H (in)", "Avg R/G/B", "StdDev R/G/B", "Aspect")

	bounds := img.Bounds()

	for i, contact := range contacts {
		b := contact.Bounds
		widthPx := b.Width
		heightPx := b.Height

		widthIn := 0.0
		heightIn := 0.0
		if dpi > 0 {
			widthIn = float64(widthPx) / dpi
			heightIn = float64(heightPx) / dpi
		}

		var sumR, sumG, sumB float64
		var sumR2, sumG2, sumB2 float64
		var count int

		for y := b.Y; y < b.Y+b.Height && y < bounds.Max.Y; y++ {
			for x := b.X; x < b.X+b.Width && x < bounds.Max.X; x++ {
				if x < bounds.Min.X || y < bounds.Min.Y {
					continue
				}
				r, g, bb, _ := img.At(x, y).RGBA()
				rf := float64(r >> 8)
				gf := float64(g >> 8)
				bf := float64(bb >> 8)

				sumR += rf
				sumG += gf
				sumB += bf
				sumR2 += rf * rf
				sumG2 += gf * gf
				sumB2 += bf * bf
				count++
			}
		}

		if count > 0 {
			n := float64(count)
			avgR := sumR / n
			avgG := sumG / n
			avgB := sumB / n

			stdR := math.Sqrt(sumR2/n - avgR*avgR)
			stdG := math.Sqrt(sumG2/n - avgG*avgG)
			stdB := math.Sqrt(sumB2/n - avgB*avgB)

			aspect := float64(heightPx) / float64(widthPx)

			fmt.Printf("%-4d %12d %12d %12.4f %12.4f %6.1f/%5.1f/%5.1f %6.1f/%5.1f/%5.1f %12.2f\n",
				i+1, widthPx, heightPx, widthIn, heightIn,
				avgR, avgG, avgB, stdR, stdG, stdB, aspect)
		}
	}
	fmt.Println()
}
