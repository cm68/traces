// Command ocrtrain performs exhaustive OCR training on a project's components.
// It explores different preprocessing strategies to find optimal OCR parameters.
//
// Usage: ocrtrain <project.pcbtrace> [options]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"pcb-tracer/internal/component"
	pcbimage "pcb-tracer/internal/image"
	"pcb-tracer/internal/logo"
	"pcb-tracer/internal/ocr"
	"pcb-tracer/pkg/geometry"

	"gocv.io/x/gocv"
)

// Strategy represents an OCR preprocessing strategy to test.
type Strategy struct {
	Name        string
	Orientation string // N, S, E, W
	MaskLogos   bool
	Params      ocr.OCRParams
}

// Result holds the OCR result for a single strategy on a single component.
type Result struct {
	ComponentID string
	GroundTruth string
	Strategy    Strategy
	DetectedText string
	Score       float64
	Duration    time.Duration
}

// ComponentResult aggregates results for a single component.
type ComponentResult struct {
	Component   *component.Component
	GroundTruth string
	Results     []Result
	BestResult  *Result
}

// CropBounds represents a crop region.
type CropBounds struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// ProjectFile mirrors the project file structure for loading.
type ProjectFile struct {
	Version        int                     `json:"version"`
	FrontImagePath string                  `json:"front_image,omitempty"`
	BackImagePath  string                  `json:"back_image,omitempty"`
	FrontCrop      *CropBounds             `json:"front_crop,omitempty"`
	BackCrop       *CropBounds             `json:"back_crop,omitempty"`
	Components     []*component.Component  `json:"components,omitempty"`
}

var (
	flagVerbose     = flag.Bool("v", false, "Verbose output")
	flagParallel    = flag.Int("j", 4, "Number of parallel workers")
	flagMaxIter     = flag.Int("max-iter", 50000, "Max iterations per orientation")
	flagMinScore    = flag.Float64("min-score", 0.5, "Minimum score to report")
	flagOutputJSON  = flag.String("json", "", "Output results to JSON file")
	flagLogoLib     = flag.Bool("logos", false, "Load logo library from preferences")
	flagOrientation = flag.String("orientation", "", "Test single orientation (N/S/E/W), empty=all")
	flagComponent   = flag.String("component", "", "Test single component ID, empty=all")
	flagDebugImg    = flag.String("debug-img", "", "Save debug image to this path")
)

func main() {
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s <project.pcbtrace> [options]\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(1)
	}

	projectPath := flag.Arg(0)

	// Load project
	fmt.Printf("Loading project: %s\n", projectPath)
	proj, err := loadProject(projectPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading project: %v\n", err)
		os.Exit(1)
	}

	projectDir := filepath.Dir(projectPath)

	// Load images
	var frontImg, backImg image.Image
	if proj.FrontImagePath != "" {
		imgPath := filepath.Join(projectDir, proj.FrontImagePath)
		fmt.Printf("Loading front image: %s\n", imgPath)
		layer, err := pcbimage.Load(imgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading front image: %v\n", err)
			os.Exit(1)
		}
		frontImg = layer.Image
		fmt.Printf("  Front image: %dx%d\n", frontImg.Bounds().Dx(), frontImg.Bounds().Dy())
	}
	if proj.BackImagePath != "" {
		imgPath := filepath.Join(projectDir, proj.BackImagePath)
		fmt.Printf("Loading back image: %s\n", imgPath)
		layer, err := pcbimage.Load(imgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading back image: %v\n", err)
			os.Exit(1)
		}
		backImg = layer.Image
		fmt.Printf("  Back image: %dx%d\n", backImg.Bounds().Dx(), backImg.Bounds().Dy())
	}

	// Load logo library if requested
	var logoLib *logo.LogoLibrary
	if *flagLogoLib {
		fmt.Println("Loading logo library from preferences...")
		logoLib, err = logo.LoadFromPreferences()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not load logo library: %v\n", err)
		} else {
			fmt.Printf("  Loaded %d logos\n", len(logoLib.Logos))
		}
	}

	// Filter components with ground truth
	var trainableComponents []*component.Component
	for _, comp := range proj.Components {
		if comp.CorrectedText == "" {
			continue
		}
		if *flagComponent != "" && comp.ID != *flagComponent {
			continue
		}
		trainableComponents = append(trainableComponents, comp)
	}

	if len(trainableComponents) == 0 {
		fmt.Println("No components with ground truth (CorrectedText) found.")
		os.Exit(0)
	}

	fmt.Printf("\nFound %d components with ground truth for training:\n", len(trainableComponents))
	for _, comp := range trainableComponents {
		fmt.Printf("  %s: %q\n", comp.ID, comp.CorrectedText)
	}
	fmt.Println()

	// Run training (pass crop offsets so bounds are correctly mapped to full image)
	results := runTraining(trainableComponents, frontImg, backImg, logoLib, proj.FrontCrop, proj.BackCrop)

	// Print results
	printResults(results)

	// Save good results to global training database
	saveToGlobalTraining(results)

	// Output JSON if requested
	if *flagOutputJSON != "" {
		if err := outputJSON(results, *flagOutputJSON); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing JSON: %v\n", err)
		} else {
			fmt.Printf("\nResults written to: %s\n", *flagOutputJSON)
		}
	}

	// Print summary statistics
	printSummary(results)
}

func loadProject(path string) (*ProjectFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var proj ProjectFile
	if err := json.Unmarshal(data, &proj); err != nil {
		return nil, err
	}

	return &proj, nil
}

func runTraining(components []*component.Component, frontImg, backImg image.Image, logoLib *logo.LogoLibrary, frontCrop, backCrop *CropBounds) []ComponentResult {
	var results []ComponentResult
	var mu sync.Mutex

	// Process each component
	for i, comp := range components {
		fmt.Printf("\n[%d/%d] Training component %s\n", i+1, len(components), comp.ID)
		fmt.Printf("  Ground truth: %q\n", comp.CorrectedText)
		fmt.Printf("  Stored orientation: %s\n", comp.OCROrientation)

		// Select image and crop offset based on layer
		img := frontImg
		crop := frontCrop
		if comp.Layer == pcbimage.SideBack {
			img = backImg
			crop = backCrop
		}
		if img == nil {
			fmt.Printf("  ERROR: No image available for layer %v\n", comp.Layer)
			continue
		}

		// Apply crop offset to bounds (component bounds are relative to cropped image)
		bounds := comp.Bounds
		if crop != nil {
			bounds = geometry.Rect{
				X:      comp.Bounds.X + crop.X,
				Y:      comp.Bounds.Y + crop.Y,
				Width:  comp.Bounds.Width,
				Height: comp.Bounds.Height,
			}
			if *flagVerbose {
				fmt.Printf("  Bounds: (%.0f,%.0f) + crop(%.0f,%.0f) = (%.0f,%.0f) %.0fx%.0f\n",
					comp.Bounds.X, comp.Bounds.Y, crop.X, crop.Y,
					bounds.X, bounds.Y, bounds.Width, bounds.Height)
			}
		}

		// Extract component region
		cropped := extractRegion(img, bounds)
		if cropped == nil {
			fmt.Printf("  ERROR: Could not extract region\n")
			continue
		}

		// Save raw cropped region if debug enabled
		if *flagDebugImg != "" {
			rawPath := fmt.Sprintf("%s_%s_raw.png", *flagDebugImg, comp.ID)
			if f, err := os.Create(rawPath); err == nil {
				png.Encode(f, cropped)
				f.Close()
				fmt.Printf("  Saved raw crop: %s (%dx%d)\n", rawPath, cropped.Bounds().Dx(), cropped.Bounds().Dy())
			}
		}

		compResult := ComponentResult{
			Component:   comp,
			GroundTruth: comp.CorrectedText,
		}

		// Test all orientations (or single if specified)
		orientations := []string{"N", "S", "E", "W"}
		if *flagOrientation != "" {
			orientations = []string{*flagOrientation}
		}

		for _, orient := range orientations {
			fmt.Printf("  Testing orientation %s...\n", orient)

			// Test with and without logo masking
			maskOptions := []bool{false}
			if logoLib != nil && len(logoLib.Logos) > 0 {
				maskOptions = []bool{false, true}
			}

			for _, maskLogos := range maskOptions {
				// Logo detection must happen BEFORE rotation (like the UI does)
				// Detect logos on unrotated image with orientation-based rotation
				srcImg := cropped
				if maskLogos {
					srcImg = maskLogosInImage(cropped, logoLib, orient)
				}

				// Now rotate for OCR
				rotated := rotateForOCR(srcImg, orient)
				testImg := rotated

				// Save debug image if requested
				if *flagDebugImg != "" && !maskLogos {
					debugPath := fmt.Sprintf("%s_%s_%s.png", *flagDebugImg, comp.ID, orient)
					if f, err := os.Create(debugPath); err == nil {
						png.Encode(f, testImg)
						f.Close()
						fmt.Printf("  Saved debug image: %s (%dx%d)\n", debugPath, testImg.Bounds().Dx(), testImg.Bounds().Dy())
					}
				}

				// Convert to OpenCV format
				mat := imageToMat(testImg)
				if mat.Empty() {
					continue
				}

				// Run exhaustive parameter search
				results := runExhaustiveSearch(comp.ID, comp.CorrectedText, mat, orient, maskLogos)

				mu.Lock()
				compResult.Results = append(compResult.Results, results...)
				mu.Unlock()

				mat.Close()
			}
		}

		// Find best result
		for i := range compResult.Results {
			r := &compResult.Results[i]
			if compResult.BestResult == nil || r.Score > compResult.BestResult.Score {
				compResult.BestResult = r
			}
		}

		if compResult.BestResult != nil {
			fmt.Printf("  BEST: score=%.1f%% orient=%s mask=%v -> %q\n",
				compResult.BestResult.Score*100,
				compResult.BestResult.Strategy.Orientation,
				compResult.BestResult.Strategy.MaskLogos,
				compResult.BestResult.DetectedText)
		}

		results = append(results, compResult)
	}

	return results
}

func runExhaustiveSearch(compID, groundTruth string, mat gocv.Mat, orientation string, maskLogos bool) []Result {
	var results []Result

	// Create OCR engine
	engine, err := ocr.NewEngine()
	if err != nil {
		fmt.Printf("    ERROR creating OCR engine: %v\n", err)
		return results
	}
	defer engine.Close()

	// First try the UI's default OCR (for comparison)
	if *flagVerbose {
		defaultParams := ocr.DefaultOCRParams()
		defaultText, _ := engine.RecognizeWithParams(mat, defaultParams)
		defaultScore := ocr.TextSimilarity(defaultText, groundTruth)
		fmt.Printf("    [default] score=%.3f -> %q\n", defaultScore, defaultText)
	}

	// Run parameter annealing
	start := time.Now()
	bestParams, bestScore, bestText := engine.AnnealOCRParams(mat, groundTruth, *flagMaxIter)
	duration := time.Since(start)

	if *flagVerbose {
		fmt.Printf("    [%s mask=%v] score=%.1f%% in %v -> %q\n",
			orientation, maskLogos, bestScore*100, duration, bestText)
	}

	// Record the best result from annealing
	results = append(results, Result{
		ComponentID:  compID,
		GroundTruth:  groundTruth,
		Strategy: Strategy{
			Name:        fmt.Sprintf("anneal_%s_mask%v", orientation, maskLogos),
			Orientation: orientation,
			MaskLogos:   maskLogos,
			Params:      bestParams,
		},
		DetectedText: bestText,
		Score:        bestScore,
		Duration:     duration,
	})

	// Also test some additional specific strategies
	additionalStrategies := []ocr.OCRParams{
		// Default Otsu
		{UseOtsu: true, InvertPolarity: true, MinScaleDim: 150, PSMMode: 6, CLAHEClipLimit: 2.0, CLAHETileSize: 8},
		// Aggressive CLAHE
		{UseOtsu: true, InvertPolarity: true, MinScaleDim: 200, PSMMode: 6, CLAHEClipLimit: 8.0, CLAHETileSize: 4},
		// Fixed threshold light text
		{FixedThreshold: 100, InvertPolarity: true, MinScaleDim: 200, PSMMode: 6},
		{FixedThreshold: 120, InvertPolarity: true, MinScaleDim: 200, PSMMode: 6},
		{FixedThreshold: 140, InvertPolarity: true, MinScaleDim: 200, PSMMode: 6},
		{FixedThreshold: 160, InvertPolarity: true, MinScaleDim: 200, PSMMode: 6},
		// Single line mode
		{UseOtsu: true, InvertPolarity: true, MinScaleDim: 200, PSMMode: 7},
		// Sparse text mode
		{UseOtsu: true, InvertPolarity: true, MinScaleDim: 200, PSMMode: 11},
		// Raw line mode
		{UseOtsu: true, InvertPolarity: true, MinScaleDim: 200, PSMMode: 13},
		// Adaptive threshold
		{UseAdaptive: true, AdaptiveBlock: 21, AdaptiveC: 10, InvertPolarity: true, MinScaleDim: 200, PSMMode: 6},
		// Morphological cleanup
		{FixedThreshold: 130, InvertPolarity: true, MinScaleDim: 200, PSMMode: 6, DilateIterations: 1},
		{FixedThreshold: 130, InvertPolarity: true, MinScaleDim: 200, PSMMode: 6, ErodeIterations: 1},
	}

	for i, params := range additionalStrategies {
		text, _ := engine.RecognizeWithParams(mat, params)
		score := ocr.TextSimilarity(text, groundTruth)

		if score >= *flagMinScore {
			results = append(results, Result{
				ComponentID:  compID,
				GroundTruth:  groundTruth,
				Strategy: Strategy{
					Name:        fmt.Sprintf("strategy_%d_%s", i, orientation),
					Orientation: orientation,
					MaskLogos:   maskLogos,
					Params:      params,
				},
				DetectedText: text,
				Score:        score,
				Duration:     0, // Not timing individual strategies
			})
		}
	}

	return results
}

func extractRegion(img image.Image, bounds geometry.Rect) *image.RGBA {
	x := int(bounds.X)
	y := int(bounds.Y)
	w := int(bounds.Width)
	h := int(bounds.Height)

	imgBounds := img.Bounds()
	if x < imgBounds.Min.X {
		x = imgBounds.Min.X
	}
	if y < imgBounds.Min.Y {
		y = imgBounds.Min.Y
	}
	if x+w > imgBounds.Max.X {
		w = imgBounds.Max.X - x
	}
	if y+h > imgBounds.Max.Y {
		h = imgBounds.Max.Y - y
	}

	if w <= 0 || h <= 0 {
		return nil
	}

	cropped := image.NewRGBA(image.Rect(0, 0, w, h))
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			cropped.Set(dx, dy, img.At(x+dx, y+dy))
		}
	}

	return cropped
}

func rotateForOCR(img *image.RGBA, orientation string) *image.RGBA {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	switch orientation {
	case "S": // 180 degrees
		rotated := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				rotated.Set(w-1-x, h-1-y, img.At(x, y))
			}
		}
		return rotated

	case "E": // 90 degrees CCW
		rotated := image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				rotated.Set(y, w-1-x, img.At(x, y))
			}
		}
		return rotated

	case "W": // 90 degrees CW
		rotated := image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				rotated.Set(h-1-y, x, img.At(x, y))
			}
		}
		return rotated

	default: // N or unknown
		result := image.NewRGBA(bounds)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				result.Set(x, y, img.At(x, y))
			}
		}
		return result
	}
}

func maskLogosInImage(img *image.RGBA, logoLib *logo.LogoLibrary, orientation string) *image.RGBA {
	if logoLib == nil || len(logoLib.Logos) == 0 {
		return img
	}

	// Convert orientation to logo rotation
	rotation := orientationToRotation(orientation)

	// Detect logos
	bounds := geometry.RectInt{X: 0, Y: 0, Width: img.Bounds().Dx(), Height: img.Bounds().Dy()}
	matches := logoLib.DetectLogos(img, bounds, 0.75, rotation)

	if len(matches) == 0 {
		return img
	}

	// Create copy and mask out logo regions
	result := image.NewRGBA(img.Bounds())
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			result.Set(x, y, img.At(x, y))
		}
	}

	// Calculate background color
	bgColor := calculateBackgroundColor(img)

	// Mask each logo region
	for _, m := range matches {
		maskRegion(result, m.Bounds, bgColor)
	}

	return result
}

func orientationToRotation(orientation string) int {
	switch orientation {
	case "S":
		return 180
	case "E":
		return 90
	case "W":
		return 270
	default:
		return 0
	}
}

func calculateBackgroundColor(img *image.RGBA) color.RGBA {
	bounds := img.Bounds()
	var r, g, b uint64
	count := 0

	// Sample edges
	for x := bounds.Min.X; x < bounds.Max.X; x++ {
		c := img.RGBAAt(x, bounds.Min.Y)
		r += uint64(c.R)
		g += uint64(c.G)
		b += uint64(c.B)
		count++

		c = img.RGBAAt(x, bounds.Max.Y-1)
		r += uint64(c.R)
		g += uint64(c.G)
		b += uint64(c.B)
		count++
	}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		c := img.RGBAAt(bounds.Min.X, y)
		r += uint64(c.R)
		g += uint64(c.G)
		b += uint64(c.B)
		count++

		c = img.RGBAAt(bounds.Max.X-1, y)
		r += uint64(c.R)
		g += uint64(c.G)
		b += uint64(c.B)
		count++
	}

	if count == 0 {
		return color.RGBA{0, 0, 0, 255}
	}

	return color.RGBA{
		R: uint8(r / uint64(count)),
		G: uint8(g / uint64(count)),
		B: uint8(b / uint64(count)),
		A: 255,
	}
}

func maskRegion(img *image.RGBA, bounds geometry.RectInt, c color.RGBA) {
	imgBounds := img.Bounds()
	x0 := bounds.X
	y0 := bounds.Y
	x1 := bounds.X + bounds.Width
	y1 := bounds.Y + bounds.Height

	if x0 < imgBounds.Min.X {
		x0 = imgBounds.Min.X
	}
	if y0 < imgBounds.Min.Y {
		y0 = imgBounds.Min.Y
	}
	if x1 > imgBounds.Max.X {
		x1 = imgBounds.Max.X
	}
	if y1 > imgBounds.Max.Y {
		y1 = imgBounds.Max.Y
	}

	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func imageToMat(img *image.RGBA) gocv.Mat {
	bounds := img.Bounds()
	mat, err := gocv.NewMatFromBytes(bounds.Dy(), bounds.Dx(), gocv.MatTypeCV8UC4, img.Pix)
	if err != nil {
		return gocv.NewMat()
	}

	bgr := gocv.NewMat()
	gocv.CvtColor(mat, &bgr, gocv.ColorRGBAToBGR)
	mat.Close()

	return bgr
}

func saveToGlobalTraining(results []ComponentResult) {
	// Load existing database
	db, err := ocr.LoadGlobalTraining()
	if err != nil {
		fmt.Printf("Warning: could not load existing training database: %v\n", err)
	}

	// Add good results (score >= 0.7) to the database
	added := 0
	for _, cr := range results {
		if cr.BestResult == nil || cr.BestResult.Score < 0.7 {
			continue
		}

		sample := ocr.CreateSampleFromResult(
			cr.GroundTruth,
			cr.BestResult.DetectedText,
			cr.BestResult.Score,
			cr.BestResult.Strategy.Orientation,
			cr.BestResult.Strategy.Params,
		)

		// Add metadata if available
		if cr.Component != nil {
			sample.Manufacturer = cr.Component.Manufacturer
			sample.Package = cr.Component.Package
		}

		db.AddSample(sample)
		added++
	}

	if added > 0 {
		if err := ocr.SaveGlobalTraining(db); err != nil {
			fmt.Printf("Warning: could not save training database: %v\n", err)
		} else {
			fmt.Printf("\nAdded %d samples to global training database (total: %d)\n", added, len(db.Samples))
			fmt.Println(db.Summary())
		}
	}
}

func printResults(results []ComponentResult) {
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("DETAILED RESULTS")
	fmt.Println(strings.Repeat("=", 80))

	for _, cr := range results {
		fmt.Printf("\nComponent: %s\n", cr.Component.ID)
		fmt.Printf("  Ground truth: %q\n", cr.GroundTruth)
		fmt.Printf("  Layer: %v\n", cr.Component.Layer)
		fmt.Printf("  Bounds: (%.0f,%.0f) %.0fx%.0f\n",
			cr.Component.Bounds.X, cr.Component.Bounds.Y,
			cr.Component.Bounds.Width, cr.Component.Bounds.Height)

		if cr.BestResult == nil {
			fmt.Println("  No successful results")
			continue
		}

		fmt.Println("  Best strategies:")

		// Sort results by score descending
		sorted := make([]Result, len(cr.Results))
		copy(sorted, cr.Results)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].Score > sorted[j].Score
		})

		// Show top 5
		shown := 0
		for _, r := range sorted {
			if r.Score < *flagMinScore {
				continue
			}
			fmt.Printf("    %.1f%% [%s] mask=%v -> %q\n",
				r.Score*100, r.Strategy.Orientation, r.Strategy.MaskLogos, r.DetectedText)
			if *flagVerbose {
				fmt.Printf("           params: %+v\n", r.Strategy.Params)
			}
			shown++
			if shown >= 5 {
				break
			}
		}
	}
}

func printSummary(results []ComponentResult) {
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("SUMMARY")
	fmt.Println(strings.Repeat("=", 80))

	var totalBestScore float64
	var perfectCount, goodCount, poorCount, failCount int
	orientationScores := make(map[string][]float64)
	maskScores := map[bool][]float64{true: {}, false: {}}

	for _, cr := range results {
		if cr.BestResult == nil {
			failCount++
			continue
		}

		score := cr.BestResult.Score
		totalBestScore += score

		if score >= 0.95 {
			perfectCount++
		} else if score >= 0.7 {
			goodCount++
		} else if score >= 0.5 {
			poorCount++
		} else {
			failCount++
		}

		// Track by orientation
		orient := cr.BestResult.Strategy.Orientation
		orientationScores[orient] = append(orientationScores[orient], score)

		// Track by masking
		maskScores[cr.BestResult.Strategy.MaskLogos] = append(
			maskScores[cr.BestResult.Strategy.MaskLogos], score)
	}

	total := len(results)
	fmt.Printf("\nComponents: %d total\n", total)
	fmt.Printf("  Perfect (>=95%%): %d (%.1f%%)\n", perfectCount, float64(perfectCount)*100/float64(total))
	fmt.Printf("  Good (>=70%%):    %d (%.1f%%)\n", goodCount, float64(goodCount)*100/float64(total))
	fmt.Printf("  Poor (>=50%%):    %d (%.1f%%)\n", poorCount, float64(poorCount)*100/float64(total))
	fmt.Printf("  Failed (<50%%):   %d (%.1f%%)\n", failCount, float64(failCount)*100/float64(total))

	if total-failCount > 0 {
		fmt.Printf("\nAverage best score: %.1f%%\n", totalBestScore*100/float64(total))
	}

	fmt.Println("\nBest orientation distribution:")
	for orient, scores := range orientationScores {
		avg := 0.0
		for _, s := range scores {
			avg += s
		}
		if len(scores) > 0 {
			avg /= float64(len(scores))
		}
		fmt.Printf("  %s: %d components, avg score %.1f%%\n", orient, len(scores), avg*100)
	}

	fmt.Println("\nLogo masking effect:")
	for mask, scores := range maskScores {
		if len(scores) == 0 {
			continue
		}
		avg := 0.0
		for _, s := range scores {
			avg += s
		}
		avg /= float64(len(scores))
		label := "without"
		if mask {
			label = "with"
		}
		fmt.Printf("  %s masking: %d best results, avg score %.1f%%\n", label, len(scores), avg*100)
	}

	// Find most effective parameters across all components
	fmt.Println("\nMost effective parameter patterns:")
	paramCounts := make(map[string]int)
	paramScores := make(map[string][]float64)

	for _, cr := range results {
		if cr.BestResult == nil || cr.BestResult.Score < 0.7 {
			continue
		}
		p := cr.BestResult.Strategy.Params

		// Categorize by thresholding method
		var method string
		switch {
		case p.UseAdaptive:
			method = fmt.Sprintf("adaptive_blk%d_c%d", p.AdaptiveBlock, p.AdaptiveC)
		case p.UseOtsu:
			method = fmt.Sprintf("otsu_clahe%.1f", p.CLAHEClipLimit)
		case p.FixedThreshold > 0:
			method = fmt.Sprintf("fixed_%d", p.FixedThreshold)
		case p.BrightestPercent > 0:
			method = fmt.Sprintf("hist_%v%%", p.BrightestPercent)
		default:
			method = "unknown"
		}

		key := fmt.Sprintf("%s_psm%d_scale%d_inv%v", method, p.PSMMode, p.MinScaleDim, p.InvertPolarity)
		paramCounts[key]++
		paramScores[key] = append(paramScores[key], cr.BestResult.Score)
	}

	// Sort by count
	type paramStat struct {
		key   string
		count int
		avg   float64
	}
	var stats []paramStat
	for k, c := range paramCounts {
		avg := 0.0
		for _, s := range paramScores[k] {
			avg += s
		}
		avg /= float64(len(paramScores[k]))
		stats = append(stats, paramStat{k, c, avg})
	}
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].count > stats[j].count
	})

	for i, s := range stats {
		if i >= 10 {
			break
		}
		fmt.Printf("  %d. %s (count=%d, avg=%.1f%%)\n", i+1, s.key, s.count, s.avg*100)
	}
}

func outputJSON(results []ComponentResult, path string) error {
	// Convert to serializable format
	type jsonResult struct {
		ComponentID   string        `json:"component_id"`
		GroundTruth   string        `json:"ground_truth"`
		BestScore     float64       `json:"best_score"`
		BestText      string        `json:"best_text"`
		BestOrient    string        `json:"best_orientation"`
		BestMaskLogos bool          `json:"best_mask_logos"`
		BestParams    ocr.OCRParams `json:"best_params"`
		AllResults    []struct {
			Score       float64       `json:"score"`
			Text        string        `json:"text"`
			Orientation string        `json:"orientation"`
			MaskLogos   bool          `json:"mask_logos"`
			Params      ocr.OCRParams `json:"params"`
		} `json:"all_results,omitempty"`
	}

	var output []jsonResult
	for _, cr := range results {
		jr := jsonResult{
			ComponentID: cr.Component.ID,
			GroundTruth: cr.GroundTruth,
		}

		if cr.BestResult != nil {
			jr.BestScore = cr.BestResult.Score
			jr.BestText = cr.BestResult.DetectedText
			jr.BestOrient = cr.BestResult.Strategy.Orientation
			jr.BestMaskLogos = cr.BestResult.Strategy.MaskLogos
			jr.BestParams = cr.BestResult.Strategy.Params
		}

		// Add top results only (to keep file manageable)
		sorted := make([]Result, len(cr.Results))
		copy(sorted, cr.Results)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].Score > sorted[j].Score
		})

		for i, r := range sorted {
			if i >= 10 {
				break
			}
			jr.AllResults = append(jr.AllResults, struct {
				Score       float64       `json:"score"`
				Text        string        `json:"text"`
				Orientation string        `json:"orientation"`
				MaskLogos   bool          `json:"mask_logos"`
				Params      ocr.OCRParams `json:"params"`
			}{
				Score:       r.Score,
				Text:        r.DetectedText,
				Orientation: r.Strategy.Orientation,
				MaskLogos:   r.Strategy.MaskLogos,
				Params:      r.Strategy.Params,
			})
		}

		output = append(output, jr)
	}

	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}
