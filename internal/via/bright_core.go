package via

import (
	"fmt"
	"image"
	"math"

	img "pcb-tracer/internal/image"
	"pcb-tracer/pkg/geometry"
)

// DetectBrightCoreVias finds vias by looking for regions where every pixel in
// a 5×5 block exceeds a brightness threshold (default 245). This reliably
// detects metallic via cores even when surrounding pads and traces are also
// bright, because the 5×5 erosion breaks thin connections between features.
//
// Each candidate is verified by casting 8 rays from the centroid outward and
// checking that at least 6 hit a brightness edge at a consistent radius.
// This rejects elongated bright blobs (traces, pads) that aren't circular.
func DetectBrightCoreVias(srcImg image.Image, side img.Side, dpi float64, minRadius, maxRadius int) (*ViaDetectionResult, error) {
	if srcImg == nil {
		return nil, fmt.Errorf("nil image")
	}

	bounds := srcImg.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	// Convert to grayscale (luminance)
	gray := make([]uint8, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b, _ := srcImg.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			// Fast luminance: (19595*R + 38470*G + 7471*B) >> 16
			gray[y*w+x] = uint8((19595*(r>>8) + 38470*(g>>8) + 7471*(b>>8)) >> 16)
		}
	}

	// Eroded core mask: pixel is set only if ALL pixels in its 5×5
	// neighborhood exceed the brightness threshold. This is equivalent
	// to threshold → erode(5×5 rect).
	const thresh = 245
	const half = 2 // 5×5 kernel → half-width 2
	core := make([]bool, w*h)
	for y := half; y < h-half; y++ {
		for x := half; x < w-half; x++ {
			allBright := true
			for dy := -half; dy <= half; dy++ {
				for dx := -half; dx <= half; dx++ {
					if gray[(y+dy)*w+(x+dx)] < thresh {
						allBright = false
						break
					}
				}
				if !allBright {
					break
				}
			}
			core[y*w+x] = allBright
		}
	}

	// Connected components via flood fill
	visited := make([]bool, w*h)
	var vias []Via
	viaID := 0
	rejected := 0

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if !core[y*w+x] || visited[y*w+x] {
				continue
			}

			// Flood fill (4-connected) to find component
			var sumX, sumY float64
			var count int
			queue := []int{y*w + x}
			visited[y*w+x] = true

			for len(queue) > 0 {
				idx := queue[0]
				queue = queue[1:]
				py, px := idx/w, idx%w
				sumX += float64(px)
				sumY += float64(py)
				count++

				for _, d := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
					nx, ny := px+d[0], py+d[1]
					if nx >= 0 && nx < w && ny >= 0 && ny < h {
						nidx := ny*w + nx
						if core[nidx] && !visited[nidx] {
							visited[nidx] = true
							queue = append(queue, nidx)
						}
					}
				}
			}

			// Core radius from area, then add back the erosion margin
			coreRadius := math.Sqrt(float64(count) / math.Pi)
			radius := coreRadius + half

			// Size filter
			if int(radius) < minRadius || int(radius) > maxRadius {
				continue
			}

			// Centroid in local (gray buffer) coordinates
			lcx := sumX / float64(count)
			lcy := sumY / float64(count)

			// 8-ray circularity check: cast rays at 0°,45°,...,315° from
			// the centroid, find where brightness drops below edgeThresh.
			// A circular via has consistent edge distances; a trace doesn't.
			const edgeThresh uint8 = 200
			const nRays = 8
			var rayDists [nRays]float64
			validRays := 0
			maxWalk := float64(maxRadius) * 2
			for ri := 0; ri < nRays; ri++ {
				angle := float64(ri) * math.Pi / 4
				dx, dy := math.Cos(angle), math.Sin(angle)
				dist := 0.0
				for step := 1.0; step <= maxWalk; step++ {
					px := int(lcx + dx*step + 0.5)
					py := int(lcy + dy*step + 0.5)
					if px < 0 || px >= w || py < 0 || py >= h {
						break
					}
					if gray[py*w+px] < edgeThresh {
						dist = step
						break
					}
				}
				rayDists[ri] = dist
				if dist > 0 {
					validRays++
				}
			}

			if validRays < 6 {
				rejected++
				continue
			}

			// Compute median of valid ray distances
			var sorted []float64
			for _, d := range rayDists {
				if d > 0 {
					sorted = append(sorted, d)
				}
			}
			for i := 0; i < len(sorted)-1; i++ {
				for j := i + 1; j < len(sorted); j++ {
					if sorted[j] < sorted[i] {
						sorted[i], sorted[j] = sorted[j], sorted[i]
					}
				}
			}
			medianR := sorted[len(sorted)/2]

			// Count rays within 30% of median — need ≥6 of 8 for circular
			const tolerance = 0.30
			circular := 0
			for _, d := range rayDists {
				if d > 0 && math.Abs(d-medianR)/medianR <= tolerance {
					circular++
				}
			}
			if circular < 6 {
				rejected++
				continue
			}

			// Use median ray distance as the via radius (more accurate than area-based)
			radius = medianR

			// Re-check size with ray-based radius
			if int(radius) < minRadius || int(radius) > maxRadius {
				continue
			}

			// Centroid in image coordinates
			cx := lcx + float64(bounds.Min.X)
			cy := lcy + float64(bounds.Min.Y)

			viaID++
			vias = append(vias, Via{
				ID:          fmt.Sprintf("bc-%s-%03d", side.String()[:1], viaID),
				Center:      geometry.Point2D{X: cx, Y: cy},
				Radius:      radius,
				Side:        side,
				Circularity: float64(circular) / nRays,
				Confidence:  0.95,
				Method:      MethodContourFit,
			})
		}
	}

	fmt.Printf("DetectBrightCoreVias: found %d vias, rejected %d non-circular (thresh=%d, erode=5x5, rays=8, minR=%d, maxR=%d)\n",
		len(vias), rejected, thresh, minRadius, maxRadius)

	return &ViaDetectionResult{Vias: vias, Side: side, DPI: dpi}, nil
}
