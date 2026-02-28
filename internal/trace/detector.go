// Package trace provides copper trace detection and vectorization.
package trace

import (
	"image"
	"image/color"
	"sort"

	"pcb-tracer/pkg/geometry"

	"gocv.io/x/gocv"
)

// TraceLayer identifies which PCB layer a trace is on.
type TraceLayer int

const (
	LayerFront TraceLayer = iota
	LayerBack
)

// Trace represents a detected copper trace.
type Trace struct {
	ID     string             `json:"id"`
	Layer  TraceLayer         `json:"layer"`
	Points []geometry.Point2D `json:"points"`
	Width  float64            `json:"width,omitempty"`
	Net    string             `json:"net,omitempty"`
}

// Bounds returns the bounding rectangle for the trace.
func (t Trace) Bounds() geometry.RectInt {
	if len(t.Points) == 0 {
		return geometry.RectInt{}
	}

	minX, minY := t.Points[0].X, t.Points[0].Y
	maxX, maxY := t.Points[0].X, t.Points[0].Y

	for _, pt := range t.Points[1:] {
		if pt.X < minX {
			minX = pt.X
		}
		if pt.X > maxX {
			maxX = pt.X
		}
		if pt.Y < minY {
			minY = pt.Y
		}
		if pt.Y > maxY {
			maxY = pt.Y
		}
	}

	// Expand bounds by half trace width
	hw := int(t.Width/2) + 1
	return geometry.RectInt{
		X:      int(minX) - hw,
		Y:      int(minY) - hw,
		Width:  int(maxX-minX) + 2*hw,
		Height: int(maxY-minY) + 2*hw,
	}
}

// DetectionResult holds trace detection results.
type DetectionResult struct {
	Traces     []Trace
	CopperMask gocv.Mat // Binary mask of detected copper
	Layer      TraceLayer
}

// DetectionOptions configures trace detection.
type DetectionOptions struct {
	NumClusters       int     // K-means clusters for auto-detection
	ColorTolerance    int     // Tolerance for manual color detection
	CleanupIterations int     // Morphological cleanup strength
	MinTraceArea      float64 // Minimum area to consider as trace
}

// DefaultOptions returns default detection options.
func DefaultOptions() DetectionOptions {
	return DetectionOptions{
		NumClusters:       4,
		ColorTolerance:    40,
		CleanupIterations: 2,
		MinTraceArea:      100,
	}
}

// AutoDetectCopper detects copper regions using K-means clustering in LAB color space.
func AutoDetectCopper(img gocv.Mat, numClusters int) gocv.Mat {
	if img.Empty() {
		return gocv.NewMat()
	}

	// Convert to LAB color space for better color segmentation
	lab := gocv.NewMat()
	defer lab.Close()
	gocv.CvtColor(img, &lab, gocv.ColorBGRToLab)

	// Reshape for k-means: (h*w) x 3 float32
	h, w := lab.Rows(), lab.Cols()
	pixels := gocv.NewMatWithSize(h*w, 3, gocv.MatTypeCV32F)
	defer pixels.Close()

	// Copy pixels to float matrix
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := y*w + x
			vec := lab.GetVecbAt(y, x)
			pixels.SetFloatAt(idx, 0, float32(vec[0]))
			pixels.SetFloatAt(idx, 1, float32(vec[1]))
			pixels.SetFloatAt(idx, 2, float32(vec[2]))
		}
	}

	// K-means clustering
	labels := gocv.NewMat()
	defer labels.Close()
	centers := gocv.NewMat()
	defer centers.Close()

	criteria := gocv.NewTermCriteria(gocv.EPS+gocv.MaxIter, 100, 0.2)
	gocv.KMeans(pixels, numClusters, &labels, criteria, 10, gocv.KMeansRandomCenters, &centers)

	// Score each cluster to find copper
	// Copper tends to have higher luminance and warm tones
	scores := make([]float64, numClusters)

	for i := 0; i < numClusters; i++ {
		// Get cluster center in LAB
		l := centers.GetFloatAt(i, 0)
		a := centers.GetFloatAt(i, 1)
		b := centers.GetFloatAt(i, 2)

		// Higher L = brighter, positive a = more red, positive b = more yellow
		// Copper is bright and warm (positive a and b)
		brightness := float64(l) / 255.0
		warmth := (float64(a) + float64(b)) / 256.0 // Normalize

		scores[i] = brightness * (1 + warmth)
	}

	// Find cluster with highest score
	copperCluster := 0
	maxScore := scores[0]
	for i := 1; i < numClusters; i++ {
		if scores[i] > maxScore {
			maxScore = scores[i]
			copperCluster = i
		}
	}

	// Create mask for copper cluster
	mask := gocv.NewMatWithSize(h, w, gocv.MatTypeCV8U)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := y*w + x
			if labels.GetIntAt(idx, 0) == int32(copperCluster) {
				mask.SetUCharAt(y, x, 255)
			}
		}
	}

	return mask
}

// AutoDetectCopperTopN detects copper using K-means but includes the top N
// scoring clusters. This captures copper under solder mask that appears dimmer
// than bare copper pads but still brighter/warmer than the board background.
func AutoDetectCopperTopN(img gocv.Mat, numClusters, topN int) gocv.Mat {
	if img.Empty() || topN <= 0 {
		return gocv.NewMat()
	}
	if topN >= numClusters {
		topN = numClusters - 1
	}

	lab := gocv.NewMat()
	defer lab.Close()
	gocv.CvtColor(img, &lab, gocv.ColorBGRToLab)

	h, w := lab.Rows(), lab.Cols()
	pixels := gocv.NewMatWithSize(h*w, 3, gocv.MatTypeCV32F)
	defer pixels.Close()

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := y*w + x
			vec := lab.GetVecbAt(y, x)
			pixels.SetFloatAt(idx, 0, float32(vec[0]))
			pixels.SetFloatAt(idx, 1, float32(vec[1]))
			pixels.SetFloatAt(idx, 2, float32(vec[2]))
		}
	}

	labels := gocv.NewMat()
	defer labels.Close()
	centers := gocv.NewMat()
	defer centers.Close()

	criteria := gocv.NewTermCriteria(gocv.EPS+gocv.MaxIter, 100, 0.2)
	gocv.KMeans(pixels, numClusters, &labels, criteria, 10, gocv.KMeansRandomCenters, &centers)

	// Score each cluster
	type clusterScore struct {
		idx   int
		score float64
	}
	scored := make([]clusterScore, numClusters)
	for i := 0; i < numClusters; i++ {
		l := centers.GetFloatAt(i, 0)
		a := centers.GetFloatAt(i, 1)
		b := centers.GetFloatAt(i, 2)
		brightness := float64(l) / 255.0
		warmth := (float64(a) + float64(b)) / 256.0
		scored[i] = clusterScore{idx: i, score: brightness * (1 + warmth)}
	}

	// Sort descending by score
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Build set of top-N cluster indices
	copperClusters := make(map[int32]bool, topN)
	for i := 0; i < topN; i++ {
		copperClusters[int32(scored[i].idx)] = true
	}

	mask := gocv.NewMatWithSize(h, w, gocv.MatTypeCV8U)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := y*w + x
			if copperClusters[labels.GetIntAt(idx, 0)] {
				mask.SetUCharAt(y, x, 255)
			}
		}
	}

	return mask
}

// DetectByColor detects copper regions based on a specific color.
func DetectByColor(img gocv.Mat, colorBGR [3]uint8, tolerance int) gocv.Mat {
	if img.Empty() {
		return gocv.NewMat()
	}

	// Convert to HSV for better color matching
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(img, &hsv, gocv.ColorBGRToHSV)

	// Convert target color to HSV
	colorMat := gocv.NewMatWithSizeFromScalar(gocv.NewScalar(float64(colorBGR[0]), float64(colorBGR[1]), float64(colorBGR[2]), 0), 1, 1, gocv.MatTypeCV8UC3)
	defer colorMat.Close()

	colorHSV := gocv.NewMat()
	defer colorHSV.Close()
	gocv.CvtColor(colorMat, &colorHSV, gocv.ColorBGRToHSV)

	targetH := colorHSV.GetUCharAt(0, 0)
	targetS := colorHSV.GetUCharAt(0, 1)
	targetV := colorHSV.GetUCharAt(0, 2)

	// Create range around target color
	hTol := uint8(tolerance / 4) // Hue has smaller range
	sTol := uint8(tolerance)
	vTol := uint8(tolerance)

	lowerH := max(0, int(targetH)-int(hTol))
	upperH := min(179, int(targetH)+int(hTol))
	lowerS := max(0, int(targetS)-int(sTol))
	upperS := min(255, int(targetS)+int(sTol))
	lowerV := max(0, int(targetV)-int(vTol))
	upperV := min(255, int(targetV)+int(vTol))

	// Create mask
	mask := gocv.NewMat()
	gocv.InRangeWithScalar(hsv,
		gocv.NewScalar(float64(lowerH), float64(lowerS), float64(lowerV), 0),
		gocv.NewScalar(float64(upperH), float64(upperS), float64(upperV), 0),
		&mask)

	return mask
}

// CleanupMask applies morphological operations to clean up a trace mask.
func CleanupMask(mask gocv.Mat, iterations int) gocv.Mat {
	if mask.Empty() {
		return gocv.NewMat()
	}

	kernel := gocv.GetStructuringElement(gocv.MorphRect, image.Point{3, 3})
	defer kernel.Close()

	cleaned := mask.Clone()

	// Close small gaps
	for i := 0; i < iterations; i++ {
		gocv.MorphologyEx(cleaned, &cleaned, gocv.MorphClose, kernel)
	}

	// Remove small noise
	for i := 0; i < iterations; i++ {
		gocv.MorphologyEx(cleaned, &cleaned, gocv.MorphOpen, kernel)
	}

	return cleaned
}

// FillRegions fills contour regions in a mask.
func FillRegions(mask gocv.Mat) gocv.Mat {
	if mask.Empty() {
		return gocv.NewMat()
	}

	filled := gocv.NewMatWithSize(mask.Rows(), mask.Cols(), gocv.MatTypeCV8U)
	contours := gocv.FindContours(mask, gocv.RetrievalExternal, gocv.ChainApproxSimple)
	defer contours.Close()

	for i := 0; i < contours.Size(); i++ {
		gocv.DrawContours(&filled, contours, i, color.RGBA{R: 255, G: 255, B: 255, A: 255}, -1)
	}

	return filled
}

// DetectTraces performs full trace detection pipeline.
// Returns a DetectionResult with CopperMask but no vectorized traces.
// Use DetectAndVectorizeTraces for the full pipeline.
func DetectTraces(img gocv.Mat, opts DetectionOptions) *DetectionResult {
	if img.Empty() {
		return nil
	}

	// Preprocess: blur to reduce noise
	blurred := gocv.NewMat()
	defer blurred.Close()
	gocv.GaussianBlur(img, &blurred, image.Point{5, 5}, 0, 0, gocv.BorderDefault)

	// Auto-detect copper
	mask := AutoDetectCopper(blurred, opts.NumClusters)
	defer mask.Close()

	// Cleanup
	cleaned := CleanupMask(mask, opts.CleanupIterations)
	defer cleaned.Close()

	// Fill regions
	filled := FillRegions(cleaned)

	return &DetectionResult{
		CopperMask: filled,
		Layer:      LayerFront,
	}
}

// DetectAndVectorizeTraces performs full trace detection and vectorization.
// This is the main entry point for trace detection.
func DetectAndVectorizeTraces(img gocv.Mat, layer TraceLayer, detOpts DetectionOptions, vecOpts VectorizeOptions) *DetectionResult {
	// First, detect copper mask
	result := DetectTraces(img, detOpts)
	if result == nil || result.CopperMask.Empty() {
		return result
	}

	result.Layer = layer

	// Vectorize the copper mask into trace paths
	extTraces := VectorizeTraces(result.CopperMask, layer, vecOpts)

	// Convert ExtendedTrace to Trace for result
	result.Traces = make([]Trace, len(extTraces))
	for i, et := range extTraces {
		result.Traces[i] = et.Trace
	}

	return result
}

// ExtractSilkscreen extracts the silkscreen layer (white text/markings).
func ExtractSilkscreen(img gocv.Mat, threshold uint8, minArea int) gocv.Mat {
	if img.Empty() {
		return gocv.NewMat()
	}

	// Convert to grayscale and HSV
	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(img, &gray, gocv.ColorBGRToGray)

	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(img, &hsv, gocv.ColorBGRToHSV)

	// White mask: high value, low saturation
	whiteMask := gocv.NewMat()
	defer whiteMask.Close()
	gocv.InRangeWithScalar(hsv,
		gocv.NewScalar(0, 0, float64(threshold), 0),
		gocv.NewScalar(180, 60, 255, 0),
		&whiteMask)

	// Bright mask from grayscale
	brightMask := gocv.NewMat()
	defer brightMask.Close()
	gocv.Threshold(gray, &brightMask, float32(threshold), 255, gocv.ThresholdBinary)

	// Combine masks
	combined := gocv.NewMat()
	gocv.BitwiseAnd(whiteMask, brightMask, &combined)

	// Cleanup
	kernelSmall := gocv.GetStructuringElement(gocv.MorphRect, image.Point{2, 2})
	defer kernelSmall.Close()
	kernelMed := gocv.GetStructuringElement(gocv.MorphRect, image.Point{3, 3})
	defer kernelMed.Close()

	gocv.MorphologyEx(combined, &combined, gocv.MorphOpen, kernelSmall)
	gocv.MorphologyEx(combined, &combined, gocv.MorphClose, kernelMed)

	// Remove small noise contours
	if minArea > 0 {
		contours := gocv.FindContours(combined, gocv.RetrievalExternal, gocv.ChainApproxSimple)
		defer contours.Close()

		noiseFree := gocv.NewMatWithSize(combined.Rows(), combined.Cols(), gocv.MatTypeCV8U)
		for i := 0; i < contours.Size(); i++ {
			if gocv.ContourArea(contours.At(i)) >= float64(minArea) {
				gocv.DrawContours(&noiseFree, contours, i, color.RGBA{R: 255, G: 255, B: 255, A: 255}, -1)
			}
		}
		combined.Close()
		combined = noiseFree
	}

	return combined
}

// ImageToMat converts a Go image.Image to a gocv.Mat in BGR format.
func ImageToMat(img image.Image) (gocv.Mat, error) {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	mat := gocv.NewMatWithSize(h, w, gocv.MatTypeCV8UC3)

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b, _ := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			mat.SetUCharAt(y, x*3+0, uint8(b>>8))
			mat.SetUCharAt(y, x*3+1, uint8(g>>8))
			mat.SetUCharAt(y, x*3+2, uint8(r>>8))
		}
	}

	return mat, nil
}

// StampCircle paints a filled white circle onto a single-channel mask.
func StampCircle(mask gocv.Mat, cx, cy, radius int) {
	rows, cols := mask.Rows(), mask.Cols()
	for dy := -radius; dy <= radius; dy++ {
		for dx := -radius; dx <= radius; dx++ {
			if dx*dx+dy*dy <= radius*radius {
				x, y := cx+dx, cy+dy
				if x >= 0 && x < cols && y >= 0 && y < rows {
					mask.SetUCharAt(y, x, 255)
				}
			}
		}
	}
}

