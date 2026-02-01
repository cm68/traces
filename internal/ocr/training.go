// Package ocr provides OCR training with parameter annealing.
package ocr

import (
	"fmt"
	"image"
	"regexp"
	"strings"
	"unicode"

	"github.com/otiai10/gosseract/v2"
	"gocv.io/x/gocv"
)

// OCRParams holds tunable OCR preprocessing parameters.
type OCRParams struct {
	// Histogram threshold: percentage of brightest pixels to treat as text (0-100)
	BrightestPercent float64 `json:"brightest_pct"`

	// Minimum threshold value (0-255) to avoid noise
	MinThreshold int `json:"min_threshold"`

	// CLAHE parameters
	CLAHEClipLimit float64 `json:"clahe_clip"`
	CLAHETileSize  int     `json:"clahe_tile"`

	// Scaling: minimum dimension target for upscaling
	MinScaleDim int `json:"min_scale_dim"`

	// Invert polarity (true = expect light text on dark background)
	InvertPolarity bool `json:"invert"`

	// Use Otsu threshold instead of histogram-based
	UseOtsu bool `json:"use_otsu"`

	// Morphological operations
	DilateIterations int `json:"dilate"`
	ErodeIterations  int `json:"erode"`

	// PSM mode (page segmentation mode)
	PSMMode int `json:"psm_mode"`
}

// DefaultOCRParams returns sensible defaults for IC package text.
func DefaultOCRParams() OCRParams {
	return OCRParams{
		BrightestPercent: 10.0,
		MinThreshold:     128,
		CLAHEClipLimit:   2.0,
		CLAHETileSize:    8,
		MinScaleDim:      150,
		InvertPolarity:   true,
		UseOtsu:          true,
		DilateIterations: 0,
		ErodeIterations:  0,
		PSMMode:          6, // PSM_SINGLE_BLOCK
	}
}

// TrainingSample holds a single OCR training example.
type TrainingSample struct {
	GroundTruth string    `json:"truth"`      // User-corrected text
	Orientation string    `json:"orientation"` // N/S/E/W
	BestParams  OCRParams `json:"params"`      // Best parameters found
	BestScore   float64   `json:"score"`       // Best similarity score achieved
}

// LearnedParams holds accumulated OCR parameters learned from training samples.
type LearnedParams struct {
	Samples    []TrainingSample `json:"samples"`
	BestParams OCRParams        `json:"best_params"`
	AvgScore   float64          `json:"avg_score"`
}

// NewLearnedParams creates a new learned params store with defaults.
func NewLearnedParams() *LearnedParams {
	return &LearnedParams{
		BestParams: DefaultOCRParams(),
	}
}

// logoMarkerPattern matches <XX> style logo markers
var logoMarkerPattern = regexp.MustCompile(`<[A-Z]{2,4}>`)

// TextSimilarity calculates similarity between detected and ground truth text.
// Returns a score from 0.0 (no match) to 1.0 (perfect match).
// Logo markers like <TI> are stripped from truth - OCR can't read logos.
// Preserves line structure for comparison.
func TextSimilarity(detected, truth string) float64 {
	// Strip logo markers from truth - OCR won't detect these
	strippedTruth := stripLogoMarkers(truth)

	// Split by lines
	detectedLines := splitLines(detected)
	truthLines := splitLines(strippedTruth)

	// Normalize for comparison
	detectedNorm := normalizeText(detected)
	truthNorm := normalizeText(strippedTruth)

	if truthNorm == "" {
		// If truth is all logos, any detection is OK
		if detected != "" {
			return 0.5
		}
		return 0.0
	}
	if detectedNorm == truthNorm {
		return 1.0
	}

	// Calculate multiple similarity metrics

	// 1. LCS (longest common subsequence) - best for partial matches
	lcs := longestCommonSubsequence(detectedNorm, truthNorm)
	maxLen := max(len(detectedNorm), len(truthNorm))
	lcsScore := 0.0
	if maxLen > 0 {
		lcsScore = float64(lcs) / float64(maxLen)
	}

	// 2. Character overlap - what percentage of truth chars appear in detected
	charOverlap := characterOverlap(detectedNorm, truthNorm)

	// 3. Line-by-line comparison
	lineScore := 0.0
	if len(truthLines) > 0 {
		matchedLines := 0.0
		for _, tl := range truthLines {
			tlNorm := normalizeText(tl)
			if tlNorm == "" {
				continue // Skip logo-only lines
			}
			bestLineMatch := 0.0
			for _, dl := range detectedLines {
				dlNorm := normalizeText(dl)
				if dlNorm == tlNorm {
					bestLineMatch = 1.0
					break
				}
				// Partial match
				lineLCS := longestCommonSubsequence(dlNorm, tlNorm)
				lineMax := max(len(dlNorm), len(tlNorm))
				if lineMax > 0 {
					match := float64(lineLCS) / float64(lineMax)
					if match > bestLineMatch {
						bestLineMatch = match
					}
				}
			}
			matchedLines += bestLineMatch
		}
		lineScore = matchedLines / float64(len(truthLines))
	}

	// 4. Substring containment - does detected contain significant truth substrings?
	substringScore := 0.0
	if len(truthNorm) >= 3 {
		// Check 3-char substrings from truth
		matches := 0
		total := 0
		for i := 0; i <= len(truthNorm)-3; i++ {
			substr := truthNorm[i : i+3]
			total++
			if strings.Contains(detectedNorm, substr) {
				matches++
			}
		}
		if total > 0 {
			substringScore = float64(matches) / float64(total)
		}
	}

	// Combined score - weight different metrics
	score := 0.35*lcsScore + 0.25*charOverlap + 0.25*lineScore + 0.15*substringScore

	return score
}

// stripLogoMarkers removes <XX> style logo markers from text.
// OCR cannot read logos, so they shouldn't be part of comparison.
func stripLogoMarkers(s string) string {
	return logoMarkerPattern.ReplaceAllString(s, "")
}

// splitLines splits text into lines, handling different line endings.
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	// Filter empty lines
	result := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			result = append(result, l)
		}
	}
	return result
}

// normalizeText normalizes text for comparison, preserving structure.
func normalizeText(s string) string {
	s = strings.ToUpper(s)
	var result strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\n' {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// longestCommonSubsequence calculates LCS length.
func longestCommonSubsequence(a, b string) int {
	m, n := len(a), len(b)
	if m == 0 || n == 0 {
		return 0
	}

	// Use two rows for space efficiency
	prev := make([]int, n+1)
	curr := make([]int, n+1)

	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				curr[j] = prev[j-1] + 1
			} else {
				curr[j] = max(prev[j], curr[j-1])
			}
		}
		prev, curr = curr, prev
	}

	return prev[n]
}

// characterOverlap calculates the percentage of ground truth characters found in detected.
func characterOverlap(detected, truth string) float64 {
	if len(truth) == 0 {
		return 0.0
	}

	// Count character frequency in detected
	detectedChars := make(map[rune]int)
	for _, r := range detected {
		detectedChars[r]++
	}

	// Count how many truth characters are found
	matched := 0
	for _, r := range truth {
		if detectedChars[r] > 0 {
			matched++
			detectedChars[r]--
		}
	}

	return float64(matched) / float64(len(truth))
}

// AnnealOCRParams tries different OCR parameter combinations to find the best match.
// Returns the best parameters found and the achieved similarity score.
func (e *Engine) AnnealOCRParams(img gocv.Mat, groundTruth string, maxIterations int) (OCRParams, float64, string) {
	if img.Empty() || groundTruth == "" {
		return DefaultOCRParams(), 0.0, ""
	}

	bestParams := DefaultOCRParams()
	bestScore := 0.0
	bestText := ""

	// Parameter search space
	brightestPcts := []float64{5.0, 10.0, 15.0, 20.0, 25.0}
	minThresholds := []int{80, 100, 128, 150, 180}
	claheClips := []float64{1.5, 2.0, 3.0, 4.0}
	claheTiles := []int{4, 8, 16}
	scaleDims := []int{100, 150, 200, 300}
	invertOptions := []bool{true, false}
	otsuOptions := []bool{true, false}
	psmModes := []int{6, 7, 11, 13} // SINGLE_BLOCK, SINGLE_LINE, SPARSE_TEXT, RAW_LINE

	iterations := 0

	fmt.Printf("OCR Annealing: searching for best params (truth=%q)\n", groundTruth)

	// First, try Otsu-based methods (usually best for IC text)
	for _, otsu := range otsuOptions {
		for _, invert := range invertOptions {
			for _, clip := range claheClips {
				for _, tile := range claheTiles {
					for _, scale := range scaleDims {
						for _, psm := range psmModes {
							if iterations >= maxIterations {
								goto done
							}

							params := OCRParams{
								UseOtsu:        otsu,
								InvertPolarity: invert,
								CLAHEClipLimit: clip,
								CLAHETileSize:  tile,
								MinScaleDim:    scale,
								PSMMode:        psm,
							}

							text := e.recognizeWithParams(img, params)
							score := TextSimilarity(text, groundTruth)
							iterations++

							if score > bestScore {
								bestScore = score
								bestParams = params
								bestText = text
								fmt.Printf("  [%d] score=%.3f otsu=%v inv=%v clip=%.1f tile=%d scale=%d psm=%d -> %q\n",
									iterations, score, otsu, invert, clip, tile, scale, psm, text)

								// Perfect match - stop early
								if score >= 0.99 {
									goto done
								}
							}
						}
					}
				}
			}
		}
	}

	// Then try histogram-based thresholding if Otsu didn't work well
	if bestScore < 0.5 {
		for _, pct := range brightestPcts {
			for _, minTh := range minThresholds {
				for _, invert := range invertOptions {
					for _, scale := range scaleDims {
						for _, psm := range psmModes {
							if iterations >= maxIterations {
								goto done
							}

							params := OCRParams{
								BrightestPercent: pct,
								MinThreshold:     minTh,
								InvertPolarity:   invert,
								UseOtsu:          false,
								MinScaleDim:      scale,
								PSMMode:          psm,
								CLAHEClipLimit:   2.0,
								CLAHETileSize:    8,
							}

							text := e.recognizeWithParams(img, params)
							score := TextSimilarity(text, groundTruth)
							iterations++

							if score > bestScore {
								bestScore = score
								bestParams = params
								bestText = text
								fmt.Printf("  [%d] score=%.3f hist=%v%% minTh=%d inv=%v scale=%d psm=%d -> %q\n",
									iterations, score, pct, minTh, invert, scale, psm, text)

								if score >= 0.99 {
									goto done
								}
							}
						}
					}
				}
			}
		}
	}

done:
	fmt.Printf("OCR Annealing: best score=%.3f after %d iterations\n", bestScore, iterations)
	fmt.Printf("  Best params: otsu=%v inv=%v clip=%.1f tile=%d scale=%d psm=%d\n",
		bestParams.UseOtsu, bestParams.InvertPolarity, bestParams.CLAHEClipLimit,
		bestParams.CLAHETileSize, bestParams.MinScaleDim, bestParams.PSMMode)
	fmt.Printf("  Best text: %q (truth: %q)\n", bestText, groundTruth)

	return bestParams, bestScore, bestText
}

// recognizeWithParams runs OCR with specific parameters.
func (e *Engine) recognizeWithParams(img gocv.Mat, params OCRParams) string {
	if img.Empty() {
		return ""
	}

	// Preprocess with given params
	processed := preprocessWithParams(img, params)
	defer processed.Close()

	// Encode to PNG
	buf, err := gocv.IMEncode(gocv.PNGFileExt, processed)
	if err != nil {
		return ""
	}
	defer buf.Close()

	// Set PSM mode
	psmMode := gosseract.PageSegMode(params.PSMMode)
	if err := e.client.SetPageSegMode(psmMode); err != nil {
		return ""
	}

	// Set whitelist
	if e.electronicsMode {
		_ = e.client.SetWhitelist(ElectronicsChars)
	}

	// Set image and recognize
	if err := e.client.SetImageFromBytes(buf.GetBytes()); err != nil {
		return ""
	}

	text, err := e.client.Text()
	if err != nil {
		return ""
	}

	text = strings.TrimSpace(text)
	text = strings.Join(strings.Fields(text), " ")
	if e.electronicsMode {
		text = strings.ToUpper(text)
	}

	return text
}

// RecognizeWithParams performs OCR using specific learned parameters.
func (e *Engine) RecognizeWithParams(img gocv.Mat, params OCRParams) (string, error) {
	text := e.recognizeWithParams(img, params)
	return text, nil
}

// preprocessWithParams applies preprocessing based on given parameters.
func preprocessWithParams(region gocv.Mat, params OCRParams) gocv.Mat {
	h, w := region.Rows(), region.Cols()

	// Scale up small images
	var scaled gocv.Mat
	minDim := min(h, w)
	if minDim < params.MinScaleDim {
		scale := float64(params.MinScaleDim) / float64(minDim)
		scaled = gocv.NewMat()
		gocv.Resize(region, &scaled, image.Point{}, scale, scale, gocv.InterpolationCubic)
	} else {
		scaled = region.Clone()
	}

	// Convert to grayscale
	gray := gocv.NewMat()
	gocv.CvtColor(scaled, &gray, gocv.ColorBGRToGray)
	scaled.Close()

	// Apply CLAHE if clip limit > 0
	var enhanced gocv.Mat
	if params.CLAHEClipLimit > 0 {
		clahe := gocv.NewCLAHEWithParams(params.CLAHEClipLimit, image.Point{params.CLAHETileSize, params.CLAHETileSize})
		enhanced = gocv.NewMat()
		clahe.Apply(gray, &enhanced)
		clahe.Close()
		gray.Close()
	} else {
		enhanced = gray
	}

	// Threshold
	binary := gocv.NewMat()
	if params.UseOtsu {
		gocv.Threshold(enhanced, &binary, 0, 255, gocv.ThresholdBinary|gocv.ThresholdOtsu)
	} else {
		// Histogram-based threshold
		threshold := findHistogramThreshold(enhanced, params.BrightestPercent, params.MinThreshold)
		gocv.Threshold(enhanced, &binary, float32(threshold), 255, gocv.ThresholdBinary)
	}
	enhanced.Close()

	// Handle polarity
	if params.InvertPolarity {
		// Check if mostly white - invert to get dark text on light background
		whiteCount := gocv.CountNonZero(binary)
		totalPixels := binary.Rows() * binary.Cols()
		whiteRatio := float64(whiteCount) / float64(totalPixels)

		if whiteRatio > 0.5 {
			gocv.BitwiseNot(binary, &binary)
		}
	}

	// Morphological operations
	if params.DilateIterations > 0 {
		kernel := gocv.GetStructuringElement(gocv.MorphRect, image.Point{3, 3})
		for i := 0; i < params.DilateIterations; i++ {
			gocv.Dilate(binary, &binary, kernel)
		}
		kernel.Close()
	}
	if params.ErodeIterations > 0 {
		kernel := gocv.GetStructuringElement(gocv.MorphRect, image.Point{3, 3})
		for i := 0; i < params.ErodeIterations; i++ {
			gocv.Erode(binary, &binary, kernel)
		}
		kernel.Close()
	}

	// Convert to BGR for Tesseract
	result := gocv.NewMat()
	gocv.CvtColor(binary, &result, gocv.ColorGrayToBGR)
	binary.Close()

	return result
}

// findHistogramThreshold finds threshold that captures brightest N% of pixels.
func findHistogramThreshold(gray gocv.Mat, brightestPct float64, minThreshold int) int {
	// Build histogram
	hist := make([]int, 256)
	totalPixels := gray.Rows() * gray.Cols()

	for y := 0; y < gray.Rows(); y++ {
		for x := 0; x < gray.Cols(); x++ {
			val := gray.GetUCharAt(y, x)
			hist[val]++
		}
	}

	// Find threshold that captures brightest N%
	targetPixels := int(float64(totalPixels) * brightestPct / 100.0)
	cumulative := 0
	threshold := 255

	for v := 255; v >= 0; v-- {
		cumulative += hist[v]
		if cumulative >= targetPixels {
			threshold = v
			break
		}
	}

	// Apply minimum
	if threshold < minThreshold {
		threshold = minThreshold
	}

	return threshold
}

// UpdateLearnedParams updates the learned parameters with a new training sample.
func (lp *LearnedParams) UpdateLearnedParams(sample TrainingSample) {
	lp.Samples = append(lp.Samples, sample)

	// Update best params if this sample scored better than average
	if len(lp.Samples) == 1 || sample.BestScore > lp.AvgScore {
		lp.BestParams = sample.BestParams
	}

	// Recalculate average score
	totalScore := 0.0
	for _, s := range lp.Samples {
		totalScore += s.BestScore
	}
	lp.AvgScore = totalScore / float64(len(lp.Samples))

	fmt.Printf("OCR Training: %d samples, avg score=%.3f\n", len(lp.Samples), lp.AvgScore)
}
