package via

import (
	"fmt"
	"image"
	"math"
	"sort"
	"sync"
	"time"

	img "pcb-tracer/internal/image"
	"pcb-tracer/pkg/geometry"

	"gocv.io/x/gocv"
)

// DetectViasFromImage detects vias from a Go image.Image.
func DetectViasFromImage(srcImg image.Image, side img.Side, params DetectionParams) (*ViaDetectionResult, error) {
	mat, err := imageToMat(srcImg)
	if err != nil {
		return nil, fmt.Errorf("failed to convert image: %w", err)
	}
	defer mat.Close()

	return DetectVias(mat, side, params)
}

// DetectViaLocations detects vias and returns just their centers and radii.
// This is the general-purpose entry point for callers that just need via positions.
func DetectViaLocations(srcImg image.Image, side img.Side, dpi float64) ([]ViaLocation, error) {
	params := DefaultParams().WithDPI(dpi)
	result, err := DetectViasFromImage(srcImg, side, params)
	if err != nil {
		return nil, err
	}
	locs := make([]ViaLocation, len(result.Vias))
	for i, v := range result.Vias {
		locs[i] = ViaLocation{Center: v.Center, Radius: v.Radius}
	}
	return locs, nil
}

// DetectVias detects vias in an OpenCV Mat using a hybrid pipeline:
// grayscale distance-transform finds candidates, then color analysis
// confirms metallic pad material (low saturation) vs solder mask artifacts.
func DetectVias(srcImg gocv.Mat, side img.Side, params DetectionParams) (*ViaDetectionResult, error) {
	if srcImg.Empty() {
		return nil, fmt.Errorf("empty image")
	}

	result := &ViaDetectionResult{
		Side:   side,
		DPI:    params.DPI,
		Params: params,
	}

	// Validate radius parameters
	if params.MinRadiusPixels <= 0 || params.MaxRadiusPixels <= 0 {
		return nil, fmt.Errorf("invalid radius parameters: min=%d, max=%d (set DPI first)",
			params.MinRadiusPixels, params.MaxRadiusPixels)
	}

	// Convert to grayscale for geometry detection
	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(srcImg, &gray, gocv.ColorBGRToGray)

	// Convert to HSV for color confirmation
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(srcImg, &hsv, gocv.ColorBGRToHSV)

	// Create brightness mask from grayscale
	brightMask := createBrightMask(gray, params)
	defer brightMask.Close()

	// Step 1: Distance transform to find centers of round bright regions.
	candidates := findDistTransformPeaks(brightMask, params, side)

	// Step 2: Verify radial symmetry and contrast
	verified := verifyRadialSymmetry(candidates, brightMask, gray, params)

	// Step 3: Color confirmation — reject candidates that are solder mask
	// (high saturation) rather than metallic via pads (low saturation)
	verified = confirmMetallicColor(verified, hsv, params)

	// Step 4: Optional Hough cross-validation
	if params.RequireHoughConfirm {
		blurred := gocv.NewMat()
		defer blurred.Close()
		gocv.GaussianBlur(gray, &blurred, image.Point{9, 9}, 2, 2, gocv.BorderDefault)

		houghCandidates := detectHoughCenters(blurred, brightMask, params)
		verified = crossValidateWithHough(verified, houghCandidates, params)
	}

	// Step 5: Deduplicate (prefer largest radius)
	result.Vias = deduplicateVias(verified, params)

	// Step 6: Refine center and find outer radius for each via
	for i := range result.Vias {
		refineViaGeometry(&result.Vias[i], brightMask)
	}

	// Assign IDs
	for i := range result.Vias {
		result.Vias[i].ID = fmt.Sprintf("via-%s-%03d", side.String()[:1], i+1)
	}

	return result, nil
}

// createBrightMask creates a binary mask of bright regions using grayscale
// thresholding. Vias are bright round blobs — hue and saturation are
// irrelevant and fragile for non-uniform surfaces.
func createBrightMask(gray gocv.Mat, params DetectionParams) gocv.Mat {
	// Light blur to reduce pixel noise without smearing small vias
	blurred := gocv.NewMat()
	defer blurred.Close()
	gocv.GaussianBlur(gray, &blurred, image.Point{5, 5}, 0, 0, gocv.BorderDefault)

	// Simple brightness threshold
	mask := gocv.NewMat()
	gocv.Threshold(blurred, &mask, float32(params.ValMin), 255, gocv.ThresholdBinary)

	// Morphological close to fill small gaps (e.g. drill holes in vias)
	closeKernel := gocv.GetStructuringElement(gocv.MorphEllipse, image.Point{5, 5})
	defer closeKernel.Close()
	gocv.MorphologyEx(mask, &mask, gocv.MorphClose, closeKernel)

	// Morphological open with smaller kernel to remove noise without eroding vias
	openKernel := gocv.GetStructuringElement(gocv.MorphEllipse, image.Point{3, 3})
	defer openKernel.Close()
	gocv.MorphologyEx(mask, &mask, gocv.MorphOpen, openKernel)

	return mask
}

// findDistTransformPeaks finds via candidates using the distance transform.
// For each bright pixel, the distance transform gives the distance to the nearest
// dark pixel. Local maxima correspond to centers of round bright regions —
// a via pad connected to a narrow trace still peaks at the pad center because
// the trace is narrow and produces small distance values.
func findDistTransformPeaks(mask gocv.Mat, params DetectionParams, side img.Side) []Via {
	dist := gocv.NewMat()
	defer dist.Close()
	labels := gocv.NewMat()
	defer labels.Close()
	gocv.DistanceTransform(mask, &dist, &labels, gocv.DistL2, gocv.DistanceMask5, gocv.DistanceLabelCComp)

	// Dilate to find local maxima efficiently: a pixel is a peak if its
	// value equals the dilated value (i.e., it's the max in its neighborhood).
	kernelSize := 2*params.MinRadiusPixels + 1
	if kernelSize%2 == 0 {
		kernelSize++
	}
	peakKernel := gocv.GetStructuringElement(gocv.MorphEllipse, image.Point{kernelSize, kernelSize})
	defer peakKernel.Close()

	dilated := gocv.NewMat()
	defer dilated.Close()
	gocv.Dilate(dist, &dilated, peakKernel)

	minR := float32(params.MinRadiusPixels)
	maxR := float32(params.MaxRadiusPixels)
	margin := params.MaxRadiusPixels
	rows, cols := dist.Rows(), dist.Cols()

	var vias []Via
	for y := margin; y < rows-margin; y++ {
		for x := margin; x < cols-margin; x++ {
			val := dist.GetFloatAt(y, x)
			if val < minR || val > maxR {
				continue
			}
			// Local maximum: value equals dilated value
			if val < dilated.GetFloatAt(y, x) {
				continue
			}
			vias = append(vias, Via{
				Center: geometry.Point2D{X: float64(x), Y: float64(y)},
				Radius: float64(val),
				Side:   side,
				Method: MethodContourFit,
			})
		}
	}
	return vias
}

// verifyRadialSymmetry checks each candidate by walking outward from the center
// at multiple angles and measuring where the bright mask ends. A round via has
// a uniform transition radius in all directions; a via merged with a trace will
// have some directions that extend much farther.
func verifyRadialSymmetry(candidates []Via, mask, gray gocv.Mat, params DetectionParams) []Via {
	var verified []Via
	for _, v := range candidates {
		symmetry := computeRadialSymmetry(mask, v.Center, v.Radius)
		if symmetry < params.CircularityMin {
			continue
		}

		contrast := computeContrast(gray, v.Center, v.Radius)
		if contrast < params.ContrastMin {
			continue
		}

		v.Circularity = symmetry
		v.Confidence = symmetry * math.Min(contrast/2.0, 1.0)
		verified = append(verified, v)
	}
	return verified
}

// confirmMetallicColor checks each candidate via against the HSV color image
// to confirm it's a metallic pad (low saturation, bright) rather than a solder
// mask artifact or silkscreen (high saturation, specific hue). Via pads are
// silver/tin — they have low saturation regardless of brightness variations.
func confirmMetallicColor(candidates []Via, hsv gocv.Mat, params DetectionParams) []Via {
	rows, cols := hsv.Rows(), hsv.Cols()

	var confirmed []Via
	for _, v := range candidates {
		cx, cy := int(v.Center.X+0.5), int(v.Center.Y+0.5)
		r := int(v.Radius + 0.5)
		if r < 1 {
			r = 1
		}

		// Sample saturation inside the candidate circle
		var satSum float64
		var count int
		for dy := -r; dy <= r; dy++ {
			for dx := -r; dx <= r; dx++ {
				if dx*dx+dy*dy > r*r {
					continue
				}
				px, py := cx+dx, cy+dy
				if px < 0 || px >= cols || py < 0 || py >= rows {
					continue
				}
				// HSV is 3 channels: H=0, S=1, V=2
				sat := float64(hsv.GetUCharAt(py, px*3+1))
				satSum += sat
				count++
			}
		}

		if count == 0 {
			continue
		}

		avgSat := satSum / float64(count)
		// Metallic surfaces have low saturation (< SatMax).
		// Solder mask is typically green/red with saturation > 100.
		if avgSat <= params.SatMax {
			confirmed = append(confirmed, v)
		}
	}
	return confirmed
}

// computeRadialSymmetry walks outward from center at 32 evenly-spaced angles,
// finding where the bright mask edge is in each direction. It computes a
// circularity score using median-based outlier rejection:
//
//  1. Collect 32 transition distances (center to mask edge)
//  2. Compute median distance as the "expected outer radius"
//  3. Classify outliers: distance > 1.5× median (trace connections extending far)
//  4. Inlier fraction: what portion of directions agree with the median
//  5. Inlier uniformity: 1 - CV(inlier distances), where CV = stddev/mean
//  6. Score = inlierFraction × uniformity
//
// A perfect circle scores 1.0. A via with 2-3 trace connections scores ~0.8.
// An irregular or elongated blob scores below 0.5.
func computeRadialSymmetry(mask gocv.Mat, center geometry.Point2D, radius float64) float64 {
	const numAngles = 32
	cx, cy := center.X, center.Y
	rows, cols := mask.Rows(), mask.Cols()
	maxWalk := radius * 3.0

	// Walk each angle to find transition distance
	dists := make([]float64, numAngles)
	for i := 0; i < numAngles; i++ {
		angle := float64(i) * 2.0 * math.Pi / numAngles
		dx := math.Cos(angle)
		dy := math.Sin(angle)

		transR := maxWalk
		for step := 1.0; step <= maxWalk; step += 1.0 {
			px := int(cx + dx*step + 0.5)
			py := int(cy + dy*step + 0.5)
			if px < 0 || px >= cols || py < 0 || py >= rows || mask.GetUCharAt(py, px) == 0 {
				transR = step
				break
			}
		}
		dists[i] = transR
	}

	// Compute median transition distance as robust "expected radius"
	sorted := make([]float64, numAngles)
	copy(sorted, dists)
	sort.Float64s(sorted)
	median := sorted[numAngles/2]

	if median < 2.0 {
		return 0 // Too small to be meaningful
	}

	// Classify inliers: transition within 1.5× median.
	// Trace connections extend much farther and become outliers.
	outlierThreshold := median * 1.5
	var inlierDists []float64
	for _, d := range dists {
		if d <= outlierThreshold {
			inlierDists = append(inlierDists, d)
		}
	}

	inlierFraction := float64(len(inlierDists)) / numAngles
	if len(inlierDists) < 4 {
		return 0
	}

	// Compute uniformity: 1 - CV(inlier distances)
	// CV = coefficient of variation = stddev / mean
	// Low CV means inlier boundary distances are tightly clustered → circular
	var sum float64
	for _, d := range inlierDists {
		sum += d
	}
	mean := sum / float64(len(inlierDists))

	var variance float64
	for _, d := range inlierDists {
		diff := d - mean
		variance += diff * diff
	}
	variance /= float64(len(inlierDists))
	stddev := math.Sqrt(variance)

	cv := stddev / mean
	uniformity := 1.0 - cv
	if uniformity < 0 {
		uniformity = 0
	}

	return inlierFraction * uniformity
}

// refineViaGeometry improves the via center and finds the outer radius.
// It walks outward at many angles, uses median-based outlier rejection
// (same as computeRadialSymmetry) to discard trace-connection directions,
// and computes a refined center and outer radius from the inlier boundary.
func refineViaGeometry(v *Via, mask gocv.Mat) {
	const numAngles = 32
	cx, cy := v.Center.X, v.Center.Y
	inscribed := v.Radius
	rows, cols := mask.Rows(), mask.Cols()
	maxWalk := inscribed * 3.0

	// Walk outward at each angle to find boundary points
	type boundaryPt struct {
		x, y float64
		dist float64
	}
	pts := make([]boundaryPt, numAngles)
	rawDists := make([]float64, numAngles)

	for i := 0; i < numAngles; i++ {
		angle := float64(i) * 2.0 * math.Pi / numAngles
		dx := math.Cos(angle)
		dy := math.Sin(angle)

		transR := maxWalk
		for step := 1.0; step <= maxWalk; step += 1.0 {
			px := int(cx + dx*step + 0.5)
			py := int(cy + dy*step + 0.5)
			if px < 0 || px >= cols || py < 0 || py >= rows || mask.GetUCharAt(py, px) == 0 {
				transR = step
				break
			}
		}
		pts[i] = boundaryPt{x: cx + dx*transR, y: cy + dy*transR, dist: transR}
		rawDists[i] = transR
	}

	// Median-based outlier rejection (consistent with computeRadialSymmetry)
	sorted := make([]float64, numAngles)
	copy(sorted, rawDists)
	sort.Float64s(sorted)
	median := sorted[numAngles/2]
	outlierThreshold := median * 1.5

	var inliers []boundaryPt
	for _, p := range pts {
		if p.dist <= outlierThreshold {
			inliers = append(inliers, p)
		}
	}
	if len(inliers) < 4 {
		return
	}

	// Refined center: centroid of inlier boundary points
	var sx, sy float64
	for _, p := range inliers {
		sx += p.x
		sy += p.y
	}
	newCX := sx / float64(len(inliers))
	newCY := sy / float64(len(inliers))

	// Outer radius: median distance from new center to inlier boundary points
	dists := make([]float64, len(inliers))
	for i, p := range inliers {
		dx := p.x - newCX
		dy := p.y - newCY
		dists[i] = math.Sqrt(dx*dx + dy*dy)
	}
	sort.Float64s(dists)
	outerRadius := dists[len(dists)/2]

	// Only update if the refinement makes sense
	if outerRadius >= inscribed*0.5 && outerRadius <= inscribed*2.0 {
		v.Center = geometry.Point2D{X: newCX, Y: newCY}
		v.Radius = outerRadius
	}
}

// computeContrast computes the ratio of mean grayscale brightness inside the
// circle vs. an annular ring from 1.5r to 2.5r outside the circle.
// The wider gap ensures we're sampling actual background, not pad edge.
func computeContrast(gray gocv.Mat, center geometry.Point2D, radius float64) float64 {
	cx, cy := int(center.X+0.5), int(center.Y+0.5)
	r := int(radius + 0.5)
	innerR := int(radius*1.5 + 0.5)
	outerR := int(radius*2.5 + 0.5)
	rows, cols := gray.Rows(), gray.Cols()

	var innerSum, outerSum float64
	var innerCount, outerCount int

	for dy := -outerR; dy <= outerR; dy++ {
		for dx := -outerR; dx <= outerR; dx++ {
			d2 := dx*dx + dy*dy
			px, py := cx+dx, cy+dy
			if px < 0 || px >= cols || py < 0 || py >= rows {
				continue
			}
			val := float64(gray.GetUCharAt(py, px))
			if d2 <= r*r {
				innerSum += val
				innerCount++
			} else if d2 >= innerR*innerR && d2 <= outerR*outerR {
				outerSum += val
				outerCount++
			}
		}
	}

	if innerCount == 0 || outerCount == 0 {
		return 0
	}
	outerMean := outerSum / float64(outerCount)
	if outerMean == 0 {
		return 0
	}
	return (innerSum / float64(innerCount)) / outerMean
}

// houghCandidate holds a Hough-detected circle center and radius.
type houghCandidate struct {
	center geometry.Point2D
	radius float64
}

// detectHoughCenters runs Hough circle detection for cross-validation only.
func detectHoughCenters(gray, mask gocv.Mat, params DetectionParams) []houghCandidate {
	masked := gocv.NewMat()
	defer masked.Close()
	gray.CopyToWithMask(&masked, mask)

	circles := gocv.NewMat()
	defer circles.Close()

	gocv.HoughCirclesWithParams(masked, &circles, gocv.HoughGradient,
		params.HoughDP, float64(params.HoughMinDist),
		params.HoughParam1, params.HoughParam2,
		params.MinRadiusPixels, params.MaxRadiusPixels)

	if circles.Empty() || circles.Cols() == 0 {
		return nil
	}

	candidates := make([]houghCandidate, circles.Cols())
	for i := 0; i < circles.Cols(); i++ {
		candidates[i] = houghCandidate{
			center: geometry.Point2D{
				X: float64(circles.GetFloatAt(0, i*3)),
				Y: float64(circles.GetFloatAt(0, i*3+1)),
			},
			radius: float64(circles.GetFloatAt(0, i*3+2)),
		}
	}
	return candidates
}

// crossValidateWithHough keeps only contour-detected vias that have a
// corresponding Hough detection within one radius distance.
func crossValidateWithHough(contourVias []Via, houghCandidates []houghCandidate, params DetectionParams) []Via {
	if len(houghCandidates) == 0 {
		return nil
	}

	var confirmed []Via
	for _, v := range contourVias {
		for _, h := range houghCandidates {
			dist := distance(v.Center, h.center)
			if dist < v.Radius {
				confirmed = append(confirmed, v)
				break
			}
		}
	}
	return confirmed
}

// deduplicateVias removes overlapping detections, preferring the largest radius.
func deduplicateVias(vias []Via, params DetectionParams) []Via {
	if len(vias) <= 1 {
		return vias
	}

	threshold := float64(params.MinRadiusPixels)

	// Sort by radius (descending) so we keep the largest detections
	sort.Slice(vias, func(i, j int) bool {
		return vias[i].Radius > vias[j].Radius
	})

	var result []Via
	for _, v := range vias {
		isDup := false
		for i := range result {
			if distance(v.Center, result[i].Center) < threshold {
				isDup = true
				break
			}
		}
		if !isDup {
			result = append(result, v)
		}
	}

	return result
}

// distance calculates Euclidean distance between two points.
func distance(a, b geometry.Point2D) float64 {
	dx := a.X - b.X
	dy := a.Y - b.Y
	return math.Sqrt(dx*dx + dy*dy)
}

// imageToMat converts a Go image.Image to an OpenCV Mat.
func imageToMat(srcImg image.Image) (gocv.Mat, error) {
	bounds := srcImg.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	mat := gocv.NewMatWithSize(h, w, gocv.MatTypeCV8UC3)

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b, _ := srcImg.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			// Convert from 16-bit to 8-bit and BGR order for OpenCV
			mat.SetUCharAt(y, x*3+0, uint8(b>>8))
			mat.SetUCharAt(y, x*3+1, uint8(g>>8))
			mat.SetUCharAt(y, x*3+2, uint8(r>>8))
		}
	}

	return mat, nil
}

// SampleViaColors samples HSV values from a region to calibrate detection.
// Returns average H, S, V values for the region.
func SampleViaColors(srcImg gocv.Mat, region geometry.RectInt) (avgH, avgS, avgV float64, err error) {
	if srcImg.Empty() {
		return 0, 0, 0, fmt.Errorf("empty image")
	}

	// Clamp region to image bounds
	x1 := clamp(region.X, 0, srcImg.Cols()-1)
	y1 := clamp(region.Y, 0, srcImg.Rows()-1)
	x2 := clamp(region.X+region.Width, 1, srcImg.Cols())
	y2 := clamp(region.Y+region.Height, 1, srcImg.Rows())

	if x2 <= x1 || y2 <= y1 {
		return 0, 0, 0, fmt.Errorf("invalid region")
	}

	// Extract region
	roi := srcImg.Region(image.Rect(x1, y1, x2, y2))
	defer roi.Close()

	// Convert to HSV
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(roi, &hsv, gocv.ColorBGRToHSV)

	// Sample pixels
	var totalH, totalS, totalV float64
	var count int

	for y := 0; y < hsv.Rows(); y++ {
		for x := 0; x < hsv.Cols(); x++ {
			totalH += float64(hsv.GetUCharAt(y, x*3+0))
			totalS += float64(hsv.GetUCharAt(y, x*3+1))
			totalV += float64(hsv.GetUCharAt(y, x*3+2))
			count++
		}
	}

	if count == 0 {
		return 0, 0, 0, fmt.Errorf("no pixels sampled")
	}

	return totalH / float64(count), totalS / float64(count), totalV / float64(count), nil
}

// ParamsFromSample creates detection params based on sampled HSV values.
// Adds tolerance around the sampled values.
func ParamsFromSample(avgH, avgS, avgV float64, tolerance float64) DetectionParams {
	p := DefaultParams()

	// Apply tolerance to create ranges
	hTol := tolerance / 4 // Hue has smaller range in OpenCV (0-180)
	sTol := tolerance
	vTol := tolerance

	p.HueMin = clampF(avgH-hTol, 0, 180)
	p.HueMax = clampF(avgH+hTol, 0, 180)
	p.SatMin = clampF(avgS-sTol, 0, 255)
	p.SatMax = clampF(avgS+sTol, 0, 255)
	p.ValMin = clampF(avgV-vTol, 0, 255)
	p.ValMax = clampF(avgV+vTol, 0, 255)

	return p
}

func clamp(v, minVal, maxVal int) int {
	if v < minVal {
		return minVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}

func clampF(v, minVal, maxVal float64) float64 {
	if v < minVal {
		return minVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}

// DetectViasAsync runs via detection in a goroutine and returns results via channel.
func DetectViasAsync(srcImg image.Image, side img.Side, params DetectionParams) <-chan *ViaDetectionResult {
	ch := make(chan *ViaDetectionResult, 1)

	go func() {
		defer close(ch)
		result, err := DetectViasFromImage(srcImg, side, params)
		if err != nil {
			// Return empty result on error
			ch <- &ViaDetectionResult{
				Side:   side,
				Params: params,
			}
			return
		}
		ch <- result
	}()

	return ch
}

// BatchDetectVias detects vias on multiple images concurrently.
func BatchDetectVias(images []image.Image, sides []img.Side, params DetectionParams) []*ViaDetectionResult {
	if len(images) != len(sides) {
		return nil
	}

	results := make([]*ViaDetectionResult, len(images))
	var wg sync.WaitGroup

	for i := range images {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			result, _ := DetectViasFromImage(images[idx], sides[idx], params)
			if result == nil {
				result = &ViaDetectionResult{Side: sides[idx], Params: params}
			}
			results[idx] = result
		}(i)
	}

	wg.Wait()
	return results
}

// DetectViaAtPoint finds a via pad around the click point (x, y) using the
// same iterative vector-shift technique as pin detection (refineCenterFromGreen).
// Walk outward at multiple angles, measure distance to the pad boundary, then
// shift the center by the first Fourier harmonic of the distance function.
// Repeat 3 times to converge on the true center.  Radius uses the 25th
// percentile of walk distances (robust to trace connections).
func DetectViaAtPoint(srcImg image.Image, x, y, dpi float64) (center geometry.Point2D, radius float64, found bool) {
	bounds := srcImg.Bounds()
	ix, iy := int(x+0.5), int(y+0.5)
	if ix < bounds.Min.X || ix >= bounds.Max.X || iy < bounds.Min.Y || iy >= bounds.Max.Y {
		return geometry.Point2D{}, 0, false
	}

	const threshold uint8 = 140
	if grayAt(srcImg, ix, iy) < threshold {
		return geometry.Point2D{}, 0, false
	}

	maxWalk := 0.120 * dpi
	if maxWalk < 40 {
		maxWalk = 40
	}

	const numAngles = 32
	const iterations = 3

	// Precompute angle unit vectors.
	cosA := make([]float64, numAngles)
	sinA := make([]float64, numAngles)
	for i := 0; i < numAngles; i++ {
		a := float64(i) * 2.0 * math.Pi / float64(numAngles)
		cosA[i] = math.Cos(a)
		sinA[i] = math.Sin(a)
	}

	cx, cy := x, y
	var distances []float64

	for iter := 0; iter < iterations; iter++ {
		distances = make([]float64, numAngles)
		for i := 0; i < numAngles; i++ {
			transR := maxWalk
			for step := 1.0; step <= maxWalk; step += 1.0 {
				px := int(cx + cosA[i]*step + 0.5)
				py := int(cy + sinA[i]*step + 0.5)
				if px < bounds.Min.X || px >= bounds.Max.X ||
					py < bounds.Min.Y || py >= bounds.Max.Y {
					transR = step
					break
				}
				if grayAt(srcImg, px, py) < threshold {
					transR = step
					break
				}
			}
			distances[i] = transR
		}

		// Vector shift toward geometric center — first Fourier harmonic
		// of the boundary distance function.  Same formula as
		// refineCenterFromGreen in pin detection.
		nf := float64(numAngles)
		var sumDCos, sumDSin float64
		for i := 0; i < numAngles; i++ {
			sumDCos += distances[i] * cosA[i]
			sumDSin += distances[i] * sinA[i]
		}
		cx += 2.0 / nf * sumDCos
		cy += 2.0 / nf * sumDSin
	}

	// Radius: 25th percentile of final walk distances (robust to traces).
	sorted := make([]float64, numAngles)
	copy(sorted, distances)
	sort.Float64s(sorted)
	fitRadius := sorted[numAngles/4]

	// Lenient range check for manual placement — the user clicked here
	// intentionally, so accept smaller pads than the batch detector would.
	if dpi > 0 {
		minR := 0.008 * dpi // ~5px at 600 DPI
		maxR := 0.065 * dpi
		if fitRadius < minR || fitRadius > maxR {
			return geometry.Point2D{}, 0, false
		}
	}

	return geometry.Point2D{X: cx, Y: cy}, fitRadius, true
}

// grayAt returns the grayscale brightness (0-255) of the pixel at (x, y).
func grayAt(img image.Image, x, y int) uint8 {
	r, g, b, _ := img.At(x, y).RGBA()
	return uint8((19595*r + 38470*g + 7471*b + 1<<15) >> 24)
}

// CreateManualVia creates a manually-placed via at the specified position.
func CreateManualVia(center geometry.Point2D, radius float64, side img.Side) Via {
	return Via{
		ID:          fmt.Sprintf("via-manual-%d", time.Now().UnixNano()),
		Center:      center,
		Radius:      radius,
		Side:        side,
		Circularity: 1.0,
		Confidence:  1.0,
		Method:      MethodManual,
	}
}
