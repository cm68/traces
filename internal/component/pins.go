package component

import (
	"fmt"
	"image"
	"math"
	"sort"
	"strconv"
	"strings"

	"pcb-tracer/internal/connector"
	"pcb-tracer/internal/via"
	"pcb-tracer/pkg/colorutil"
	"pcb-tracer/pkg/geometry"
)

// ExpectedPin represents a pin's expected image-coordinate position.
type ExpectedPin struct {
	Number   int              // Pin number (1-based)
	Position geometry.Point2D // Absolute image coordinates
	Row      int              // 1 or 2 (which side of the DIP)
}

// ExpectedDIPPinPositions returns the expected image-coordinate positions for
// all pins of a DIP component, based on its bounds, package, and orientation.
// DIP numbering: pins 1..N/2 down one side, N/2+1..N back up the other.
// Returns nil if the package is not a DIP.
func ExpectedDIPPinPositions(comp *Component, dpi float64) []ExpectedPin {
	pinCount, ok := ParseDIPPinCount(comp.Package)
	if !ok || pinCount < 4 {
		return nil
	}

	pkg, hasPkg := StandardPackages[strings.ToUpper(comp.Package)]
	if !hasPkg {
		return nil
	}

	half := pinCount / 2
	pitchPx := pkg.PinPitch / 25.4 * dpi  // mm to pixels
	rowPx := pkg.RowSpacing / 25.4 * dpi   // mm to pixels

	center := comp.Center()

	// Determine long axis from bounds (pins run along the long edge)
	vertical := comp.Bounds.Height >= comp.Bounds.Width

	// Long axis unit vector and short axis unit vector
	var longAxis, shortAxis geometry.Point2D
	if vertical {
		longAxis = geometry.Point2D{X: 0, Y: 1}
		shortAxis = geometry.Point2D{X: 1, Y: 0}
	} else {
		longAxis = geometry.Point2D{X: 1, Y: 0}
		shortAxis = geometry.Point2D{X: 0, Y: 1}
	}

	// Apply component rotation if non-zero
	if comp.Rotation != 0 {
		rad := comp.Rotation * math.Pi / 180.0
		cos, sin := math.Cos(rad), math.Sin(rad)
		longAxis = geometry.Point2D{
			X: longAxis.X*cos - longAxis.Y*sin,
			Y: longAxis.X*sin + longAxis.Y*cos,
		}
		shortAxis = geometry.Point2D{
			X: shortAxis.X*cos - shortAxis.Y*sin,
			Y: shortAxis.X*sin + shortAxis.Y*cos,
		}
	}

	pins := make([]ExpectedPin, pinCount)
	halfCenter := float64(half-1) / 2.0

	for i := 0; i < pinCount; i++ {
		pinNum := i + 1
		var longOffset, shortOffset float64
		var row int

		if pinNum <= half {
			// Row 1: pins 1..N/2, top to bottom (increasing long axis)
			row = 1
			longOffset = (float64(pinNum-1) - halfCenter) * pitchPx
			shortOffset = -rowPx / 2.0
		} else {
			// Row 2: pins N/2+1..N, bottom to top (decreasing long axis)
			row = 2
			longOffset = (float64(pinCount-pinNum) - halfCenter) * pitchPx
			shortOffset = rowPx / 2.0
		}

		pos := geometry.Point2D{
			X: center.X + longAxis.X*longOffset + shortAxis.X*shortOffset,
			Y: center.Y + longAxis.Y*longOffset + shortAxis.Y*shortOffset,
		}

		pins[i] = ExpectedPin{
			Number:   pinNum,
			Position: pos,
			Row:      row,
		}
	}

	return pins
}

// DetectPins finds DIP pin pads on the back image. The approach exploits the
// manufacturing reality that all pads for a DIP are identical in size and on a
// perfectly regular grid:
//
//  1. Detect pads at expected positions (metallic confirmation).
//  2. Find "pristine" pads — fully surrounded by green, consistent radial profile.
//     These establish the true pad radius and high-quality center anchors.
//  3. Fit a rigid grid (translation + rotation) using only pristine pad centers.
//  4. Validate every pin at its grid-fitted position: no green pixels inside the
//     consensus-radius circle. Any green = reject.
//  5. Identify pin 1 (most square pad), assign counterclockwise numbering.
func DetectPins(backImg image.Image, comp *Component, expectedPins []ExpectedPin, dpi float64, nextID func() int) []*via.ConfirmedVia {
	if backImg == nil || len(expectedPins) == 0 {
		return nil
	}

	bounds := backImg.Bounds()
	imgW := bounds.Max.X
	imgH := bounds.Max.Y

	pitchPx := 2.54 / 25.4 * dpi
	searchRadius := int(pitchPx * 0.75)
	if searchRadius < 15 {
		searchRadius = 15
	}
	maxRadialDist := pitchPx * 0.7
	minRadius := 0.4 / 25.4 * dpi

	const (
		satMax      = 120
		minContrast = 40.0
		absValFloor = 80.0
	)

	n := len(expectedPins)

	// ── Phase 1: detect pads, get rough centers ──
	roughCenters := make([]geometry.Point2D, n)
	detected := make([]bool, n)

	for i, ep := range expectedPins {
		cx := int(ep.Position.X)
		cy := int(ep.Position.Y)

		x0, y0, x1, y1 := cx-searchRadius, cy-searchRadius, cx+searchRadius, cy+searchRadius
		if x0 < 0 {
			x0 = 0
		}
		if y0 < 0 {
			y0 = 0
		}
		if x1 >= imgW {
			x1 = imgW - 1
		}
		if y1 >= imgH {
			y1 = imgH - 1
		}
		if x0 >= x1 || y0 >= y1 {
			continue
		}

		windowW := x1 - x0 + 1
		windowH := y1 - y0 + 1
		vValues := make([]float64, 0, windowW*windowH)
		var peakMetalV float64

		for py := y0; py <= y1; py++ {
			for px := x0; px <= x1; px++ {
				r32, g32, b32, _ := backImg.At(px, py).RGBA()
				r := float64(r32 >> 8)
				g := float64(g32 >> 8)
				b := float64(b32 >> 8)
				_, s, v := colorutil.RGBToHSV(r, g, b)
				vValues = append(vValues, v)
				if s < satMax && v > peakMetalV {
					peakMetalV = v
				}
			}
		}

		sortedV := make([]float64, len(vValues))
		copy(sortedV, vValues)
		sort.Float64s(sortedV)
		bgLevel := sortedV[len(sortedV)/4]

		adaptiveThresh := bgLevel + minContrast
		if adaptiveThresh < absValFloor {
			adaptiveThresh = absValFloor
		}
		if peakMetalV < adaptiveThresh {
			continue
		}

		// Rough center from metallic centroid
		var mCount int
		var mSumX, mSumY, mSumW float64
		idx := 0
		for py := y0; py <= y1; py++ {
			for px := x0; px <= x1; px++ {
				v := vValues[idx]
				idx++
				r32, g32, b32, _ := backImg.At(px, py).RGBA()
				r := float64(r32 >> 8)
				g := float64(g32 >> 8)
				b := float64(b32 >> 8)
				_, s, _ := colorutil.RGBToHSV(r, g, b)
				if v >= adaptiveThresh && s < satMax {
					mCount++
					mSumX += float64(px) * v
					mSumY += float64(py) * v
					mSumW += v
				}
			}
		}
		if mCount < 3 {
			continue
		}

		roughCenters[i] = geometry.Point2D{X: mSumX / mSumW, Y: mSumY / mSumW}
		detected[i] = true
	}

	// ── Phase 2: find pristine pads and establish consensus radius ──
	// A pristine pad is fully surrounded by green with a consistent radial profile.
	// Traces crossing through a pad inflate some rays, increasing variance.
	type padProfile struct {
		center    geometry.Point2D // green-boundary-refined center
		radius    float64          // median radial distance
		relStdDev float64          // relative std dev of radial distances
		pristine  bool
	}

	profiles := make([]padProfile, n)
	for i := range expectedPins {
		if !detected[i] {
			continue
		}
		// Refine toward green boundary center
		cx, cy := refineCenterFromGreen(backImg,
			roughCenters[i].X, roughCenters[i].Y, maxRadialDist, imgW, imgH)
		distances := radialScanToGreen(backImg, cx, cy, maxRadialDist, imgW, imgH)

		// Separate bounded rays (hit green ring) from unbounded (through traces)
		var bounded []float64
		for _, d := range distances {
			if d < maxRadialDist-1 {
				bounded = append(bounded, d)
			}
		}
		numBounded := len(bounded)

		radius := 0.0
		relSD := 1.0
		if numBounded > 0 {
			sort.Float64s(bounded)
			// 25th percentile of bounded rays — robust to trace inflation.
			// Traces inflate some rays, but non-trace directions give the true
			// pad radius. The 25th percentile captures the short (correct) rays.
			idx25 := numBounded / 4
			radius = bounded[idx25]

			// Variance computed on bounded rays only
			var sumD, sumDSq float64
			for _, d := range bounded {
				sumD += d
				sumDSq += d * d
			}
			meanD := sumD / float64(numBounded)
			variance := sumDSq/float64(numBounded) - meanD*meanD
			stdDev := math.Sqrt(math.Abs(variance))
			if meanD > 0 {
				relSD = stdDev / meanD
			}
		}

		// "Pristine" = enough bounded rays for a reliable measurement.
		// Don't require ALL rays — traces cross through most pads.
		pristine := numBounded >= 8 && relSD < 0.25

		profiles[i] = padProfile{
			center:    geometry.Point2D{X: cx, Y: cy},
			radius:    radius,
			relStdDev: relSD,
			pristine:  pristine,
		}
	}

	// Consensus radius: use ALL detected pad radii (each already uses
	// 25th-percentile of bounded rays, so is robust to traces).
	// Take the 25th percentile across pads — trace inflation can only increase
	// radii, so lower percentile captures the true pad size.
	var allRadii []float64
	var pristineCount int
	for i, p := range profiles {
		if detected[i] && p.radius > 0 {
			allRadii = append(allRadii, p.radius)
		}
		if detected[i] && p.pristine {
			pristineCount++
		}
	}

	consensusRadius := 0.0
	if len(allRadii) > 0 {
		sort.Float64s(allRadii)
		// 25th percentile across all pads
		idx25 := len(allRadii) / 4
		consensusRadius = allRadii[idx25]
	}
	if consensusRadius < minRadius {
		consensusRadius = minRadius
	}

	if len(allRadii) > 0 {
		fmt.Printf("  Pristine pads: %d/%d, consensus radius: %.1f px (from %d radii, range %.1f-%.1f)\n",
			pristineCount, countTrue(detected), consensusRadius,
			len(allRadii), allRadii[0], allRadii[len(allRadii)-1])
	} else {
		fmt.Printf("  Pristine pads: 0/%d, consensus radius: %.1f px (fallback)\n",
			countTrue(detected), consensusRadius)
	}

	// ── Phase 3: rigid grid fit using only pristine pad centers ──
	expected := make([]geometry.Point2D, n)
	pristineCenters := make([]geometry.Point2D, n)
	pristineMask := make([]bool, n)
	for i, ep := range expectedPins {
		expected[i] = ep.Position
		if detected[i] && profiles[i].pristine {
			pristineCenters[i] = profiles[i].center
			pristineMask[i] = true
		}
	}

	// Fall back to all detected if too few pristine
	fitMask := pristineMask
	fitCenters := pristineCenters
	if countTrue(pristineMask) < 3 {
		fitMask = detected
		fitCenters = make([]geometry.Point2D, n)
		for i := range profiles {
			if detected[i] {
				fitCenters[i] = profiles[i].center
			}
		}
		fmt.Println("  Grid fit: too few pristine pads, using all detected")
	}

	fittedCenters := fitRigidGrid(expected, fitCenters, fitMask, pitchPx)

	// ── Phase 4: validate — no green inside the consensus radius at each fitted position ──
	valid := make([]bool, n)
	for i := range expectedPins {
		if !detected[i] {
			continue
		}
		fc := fittedCenters[i]
		if validatePadCenter(backImg, fc.X, fc.Y, consensusRadius, imgW, imgH) {
			valid[i] = true
		} else {
			fmt.Printf("  Idx %d: REJECTED (no metallic center at %.0f,%.0f r=%.0f)\n",
				i, fc.X, fc.Y, consensusRadius)
		}
	}

	fmt.Printf("  Validated: %d/%d pins\n", countTrue(valid), countTrue(detected))

	// ── Phase 5: pin 1 identification ──
	// Pin 1 has a square pad. Detect it by checking diagonal corners at 1.15×
	// radius — a square pad has bright pixels there, a round pad has green mask.
	half := n / 2
	corners := [4]int{0, half - 1, half, n - 1}

	// Check each corner position for squareness
	pin1Corner := -1
	maxCornerBright := 0
	for ci, idx := range corners {
		if !valid[idx] {
			continue
		}
		fc := fittedCenters[idx]
		bright := countSquareCorners(backImg, fc.X, fc.Y, consensusRadius, imgW, imgH)
		fmt.Printf("  Corner %d (idx %d): %d/4 bright diagonal corners\n", ci, idx, bright)
		if bright > maxCornerBright {
			maxCornerBright = bright
			pin1Corner = ci
		}
	}

	// Fall back: if no corner has 3+ bright diagonals, pick first valid corner
	if pin1Corner < 0 || maxCornerBright < 3 {
		for ci, idx := range corners {
			if valid[idx] {
				pin1Corner = ci
				break
			}
			_ = ci
		}
		if pin1Corner < 0 {
			pin1Corner = 0
		}
		fmt.Printf("  Pin 1: no square pad found (max=%d), defaulting to corner %d\n",
			maxCornerBright, pin1Corner)
	} else {
		fmt.Printf("  Pin 1 at corner %d (idx %d, %d/4 bright)\n",
			pin1Corner, corners[pin1Corner], maxCornerBright)
	}

	// Build pin number mapping from the identified corner.
	// DIP numbering: pin 1..N/2 UP one side, then N/2+1..N DOWN the other.
	// Pin N/2+1 is across from pin N/2 (far end), pin N is across from pin 1.
	pinNums := make([]int, n)
	switch pin1Corner {
	case 0: // pin 1 at idx 0 (top of row 1)
		// 1..N/2 down row 1, N/2+1..N up row 2
		for i := 0; i < n; i++ {
			pinNums[i] = i + 1
		}
	case 1: // pin 1 at idx half-1 (bottom of row 1)
		// 1..N/2 up row 1 (idx half-1 → 0), N/2+1..N down row 2 (idx n-1 → half)
		for i := 0; i < half; i++ {
			pinNums[i] = half - i
		}
		for i := half; i < n; i++ {
			pinNums[i] = n + half - i
		}
	case 2: // pin 1 at idx half (bottom of row 2)
		// 1..N/2 up row 2 (idx half → n-1), N/2+1..N down row 1 (idx 0 → half-1)
		for i := 0; i < half; i++ {
			pinNums[i] = half + 1 + i
		}
		for i := half; i < n; i++ {
			pinNums[i] = i - half + 1
		}
	case 3: // pin 1 at idx n-1 (top of row 2)
		// 1..N/2 down row 2, N/2+1..N up row 1
		for i := 0; i < n; i++ {
			pinNums[i] = n - i
		}
	}

	// ── Phase 6: create ConfirmedVias ──
	var results []*via.ConfirmedVia

	for i := range expectedPins {
		if !valid[i] {
			continue
		}

		pinNum := pinNums[i]

		fc := fittedCenters[i]
		id := fmt.Sprintf("cvia-%03d", nextID())

		var boundary []geometry.Point2D
		if pinNum == 1 {
			boundary = generateSquarePoints(fc.X, fc.Y, consensusRadius)
		} else {
			boundary = geometry.GenerateCirclePoints(fc.X, fc.Y, consensusRadius, 32)
		}

		cv := &via.ConfirmedVia{
			ID:                   id,
			Center:               fc,
			Radius:               consensusRadius,
			IntersectionBoundary: boundary,
			Confidence:           0.9,
			ComponentID:          comp.ID,
			PinNumber:            strconv.Itoa(pinNum),
		}
		results = append(results, cv)
		fmt.Printf("  Pin %d: (%.0f,%.0f) r=%.1f\n", pinNum, fc.X, fc.Y, consensusRadius)
	}

	return results
}

// validatePadCenter checks that the grid-fitted position is on a real solder pad
// by looking for metallic content in the interior. The inner 70% of the radius
// (outside the drill hole, inside the pad boundary) should contain solder.
// Returns true if the position is valid (sufficient metallic content).
func validatePadCenter(img image.Image, cx, cy, radius float64, imgW, imgH int) bool {
	// Check inner disk (0 to 70% of radius) for metallic pixels.
	// This avoids the boundary zone where green solder mask naturally appears
	// and where traces may extend the solder asymmetrically.
	checkR := 0.7 * radius
	checkSq := checkR * checkR
	r := int(checkR) + 1

	metallicCount := 0
	totalCount := 0
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			dSq := float64(dx*dx + dy*dy)
			if dSq > checkSq {
				continue
			}
			px := int(cx) + dx
			py := int(cy) + dy
			if px < 0 || py < 0 || px >= imgW || py >= imgH {
				continue
			}
			totalCount++
			r32, g32, b32, _ := img.At(px, py).RGBA()
			rf := float64(r32 >> 8)
			gf := float64(g32 >> 8)
			bf := float64(b32 >> 8)
			_, s, v := colorutil.RGBToHSV(rf, gf, bf)
			if v > 100 && s < 120 {
				metallicCount++
			}
		}
	}

	if totalCount == 0 {
		return false
	}
	// At least 25% metallic — tolerates large drill holes (up to ~75% of inner area)
	// while rejecting positions on green solder mask (0% metallic).
	return float64(metallicCount)/float64(totalCount) > 0.25
}

// fitRigidGrid computes the optimal translation (no rotation) mapping expected
// positions to detected centers, with outlier rejection. DIP holes are drilled
// by machine on a perfectly axis-aligned grid, so rotation is never correct —
// any apparent tilt comes from asymmetric solder pulling center estimates.
// Returns the fitted position for every expected pin.
func fitRigidGrid(expected, detected []geometry.Point2D, mask []bool, pitchPx float64) []geometry.Point2D {
	n := len(expected)
	result := make([]geometry.Point2D, n)

	inlier := make([]bool, n)
	copy(inlier, mask)

	for pass := 0; pass < 2; pass++ {
		cnt := countTrue(inlier)
		if cnt == 0 {
			copy(result, expected)
			return result
		}

		// Translation only: average offset from expected to detected
		var tx, ty float64
		for i := 0; i < n; i++ {
			if !inlier[i] {
				continue
			}
			tx += detected[i].X - expected[i].X
			ty += detected[i].Y - expected[i].Y
		}
		cf := float64(cnt)
		tx /= cf
		ty /= cf

		for i := 0; i < n; i++ {
			result[i] = geometry.Point2D{
				X: expected[i].X + tx,
				Y: expected[i].Y + ty,
			}
		}

		if pass == 0 {
			threshold := pitchPx * 0.3
			for i := 0; i < n; i++ {
				if !mask[i] {
					continue
				}
				dx := detected[i].X - result[i].X
				dy := detected[i].Y - result[i].Y
				dist := math.Sqrt(dx*dx + dy*dy)
				inlier[i] = dist <= threshold
				if !inlier[i] {
					fmt.Printf("  Grid fit: rejecting idx %d (residual=%.1f)\n", i, dist)
				}
			}
		}
	}

	return result
}

func countTrue(b []bool) int {
	c := 0
	for _, v := range b {
		if v {
			c++
		}
	}
	return c
}

// refineCenterFromGreen starts from a rough center (must be ON the pad), scans
// radially to find the green solder mask boundary, then iteratively shifts the
// center toward the geometric center of the boundary.
func refineCenterFromGreen(img image.Image, startX, startY, maxDist float64, imgW, imgH int) (centerX, centerY float64) {
	const (
		numRays    = 16
		iterations = 3
	)

	centerX, centerY = startX, startY

	for iter := 0; iter < iterations; iter++ {
		distances := radialScanToGreen(img, centerX, centerY, maxDist, imgW, imgH)

		nf := float64(numRays)
		var sumDCos, sumDSin float64
		for i := 0; i < numRays; i++ {
			angle := float64(i) * 2.0 * math.Pi / float64(numRays)
			sumDCos += distances[i] * math.Cos(angle)
			sumDSin += distances[i] * math.Sin(angle)
		}

		centerX += 2.0 / nf * sumDCos
		centerY += 2.0 / nf * sumDSin
	}

	return centerX, centerY
}

// radialScanToGreen measures the pad boundary distance along each ray using two
// complementary methods and taking the best result:
//
// Method A (ring scan): Scan from OUTSIDE IN to find the inner edge of the green
// solder mask ring. This handles interior "donut" reflections — green spots inside
// the solder are never reached because we stop at the first ring exit.
//
// Method B (brightness gradient): Scan outward from center to find where brightness
// drops below the midpoint of peak-vs-background. This handles edge reflections —
// greenish tint at the transition zone that makes Method A measure too small.
//
// Per-ray result: use Method A, but let Method B expand it (up to 1.5×) when the
// green scan underestimates due to edge reflections.
func radialScanToGreen(img image.Image, cx, cy, maxDist float64, imgW, imgH int) []float64 {
	const (
		numRays     = 16
		greenHueMin = 35.0 // OpenCV H range for green
		greenHueMax = 85.0
		greenSatMin = 60.0 // Minimum saturation to be "green"
		greenRun    = 3    // Consecutive green pixels to confirm ring entry
		nonGreenRun = 3    // Consecutive non-green pixels to confirm pad edge
	)

	maxD := int(maxDist)
	distances := make([]float64, numRays)

	for i := 0; i < numRays; i++ {
		angle := float64(i) * 2.0 * math.Pi / float64(numRays)
		dirX := math.Cos(angle)
		dirY := math.Sin(angle)

		// Collect brightness and green flag along the full ray
		vv := make([]float64, 0, maxD+1)
		gg := make([]bool, 0, maxD+1)
		for d := 0; d <= maxD; d++ {
			px := int(cx + dirX*float64(d))
			py := int(cy + dirY*float64(d))
			if px < 0 || py < 0 || px >= imgW || py >= imgH {
				break
			}
			r32, g32, b32, _ := img.At(px, py).RGBA()
			rf := float64(r32 >> 8)
			gf := float64(g32 >> 8)
			bf := float64(b32 >> 8)
			h, s, v := colorutil.RGBToHSV(rf, gf, bf)
			vv = append(vv, v)
			gg = append(gg, h >= greenHueMin && h <= greenHueMax && s >= greenSatMin)
		}
		vLen := len(vv)

		// ── Method A: outside-in scan for green ring inner edge ──
		greenEdge := maxDist
		{
			foundGreen := false
			gR, ngR := 0, 0
			for d := vLen - 1; d >= 1; d-- {
				if gg[d] {
					gR++
					ngR = 0
					if gR >= greenRun {
						foundGreen = true
					}
				} else {
					gR = 0
					if foundGreen {
						ngR++
						if ngR >= nonGreenRun {
							greenEdge = float64(d + nonGreenRun)
							break
						}
					}
				}
			}
		}

		// ── Method B: brightness threshold crossing ──
		gradEdge := maxDist
		if vLen > 6 {
			// Peak brightness along the ray
			peakV := 0.0
			peakD := 0
			for d := 0; d < vLen; d++ {
				if vv[d] > peakV {
					peakV = vv[d]
					peakD = d
				}
			}
			// Background from outer quarter
			outerStart := vLen * 3 / 4
			var bgSum float64
			bgN := 0
			for d := outerStart; d < vLen; d++ {
				bgSum += vv[d]
				bgN++
			}
			if bgN > 0 && peakV > 100 {
				bgV := bgSum / float64(bgN)
				thresh := (peakV + bgV) / 2.0
				// Scan outward from peak, find sustained drop below threshold
				belowRun := 0
				for d := peakD; d < vLen; d++ {
					if vv[d] < thresh {
						belowRun++
						if belowRun >= 3 {
							gradEdge = float64(d - belowRun + 1)
							break
						}
					} else {
						belowRun = 0
					}
				}
			}
		}

		// Combine: use green ring edge, but let gradient expand it when the
		// ring scan underestimates due to edge reflections. Cap at 1.5× to
		// prevent wild overestimates from traces or other bright features.
		dist := greenEdge
		if greenEdge < maxDist && gradEdge > greenEdge && gradEdge < maxDist && gradEdge <= greenEdge*1.5 {
			dist = gradEdge
		}
		distances[i] = dist
	}
	return distances
}

// countSquareCorners checks the 4 diagonal corners at 1.15× radius from center.
// A square pad has bright metallic pixels there (inside the square corners),
// while a round pad has green solder mask (outside the inscribed circle).
// Returns 0-4 indicating how many corners are bright.
func countSquareCorners(img image.Image, cx, cy, radius float64, imgW, imgH int) int {
	// At distance 1.15r from center in diagonal directions:
	// - Square pad (half-side ≈ r): diagonal ≈ 1.414r → inside (bright)
	// - Round pad (radius r): 1.15r > r → outside (dark/green)
	d := radius * 1.15 / math.Sqrt(2)
	bright := 0
	for _, off := range [4][2]float64{{d, d}, {d, -d}, {-d, d}, {-d, -d}} {
		px := int(cx + off[0])
		py := int(cy + off[1])
		if px < 0 || py < 0 || px >= imgW || py >= imgH {
			continue
		}
		r32, g32, b32, _ := img.At(px, py).RGBA()
		rf := float64(r32 >> 8)
		gf := float64(g32 >> 8)
		bf := float64(b32 >> 8)
		_, s, v := colorutil.RGBToHSV(rf, gf, bf)
		if v > 100 && s < 120 {
			bright++
		}
	}
	return bright
}

// generateSquarePoints returns a square boundary polygon centered at (cx, cy)
// with half-side length equal to radius.
func generateSquarePoints(cx, cy, radius float64) []geometry.Point2D {
	return []geometry.Point2D{
		{X: cx - radius, Y: cy - radius},
		{X: cx + radius, Y: cy - radius},
		{X: cx + radius, Y: cy + radius},
		{X: cx - radius, Y: cy + radius},
	}
}

// ResolveSignalName looks up the pin's function name from the parts library and sets
// cv.SignalName (e.g. "C3-GND"). Returns the pin direction so callers can decide
// whether to rename nets (output pins drive net names).
func ResolveSignalName(cv *via.ConfirmedVia, components []*Component, lib *ComponentLibrary) connector.SignalDirection {
	if cv.ComponentID == "" || cv.PinNumber == "" {
		return connector.DirectionInput
	}

	// Find the board component
	var comp *Component
	for _, c := range components {
		if c.ID == cv.ComponentID {
			comp = c
			break
		}
	}
	if comp == nil || comp.PartNumber == "" || comp.Package == "" {
		return connector.DirectionInput
	}

	// Look up part definition
	if lib == nil {
		return connector.DirectionInput
	}
	partDef := lib.GetByAlias(comp.PartNumber, comp.Package)
	if partDef == nil {
		return connector.DirectionInput
	}

	// Find the pin by number
	pinNum, err := strconv.Atoi(cv.PinNumber)
	if err != nil {
		return connector.DirectionInput
	}

	var pinName string
	var pinDir connector.SignalDirection
	for _, pin := range partDef.Pins {
		if pin.Number == pinNum {
			pinName = pin.Name
			pinDir = pin.Direction
			break
		}
	}
	if pinName == "" {
		return connector.DirectionInput
	}

	cv.SignalName = cv.ComponentID + "-" + pinName
	fmt.Printf("Resolved signal name: %s pin %d = %s (%s)\n", cv.ComponentID, pinNum, cv.SignalName, pinDir)
	return pinDir
}

// ParseDIPPinCount extracts the pin count from a DIP package string (e.g. "DIP-16" → 16).
func ParseDIPPinCount(pkg string) (int, bool) {
	pkg = strings.ToUpper(strings.TrimSpace(pkg))
	if !strings.HasPrefix(pkg, "DIP-") {
		return 0, false
	}
	n, err := strconv.Atoi(pkg[4:])
	if err != nil || n < 4 || n%2 != 0 {
		return 0, false
	}
	return n, true
}
