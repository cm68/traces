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

// DetectVias detects vias in an OpenCV Mat.
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

	// Convert to grayscale for Hough circles
	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(srcImg, &gray, gocv.ColorBGRToGray)

	// Blur to reduce noise
	blurred := gocv.NewMat()
	defer blurred.Close()
	gocv.GaussianBlur(gray, &blurred, image.Point{9, 9}, 2, 2, gocv.BorderDefault)

	// Create metallic/bright mask using HSV filtering
	metallicMask := createMetallicMask(srcImg, params)
	defer metallicMask.Close()

	// Primary detection: Hough circles on masked grayscale
	houghVias := detectHoughCircles(blurred, metallicMask, params, side)

	// Secondary detection: Contour analysis for missed vias
	contourVias := detectContourCircles(metallicMask, params, side, houghVias)

	// Combine and deduplicate
	allVias := append(houghVias, contourVias...)
	result.Vias = deduplicateVias(allVias, params)

	// Assign IDs
	for i := range result.Vias {
		result.Vias[i].ID = fmt.Sprintf("via-%s-%03d", side.String()[:1], i+1)
	}

	return result, nil
}

// createMetallicMask creates a binary mask for metallic/bright regions.
func createMetallicMask(srcImg gocv.Mat, params DetectionParams) gocv.Mat {
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(srcImg, &hsv, gocv.ColorBGRToHSV)

	mask := gocv.NewMat()
	gocv.InRangeWithScalar(hsv,
		gocv.NewScalar(params.HueMin, params.SatMin, params.ValMin, 0),
		gocv.NewScalar(params.HueMax, params.SatMax, params.ValMax, 0),
		&mask)

	// Morphological cleanup: close gaps, then remove noise
	kernel := gocv.GetStructuringElement(gocv.MorphEllipse, image.Point{3, 3})
	defer kernel.Close()

	gocv.MorphologyEx(mask, &mask, gocv.MorphClose, kernel)
	gocv.MorphologyEx(mask, &mask, gocv.MorphOpen, kernel)

	return mask
}

// detectHoughCircles finds circles using Hough transform.
func detectHoughCircles(gray, mask gocv.Mat, params DetectionParams, side img.Side) []Via {
	// Apply mask to grayscale image
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

	var vias []Via
	for i := 0; i < circles.Cols(); i++ {
		cx := float64(circles.GetFloatAt(0, i*3))
		cy := float64(circles.GetFloatAt(0, i*3+1))
		r := float64(circles.GetFloatAt(0, i*3+2))

		vias = append(vias, Via{
			Center:      geometry.Point2D{X: cx, Y: cy},
			Radius:      r,
			Side:        side,
			Circularity: 1.0, // Hough assumes perfect circles
			Confidence:  0.9,
			Method:      MethodHoughCircle,
		})
	}

	return vias
}

// detectContourCircles finds circular regions via contour analysis.
func detectContourCircles(mask gocv.Mat, params DetectionParams, side img.Side, existing []Via) []Via {
	contours := gocv.FindContours(mask, gocv.RetrievalExternal, gocv.ChainApproxSimple)
	defer contours.Close()

	var vias []Via

	// Calculate expected area range
	minArea := math.Pi * float64(params.MinRadiusPixels*params.MinRadiusPixels) * 0.5
	maxArea := math.Pi * float64(params.MaxRadiusPixels*params.MaxRadiusPixels) * 2.0

	for i := 0; i < contours.Size(); i++ {
		contour := contours.At(i)
		area := gocv.ContourArea(contour)

		// Size filter
		if area < minArea || area > maxArea {
			continue
		}

		// Circularity check: 4*pi*area / perimeter^2
		perimeter := gocv.ArcLength(contour, true)
		if perimeter == 0 {
			continue
		}
		circularity := (4 * math.Pi * area) / (perimeter * perimeter)

		if circularity < params.CircularityMin {
			continue
		}

		// Get center via minimum enclosing circle
		cx, cy, radius := gocv.MinEnclosingCircle(contour)
		center := geometry.Point2D{X: float64(cx), Y: float64(cy)}

		// Skip if radius outside expected range
		if float64(radius) < float64(params.MinRadiusPixels)*0.5 ||
			float64(radius) > float64(params.MaxRadiusPixels)*2.0 {
			continue
		}

		// Skip if too close to an existing Hough-detected via
		if isNearExisting(center, existing, float64(params.MinRadiusPixels)) {
			continue
		}

		vias = append(vias, Via{
			Center:      center,
			Radius:      float64(radius),
			Side:        side,
			Circularity: circularity,
			Confidence:  circularity * 0.8, // Lower base confidence for contour method
			Method:      MethodContourFit,
		})
	}

	return vias
}

// deduplicateVias removes duplicate vias detected by both methods.
func deduplicateVias(vias []Via, params DetectionParams) []Via {
	if len(vias) <= 1 {
		return vias
	}

	threshold := float64(params.MinRadiusPixels)

	// Sort by confidence (descending) so we keep the best detections
	sort.Slice(vias, func(i, j int) bool {
		return vias[i].Confidence > vias[j].Confidence
	})

	var result []Via
	for _, v := range vias {
		isDup := false
		for i := range result {
			dist := distance(v.Center, result[i].Center)
			if dist < threshold {
				// Keep the one with higher confidence (already sorted)
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

// isNearExisting returns true if center is within threshold distance of any existing via.
func isNearExisting(center geometry.Point2D, vias []Via, threshold float64) bool {
	for _, v := range vias {
		if distance(center, v.Center) < threshold {
			return true
		}
	}
	return false
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
