// Package ocr provides OCR (Optical Character Recognition) for component labels.
package ocr

import (
	"fmt"
	"image"
	"strings"

	"pcb-tracer/pkg/geometry"

	"github.com/otiai10/gosseract/v2"
	"gocv.io/x/gocv"
)

// ElectronicsChars is the character set for electronic component OCR.
// Excludes lowercase to reduce confusion (0/O, 1/I, etc.)
const ElectronicsChars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ-/"

// Engine provides OCR functionality using Tesseract.
type Engine struct {
	client          *gosseract.Client
	electronicsMode bool
}

// NewEngine creates a new OCR engine.
func NewEngine() (*Engine, error) {
	client := gosseract.NewClient()

	// Set English language for base character recognition
	if err := client.SetLanguage("eng"); err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to set OCR language: %w", err)
	}

	// Disable dictionary-based word correction - part numbers aren't English words
	// This prevents Tesseract from "correcting" DM74LS244N to something else
	_ = client.SetVariable("load_system_dawg", "false")
	_ = client.SetVariable("load_freq_dawg", "false")
	_ = client.SetVariable("language_model_penalty_non_dict_word", "0")
	_ = client.SetVariable("language_model_penalty_non_freq_dict_word", "0")

	return &Engine{
		client:          client,
		electronicsMode: true,
	}, nil
}

// Close releases OCR resources.
func (e *Engine) Close() error {
	if e.client != nil {
		return e.client.Close()
	}
	return nil
}

// SetElectronicsMode enables/disables electronics character set restriction.
func (e *Engine) SetElectronicsMode(enabled bool) {
	e.electronicsMode = enabled
}

// RecognizeRegion performs OCR on a region of an image.
func (e *Engine) RecognizeRegion(img gocv.Mat, bounds geometry.RectInt) (string, error) {
	if img.Empty() {
		return "", fmt.Errorf("empty image")
	}

	// Validate bounds
	x, y, w, h := bounds.X, bounds.Y, bounds.Width, bounds.Height
	imgH, imgW := img.Rows(), img.Cols()

	x = max(0, x)
	y = max(0, y)
	w = min(w, imgW-x)
	h = min(h, imgH-y)

	if w <= 0 || h <= 0 {
		return "", fmt.Errorf("invalid region bounds")
	}

	// Extract region
	region := img.Region(image.Rect(x, y, x+w, y+h))
	defer region.Close()

	// Preprocess for better OCR
	processed := preprocessForOCR(region, e.electronicsMode)
	defer processed.Close()

	// Convert to image bytes (PNG format)
	buf, err := gocv.IMEncode(gocv.PNGFileExt, processed)
	if err != nil {
		return "", fmt.Errorf("failed to encode image: %w", err)
	}
	defer buf.Close()

	// Configure Tesseract
	// PSM 6 = Assume a single uniform block of text
	if err := e.client.SetPageSegMode(gosseract.PSM_SINGLE_BLOCK); err != nil {
		return "", fmt.Errorf("failed to set PSM: %w", err)
	}

	if e.electronicsMode {
		if err := e.client.SetWhitelist(ElectronicsChars); err != nil {
			return "", fmt.Errorf("failed to set whitelist: %w", err)
		}
	} else {
		// Clear whitelist
		if err := e.client.SetWhitelist(""); err != nil {
			// Ignore error - some versions don't support empty whitelist
		}
	}

	// Set image
	if err := e.client.SetImageFromBytes(buf.GetBytes()); err != nil {
		return "", fmt.Errorf("failed to set image: %w", err)
	}

	// Get text
	text, err := e.client.Text()
	if err != nil {
		return "", fmt.Errorf("OCR failed: %w", err)
	}

	// Clean up result
	text = strings.TrimSpace(text)
	text = strings.Join(strings.Fields(text), " ")

	if e.electronicsMode {
		text = strings.ToUpper(text)
	}

	return text, nil
}

// RecognizeImage performs OCR on an entire image.
func (e *Engine) RecognizeImage(img gocv.Mat) (string, error) {
	bounds := geometry.RectInt{
		X: 0, Y: 0,
		Width:  img.Cols(),
		Height: img.Rows(),
	}
	return e.RecognizeRegion(img, bounds)
}

// preprocessForOCR prepares an image region for OCR.
func preprocessForOCR(region gocv.Mat, electronicsMode bool) gocv.Mat {
	h, w := region.Rows(), region.Cols()

	// Upscale small images for better OCR (target ~150px height minimum)
	var scaled gocv.Mat
	minDim := min(h, w)
	if minDim < 150 {
		scale := 150.0 / float64(minDim)
		scaled = gocv.NewMat()
		gocv.Resize(region, &scaled, image.Point{}, scale, scale, gocv.InterpolationCubic)
	} else {
		scaled = region.Clone()
	}

	if !electronicsMode {
		// Just convert BGR to RGB for non-electronics mode
		result := gocv.NewMat()
		gocv.CvtColor(scaled, &result, gocv.ColorBGRToRGB)
		scaled.Close()
		return result
	}

	// Convert to grayscale
	gray := gocv.NewMat()
	gocv.CvtColor(scaled, &gray, gocv.ColorBGRToGray)
	scaled.Close()

	// Apply CLAHE (Contrast Limited Adaptive Histogram Equalization)
	clahe := gocv.NewCLAHEWithParams(2.0, image.Point{8, 8})
	defer clahe.Close()

	enhanced := gocv.NewMat()
	clahe.Apply(gray, &enhanced)
	gray.Close()

	// Otsu's threshold for clean text/background separation
	binary := gocv.NewMat()
	gocv.Threshold(enhanced, &binary, 0, 255, gocv.ThresholdBinary|gocv.ThresholdOtsu)
	enhanced.Close()

	// Check if text is light-on-dark (common for IC packages)
	// OCR expects dark text on light background
	whiteCount := gocv.CountNonZero(binary)
	totalPixels := binary.Rows() * binary.Cols()
	whiteRatio := float64(whiteCount) / float64(totalPixels)

	if whiteRatio > 0.5 {
		// More white than black - likely light text on dark, invert
		gocv.BitwiseNot(binary, &binary)
	}

	// Convert to BGR for Tesseract (it handles the format internally)
	result := gocv.NewMat()
	gocv.CvtColor(binary, &result, gocv.ColorGrayToBGR)
	binary.Close()

	return result
}

// Result represents a single OCR detection result.
type Result struct {
	Text       string
	Bounds     geometry.RectInt
	Confidence float64
}

// DetectAllText finds and recognizes all text regions in an image.
func (e *Engine) DetectAllText(img gocv.Mat) ([]Result, error) {
	if img.Empty() {
		return nil, fmt.Errorf("empty image")
	}

	// Preprocess entire image
	processed := preprocessForOCR(img, e.electronicsMode)
	defer processed.Close()

	// Convert to image bytes
	buf, err := gocv.IMEncode(gocv.PNGFileExt, processed)
	if err != nil {
		return nil, fmt.Errorf("failed to encode image: %w", err)
	}
	defer buf.Close()

	// Configure for word-level detection
	if err := e.client.SetPageSegMode(gosseract.PSM_SPARSE_TEXT); err != nil {
		return nil, fmt.Errorf("failed to set PSM: %w", err)
	}

	if e.electronicsMode {
		if err := e.client.SetWhitelist(ElectronicsChars); err != nil {
			return nil, fmt.Errorf("failed to set whitelist: %w", err)
		}
	}

	if err := e.client.SetImageFromBytes(buf.GetBytes()); err != nil {
		return nil, fmt.Errorf("failed to set image: %w", err)
	}

	// Get bounding boxes with text
	boxes, err := e.client.GetBoundingBoxes(gosseract.RIL_WORD)
	if err != nil {
		return nil, fmt.Errorf("failed to get boxes: %w", err)
	}

	var results []Result
	for _, box := range boxes {
		text := strings.TrimSpace(box.Word)
		if text == "" {
			continue
		}

		if e.electronicsMode {
			text = strings.ToUpper(text)
		}

		results = append(results, Result{
			Text: text,
			Bounds: geometry.RectInt{
				X:      box.Box.Min.X,
				Y:      box.Box.Min.Y,
				Width:  box.Box.Dx(),
				Height: box.Box.Dy(),
			},
			Confidence: box.Confidence,
		})
	}

	return results, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
