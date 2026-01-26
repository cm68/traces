package via

import (
	"image"
	"math"

	pcbimage "pcb-tracer/internal/image"
	"pcb-tracer/pkg/geometry"
)

// ViaClassifier scores candidate locations for via-ness using training data.
// It uses a simple feature-based approach that can handle non-round vias.
type ViaClassifier struct {
	trainingSet *TrainingSet
	dpi         float64

	// Learned feature statistics from positive samples
	posHSVMean, posHSVStd FeatureVector
	posTextureMean        float64
	posTextureStd         float64

	// Learned feature statistics from negative samples
	negHSVMean, negHSVStd FeatureVector
	negTextureMean        float64
	negTextureStd         float64

	trained bool
}

// FeatureVector holds extracted features for classification.
type FeatureVector struct {
	// Color features
	HueMean   float64
	HueStd    float64
	SatMean   float64
	SatStd    float64
	ValMean   float64
	ValStd    float64

	// Shape features
	EdgeDensity   float64 // Proportion of edge pixels
	Compactness   float64 // 4*pi*area/perimeter^2 (1.0 for circle)
	AspectRatio   float64 // Width/Height (1.0 for square/circle)
	Rectangularity float64 // How rectangular vs circular (1.0 = rectangular, 0.0 = circular)
	Symmetry      float64 // Radial symmetry score (1.0 = perfect symmetry)
}

// NewViaClassifier creates a new classifier with the given training set.
func NewViaClassifier(ts *TrainingSet, dpi float64) *ViaClassifier {
	return &ViaClassifier{
		trainingSet: ts,
		dpi:         dpi,
	}
}

// Train computes feature statistics from the training set.
// This should be called after the training set has samples.
func (c *ViaClassifier) Train(img image.Image, side pcbimage.Side) {
	if c.trainingSet == nil {
		return
	}

	posSamples := c.trainingSet.GetPositiveSamples()
	negSamples := c.trainingSet.GetNegativeSamples()

	// Extract features from positive samples
	if len(posSamples) > 0 {
		var posFeatures []FeatureVector
		for _, sample := range posSamples {
			if sample.Side != side {
				continue
			}
			fv := c.extractFeatures(img, sample.Center, sample.Radius)
			posFeatures = append(posFeatures, fv)
		}
		if len(posFeatures) > 0 {
			c.posHSVMean, c.posHSVStd = computeFeatureStats(posFeatures)
		}
	}

	// Extract features from negative samples
	if len(negSamples) > 0 {
		var negFeatures []FeatureVector
		for _, sample := range negSamples {
			if sample.Side != side {
				continue
			}
			fv := c.extractFeatures(img, sample.Center, sample.Radius)
			negFeatures = append(negFeatures, fv)
		}
		if len(negFeatures) > 0 {
			c.negHSVMean, c.negHSVStd = computeFeatureStats(negFeatures)
		}
	}

	c.trained = len(posSamples) > 0 || len(negSamples) > 0
}

// Score returns a score indicating how likely a location contains a via.
// Returns a value between 0.0 (definitely not a via) and 1.0 (definitely a via).
// If the classifier isn't trained, returns 0.5 (unknown).
func (c *ViaClassifier) Score(img image.Image, center geometry.Point2D, radius float64) float64 {
	if !c.trained {
		return 0.5 // No training data, return neutral score
	}

	fv := c.extractFeatures(img, center, radius)

	// Compute Mahalanobis-like distance to positive and negative distributions
	posScore := c.mahalanobisScore(fv, c.posHSVMean, c.posHSVStd)
	negScore := c.mahalanobisScore(fv, c.negHSVMean, c.negHSVStd)

	// Convert distances to probability using softmax-like approach
	// Lower distance = higher probability
	if posScore == 0 && negScore == 0 {
		return 0.5
	}

	// Use inverse distances as weights
	posWeight := 1.0 / (posScore + 0.001)
	negWeight := 1.0 / (negScore + 0.001)

	return posWeight / (posWeight + negWeight)
}

// ScoreCandidate scores an already-detected via candidate.
func (c *ViaClassifier) ScoreCandidate(img image.Image, v Via) float64 {
	return c.Score(img, v.Center, v.Radius)
}

// extractFeatures extracts a feature vector from an image region.
func (c *ViaClassifier) extractFeatures(img image.Image, center geometry.Point2D, radius float64) FeatureVector {
	bounds := img.Bounds()

	// Define sampling region (slightly larger than via to capture edge)
	margin := radius * 0.2
	x1 := int(center.X - radius - margin)
	y1 := int(center.Y - radius - margin)
	x2 := int(center.X + radius + margin)
	y2 := int(center.Y + radius + margin)

	// Clamp to image bounds
	if x1 < bounds.Min.X {
		x1 = bounds.Min.X
	}
	if y1 < bounds.Min.Y {
		y1 = bounds.Min.Y
	}
	if x2 >= bounds.Max.X {
		x2 = bounds.Max.X - 1
	}
	if y2 >= bounds.Max.Y {
		y2 = bounds.Max.Y - 1
	}

	// Collect pixel data for analysis
	width := x2 - x1 + 1
	height := y2 - y1 + 1
	grayValues := make([][]float64, height)
	for i := range grayValues {
		grayValues[i] = make([]float64, width)
	}

	// Collect HSV values and gray for shape analysis
	var hues, sats, vals []float64
	var edgeCount, totalCount int

	for y := y1; y <= y2; y++ {
		for x := x1; x <= x2; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			// Convert from 16-bit to 8-bit
			r8 := float64(r >> 8)
			g8 := float64(g >> 8)
			b8 := float64(b >> 8)

			h, s, v := rgbToHSV(r8, g8, b8)
			hues = append(hues, h)
			sats = append(sats, s)
			vals = append(vals, v)

			// Store gray for shape analysis
			gray := 0.299*r8 + 0.587*g8 + 0.114*b8
			grayValues[y-y1][x-x1] = gray
			totalCount++
		}
	}

	fv := FeatureVector{}

	// Color features
	if len(hues) > 0 {
		fv.HueMean = mean(hues)
		fv.HueStd = stdDev(hues)
		fv.SatMean = mean(sats)
		fv.SatStd = stdDev(sats)
		fv.ValMean = mean(vals)
		fv.ValStd = stdDev(vals)
	}

	// Edge density using Sobel-like operator
	for y := 1; y < height-1; y++ {
		for x := 1; x < width-1; x++ {
			// Sobel X
			gx := -grayValues[y-1][x-1] + grayValues[y-1][x+1] +
				-2*grayValues[y][x-1] + 2*grayValues[y][x+1] +
				-grayValues[y+1][x-1] + grayValues[y+1][x+1]
			// Sobel Y
			gy := -grayValues[y-1][x-1] - 2*grayValues[y-1][x] - grayValues[y-1][x+1] +
				grayValues[y+1][x-1] + 2*grayValues[y+1][x] + grayValues[y+1][x+1]
			mag := math.Sqrt(gx*gx + gy*gy)
			if mag > 50 { // Edge threshold
				edgeCount++
			}
		}
	}
	if totalCount > 0 {
		fv.EdgeDensity = float64(edgeCount) / float64(totalCount)
	}

	// Shape features
	fv.AspectRatio = float64(width) / float64(height)
	if fv.AspectRatio > 1 {
		fv.AspectRatio = 1.0 / fv.AspectRatio // Keep in 0-1 range
	}

	// Rectangularity: compare corner vs edge-center brightness
	// Vias (circular) should have similar brightness at edge centers and corners
	// Rectangular pads have corners at different positions relative to center
	fv.Rectangularity = c.computeRectangularity(grayValues, width, height)

	// Radial symmetry: compare quadrants
	fv.Symmetry = c.computeSymmetry(grayValues, width, height)

	// Compactness approximation (without contour)
	// For a circle inscribed in the bounding box, compactness is ~0.785
	// For a square, it's ~0.785 * (4/pi) = 1.0
	// We estimate based on how fill pattern matches circular vs rectangular
	fv.Compactness = 0.785 // Default for circular assumption

	return fv
}

// computeRectangularity estimates how rectangular vs circular a region is.
// Returns 1.0 for rectangular, 0.0 for circular.
func (c *ViaClassifier) computeRectangularity(gray [][]float64, width, height int) float64 {
	if width < 4 || height < 4 {
		return 0.5
	}

	centerX, centerY := width/2, height/2

	// Sample at 45° angles (corners) and 90° angles (edge centers)
	// For a circle, both should be similar distance from center
	// For a rectangle, corners are further from center

	// Distance to sample at
	sampleDist := math.Min(float64(width), float64(height)) * 0.35

	// Sample at 90° angles (top, right, bottom, left)
	edgeSamples := []float64{}
	dx := []int{0, 1, 0, -1}
	dy := []int{-1, 0, 1, 0}
	for i := 0; i < 4; i++ {
		sx := centerX + int(float64(dx[i])*sampleDist)
		sy := centerY + int(float64(dy[i])*sampleDist)
		if sx >= 0 && sx < width && sy >= 0 && sy < height {
			edgeSamples = append(edgeSamples, gray[sy][sx])
		}
	}

	// Sample at 45° angles (corners)
	cornerSamples := []float64{}
	cdx := []int{1, 1, -1, -1}
	cdy := []int{-1, 1, 1, -1}
	cornerDist := sampleDist * 0.707 // For circle, corners at same distance
	for i := 0; i < 4; i++ {
		sx := centerX + int(float64(cdx[i])*cornerDist)
		sy := centerY + int(float64(cdy[i])*cornerDist)
		if sx >= 0 && sx < width && sy >= 0 && sy < height {
			cornerSamples = append(cornerSamples, gray[sy][sx])
		}
	}

	if len(edgeSamples) == 0 || len(cornerSamples) == 0 {
		return 0.5
	}

	// For a circular via, edge and corner samples should be similar
	// For a rectangular pad, they'll differ more
	edgeMean := mean(edgeSamples)
	cornerMean := mean(cornerSamples)
	diff := math.Abs(edgeMean - cornerMean)

	// Normalize difference to 0-1 range
	// Large difference = more rectangular
	return math.Min(diff/100.0, 1.0)
}

// computeSymmetry estimates radial symmetry by comparing quadrants.
func (c *ViaClassifier) computeSymmetry(gray [][]float64, width, height int) float64 {
	if width < 4 || height < 4 {
		return 0.5
	}

	halfW, halfH := width/2, height/2

	// Compare quadrant averages
	var q1, q2, q3, q4 float64
	var n1, n2, n3, n4 int

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if y < halfH {
				if x < halfW {
					q1 += gray[y][x]
					n1++
				} else {
					q2 += gray[y][x]
					n2++
				}
			} else {
				if x < halfW {
					q3 += gray[y][x]
					n3++
				} else {
					q4 += gray[y][x]
					n4++
				}
			}
		}
	}

	if n1 > 0 {
		q1 /= float64(n1)
	}
	if n2 > 0 {
		q2 /= float64(n2)
	}
	if n3 > 0 {
		q3 /= float64(n3)
	}
	if n4 > 0 {
		q4 /= float64(n4)
	}

	// Measure deviation from mean
	quadMean := (q1 + q2 + q3 + q4) / 4
	variance := (math.Pow(q1-quadMean, 2) + math.Pow(q2-quadMean, 2) +
		math.Pow(q3-quadMean, 2) + math.Pow(q4-quadMean, 2)) / 4

	// Low variance = high symmetry
	// Normalize to 0-1 range (higher = more symmetric)
	return math.Max(0, 1.0-math.Sqrt(variance)/50.0)
}

// mahalanobisScore computes a distance-like score from a distribution.
func (c *ViaClassifier) mahalanobisScore(fv, mean, std FeatureVector) float64 {
	// Compute normalized squared differences for each feature
	score := 0.0

	// HSV features (weighted for distinctive metallic appearance)
	score += sqDiff(fv.HueMean, mean.HueMean, std.HueMean+1) * 1.5
	score += sqDiff(fv.SatMean, mean.SatMean, std.SatMean+1)
	score += sqDiff(fv.ValMean, mean.ValMean, std.ValMean+1)

	// Edge density
	score += sqDiff(fv.EdgeDensity, mean.EdgeDensity, std.EdgeDensity+0.01)

	// Shape features (important for distinguishing vias from pads)
	score += sqDiff(fv.Rectangularity, mean.Rectangularity, std.Rectangularity+0.1) * 2.0
	score += sqDiff(fv.Symmetry, mean.Symmetry, std.Symmetry+0.1)
	score += sqDiff(fv.AspectRatio, mean.AspectRatio, std.AspectRatio+0.1)

	return math.Sqrt(score)
}

// sqDiff computes (a-b)^2 / s^2 with safeguards.
func sqDiff(a, b, s float64) float64 {
	if s < 0.001 {
		s = 0.001
	}
	d := (a - b) / s
	return d * d
}

// computeFeatureStats computes mean and standard deviation for each feature.
func computeFeatureStats(features []FeatureVector) (mean, std FeatureVector) {
	n := float64(len(features))
	if n == 0 {
		return
	}

	// Compute means
	for _, fv := range features {
		mean.HueMean += fv.HueMean
		mean.SatMean += fv.SatMean
		mean.ValMean += fv.ValMean
		mean.EdgeDensity += fv.EdgeDensity
		mean.Rectangularity += fv.Rectangularity
		mean.Symmetry += fv.Symmetry
		mean.AspectRatio += fv.AspectRatio
	}
	mean.HueMean /= n
	mean.SatMean /= n
	mean.ValMean /= n
	mean.EdgeDensity /= n
	mean.Rectangularity /= n
	mean.Symmetry /= n
	mean.AspectRatio /= n

	// Compute standard deviations
	for _, fv := range features {
		std.HueMean += (fv.HueMean - mean.HueMean) * (fv.HueMean - mean.HueMean)
		std.SatMean += (fv.SatMean - mean.SatMean) * (fv.SatMean - mean.SatMean)
		std.ValMean += (fv.ValMean - mean.ValMean) * (fv.ValMean - mean.ValMean)
		std.EdgeDensity += (fv.EdgeDensity - mean.EdgeDensity) * (fv.EdgeDensity - mean.EdgeDensity)
		std.Rectangularity += (fv.Rectangularity - mean.Rectangularity) * (fv.Rectangularity - mean.Rectangularity)
		std.Symmetry += (fv.Symmetry - mean.Symmetry) * (fv.Symmetry - mean.Symmetry)
		std.AspectRatio += (fv.AspectRatio - mean.AspectRatio) * (fv.AspectRatio - mean.AspectRatio)
	}
	std.HueMean = math.Sqrt(std.HueMean / n)
	std.SatMean = math.Sqrt(std.SatMean / n)
	std.ValMean = math.Sqrt(std.ValMean / n)
	std.EdgeDensity = math.Sqrt(std.EdgeDensity / n)
	std.Rectangularity = math.Sqrt(std.Rectangularity / n)
	std.Symmetry = math.Sqrt(std.Symmetry / n)
	std.AspectRatio = math.Sqrt(std.AspectRatio / n)

	return
}

// Helper functions

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

// rgbToHSV converts RGB (0-255) to HSV (OpenCV convention: H 0-180, S 0-255, V 0-255).
func rgbToHSV(r, g, b float64) (h, s, v float64) {
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

// FilterWithClassifier re-scores detected vias using the classifier.
// Vias with scores below the threshold are removed.
func FilterWithClassifier(result *ViaDetectionResult, classifier *ViaClassifier, img image.Image, threshold float64) {
	if classifier == nil || !classifier.trained {
		return
	}

	var filtered []Via
	for _, v := range result.Vias {
		score := classifier.ScoreCandidate(img, v)
		if score >= threshold {
			// Update confidence with classifier score
			v.Confidence = (v.Confidence + score) / 2
			filtered = append(filtered, v)
		}
	}
	result.Vias = filtered
}

// ScoreWithCrossSideValidation computes a combined score using both
// image features and cross-side detection. Cross-side validation is the
// strongest indicator of a true via since vias are through-holes.
//
// Parameters:
//   - img: the image to extract features from
//   - v: the via to score
//   - bothSidesWeight: how much weight to give cross-side validation (0-1, default 0.5)
//
// Returns a score between 0.0 and 1.0.
func (c *ViaClassifier) ScoreWithCrossSideValidation(img image.Image, v Via, bothSidesWeight float64) float64 {
	// Get base feature score
	baseScore := c.Score(img, v.Center, v.Radius)

	// If detected on both sides, boost significantly
	if v.BothSidesConfirmed {
		// Interpolate between base score and 1.0 based on weight
		return baseScore*(1-bothSidesWeight) + 1.0*bothSidesWeight
	}

	// If not confirmed on both sides, slight penalty (but not severe - could be alignment issue)
	return baseScore * (1 - bothSidesWeight*0.3)
}

// ValidateAndScoreResults performs cross-side matching and rescores all vias.
// This should be called after detection on both sides and alignment.
//
// Parameters:
//   - frontResult: vias detected on front side
//   - backResult: vias detected on back side
//   - frontImg, backImg: images for feature extraction
//   - dpi: image DPI for computing match tolerance
//
// Returns the match statistics.
func (c *ViaClassifier) ValidateAndScoreResults(
	frontResult, backResult *ViaDetectionResult,
	frontImg, backImg image.Image,
	dpi float64,
) MatchResult {
	if frontResult == nil || backResult == nil {
		return MatchResult{}
	}

	// Match vias across sides
	tolerance := SuggestMatchTolerance(dpi)
	matchResult := MatchViasAcrossSides(frontResult.Vias, backResult.Vias, tolerance)

	// Rescore all vias with cross-side validation
	bothSidesWeight := 0.4 // Cross-side confirmation is very important
	for i := range frontResult.Vias {
		frontResult.Vias[i].Confidence = c.ScoreWithCrossSideValidation(
			frontImg, frontResult.Vias[i], bothSidesWeight,
		)
	}
	for i := range backResult.Vias {
		backResult.Vias[i].Confidence = c.ScoreWithCrossSideValidation(
			backImg, backResult.Vias[i], bothSidesWeight,
		)
	}

	return matchResult
}
