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
	BrightestPercent float64 `json:"brightest_pct,omitempty"`

	// Minimum threshold value (0-255) to avoid noise
	MinThreshold int `json:"min_threshold,omitempty"`

	// Fixed threshold value (0-255) - used when not Otsu/histogram
	FixedThreshold int `json:"fixed_threshold,omitempty"`

	// CLAHE parameters
	CLAHEClipLimit float64 `json:"clahe_clip,omitempty"`
	CLAHETileSize  int     `json:"clahe_tile,omitempty"`

	// Scaling: minimum dimension target for upscaling
	MinScaleDim int `json:"min_scale_dim,omitempty"`

	// Invert polarity (true = expect light text on dark background)
	InvertPolarity bool `json:"invert,omitempty"`

	// Use Otsu threshold instead of histogram-based
	UseOtsu bool `json:"use_otsu,omitempty"`

	// Adaptive threshold parameters
	UseAdaptive   bool `json:"use_adaptive,omitempty"`
	AdaptiveBlock int  `json:"adaptive_block,omitempty"` // Block size (odd number)
	AdaptiveC     int  `json:"adaptive_c,omitempty"`     // Constant subtracted from mean

	// Morphological operations
	DilateIterations int `json:"dilate,omitempty"`
	ErodeIterations  int `json:"erode,omitempty"`

	// PSM mode (page segmentation mode)
	PSMMode int `json:"psm_mode,omitempty"`
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
// This is VERY aggressive with threshold manipulation.
func (e *Engine) AnnealOCRParams(img gocv.Mat, groundTruth string, maxIterations int) (OCRParams, float64, string) {
	if img.Empty() || groundTruth == "" {
		return DefaultOCRParams(), 0.0, ""
	}

	// Strip logo markers from truth for clean comparison
	cleanTruth := stripLogoMarkers(groundTruth)
	fmt.Printf("OCR Annealing: searching (truth=%q, clean=%q)\n", groundTruth, cleanTruth)

	bestParams := DefaultOCRParams()
	bestScore := 0.0
	bestText := ""
	iterations := 0

	// Helper to try a param set
	tryParams := func(params OCRParams, desc string) bool {
		if iterations >= maxIterations {
			return true // stop
		}
		text := e.recognizeWithParams(img, params)
		score := TextSimilarity(text, groundTruth)
		iterations++

		if score > bestScore {
			bestScore = score
			bestParams = params
			bestText = text
			fmt.Printf("  [%d] score=%.3f %s -> %q\n", iterations, score, desc, text)
			if score >= 0.95 {
				return true // stop early
			}
		}
		return false
	}

	// ========== Declare all parameter slices upfront (Go doesn't allow goto over declarations) ==========
	fixedThresholds := []int{40, 50, 60, 70, 80, 90, 100, 110, 120, 130, 140, 150, 160, 170, 180, 190, 200, 210, 220, 230}
	scales := []int{100, 150, 200, 300, 400}
	psmModes := []int{6, 7, 13, 11, 3} // BLOCK, LINE, RAW_LINE, SPARSE, AUTO
	claheClips := []float64{1.0, 1.5, 2.0, 3.0, 4.0, 6.0, 8.0}
	claheTiles := []int{2, 4, 8, 16}
	brightestPcts := []float64{3, 5, 7, 10, 15, 20, 25, 30, 40, 50}
	minThresholds := []int{50, 80, 100, 120, 140, 160}

	// ========== PHASE 1: Fixed thresholds (most direct for IC text) ==========
	// IC text is typically light markings on dark plastic
	// Try many fixed threshold values
	fmt.Println("  Phase 1: Fixed thresholds...")

	for _, thresh := range fixedThresholds {
		for _, invert := range []bool{true, false} {
			for _, scale := range scales {
				for _, psm := range psmModes {
					params := OCRParams{
						UseOtsu:          false,
						FixedThreshold:   thresh,
						InvertPolarity:   invert,
						MinScaleDim:      scale,
						PSMMode:          psm,
						CLAHEClipLimit:   0, // No CLAHE
					}
					if tryParams(params, fmt.Sprintf("fixed=%d inv=%v scale=%d psm=%d", thresh, invert, scale, psm)) {
						goto done
					}
				}
			}
		}
	}

	// ========== PHASE 2: CLAHE + Otsu ==========
	fmt.Println("  Phase 2: CLAHE + Otsu...")

	for _, clip := range claheClips {
		for _, tile := range claheTiles {
			for _, invert := range []bool{true, false} {
				for _, scale := range scales {
					for _, psm := range psmModes {
						params := OCRParams{
							UseOtsu:        true,
							InvertPolarity: invert,
							CLAHEClipLimit: clip,
							CLAHETileSize:  tile,
							MinScaleDim:    scale,
							PSMMode:        psm,
						}
						if tryParams(params, fmt.Sprintf("otsu clip=%.1f tile=%d inv=%v scale=%d psm=%d", clip, tile, invert, scale, psm)) {
							goto done
						}
					}
				}
			}
		}
	}

	// ========== PHASE 3: Histogram-based (brightest N%) ==========
	fmt.Println("  Phase 3: Histogram brightest %...")

	for _, pct := range brightestPcts {
		for _, minTh := range minThresholds {
			for _, invert := range []bool{true, false} {
				for _, scale := range scales {
					for _, psm := range psmModes {
						params := OCRParams{
							UseOtsu:          false,
							BrightestPercent: pct,
							MinThreshold:     minTh,
							InvertPolarity:   invert,
							MinScaleDim:      scale,
							PSMMode:          psm,
						}
						if tryParams(params, fmt.Sprintf("hist=%v%% min=%d inv=%v scale=%d psm=%d", pct, minTh, invert, scale, psm)) {
							goto done
						}
					}
				}
			}
		}
	}

	// ========== PHASE 4: Morphological operations ==========
	fmt.Println("  Phase 4: Morphological operations...")
	for _, thresh := range []int{80, 100, 120, 140, 160, 180} {
		for _, dilate := range []int{0, 1, 2} {
			for _, erode := range []int{0, 1, 2} {
				for _, invert := range []bool{true, false} {
					for _, scale := range []int{150, 200, 300} {
						params := OCRParams{
							UseOtsu:          false,
							FixedThreshold:   thresh,
							InvertPolarity:   invert,
							MinScaleDim:      scale,
							PSMMode:          6,
							DilateIterations: dilate,
							ErodeIterations:  erode,
						}
						if tryParams(params, fmt.Sprintf("morph th=%d d=%d e=%d inv=%v", thresh, dilate, erode, invert)) {
							goto done
						}
					}
				}
			}
		}
	}

	// ========== PHASE 5: Adaptive threshold ==========
	fmt.Println("  Phase 5: Adaptive threshold...")
	for _, blockSize := range []int{11, 21, 31, 51} {
		for _, c := range []int{2, 5, 10, 15, 20} {
			for _, invert := range []bool{true, false} {
				for _, scale := range []int{150, 200, 300} {
					params := OCRParams{
						UseAdaptive:      true,
						AdaptiveBlock:    blockSize,
						AdaptiveC:        c,
						InvertPolarity:   invert,
						MinScaleDim:      scale,
						PSMMode:          6,
					}
					if tryParams(params, fmt.Sprintf("adaptive blk=%d c=%d inv=%v", blockSize, c, invert)) {
						goto done
					}
				}
			}
		}
	}

done:
	fmt.Printf("OCR Annealing: best score=%.3f after %d iterations\n", bestScore, iterations)
	fmt.Printf("  Best text: %q\n", bestText)

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

	// Threshold - multiple strategies
	binary := gocv.NewMat()
	if params.UseAdaptive {
		// Adaptive threshold - works well for uneven lighting
		blockSize := params.AdaptiveBlock
		if blockSize < 3 {
			blockSize = 11
		}
		// Ensure block size is odd
		if blockSize%2 == 0 {
			blockSize++
		}
		c := params.AdaptiveC
		if c == 0 {
			c = 5
		}
		gocv.AdaptiveThreshold(enhanced, &binary, 255,
			gocv.AdaptiveThresholdMean, gocv.ThresholdBinary, blockSize, float32(c))
	} else if params.UseOtsu {
		// Otsu's method - automatic threshold selection
		gocv.Threshold(enhanced, &binary, 0, 255, gocv.ThresholdBinary|gocv.ThresholdOtsu)
	} else if params.FixedThreshold > 0 {
		// Fixed threshold - direct control
		gocv.Threshold(enhanced, &binary, float32(params.FixedThreshold), 255, gocv.ThresholdBinary)
	} else {
		// Histogram-based threshold - brightest N%
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
