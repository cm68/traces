package alignment

import (
	"fmt"
	"image"
	"math"
	"os"

	"pcb-tracer/pkg/geometry"

	"gocv.io/x/gocv"
)

// DetectBoardOutline extracts the board's outline polygon from the scans
// using background subtraction — the same segmentation the board-bounds
// detection uses, but keeping the full contour instead of a bounding
// rectangle, so notches, cutouts, and the connector tongue survive.
//
// When the aligned back-side scan is available, the front and back masks are
// intersected: the board itself appears on both sides, while component
// bodies protruding past the board edge (connector housings, ejectors)
// appear only on one, and would otherwise bulge the outline.
//
// Component bodies that straddle the board edge (connector housings, card
// ejectors) protrude on both scans, so they survive the mask intersection as
// outward lobes. The one outward lobe that is genuinely board material is
// the gold-finger tongue — identified by containing known edge contacts —
// so every other lobe is clamped back to the dominant board edge.
//
// Returns polygon vertices in image coordinates (nil if detection fails).
func DetectBoardOutline(front, back image.Image, contacts []geometry.Point2D) []geometry.Point2D {
	mat, err := imageToMat(front)
	if err != nil {
		return nil
	}
	defer mat.Close()

	imgH := mat.Rows()
	imgW := mat.Cols()

	// Downsample for speed; 2000px keeps ~1mm outline fidelity on a 600 DPI scan.
	scale := math.Min(1.0, 2000.0/float64(max(imgW, imgH)))

	mask := boardMask(mat, scale)
	defer mask.Close()

	if back != nil {
		if bmat, err := imageToMat(back); err == nil {
			bmask := boardMask(bmat, scale)
			bmat.Close()
			if bmask.Rows() == mask.Rows() && bmask.Cols() == mask.Cols() {
				gocv.BitwiseAnd(mask, bmask, &mask)
			}
			bmask.Close()
		}
	}

	contours := gocv.FindContours(mask, gocv.RetrievalExternal, gocv.ChainApproxSimple)
	defer contours.Close()
	if contours.Size() == 0 {
		return nil
	}

	// Largest contour that isn't the whole image
	var best gocv.PointVector
	bestArea := 0.0
	fullArea := float64(mask.Rows() * mask.Cols())
	for i := 0; i < contours.Size(); i++ {
		c := contours.At(i)
		area := gocv.ContourArea(c)
		if area < fullArea*0.98 && area > bestArea {
			bestArea = area
			best = c
		}
	}
	if bestArea < fullArea*0.25 {
		fmt.Printf("DetectBoardOutline: largest contour too small (%.0f%% of image)\n",
			100*bestArea/fullArea)
		return nil
	}

	// Simplify: epsilon ~0.15% of perimeter keeps real notches, drops noise.
	peri := gocv.ArcLength(best, true)
	approx := gocv.ApproxPolyDP(best, 0.0015*peri, true)
	defer approx.Close()

	pts := make([]geometry.Point2D, 0, approx.Size())
	for i := 0; i < approx.Size(); i++ {
		p := approx.At(i)
		pts = append(pts, geometry.Point2D{
			X: float64(p.X) / scale,
			Y: float64(p.Y) / scale,
		})
	}
	if len(pts) < 3 {
		return nil
	}

	pts = clampForeignLobes(pts, contacts, 12.0/scale)
	return orthogonalizeOutline(pts, 4.0/scale)
}

// clampForeignLobes flattens outward protrusions of the outline that do not
// contain any known edge contact. tol is the outward deviation (image px)
// beyond the dominant board edge that counts as a protrusion.
func clampForeignLobes(pts []geometry.Point2D, contacts []geometry.Point2D, tol float64) []geometry.Point2D {
	n := len(pts)
	if n < 4 {
		return pts
	}

	// Dominant board edges: length-weighted histogram of near-axis segment
	// positions; among well-supported bins take the outermost. Short foreign
	// lobes (a connector housing) never reach the support threshold, while
	// long board edges — including the finger tongue's tip — do.
	dominantEdge := func(horizontal bool, outerSign float64) float64 {
		const bin = 10.0
		weights := make(map[int]float64)
		for i := 0; i < n; i++ {
			p, q := pts[i], pts[(i+1)%n]
			var pos, span, drift float64
			if horizontal {
				drift, span = math.Abs(q.Y-p.Y), math.Abs(q.X-p.X)
				pos = (p.Y + q.Y) / 2
			} else {
				drift, span = math.Abs(q.X-p.X), math.Abs(q.Y-p.Y)
				pos = (p.X + q.X) / 2
			}
			// Near-axis: slope-based so long, slightly tilted board edges
			// still count (residual scan tilt drifts ~1% over their length).
			if drift > math.Max(15, 0.02*span) {
				continue
			}
			weights[int(math.Round(pos/bin))] += span
		}
		maxW := 0.0
		for _, w := range weights {
			maxW = math.Max(maxW, w)
		}
		best, found := 0.0, false
		for k, w := range weights {
			if w < maxW*0.3 {
				continue
			}
			v := float64(k) * bin
			if !found || outerSign*v > outerSign*best {
				best, found = v, true
			}
		}
		if !found {
			return outerSign * math.Inf(1) // nothing to clamp against
		}
		return best
	}
	top := dominantEdge(true, -1)
	bottom := dominantEdge(true, 1)
	left := dominantEdge(false, -1)
	right := dominantEdge(false, 1)
	if os.Getenv("OUTLINE_DEBUG") != "" {
		fmt.Printf("clampForeignLobes: top=%.0f bottom=%.0f left=%.0f right=%.0f tol=%.0f n=%d\n",
			top, bottom, left, right, tol, n)
	}

	// Which side (if any) each point protrudes beyond.
	type sideClamp struct {
		out   bool
		clamp func(p geometry.Point2D) geometry.Point2D
	}
	classify := func(p geometry.Point2D) sideClamp {
		switch {
		case p.Y < top-tol:
			return sideClamp{true, func(q geometry.Point2D) geometry.Point2D { q.Y = top; return q }}
		case p.Y > bottom+tol:
			return sideClamp{true, func(q geometry.Point2D) geometry.Point2D { q.Y = bottom; return q }}
		case p.X < left-tol:
			return sideClamp{true, func(q geometry.Point2D) geometry.Point2D { q.X = left; return q }}
		case p.X > right+tol:
			return sideClamp{true, func(q geometry.Point2D) geometry.Point2D { q.X = right; return q }}
		}
		return sideClamp{}
	}

	// Group consecutive protruding points into lobes (wrapping around).
	out := make([]geometry.Point2D, n)
	copy(out, pts)
	i := 0
	for i < n {
		sc := classify(pts[i])
		if !sc.out {
			i++
			continue
		}
		// Collect the lobe [i, j)
		j := i
		var lobe []int
		for j < n && classify(pts[j]).out {
			lobe = append(lobe, j)
			j++
		}
		// Does the lobe contain a known contact? Expand its bounding box a
		// little — contact centers sit inside the fingers, not on the edge.
		minX, minY := math.Inf(1), math.Inf(1)
		maxX, maxY := math.Inf(-1), math.Inf(-1)
		for _, k := range lobe {
			minX = math.Min(minX, pts[k].X)
			maxX = math.Max(maxX, pts[k].X)
			minY = math.Min(minY, pts[k].Y)
			maxY = math.Max(maxY, pts[k].Y)
		}
		const pad = 40.0
		hasContact := false
		for _, c := range contacts {
			if c.X > minX-pad && c.X < maxX+pad && c.Y > minY-pad && c.Y < maxY+pad {
				hasContact = true
				break
			}
		}
		if os.Getenv("OUTLINE_DEBUG") != "" {
			fmt.Printf("  lobe %v-%v bbox=(%.0f,%.0f)-(%.0f,%.0f) contact=%v\n",
				lobe[0], lobe[len(lobe)-1], minX, minY, maxX, maxY, hasContact)
		}
		if !hasContact {
			for _, k := range lobe {
				out[k] = classify(pts[k]).clamp(pts[k])
			}
		}
		i = j
	}

	// Drop consecutive near-duplicate points introduced by clamping.
	var cleaned []geometry.Point2D
	for _, p := range out {
		if len(cleaned) > 0 {
			q := cleaned[len(cleaned)-1]
			if math.Abs(p.X-q.X) < 2 && math.Abs(p.Y-q.Y) < 2 {
				continue
			}
		}
		cleaned = append(cleaned, p)
	}
	return cleaned
}

// boardMask builds the not-background mask for one scan, downscaled by scale.
func boardMask(mat gocv.Mat, scale float64) gocv.Mat {
	var small gocv.Mat
	if scale < 1.0 {
		small = gocv.NewMat()
		gocv.Resize(mat, &small, image.Point{}, scale, scale, gocv.InterpolationArea)
	} else {
		small = mat.Clone()
	}
	defer small.Close()

	bgColor := sampleBackgroundColor(small, 30)
	diffThreshold := 25
	if bgColor.R < 30 && bgColor.G < 30 && bgColor.B < 30 {
		diffThreshold = 40
	}
	mask := createBackgroundDiffMask(small, bgColor, diffThreshold)

	// A larger closing kernel than bounds detection: silkscreen and labels at
	// the board edge shouldn't punch holes in the outline.
	kernel := gocv.GetStructuringElement(gocv.MorphRect, image.Point{9, 9})
	defer kernel.Close()
	gocv.MorphologyEx(mask, &mask, gocv.MorphClose, kernel)
	gocv.MorphologyEx(mask, &mask, gocv.MorphOpen, kernel)
	return mask
}

// orthogonalizeOutline snaps nearly-horizontal and nearly-vertical polygon
// edges to exact axis alignment. Scanned boards are square to the image after
// normalization, so residual sub-degree tilts are noise; true diagonal edges
// (corner chamfers) are left alone. tol is the cross-axis deviation (in image
// pixels) below which an edge counts as axis-aligned.
func orthogonalizeOutline(pts []geometry.Point2D, tol float64) []geometry.Point2D {
	n := len(pts)
	const (
		classD = iota // diagonal — leave alone
		classH        // horizontal
		classV        // vertical
	)
	class := make([]int, n)
	target := make([]float64, n) // shared y (H) or x (V) for the segment
	for i := 0; i < n; i++ {
		p, q := pts[i], pts[(i+1)%n]
		dx, dy := math.Abs(q.X-p.X), math.Abs(q.Y-p.Y)
		switch {
		case dy <= tol && dx > dy:
			class[i] = classH
			target[i] = (p.Y + q.Y) / 2
		case dx <= tol && dy > dx:
			class[i] = classV
			target[i] = (p.X + q.X) / 2
		default:
			class[i] = classD
		}
	}

	out := make([]geometry.Point2D, n)
	for i := 0; i < n; i++ {
		prev := (i - 1 + n) % n // segment ending at vertex i
		next := i               // segment starting at vertex i
		v := pts[i]
		if class[prev] == classH {
			v.Y = target[prev]
		} else if class[next] == classH {
			v.Y = target[next]
		}
		if class[prev] == classV {
			v.X = target[prev]
		} else if class[next] == classV {
			v.X = target[next]
		}
		out[i] = v
	}
	return out
}
